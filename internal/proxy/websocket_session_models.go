package proxy

import (
	"time"

	"switch-a/internal/model"
	"switch-a/internal/selector"

	"github.com/coder/websocket"
)

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
