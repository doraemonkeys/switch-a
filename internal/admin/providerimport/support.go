package providerimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth"
	"github.com/doraemonkeys/switch-a/internal/store"

	"go.uber.org/zap"
)

const providerImportCommitReceiptTTL = 15 * time.Minute

func providerImportCredentialMutationIDs(bundle *store.ProviderImportBundle) []string {
	if bundle == nil {
		return nil
	}
	providerIDs := make([]string, 0, len(bundle.Creates)+len(bundle.CredentialUpdates))
	for i := range bundle.Creates {
		providerIDs = append(providerIDs, bundle.Creates[i].Provider.ID)
	}
	for i := range bundle.CredentialUpdates {
		providerIDs = append(providerIDs, bundle.CredentialUpdates[i].ProviderID)
	}
	return providerIDs
}

func (h *Handler) writeDurableProviderImportReplay(
	w http.ResponseWriter,
	ctx context.Context,
	importID string,
	fingerprint string,
) (bool, error) {
	receipt, err := h.providerImportStore.GetProviderImportReceipt(ctx, importID)
	if errors.Is(err, store.ErrProviderImportReceiptNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if receipt.Fingerprint != fingerprint {
		writeProviderImportCommitMismatch(w, importID)
		return true, nil
	}
	if h.logger != nil {
		h.logger.Info("provider import durable commit replayed", zap.String("import_id", importID))
	}
	writeProviderImportPayload(w, http.StatusOK, receipt.ResponsePayload)
	return true, nil
}

func writeProviderImportCommitMismatch(w http.ResponseWriter, importID string) {
	writeErrorWithDetails(w, http.StatusConflict, ErrCodeConflict,
		"This import was already committed with a different selection",
		map[string]string{"kind": "provider_import_commit_mismatch", "import_id": importID})
}

// providerImportCommitReceiptRegistry coordinates only in-flight commits inside
// this process. Completed results live exclusively in the durable receipt store,
// which keeps this registry bounded by concurrent work instead of recent imports.
type providerImportCommitReceiptRegistry struct {
	mu      sync.Mutex
	clock   interface{ Now() time.Time }
	ttl     time.Duration
	entries map[string]*providerImportCommitReceipt
}

type providerImportCommitReceipt struct {
	fingerprint string
	done        chan struct{}
}

func newProviderImportCommitReceiptRegistry(
	clock interface{ Now() time.Time },
	ttl time.Duration,
) *providerImportCommitReceiptRegistry {
	if clock == nil {
		clock = internal.RealClock{}
	}
	if ttl <= 0 {
		ttl = providerImportCommitReceiptTTL
	}
	return &providerImportCommitReceiptRegistry{
		clock:   clock,
		ttl:     ttl,
		entries: make(map[string]*providerImportCommitReceipt),
	}
}

// acquire grants sole ownership of an import ID and requires complete or abort.
// Waiters do not compare requests until the owner finishes: only a successful,
// durable receipt makes a request mismatch authoritative.
func (r *providerImportCommitReceiptRegistry) acquire(
	ctx context.Context,
	importID string,
	fingerprint string,
) error {
	for {
		r.mu.Lock()
		entry, exists := r.entries[importID]
		if !exists {
			r.entries[importID] = &providerImportCommitReceipt{
				fingerprint: fingerprint,
				done:        make(chan struct{}),
			}
			r.mu.Unlock()
			return nil
		}
		done := entry.done
		r.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			// The owner either completed or aborted. Re-check while holding the
			// lock so a failed owner hands exactly one waiter the next attempt.
		}
	}
}

func (r *providerImportCommitReceiptRegistry) complete(importID, fingerprint string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, exists := r.entries[importID]
	if !exists || entry.fingerprint != fingerprint {
		return
	}
	delete(r.entries, importID)
	close(entry.done)
}

