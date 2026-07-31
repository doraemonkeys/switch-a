package providerimport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth"
	"github.com/doraemonkeys/switch-a/internal/store"

	"go.uber.org/zap"
)

// PreviewProviderImport handles POST /admin/api/provider-imports. Preview is a
// local parse-and-stage operation: it intentionally performs no token refresh or
// other network request, so reviewing a file cannot mutate upstream credentials.
func (h *Handler) PreviewProviderImport(w http.ResponseWriter, r *http.Request) {
	startedAt := time.Now()
	setProviderImportResponseHeaders(w)
	if h.providerImports == nil || h.store == nil {
		writeError(w, http.StatusNotImplemented, ErrCodeInternal, "Provider import is unavailable in this build")
		return
	}
	if !h.tryAcquireProviderImportBodyRead() {
		w.Header().Set("Retry-After", providerImportRetryAfter)
		writeErrorWithDetails(w, http.StatusTooManyRequests, ErrCodeConflict,
			"Too many provider import files are being read; retry shortly",
			map[string]string{"kind": "provider_import_capacity_exceeded"})
		return
	}
	readSlotOwned := true
	defer func() {
		if readSlotOwned {
			h.releaseProviderImportBodyRead()
		}
	}()

	raw, err := h.readProviderImportBody(w, r)
	if err != nil {
		if errors.Is(err, errProviderImportBodyReadTimeout) {
			writeErrorWithDetails(w, http.StatusRequestTimeout, ErrCodeConflict,
				"Timed out while reading provider import file",
				map[string]string{"kind": "provider_import_body_read_timeout"})
			return
		}
		if errors.Is(err, errProviderImportBodyReadDeadlineUnavailable) {
			w.Header().Set("Retry-After", providerImportRetryAfter)
			writeErrorWithDetails(w, http.StatusServiceUnavailable, ErrCodeInternal,
				"Provider import upload protection is temporarily unavailable",
				map[string]string{"kind": "provider_import_upload_protection_unavailable"})
			return
		}
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, ErrCodeValidation, "Import file exceeds the 5 MB limit")
			return
		}
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Unable to read import file")
		return
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Import file is empty")
		return
	}

	preview, err := h.providerImports.PreviewSub2APIChatGPTImport(raw)
	h.releaseProviderImportBodyRead()
	readSlotOwned = false
	if err != nil {
		h.writeProviderImportDraftError(w, err, "Invalid sub2api import file")
		return
	}
	providers, err := h.store.ListProviders(r.Context())
	if err != nil {
		h.cancelProviderImportAfterPreviewFailure(preview.ImportID)
		h.logProviderImportError("provider import preview enrichment failed", preview.ImportID, err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to inspect existing providers")
		return
	}

	response, dispositions := buildProviderImportPreviewResponse(preview, providers)
	responsePayload, err := json.Marshal(response)
	if err != nil {
		h.cancelProviderImportAfterPreviewFailure(preview.ImportID)
		h.logProviderImportError("provider import preview encoding failed", preview.ImportID, err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to encode provider import preview")
		return
	}
	responsePayload = append(responsePayload, '\n')
	if err := h.providerImports.SealChatGPTProviderImportPreview(preview.ImportID, dispositions); err != nil {
		h.cancelProviderImportAfterPreviewFailure(preview.ImportID)
		h.logProviderImportError("provider import preview sealing failed", preview.ImportID, err)
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to finalize provider import preview")
		return
	}
	if h.logger != nil {
		h.logger.Info("provider import preview created",
			zap.String("import_id", response.ImportID),
			zap.Int("total", response.Summary.Total),
			zap.Int("ready", response.Summary.Ready),
			zap.Int("existing", response.Summary.Existing),
			zap.Int("blocked", response.Summary.Duplicate+response.Summary.Invalid+response.Summary.Unsupported),
			zap.Duration("duration", time.Since(startedAt)),
		)
	}
	writeProviderImportPayload(w, http.StatusCreated, responsePayload)
}

// CancelProviderImport handles DELETE /admin/api/provider-imports/{import_id}.
// Cancellation is idempotent because closing a review dialog should not surface a
// race when the draft has just expired or was already committed.
func (h *Handler) CancelProviderImport(w http.ResponseWriter, r *http.Request) {
	setProviderImportResponseHeaders(w)
	if h.providerImports == nil {
		writeError(w, http.StatusNotImplemented, ErrCodeInternal, "Provider import is unavailable in this build")
		return
	}
	importID := strings.TrimSpace(r.PathValue("import_id"))
	if importID == "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "import_id is required")
		return
	}
	if err := h.providerImports.CancelChatGPTProviderImport(importID); err != nil {
		switch {
		case errors.Is(err, providerauth.ErrChatGPTProviderImportNotFound),
			errors.Is(err, providerauth.ErrChatGPTProviderImportExpired):
			// Closing an already consumed review remains idempotent.
		case errors.Is(err, providerauth.ErrChatGPTProviderImportInProgress):
			h.writeProviderImportDraftError(w, err, "Unable to cancel provider import")
			return
		default:
			h.logProviderImportError("provider import cancellation failed", importID, err)
			writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to cancel provider import")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func buildProviderImportPreviewResponse(
	preview *providerauth.ChatGPTProviderImportPreview,
	providers []model.Provider,
) (ProviderImportPreviewResponse, []providerauth.ChatGPTProviderImportCandidateDisposition) {
	bindings := indexProviderImportBindings(providers)
	providerIDs := newProviderImportIDAllocator(providers, len(preview.Items))

	items := make([]ProviderImportPreviewItem, 0, len(preview.Items))
	dispositions := make([]providerauth.ChatGPTProviderImportCandidateDisposition, 0, len(preview.Items))
	summary := providerauth.ChatGPTProviderImportSummary{Total: len(preview.Items)}
	for i := range preview.Items {
		source := preview.Items[i]
		disposition := providerauth.ChatGPTProviderImportCandidateDisposition{
			CandidateID: source.CandidateID,
			State:       source.State,
		}
		item := ProviderImportPreviewItem{
			CandidateID: source.CandidateID,
			SourceIndex: source.SourceIndex,
			Status:      source.State,
			Name:        providerImportDisplayName(source.Name, source.Auth, source.SourceIndex),
			Priority:    source.Priority,
			Concurrency: source.Concurrency,
			Warnings:    boundedProviderImportWarnings(source.Warnings),
		}
		if source.Auth != nil {
			item.Email = boundedProviderImportText(source.Auth.Email, maxProviderImportEmailCharacters)
			item.AccountID = boundedProviderImportText(source.Auth.AccountID, maxProviderImportAccountIDCharacters)
			item.PlanType = boundedProviderImportText(source.Auth.PlanType, maxProviderImportPlanTypeCharacters)
			if source.Auth.ExpiresAt != nil {
				expiresAt := *source.Auth.ExpiresAt
				item.ExpiresAt = &expiresAt
			}
		}
		if item.Warnings == nil {
			item.Warnings = []providerauth.ChatGPTProviderImportWarning{}
		}

		if source.State == providerauth.ChatGPTProviderImportCandidateStateReady {
			if binding, ok := bindingForPreviewItem(bindings, source.Auth); ok {
				item.Status = providerauth.ChatGPTProviderImportCandidateStateExisting
				disposition.State = providerauth.ChatGPTProviderImportCandidateStateExisting
				disposition.ExpectedProviderID = binding.provider.ID
				disposition.ExpectedCredentialVersion = binding.version
				disposition.ExpectedCredentialCreatedAt = binding.provider.Credential.CreatedAt
				item.ProviderID = binding.provider.ID
				item.Name = boundedProviderImportText(binding.provider.Name, maxProviderImportNameCharacters)
				item.Priority = binding.provider.Priority
				item.Concurrency = binding.provider.Concurrency
				item.ExistingProviderID = binding.provider.ID
				item.ExistingProviderName = item.Name
				item.Message = fmt.Sprintf("This account is already connected to provider %q; updating replaces credentials without changing its settings.", binding.provider.Name)
			} else {
				item.ProviderID = providerIDs.allocate(item.Name, source.Auth)
				item.DefaultSelected = true
			}
		} else {
			// Blocked rows still get a stable display ID, but do not reserve it
			// from actionable rows that may share the same source name.
			item.ProviderID = providerImportUnreservedID(item.Name, source.Auth)
		}
		if item.Message == "" && item.Status != providerauth.ChatGPTProviderImportCandidateStateReady {
			item.Message = providerImportItemMessage(item.Status, item.Warnings)
		}
		item.Message = boundedProviderImportText(item.Message, maxProviderImportMessageCharacters)
		incrementProviderImportSummary(&summary, item.Status)
		items = append(items, item)
		dispositions = append(dispositions, disposition)
	}
	warnings := boundedProviderImportWarnings(preview.Warnings)
	if warnings == nil {
		warnings = []providerauth.ChatGPTProviderImportWarning{}
	}
	return ProviderImportPreviewResponse{
		ImportID:  preview.ImportID,
		ExpiresAt: preview.ExpiresAt,
		Items:     items,
		Summary:   summary,
		Warnings:  warnings,
	}, dispositions
}

func (h *Handler) buildProviderImportBundle(
	req ProviderImportCommitRequest,
	candidates map[string]providerauth.ChatGPTProviderImportCandidate,
) (*store.ProviderImportBundle, map[string]ProviderImportCommitResultItem, error) {
	bundle := &store.ProviderImportBundle{}
	results := make(map[string]ProviderImportCommitResultItem, len(req.Items))
	groupID := normalizedProviderImportGroupID(req.GroupID)

	for i := range req.Items {
		selection := req.Items[i]
		candidate := candidates[selection.CandidateID]
		if candidate.State != providerauth.ChatGPTProviderImportCandidateStateReady || candidate.Credential == nil || candidate.AuthState == nil {
			return nil, nil, fmt.Errorf("candidate %q is not importable", selection.CandidateID)
		}
		if candidate.Disposition == nil {
			return nil, nil, fmt.Errorf("candidate %q has no sealed preview disposition", selection.CandidateID)
		}
		disposition := *candidate.Disposition

		switch selection.Action {
		case providerImportActionCreate:
			if disposition.State != providerauth.ChatGPTProviderImportCandidateStateReady {
				return nil, nil, &store.ProviderImportConflictError{Conflicts: []store.ProviderImportConflict{{
					CandidateID:           selection.CandidateID,
					Kind:                  store.ProviderImportConflictAccountAlreadyBound,
					ProviderID:            selection.ProviderID,
					ConflictingProviderID: disposition.ExpectedProviderID,
				}}}
			}
			provider := model.Provider{
				ID:             selection.ProviderID,
				Name:           strings.TrimSpace(selection.Name),
				CredentialType: model.ProviderCredentialTypeChatGPT,
				GroupID:        groupID,
				Weight:         DefaultWeight,
				Priority:       selection.Priority,
				Concurrency:    selection.Concurrency,
				MaxRetries:     DefaultProviderMaxRetries,
				FailoverScope:  model.ScopeAny,
				AcceptFailover: model.ScopeAny,
				Enabled:        true,
			}
			if err := providerauth.ApplyChatGPTProviderImportCandidate(&provider, candidate); err != nil {
				return nil, nil, err
			}
			bundle.Creates = append(bundle.Creates, store.ProviderImportCreate{
				CandidateID: selection.CandidateID,
				Provider:    provider,
			})
			results[selection.CandidateID] = ProviderImportCommitResultItem{
				CandidateID: selection.CandidateID,
				Outcome:     providerImportOutcomeCreated,
				ProviderID:  provider.ID,
				Name:        provider.Name,
			}

		case providerImportActionUpdate:
			if disposition.State != providerauth.ChatGPTProviderImportCandidateStateExisting {
				return nil, nil, &store.ProviderImportConflictError{Conflicts: []store.ProviderImportConflict{{
					CandidateID: selection.CandidateID,
					Kind:        store.ProviderImportConflictAccountBindingMismatch,
					ProviderID:  selection.ProviderID,
				}}}
			}
			if selection.ProviderID != disposition.ExpectedProviderID {
				return nil, nil, &store.ProviderImportConflictError{Conflicts: []store.ProviderImportConflict{{
					CandidateID:           selection.CandidateID,
					Kind:                  store.ProviderImportConflictAccountBindingMismatch,
					ProviderID:            selection.ProviderID,
					ConflictingProviderID: disposition.ExpectedProviderID,
				}}}
			}
			credentialTarget := model.Provider{ID: disposition.ExpectedProviderID}
			if err := providerauth.ApplyChatGPTProviderImportCandidate(&credentialTarget, candidate); err != nil {
				return nil, nil, err
			}
			bundle.CredentialUpdates = append(bundle.CredentialUpdates, store.ProviderImportCredentialUpdate{
				CandidateID:                 selection.CandidateID,
				ProviderID:                  disposition.ExpectedProviderID,
				ExpectedCredentialVersion:   disposition.ExpectedCredentialVersion,
				ExpectedCredentialCreatedAt: disposition.ExpectedCredentialCreatedAt,
				Credential:                  *credentialTarget.Credential,
				AuthState:                   *credentialTarget.AuthState,
			})
			results[selection.CandidateID] = ProviderImportCommitResultItem{
				CandidateID: selection.CandidateID,
				Outcome:     providerImportOutcomeUpdated,
				ProviderID:  disposition.ExpectedProviderID,
			}
		}
	}
	return bundle, results, nil
}

func buildProviderImportCommitResponse(
	importID string,
	allCandidates []providerauth.ChatGPTProviderImportCandidate,
	selected map[string]ProviderImportCommitResultItem,
) ProviderImportCommitResponse {
	response := ProviderImportCommitResponse{
		ImportID: importID,
		Items:    make([]ProviderImportCommitResultItem, 0, len(selected)),
	}
	for i := range allCandidates {
		candidate := allCandidates[i]
		if result, ok := selected[candidate.CandidateID]; ok {
			response.Items = append(response.Items, result)
			if result.Outcome == providerImportOutcomeCreated {
				response.Summary.Created++
			} else {
				response.Summary.Updated++
			}
			continue
		}
		response.Summary.Skipped++
	}
	return response
}

func decodeProviderImportCommitRequest(w http.ResponseWriter, r *http.Request, target *ProviderImportCommitRequest) error {
	r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBodySize)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("request body must contain exactly one JSON object")
	}
	return nil
}

