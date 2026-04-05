package proxy

import (
	"context"
	"strings"
	"time"

	"switch-a/internal/model"
	"switch-a/internal/selector"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type providerSwitchMode = model.SwitchMode

const (
	providerSwitchModeInitial     = model.SwitchModeInitial
	providerSwitchModeReplacement = model.SwitchModeReplacement
	providerSwitchModeFailover    = model.SwitchModeFailover
)

// providerSwitchTracker owns the request-local switch model so HTTP and
// WebSocket runtime code can pass the selector's explicit switch inputs
// directly instead of reconstructing them from legacy failover state.
type providerSwitchTracker struct {
	selectReq           *model.SelectRequest
	maxProviderSwitches int
	seedStore           model.VisibleContinuitySeedStore
	nextMode            model.SwitchMode
	switchHistory       *model.ProviderSwitchHistory
	continuityContext   *model.ProviderContinuityContext
	continuityCandidate *model.VisibleContinuitySeedCandidate
}

func newProviderSwitchTracker(
	selectReq *model.SelectRequest,
	totalAttempts int,
	seedStore model.VisibleContinuitySeedStore,
) providerSwitchTracker {
	tracker := providerSwitchTracker{
		selectReq:           selectReq,
		maxProviderSwitches: max(0, totalAttempts-1),
		seedStore:           seedStore,
		nextMode:            model.SwitchModeInitial,
	}
	tracker.syncRequest()
	return tracker
}

func (t *providerSwitchTracker) prepareSelection() model.SwitchMode {
	if t == nil {
		return model.SwitchModeInitial
	}
	t.syncRequest()
	return t.currentMode()
}

func (t *providerSwitchTracker) lookupVisibleContinuityCandidate() bool {
	if t == nil || t.seedStore == nil || t.selectReq == nil || t.continuityContext != nil || t.continuityCandidate != nil {
		return false
	}

	key := selector.BuildContinuityKey(t.selectReq)
	candidate, ok := t.seedStore.Lookup(key)
	if !ok {
		t.syncRequest()
		return false
	}

	t.continuityCandidate = candidate
	t.syncRequest()
	return true
}

func (t *providerSwitchTracker) recordSelection(
	provider *model.Provider,
	selectionMetadata selector.SelectionMetadata,
) model.SwitchMode {
	if t == nil {
		return model.SwitchModeInitial
	}

	mode := t.currentMode()
	firstSelection := t.switchHistory == nil
	if provider == nil {
		t.nextMode = model.SwitchModeInitial
		t.syncRequest()
		return mode
	}
	t.recordProviderSelection(provider, firstSelection)
	t.attachContinuityCandidateOnFirstSelection(provider, selectionMetadata, firstSelection)
	t.observeFailoverProvider(provider, mode)

	t.nextMode = model.SwitchModeInitial
	t.syncRequest()
	return mode
}

func (t *providerSwitchTracker) recordProviderSelection(provider *model.Provider, firstSelection bool) {
	if t == nil || provider == nil {
		return
	}
	if firstSelection {
		t.switchHistory = model.NewProviderSwitchHistory(provider)
		return
	}
	t.switchHistory.RecordProvider(provider)
}

func (t *providerSwitchTracker) attachContinuityCandidateOnFirstSelection(
	provider *model.Provider,
	selectionMetadata selector.SelectionMetadata,
	firstSelection bool,
) {
	if t == nil || !firstSelection || t.continuityCandidate == nil {
		return
	}
	if !t.attachVisibleContinuityCandidate(provider, selectionMetadata) {
		t.continuityCandidate = nil
	}
}

func (t *providerSwitchTracker) observeFailoverProvider(
	provider *model.Provider,
	mode model.SwitchMode,
) {
	if t == nil || provider == nil || mode != model.SwitchModeFailover || t.continuityContext == nil {
		return
	}
	t.continuityContext.ObserveProvider(provider)
}

func (t *providerSwitchTracker) attachVisibleContinuityCandidate(
	provider *model.Provider,
	selectionMetadata selector.SelectionMetadata,
) bool {
	if t == nil || t.seedStore == nil || t.continuityCandidate == nil || provider == nil {
		return false
	}
	if !selectionMetadata.UsesContinuity() || t.continuityCandidate.OriginProviderID == "" {
		return false
	}
	if provider.ID != t.continuityCandidate.OriginProviderID {
		return false
	}

	seed, ok := t.seedStore.CompareAndConsume(
		t.continuityCandidate.ContinuityKey,
		t.continuityCandidate.SeedID,
	)
	if !ok {
		return false
	}

	t.continuityContext = seed.ProviderContinuityContext()
	if t.continuityContext == nil {
		t.continuityContext = model.NewProviderContinuityContext(provider, seed.ObservedAt)
	}
	return true
}

func (t *providerSwitchTracker) markClientVisible(provider *model.Provider, observedAt time.Time) {
	if t == nil || provider == nil || t.continuityContext != nil {
		return
	}

	t.continuityContext = model.NewProviderContinuityContext(provider, observedAt)
	t.syncRequest()
}

func (t *providerSwitchTracker) prepareProviderSwitch() model.SwitchMode {
	if t == nil {
		return model.SwitchModeInitial
	}

	if t.continuityContext != nil {
		t.nextMode = model.SwitchModeFailover
	} else {
		t.nextMode = model.SwitchModeReplacement
	}
	t.syncRequest()
	return t.nextMode
}

func (t *providerSwitchTracker) currentMode() model.SwitchMode {
	if t == nil {
		return model.SwitchModeInitial
	}
	return model.NormalizeSwitchMode(t.nextMode)
}

func (t *providerSwitchTracker) providerSwitchCount() int {
	if t == nil || t.switchHistory == nil {
		return 0
	}
	return t.switchHistory.ProviderSwitchCount
}

func (t *providerSwitchTracker) visibleContinuitySeed(observedAt time.Time) *model.VisibleContinuitySeed {
	if t == nil || t.selectReq == nil || t.continuityContext == nil {
		return nil
	}
	key := selector.BuildContinuityKey(t.selectReq)
	if key.APIType == "" {
		return nil
	}

	contaminatedVendors := append([]string(nil), t.continuityContext.ContaminatedVendors...)
	if len(contaminatedVendors) == 0 && t.continuityContext.VisibleOriginVendor != "" {
		contaminatedVendors = append(contaminatedVendors, t.continuityContext.VisibleOriginVendor)
	}
	strictestScope := t.continuityContext.StrictestScope
	if strictestScope == "" {
		strictestScope = model.ScopeAny
	}

	return &model.VisibleContinuitySeed{
		SeedID:              uuid.NewString(),
		ContinuityKey:       key,
		OriginProviderID:    t.continuityContext.VisibleOriginProviderID,
		OriginVendor:        t.continuityContext.VisibleOriginVendor,
		ContaminatedVendors: contaminatedVendors,
		StrictestScope:      strictestScope,
		ObservedAt:          observedAt,
	}
}

func (t *providerSwitchTracker) syncRequest() {
	if t == nil || t.selectReq == nil {
		return
	}

	t.selectReq.SwitchMode = t.currentMode()
	t.selectReq.ProviderSwitchHistory = t.switchHistory
	t.selectReq.ProviderContinuityContext = t.continuityContext
	t.selectReq.VisibleContinuitySeedCandidate = t.continuityCandidate
	t.selectReq.FailoverContext = nil
	t.selectReq.MaxProviderSwitches = t.maxProviderSwitches
}

func requestAttemptSwitchMode(mode model.SwitchMode) model.RequestAttemptSwitchMode {
	switch model.NormalizeSwitchMode(mode) {
	case model.SwitchModeReplacement:
		return model.RequestAttemptSwitchModeReplacement
	case model.SwitchModeFailover:
		return model.RequestAttemptSwitchModeFailover
	default:
		return model.RequestAttemptSwitchModeInitial
	}
}

func selectionMetadataContinuitySeedAgeMs(
	metadata selector.SelectionMetadata,
) *int64 {
	if !metadata.ContinuitySeeded {
		return nil
	}
	ageMs := int64(0)
	if metadata.ContinuitySeedAgeAtSelectionMs != nil {
		ageMs = *metadata.ContinuitySeedAgeAtSelectionMs
	}
	return &ageMs
}

func hasUsableSelectionModel(modelName string) bool {
	trimmed := strings.TrimSpace(modelName)
	return trimmed != "" && !strings.EqualFold(trimmed, ModelUnknown)
}

func (h *Handler) maybeLookupVisibleContinuityCandidate(
	ctx context.Context,
	switchTracker *providerSwitchTracker,
) {
	if h == nil || switchTracker == nil || switchTracker.selectReq == nil {
		return
	}
	if hasUsableSelectionModel(switchTracker.selectReq.Model) {
		switchTracker.lookupVisibleContinuityCandidate()
		return
	}

	consumesHiddenModel, err := selector.ResolveSelectionHiddenModelDemand(ctx, h.store, switchTracker.selectReq)
	if err != nil {
		h.logger.Warn(
			"failed to resolve hidden-model demand for continuity seed lookup",
			zap.String("api_type", switchTracker.selectReq.APIType),
			zap.Error(err),
		)
		return
	}
	if consumesHiddenModel {
		return
	}

	switchTracker.lookupVisibleContinuityCandidate()
}

func (h *Handler) storeVisibleContinuitySeed(
	switchTracker *providerSwitchTracker,
	observedAt time.Time,
) {
	if h == nil || h.visibleContinuitySeedStore == nil || switchTracker == nil {
		return
	}
	seed := switchTracker.visibleContinuitySeed(observedAt)
	h.storeVisibleContinuitySeedFromContext(switchTracker.selectReq, switchTracker.continuityContext, observedAt, seed)
}

func (h *Handler) storeVisibleContinuitySeedFromContext(
	selectReq *model.SelectRequest,
	continuityContext *model.ProviderContinuityContext,
	observedAt time.Time,
	prebuiltSeed *model.VisibleContinuitySeed,
) {
	if h == nil || h.visibleContinuitySeedStore == nil {
		return
	}

	seed := prebuiltSeed
	if seed == nil {
		if selectReq == nil || continuityContext == nil {
			return
		}
		key := selector.BuildContinuityKey(selectReq)
		if key.APIType == "" || continuityContext.VisibleOriginProviderID == "" {
			return
		}
		contaminatedVendors := append([]string(nil), continuityContext.ContaminatedVendors...)
		if len(contaminatedVendors) == 0 && continuityContext.VisibleOriginVendor != "" {
			contaminatedVendors = append(contaminatedVendors, continuityContext.VisibleOriginVendor)
		}
		strictestScope := continuityContext.StrictestScope
		if strictestScope == "" {
			strictestScope = model.ScopeAny
		}
		seed = &model.VisibleContinuitySeed{
			SeedID:              uuid.NewString(),
			ContinuityKey:       key,
			OriginProviderID:    continuityContext.VisibleOriginProviderID,
			OriginVendor:        continuityContext.VisibleOriginVendor,
			ContaminatedVendors: contaminatedVendors,
			StrictestScope:      strictestScope,
			ObservedAt:          observedAt,
		}
	}
	h.visibleContinuitySeedStore.Store(*seed)
}

func shouldStoreHTTPVisibleContinuitySeed(state *retryState) bool {
	if state == nil || !state.responseCommitted || state.success {
		return false
	}
	if state.lastErr == nil {
		return false
	}
	return !isClientCancellation(state.lastErr)
}

func shouldStoreWebSocketVisibleContinuitySeed(session *WebSocketSessionResult) bool {
	if session == nil || session.FinalResult == nil || !session.FinalResult.ClientVisible {
		return false
	}
	switch session.FinalResult.TerminalCause {
	case model.TerminalCleanClose, model.TerminalClientDisconnect:
		return false
	default:
		return true
	}
}