func (r *providerImportCommitReceiptRegistry) abort(importID, fingerprint string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, exists := r.entries[importID]
	if !exists || entry.fingerprint != fingerprint {
		return
	}
	delete(r.entries, importID)
	close(entry.done)
}

type providerImportCommitFingerprintItem struct {
	CandidateID string `json:"candidate_id"`
	Action      string `json:"action"`
	ProviderID  string `json:"provider_id"`
	Name        string `json:"name,omitempty"`
	Priority    int    `json:"priority,omitempty"`
	Concurrency int    `json:"concurrency,omitempty"`
}

type providerImportCommitFingerprintInput struct {
	GroupID string                                `json:"group_id,omitempty"`
	Items   []providerImportCommitFingerprintItem `json:"items"`
}

func providerImportCommitRequestFingerprint(req ProviderImportCommitRequest) string {
	input := providerImportCommitFingerprintInput{
		Items: make([]providerImportCommitFingerprintItem, 0, len(req.Items)),
	}
	if req.GroupID != nil {
		input.GroupID = strings.TrimSpace(*req.GroupID)
	}
	for i := range req.Items {
		item := req.Items[i]
		canonical := providerImportCommitFingerprintItem{
			CandidateID: strings.TrimSpace(item.CandidateID),
			Action:      strings.TrimSpace(item.Action),
			ProviderID:  strings.TrimSpace(item.ProviderID),
		}
		if canonical.Action == providerImportActionCreate {
			canonical.Name = strings.TrimSpace(item.Name)
			canonical.Priority = item.Priority
			canonical.Concurrency = item.Concurrency
		}
		input.Items = append(input.Items, canonical)
	}
	sort.Slice(input.Items, func(i, j int) bool {
		left, right := input.Items[i], input.Items[j]
		if left.CandidateID != right.CandidateID {
			return left.CandidateID < right.CandidateID
		}
		if left.Action != right.Action {
			return left.Action < right.Action
		}
		return left.ProviderID < right.ProviderID
	})
	payload, _ := json.Marshal(input) // primitive-only canonical input cannot fail
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func providerImportSlug(value string) string {
	var result strings.Builder
	lastWasSeparator := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		isAlphaNumeric := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if isAlphaNumeric {
			result.WriteRune(r)
			lastWasSeparator = false
			continue
		}
		if result.Len() > 0 && !lastWasSeparator {
			result.WriteByte('-')
			lastWasSeparator = true
		}
	}
	return strings.TrimRight(result.String(), "-")
}