func validateProviderImportCommitRequest(req *ProviderImportCommitRequest) error {
	if len(req.Items) == 0 {
		return fmt.Errorf("at least one import item is required")
	}
	if len(req.Items) > maxProviderImportSelections {
		return fmt.Errorf("at most %d import items are allowed", maxProviderImportSelections)
	}
	if err := normalizeProviderImportCommitGroupID(req); err != nil {
		return err
	}

	seenCandidates := make(map[string]struct{}, len(req.Items))
	for i := range req.Items {
		item := &req.Items[i]
		if err := normalizeProviderImportCommitCandidateID(item, i); err != nil {
			return err
		}
		if _, exists := seenCandidates[item.CandidateID]; exists {
			return fmt.Errorf("candidate_id %q is selected more than once", item.CandidateID)
		}
		seenCandidates[item.CandidateID] = struct{}{}
		if err := normalizeProviderImportCommitAction(item, i); err != nil {
			return err
		}
	}
	return nil
}

func normalizeProviderImportCommitGroupID(req *ProviderImportCommitRequest) error {
	if req.GroupID == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*req.GroupID)
	if utf8.RuneCountInString(trimmed) > maxProviderImportIdentifierCharacters {
		return fmt.Errorf("group_id must not exceed %d characters", maxProviderImportIdentifierCharacters)
	}
	req.GroupID = &trimmed
	return nil
}

