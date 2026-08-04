package providerimport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/providerauth"
	"github.com/doraemonkeys/switch-a/internal/store"

	"go.uber.org/zap"
)

// CommitProviderImport handles POST /admin/api/provider-imports/{import_id}/commit.
// Every selected operation is translated into one store bundle so a stale row,
// group, binding, or credential version rolls back the entire selection.
func (h *Handler) CommitProviderImport(w http.ResponseWriter, r *http.Request) {
	startedAt := time.Now()
	setProviderImportResponseHeaders(w)
	prepared, ok := h.prepareProviderImportCommit(w, r)
	if !ok {
		return
	}
	importID := prepared.importID
	req := prepared.request
	fingerprint := prepared.fingerprint
	if h.replayDurableProviderImport(w, r, importID, fingerprint, "lookup") {
		return
	}
	if err := h.providerImportReceipts.acquire(r.Context(), importID, fingerprint); err != nil {
		writeError(w, http.StatusRequestTimeout, ErrCodeConflict, "Timed out waiting for the in-progress import commit")
		return
	}
	inFlightOwned := true
	defer func() {
		if inFlightOwned {
			h.providerImportReceipts.abort(importID, fingerprint)
		}
	}()
	if h.replayDurableProviderImport(w, r, importID, fingerprint, "recheck") {
		return
	}

	allCandidates, err := h.providerImports.ClaimChatGPTProviderImport(importID)
	if err != nil {
		h.writeProviderImportDraftError(w, err, "Unable to claim provider import")
		return
	}
	claimOwned := true
	defer func() {
		if claimOwned {
			if releaseErr := h.providerImports.ReleaseChatGPTProviderImportClaim(importID); releaseErr != nil {
				h.logProviderImportError("provider import claim release failed", importID, releaseErr)
			}
		}
	}()

	candidates, err := indexProviderImportCandidates(req.Items, allCandidates)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, err.Error())
		return
	}
	bundle, selectedResults, err := h.buildProviderImportBundle(req, candidates)
	if err != nil {
		var conflict *store.ProviderImportConflictError
		if errors.As(err, &conflict) {
			writeProviderImportConflict(w, conflict)
			return
		}
		writeError(w, http.StatusBadRequest, ErrCodeValidation, err.Error())
		return
	}
	if err := h.verifyProviderImportCommitCandidates(r.Context(), req.Items, candidates); err != nil {
		h.logProviderImportError("provider import credential verification failed", importID, err)
		writeProviderImportVerificationError(w, err)
		return
	}

	response := buildProviderImportCommitResponse(importID, allCandidates, selectedResults)
	responsePayload, err := json.Marshal(response)
	if err != nil { // coverage-ignore -- response contains only JSON-native scalar fields
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to encode provider import result")
		return
	}
	responsePayload = append(responsePayload, '\n')
	bundle.Receipt = &store.ProviderImportReceipt{
		ImportID:        importID,
		Fingerprint:     fingerprint,
		ResponsePayload: append([]byte(nil), responsePayload...),
		ExpiresAt:       h.providerImportReceipts.clock.Now().Add(h.providerImportReceipts.ttl),
	}

	committedPayload, committed := h.commitProviderImportAtLifecycleBoundary(
		w, r.Context(), importID, bundle, responsePayload,
	)
	if !committed {
		return
	}

	// The durable receipt is visible before local waiters resume, so every waiter
	// rechecks authoritative state instead of retaining another response copy.
	h.providerImportReceipts.complete(importID, fingerprint)
	inFlightOwned = false
	if err := h.providerImports.FinalizeChatGPTProviderImport(importID); err != nil {
		h.logProviderImportError("provider import committed but draft cleanup failed", importID, err)
	} else {
		claimOwned = false
	}
	h.logProviderImportCommitted(importID, response.Summary, startedAt)
	writeProviderImportPayload(w, http.StatusOK, committedPayload)
}

type preparedProviderImportCommit struct {
	importID    string
	request     ProviderImportCommitRequest
	fingerprint string
}

func (h *Handler) prepareProviderImportCommit(
	w http.ResponseWriter,
	r *http.Request,
) (preparedProviderImportCommit, bool) {
	if h.providerImports == nil || h.providerImportStore == nil {
		writeError(w, http.StatusNotImplemented, ErrCodeInternal, "Provider import is unavailable in this build")
		return preparedProviderImportCommit{}, false
	}

	importID := strings.TrimSpace(r.PathValue("import_id"))
	if importID == "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "import_id is required")
		return preparedProviderImportCommit{}, false
	}
	var req ProviderImportCommitRequest
	if err := decodeProviderImportCommitRequest(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, err.Error())
		return preparedProviderImportCommit{}, false
	}
	if err := validateProviderImportCommitRequest(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, err.Error())
		return preparedProviderImportCommit{}, false
	}

	return preparedProviderImportCommit{
		importID:    importID,
		request:     req,
		fingerprint: providerImportCommitRequestFingerprint(req),
	}, true
}

func (h *Handler) verifyProviderImportCommitCandidates(
	ctx context.Context,
	items []ProviderImportCommitItem,
	candidates map[string]providerauth.ChatGPTProviderImportCandidate,
) error {
	selected := make([]providerauth.ChatGPTProviderImportCandidate, 0, len(items))
	for i := range items {
		selected = append(selected, candidates[items[i].CandidateID])
	}
	return h.providerImports.VerifyChatGPTProviderImportCandidates(ctx, selected)
}