func isValidProviderImportID(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func incrementProviderImportSummary(
	summary *providerauth.ChatGPTProviderImportSummary,
	state providerauth.ChatGPTProviderImportCandidateState,
) {
	switch state {
	case providerauth.ChatGPTProviderImportCandidateStateReady:
		summary.Ready++
	case providerauth.ChatGPTProviderImportCandidateStateExisting:
		summary.Existing++
	case providerauth.ChatGPTProviderImportCandidateStateDuplicate:
		summary.Duplicate++
	case providerauth.ChatGPTProviderImportCandidateStateInvalid:
		summary.Invalid++
	case providerauth.ChatGPTProviderImportCandidateStateUnsupported:
		summary.Unsupported++
	}
}

func providerImportItemMessage(
	state providerauth.ChatGPTProviderImportCandidateState,
	warnings []providerauth.ChatGPTProviderImportWarning,
) string {
	if len(warnings) > 0 && strings.TrimSpace(warnings[0].Message) != "" {
		return warnings[0].Message
	}
	switch state {
	case providerauth.ChatGPTProviderImportCandidateStateDuplicate:
		return "This account appears more than once in the import file."
	case providerauth.ChatGPTProviderImportCandidateStateInvalid:
		return "This account has invalid or incomplete credentials."
	case providerauth.ChatGPTProviderImportCandidateStateUnsupported:
		return "This account type is not supported by Switch-A."
	default:
		return "This account cannot be imported."
	}
}

func boundedProviderImportWarnings(
	warnings []providerauth.ChatGPTProviderImportWarning,
) []providerauth.ChatGPTProviderImportWarning {
	result := make([]providerauth.ChatGPTProviderImportWarning, 0, len(warnings))
	for i := range warnings {
		result = append(result, providerauth.ChatGPTProviderImportWarning{
			Code:    boundedProviderImportText(warnings[i].Code, maxProviderImportWarningCodeCharacters),
			Message: boundedProviderImportText(warnings[i].Message, maxProviderImportMessageCharacters),
		})
	}
	return result
}

func boundedProviderImportText(value string, maximumCharacters int) string {
	trimmed := strings.TrimSpace(value)
	if maximumCharacters <= 0 {
		return ""
	}
	characterCount := 0
	for index := range trimmed {
		if characterCount == maximumCharacters {
			return strings.TrimSpace(trimmed[:index])
		}
		characterCount++
	}
	return trimmed
}

func (h *Handler) writeProviderImportDraftError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, providerauth.ErrChatGPTProviderImportCapacityExceeded):
		w.Header().Set("Retry-After", providerImportRetryAfter)
		writeErrorWithDetails(w, http.StatusTooManyRequests, ErrCodeConflict,
			"Too many provider imports are awaiting review; retry shortly",
			map[string]string{"kind": "provider_import_capacity_exceeded"})
	case errors.Is(err, providerauth.ErrChatGPTProviderImportInProgress):
		writeErrorWithDetails(w, http.StatusConflict, ErrCodeConflict,
			"Provider import is currently being committed",
			map[string]string{"kind": "provider_import_in_progress"})
	case errors.Is(err, providerauth.ErrChatGPTProviderImportExpired),
		errors.Is(err, providerauth.ErrChatGPTProviderImportNotFound):
		writeError(w, http.StatusGone, ErrCodeConflict, "Provider import expired or unavailable; preview the file again")
	case errors.Is(err, providerauth.ErrChatGPTProviderImportCandidateNotFound):
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "One or more candidate IDs are not part of this import")
	case errors.Is(err, providerauth.ErrChatGPTProviderImportInvalidDocument),
		errors.Is(err, providerauth.ErrChatGPTProviderImportInvalidCandidate):
		writeError(w, http.StatusBadRequest, ErrCodeValidation, err.Error())
	default:
		writeError(w, http.StatusBadRequest, ErrCodeValidation, fallback)
	}
}

func writeProviderImportConflict(w http.ResponseWriter, conflict *store.ProviderImportConflictError) {
	type conflictDetails struct {
		Conflicts []store.ProviderImportConflict `json:"conflicts"`
	}
	type conflictResponse struct {
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Details conflictDetails `json:"details"`
	}
	writeJSON(w, http.StatusConflict, conflictResponse{
		Code:    ErrCodeConflict,
		Message: "Provider state changed after preview; review the conflicts and try again",
		Details: conflictDetails{Conflicts: conflict.Conflicts},
	})
}

func writeProviderImportPayload(w http.ResponseWriter, status int, payload []byte) {
	w.Header().Set("Content-Type", ContentTypeJSON)
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}

func setProviderImportResponseHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
}

func (h *Handler) cancelProviderImportAfterPreviewFailure(importID string) {
	if h.providerImports == nil || importID == "" {
		return
	}
	if err := h.providerImports.CancelChatGPTProviderImport(importID); err != nil {
		h.logProviderImportError("provider import cleanup after preview failure failed", importID, err)
	}
}

func (h *Handler) logProviderImportError(message, importID string, err error) {
	if h.logger == nil {
		return
	}
	h.logger.Error(message, zap.String("import_id", importID), zap.Error(err))
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", ContentTypeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeErrorWithDetails(w, status, code, message, nil)
}

func writeErrorWithDetails(
	w http.ResponseWriter,
	status int,
	code string,
	message string,
	details map[string]string,
) {
	writeJSON(w, status, model.ErrorResponse{Code: code, Message: message, Details: details})
}