func normalizeProviderImportCommitCandidateID(item *ProviderImportCommitItem, index int) error {
	item.CandidateID = strings.TrimSpace(item.CandidateID)
	if item.CandidateID == "" {
		return fmt.Errorf("items[%d].candidate_id is required", index)
	}
	if utf8.RuneCountInString(item.CandidateID) > maxProviderImportCandidateIDCharacters {
		return fmt.Errorf(
			"items[%d].candidate_id must not exceed %d characters",
			index,
			maxProviderImportCandidateIDCharacters,
		)
	}
	return nil
}

func normalizeProviderImportCommitAction(item *ProviderImportCommitItem, index int) error {
	switch item.Action {
	case providerImportActionCreate:
		return normalizeProviderImportCreateItem(item, index)
	case providerImportActionUpdate:
		return normalizeProviderImportUpdateItem(item, index)
	default:
		return fmt.Errorf(
			"items[%d].action must be %q or %q",
			index,
			providerImportActionCreate,
			providerImportActionUpdate,
		)
	}
}

func normalizeProviderImportCreateItem(item *ProviderImportCommitItem, index int) error {
	item.ProviderID = strings.TrimSpace(item.ProviderID)
	item.Name = strings.TrimSpace(item.Name)
	if !isValidProviderImportID(item.ProviderID) {
		return fmt.Errorf("items[%d].provider_id must contain only lowercase letters, numbers, and hyphens", index)
	}
	if len(item.ProviderID) > maxProviderImportIdentifierCharacters {
		return fmt.Errorf("items[%d].provider_id must not exceed %d characters", index, maxProviderImportIdentifierCharacters)
	}
	if item.Name == "" {
		return fmt.Errorf("items[%d].name is required for create", index)
	}
	if utf8.RuneCountInString(item.Name) > maxProviderImportNameCharacters {
		return fmt.Errorf("items[%d].name must not exceed %d characters", index, maxProviderImportNameCharacters)
	}
	if item.Priority < 0 || item.Priority > maxProviderImportRoutingValue {
		return fmt.Errorf("items[%d].priority must be between 0 and %d", index, maxProviderImportRoutingValue)
	}
	if item.Concurrency < 0 || item.Concurrency > maxProviderImportRoutingValue {
		return fmt.Errorf("items[%d].concurrency must be between 0 and %d", index, maxProviderImportRoutingValue)
	}
	return nil
}

