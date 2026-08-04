package websocketproxy

import (
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/selector"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

type providerSwitchMode = model.SwitchMode

const (
	providerSwitchModeInitial     = model.SwitchModeInitial
	providerSwitchModeReplacement = model.SwitchModeReplacement
	providerSwitchModeFailover    = model.SwitchModeFailover
)

// providerSwitchTracker owns request-local continuity state. The selector sees
// explicit switch semantics, while transport attempts only report lifecycle
// facts and cannot reconstruct routing intent from retry counters.
type providerSwitchTracker struct {
	selectReq           *model.SelectRequest
	maxProviderSwitches int
	seedStore           model.VisibleContinuitySeedStore
	nextMode            model.SwitchMode
	switchHistory       *model.ProviderSwitchHistory
	continuityContext   *model.ProviderContinuityContext
	continuityCandidate *model.VisibleContinuitySeedCandidate
}

func newProviderSwitchTracker(req *model.SelectRequest, totalAttempts int, store model.VisibleContinuitySeedStore) providerSwitchTracker {
	tracker := providerSwitchTracker{
		selectReq: req, maxProviderSwitches: max(0, totalAttempts-1),
		seedStore: store, nextMode: model.SwitchModeInitial,
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
	candidate, ok := t.seedStore.Lookup(selector.BuildContinuityKey(t.selectReq))
	if ok {
		t.continuityCandidate = candidate
	}
	t.syncRequest()
	return ok
}

func (t *providerSwitchTracker) recordSelection(provider *model.Provider, metadata selector.SelectionMetadata) model.SwitchMode {
	if t == nil {
		return model.SwitchModeInitial
	}
	mode := t.currentMode()
	first := t.switchHistory == nil
	if provider == nil {
		t.nextMode = model.SwitchModeInitial
		t.syncRequest()
		return mode
	}
	if first {
		t.switchHistory = model.NewProviderSwitchHistory(provider)
	} else {
		t.switchHistory.RecordProvider(provider)
	}
	if first && t.continuityCandidate != nil && !t.attachVisibleContinuityCandidate(provider, metadata) {
		t.continuityCandidate = nil
	}
	if mode == model.SwitchModeFailover && t.continuityContext != nil {
		t.continuityContext.ObserveProvider(provider)
	}
	t.nextMode = model.SwitchModeInitial
	t.syncRequest()
	return mode
}

func (t *providerSwitchTracker) attachVisibleContinuityCandidate(provider *model.Provider, metadata selector.SelectionMetadata) bool {
	if t == nil || t.seedStore == nil || t.continuityCandidate == nil || provider == nil ||
		!metadata.UsesContinuity() || t.continuityCandidate.OriginProviderID == "" ||
		provider.ID != t.continuityCandidate.OriginProviderID {
		return false
	}
	seed, ok := t.seedStore.CompareAndConsume(t.continuityCandidate.ContinuityKey, t.continuityCandidate.SeedID)
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
	vendors := append([]string(nil), t.continuityContext.ContaminatedVendors...)
	if len(vendors) == 0 && t.continuityContext.VisibleOriginVendor != "" {
		vendors = append(vendors, t.continuityContext.VisibleOriginVendor)
	}
	scope := t.continuityContext.StrictestScope
	if scope == "" {
		scope = model.ScopeAny
	}
	return &model.VisibleContinuitySeed{
		SeedID: uuid.NewString(), ContinuityKey: key,
		OriginProviderID:    t.continuityContext.VisibleOriginProviderID,
		OriginVendor:        t.continuityContext.VisibleOriginVendor,
		ContaminatedVendors: vendors, StrictestScope: scope, ObservedAt: observedAt,
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

func selectionMetadataContinuitySeedAgeMs(metadata selector.SelectionMetadata) *int64 {
	if !metadata.ContinuitySeeded {
		return nil
	}
	age := int64(0)
	if metadata.ContinuitySeedAgeAtSelectionMs != nil {
		age = *metadata.ContinuitySeedAgeAtSelectionMs
	}
	return &age
}

func hasUsableSelectionModel(modelName string) bool {
	trimmed := strings.TrimSpace(modelName)
	return trimmed != "" && !strings.EqualFold(trimmed, ModelUnknown)
}

func newNormalizedRequestAttempt(requestID, providerID string, createdAt time.Time) model.RequestAttempt {
	return model.RequestAttempt{
		RequestID: requestID, ProviderID: providerID,
		SemanticsVersion: model.RequestSemanticsVersionNormalizedV1,
		CreatedAt:        createdAt,
	}
}

// WebSocketAttemptResult keeps provider-attempt facts separate from the final
// session row so the handler can attribute replacement, failover, health, and
// persistence to the provider that actually produced each pre-visible outcome.
type WebSocketAttemptResult struct {
	Provider            *model.Provider
	Attempt             int
	SelectionMode       providerSwitchMode
	SelectionMetadata   selector.SelectionMetadata
	ProviderAttempt     int
	ProviderSwitchCount int
	Result              *WebSocketResult
	ForwardErr          error
	LatencyMs           int64
	SwitchReason        string
	CreatedAt           time.Time
	GatewayStatusCode   int
	GatewayErrorCode    string
	GatewayMessage      string
	RecoveryAttempted   bool
	RecoverySucceeded   bool
	ReplayFailed        bool
}

type webSocketSelectionProbeOutcome = model.WebSocketProbeOutcome

const (
	webSocketSelectionProbeOutcomeUnknown                     webSocketSelectionProbeOutcome = model.WebSocketProbeOutcomeUnknown
	webSocketSelectionProbeOutcomeBypassed                    webSocketSelectionProbeOutcome = model.WebSocketProbeOutcomeBypassed
	webSocketSelectionProbeOutcomeDemandResolutionFailed      webSocketSelectionProbeOutcome = model.WebSocketProbeOutcomeDemandResolutionFailed
	webSocketSelectionProbeOutcomeUnsupported                 webSocketSelectionProbeOutcome = model.WebSocketProbeOutcomeUnsupported
	webSocketSelectionProbeOutcomeObservedUsableModel         webSocketSelectionProbeOutcome = model.WebSocketProbeOutcomeObservedUsableModel
	webSocketSelectionProbeOutcomeCompletedWithoutUsableModel webSocketSelectionProbeOutcome = model.WebSocketProbeOutcomeCompletedWithoutUsableModel
	webSocketSelectionProbeOutcomeTransportFailed             webSocketSelectionProbeOutcome = model.WebSocketProbeOutcomeTransportFailed
)

func (r WebSocketAttemptResult) clientAccepted() bool {
	if r.Result == nil {
		return false
	}
	if r.Result.ClientAccepted {
		return true
	}
	return r.Result.HandshakeAccepted && r.ForwardErr == nil
}

func (r WebSocketAttemptResult) terminalErr() error {
	if r.ForwardErr != nil {
		return r.ForwardErr
	}
	if r.Result != nil {
		return r.Result.Err
	}
	return nil
}

func (r WebSocketAttemptResult) statusCode() int {
	return webSocketAttemptTransportStatusCode(r.Result)
}

func (r WebSocketAttemptResult) bodySnippet() string {
	if r.Result == nil {
		return ""
	}
	if r.Result.HandshakeBodySnippet != "" {
		return r.Result.HandshakeBodySnippet
	}
	if r.Result.UpstreamError != nil {
		return r.Result.UpstreamError.Raw
	}
	return ""
}

func (r WebSocketAttemptResult) phase() *model.RequestAttemptPhase {
	phase := model.RequestAttemptPhasePreAccept
	if r.Result != nil {
		switch {
		case r.Result.ClientVisible:
			phase = model.RequestAttemptPhaseVisible
		case r.Result.ClientAccepted || r.Result.HandshakeAccepted:
			phase = model.RequestAttemptPhasePostUpgradePreVisible
		}
	}
	return &phase
}

func (r WebSocketAttemptResult) outcome() *model.RequestAttemptOutcome {
	outcome := model.RequestAttemptOutcomeUpstreamTransportError
	if r.Result == nil {
		return &outcome
	}

	switch {
	case r.Result.ClientVisible || r.Result.SessionCommitted:
		outcome = model.RequestAttemptOutcomeVisibleSession
	case r.Result.UpstreamError != nil || r.Result.TerminalCause == model.TerminalUpstreamSemanticError:
		outcome = model.RequestAttemptOutcomeUpstreamSemanticError
	case r.Result.TerminalCause == model.TerminalUpstreamHandshakeRejected:
		outcome = model.RequestAttemptOutcomeUpstreamHandshakeRejected
	case r.Result.TerminalCause == model.TerminalProviderConfigurationError,
		r.Result.TerminalCause == model.TerminalCleanClose,
		r.Result.TerminalCause == model.TerminalUpstreamTransportError,
		r.ForwardErr != nil:
		outcome = model.RequestAttemptOutcomeUpstreamTransportError
	case !r.Result.HandshakeAccepted:
		outcome = model.RequestAttemptOutcomeUpstreamHandshakeRejected
	default:
		return nil
	}
	return &outcome
}

func (r WebSocketAttemptResult) resultVisibleToClient() *bool {
	visible := r.Result != nil && r.Result.ClientVisible
	return &visible
}

func (r WebSocketAttemptResult) shouldReplaceBeforeClientVisible() bool {
	if r.Result == nil || r.Result.ClientVisible {
		return false
	}

	switch r.Result.TerminalCause {
	case model.TerminalUpstreamHandshakeRejected,
		model.TerminalUpstreamTransportError,
		model.TerminalProviderConfigurationError:
		return true
	default:
		return false
	}
}

// WebSocketSessionResult is the handler-owned aggregate that survives provider
// switches. The runtime worker can extend this with post-upgrade visibility
// boundaries later without changing the pre-visible orchestration contract.
type WebSocketSessionResult struct {
	RequestID         string
	FinalProvider     *model.Provider
	FinalResult       *WebSocketResult
	FinalErr          error
	Attempts          []WebSocketAttemptResult
	IsSticky          bool
	StickyWritten     bool
	ClientAccepted    bool
	ResolvedModel     string
	ProbeOutcome      webSocketSelectionProbeOutcome
	GatewayStatusCode int
	GatewayErrorCode  string
	GatewayMessage    string
	// syntheticFinalFromSuppressedPayload marks sessions produced by the
	// replaced-attempt suppressed-payload path. Session-level evidence
	// derivation consults it as a second barrier on top of the structural
	// TransportObservation zero-out, ensuring a replaced attempt's transport
	// fact cannot attach to the synthetic final session even if some future
	// code path reintroduces a non-zero observation into the gateway result.
	syntheticFinalFromSuppressedPayload bool
}

func (r *WebSocketSessionResult) RetryCount() int {
	if len(r.Attempts) <= 1 {
		return 0
	}
	return len(r.Attempts) - 1
}

func (r *WebSocketSessionResult) RequestAttempts() []model.RequestAttempt {
	if r == nil || len(r.Attempts) == 0 {
		return nil
	}

	attempts := make([]model.RequestAttempt, 0, len(r.Attempts))
	for _, attempt := range r.Attempts {
		if attempt.Provider == nil {
			continue
		}

		providerAttempt := attempt.ProviderAttempt
		if providerAttempt <= 0 {
			providerAttempt = 1
		}

		record := newNormalizedRequestAttempt(r.RequestID, attempt.Provider.ID, attempt.CreatedAt)
		record.Attempt = attempt.Attempt
		record.SwitchMode = requestAttemptSwitchMode(attempt.SelectionMode)
		record.ProviderAttempt = providerAttempt
		record.ProviderSwitchCount = attempt.ProviderSwitchCount
		record.StatusCode = attempt.statusCode()
		record.Error = errorString(attempt.terminalErr())
		record.Phase = attempt.phase()
		record.Outcome = attempt.outcome()
		record.ResultVisibleToClient = attempt.resultVisibleToClient()
		record.AttemptEvidenceJSON = buildWebSocketAttemptEvidence(attempt)
		record.BodySnippet = attempt.bodySnippet()
		record.LatencyMs = attempt.LatencyMs
		record.SwitchReason = attempt.SwitchReason
		record.ContinuitySeeded = attempt.SelectionMetadata.ContinuitySeeded
		record.ContinuityOriginProviderID = attempt.SelectionMetadata.ContinuityOriginProviderID
		record.ContinuitySeedAgeMs = selectionMetadataContinuitySeedAgeMs(attempt.SelectionMetadata)
		attempts = append(attempts, record)
	}

	return attempts
}

type webSocketSuppressedAttempt struct {
	provider      *model.Provider
	messageType   websocket.MessageType
	payload       []byte
	upstreamError *WebSocketUpstreamError
}