func (h *Handler) logProviderImportCommitted(
	importID string,
	summary ProviderImportCommitSummary,
	startedAt time.Time,
) {
	if h.logger == nil {
		return
	}
	h.logger.Info("provider import committed",
		zap.String("import_id", importID),
		zap.Int("created", summary.Created),
		zap.Int("updated", summary.Updated),
		zap.Int("skipped", summary.Skipped),
		zap.Duration("duration", time.Since(startedAt)),
	)
}

func writeProviderImportVerificationError(w http.ResponseWriter, err error) {
	if errors.Is(err, providerauth.ErrChatGPTProviderImportJWKSUnavailable) {
		w.Header().Set("Retry-After", providerImportRetryAfter)
		writeErrorWithDetails(w, http.StatusServiceUnavailable, ErrCodeInternal,
			"Credential signing keys are temporarily unavailable; retry shortly",
			map[string]string{"kind": "provider_import_signing_keys_unavailable"})
		return
	}
	var verificationError *providerauth.ChatGPTProviderImportVerificationError
	if errors.As(err, &verificationError) {
		writeErrorWithDetails(w, http.StatusUnprocessableEntity, ErrCodeValidation,
			"A selected credential could not be verified",
			map[string]string{
				"kind":         "provider_import_token_verification_failed",
				"candidate_id": verificationError.CandidateID,
			})
		return
	}
	writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to verify selected credentials")
}

func (h *Handler) replayDurableProviderImport(
	w http.ResponseWriter,
	r *http.Request,
	importID string,
	fingerprint string,
	phase string,
) bool {
	handled, err := h.writeDurableProviderImportReplay(w, r.Context(), importID, fingerprint)
	if err == nil {
		return handled
	}
	h.logProviderImportError("provider import receipt "+phase+" failed", importID, err)
	writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to inspect provider import result")
	return true
}

func indexProviderImportCandidates(
	items []ProviderImportCommitItem,
	allCandidates []providerauth.ChatGPTProviderImportCandidate,
) (map[string]providerauth.ChatGPTProviderImportCandidate, error) {
	candidates := make(map[string]providerauth.ChatGPTProviderImportCandidate, len(allCandidates))
	for i := range allCandidates {
		candidates[allCandidates[i].CandidateID] = allCandidates[i]
	}
	for i := range items {
		if _, ok := candidates[items[i].CandidateID]; !ok {
			return nil, errors.New("Unknown candidate_id: " + items[i].CandidateID)
		}
	}
	return candidates, nil
}

func (h *Handler) applyProviderImportBundle(
	w http.ResponseWriter,
	ctx context.Context,
	importID string,
	bundle *store.ProviderImportBundle,
	responsePayload []byte,
) ([]byte, bool) {
	err := h.providerImportStore.ApplyProviderImport(ctx, bundle)
	if err == nil {
		return responsePayload, true
	}

	var durableReplay *store.ProviderImportReceiptReplayError
	if errors.As(err, &durableReplay) {
		return append([]byte(nil), durableReplay.Receipt.ResponsePayload...), true
	}
	if errors.Is(err, store.ErrProviderImportReceiptConflict) {
		writeProviderImportCommitMismatch(w, importID)
		return nil, false
	}
	var conflict *store.ProviderImportConflictError
	if errors.As(err, &conflict) {
		if h.logger != nil {
			h.logger.Warn("provider import commit conflicted",
				zap.String("import_id", importID),
				zap.Int("conflict_count", len(conflict.Conflicts)),
			)
		}
		writeProviderImportConflict(w, conflict)
		return nil, false
	}
	h.logProviderImportError("provider import commit failed", importID, err)
	writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to commit provider import")
	return nil, false
}

func (h *Handler) commitProviderImportAtLifecycleBoundary(
	w http.ResponseWriter,
	ctx context.Context,
	importID string,
	bundle *store.ProviderImportBundle,
	responsePayload []byte,
) ([]byte, bool) {
	var committedPayload []byte
	committed := false
	mutation := func() error {
		providerIDs := providerImportCredentialMutationIDs(bundle)
		mutationContext, releaseCredentialMutations, err := h.providerImportStore.WithProviderCredentialMutations(
			ctx,
			providerIDs,
		)
		if err != nil {
			return err
		}
		defer releaseCredentialMutations()

		committedPayload, committed = h.applyProviderImportBundle(
			w, mutationContext, importID, bundle, responsePayload,
		)
		if committed {
			h.providerImports.InvalidateProviderCredentialSessions(providerIDs)
		}
		return nil
	}

	var err error
	if h.providerLifecycles != nil {
		err = h.providerLifecycles.RetireAllProviderGenerations(mutation)
	} else {
		err = mutation()
	}
	if err != nil {
		h.logProviderImportError("provider import credential mutation lease failed", importID, err)
		writeError(w, http.StatusRequestTimeout, ErrCodeConflict, "Timed out waiting to update provider credentials")
		return nil, false
	}
	return committedPayload, committed
}