func normalizeProviderImportUpdateItem(item *ProviderImportCommitItem, index int) error {
	item.ProviderID = strings.TrimSpace(item.ProviderID)
	if item.ProviderID == "" {
		return fmt.Errorf("items[%d].provider_id is required for update", index)
	}
	if utf8.RuneCountInString(item.ProviderID) > maxProviderImportIdentifierCharacters {
		return fmt.Errorf("items[%d].provider_id must not exceed %d characters", index, maxProviderImportIdentifierCharacters)
	}
	// Update intentionally ignores provider settings: the explicit action only
	// rotates credentials and preserves the provider's routing configuration.
	return nil
}

func indexProviderImportBindings(providers []model.Provider) map[string]providerImportBinding {
	bindings := make(map[string]providerImportBinding)
	for i := range providers {
		provider := &providers[i]
		if provider.Credential == nil || provider.Credential.BindingAccountID == nil {
			continue
		}
		accountID := strings.TrimSpace(*provider.Credential.BindingAccountID)
		if accountID == "" {
			continue
		}
		bindings[accountID] = providerImportBinding{provider: provider, version: provider.Credential.Version}
	}
	return bindings
}

func bindingForPreviewItem(
	bindings map[string]providerImportBinding,
	auth *providerauth.ProviderAuthView,
) (providerImportBinding, bool) {
	if auth == nil {
		return providerImportBinding{}, false
	}
	binding, ok := bindings[strings.TrimSpace(auth.AccountID)]
	return binding, ok
}

func normalizedProviderImportGroupID(groupID *string) *string {
	if groupID == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*groupID)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func providerImportDisplayName(name string, auth *providerauth.ProviderAuthView, sourceIndex int) string {
	if trimmed := strings.TrimSpace(name); trimmed != "" {
		return boundedProviderImportText(trimmed, maxProviderImportNameCharacters)
	}
	if auth != nil && strings.TrimSpace(auth.Email) != "" {
		return boundedProviderImportText(auth.Email, maxProviderImportNameCharacters)
	}
	return fmt.Sprintf("ChatGPT Account %d", sourceIndex+1)
}

type providerImportIDAllocator struct {
	used       map[string]struct{}
	nextSuffix map[string]int
}

func newProviderImportIDAllocator(providers []model.Provider, pending int) *providerImportIDAllocator {
	allocator := &providerImportIDAllocator{
		used:       make(map[string]struct{}, len(providers)+pending),
		nextSuffix: make(map[string]int, pending),
	}
	for i := range providers {
		allocator.used[providers[i].ID] = struct{}{}
	}
	return allocator
}

func (a *providerImportIDAllocator) allocate(name string, auth *providerauth.ProviderAuthView) string {
	base := providerImportUnreservedID(name, auth)
	if len(base) > maxProviderImportGeneratedIDBaseLength {
		base = strings.TrimRight(base[:maxProviderImportGeneratedIDBaseLength], "-")
	}
	if _, exists := a.used[base]; !exists {
		a.used[base] = struct{}{}
		a.nextSuffix[base] = 2
		return base
	}
	suffix := a.nextSuffix[base]
	if suffix < 2 {
		suffix = 2
	}
	for {
		candidate := fmt.Sprintf("%s-%d", base, suffix)
		suffix++
		if _, exists := a.used[candidate]; exists {
			continue
		}
		a.used[candidate] = struct{}{}
		a.nextSuffix[base] = suffix
		return candidate
	}
}

func providerImportUnreservedID(name string, auth *providerauth.ProviderAuthView) string {
	base := providerImportSlug(name)
	if base == "" && auth != nil {
		base = providerImportSlug(strings.SplitN(auth.Email, "@", 2)[0])
	}
	if base == "" {
		return providerImportFallbackID
	}
	return base
}
