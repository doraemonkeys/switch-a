package proxy

import (
	"encoding/json"
	"errors"

	"github.com/doraemonkeys/switch-a/internal/attemptevidence"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis"

	"go.uber.org/zap"
)

// Evidence schema version bumped whenever `transport` or any sibling key
// changes shape. The frontend renderer keys off `v` to route between v1 and
// v2 handlers; emitting `"v": 2` unconditionally on new rows lets the two
// layers deploy independently.
const nonWebSocketEvidenceSchemaVersion = 2

// nonWebSocketEvidence is the JSON shape written to
// `request_logs.session_evidence_json` / `request_attempts.attempt_evidence_json`
// for SSE and plain-HTTP requests. Mirrors the WS evidence schema so the
// frontend can share the `transport` renderer across both protocols.
//
// Fields outside `transport` intentionally stay optional; the non-WS path
// does not yet populate a `gateway` / `upstream_event` / `upstream_handshake`
// sub-object, so omitempty leaves the JSON tight. New optional keys can be
// added without bumping the schema version as long as renderers tolerate
// unknown fields — only breaking shape changes warrant a version bump.
type nonWebSocketEvidence struct {
	Version   int                           `json:"v"`
	Transport *nonWebSocketTransportPayload `json:"transport,omitempty"`
}

// nonWebSocketTransportPayload is the projection of `transportDiagnostic`
// onto the wire. SSE does not use `close_code` / `close_reason_snippet`
// (those are WS-only), so they are absent here by construction — adding
// them as zero-value fields would be a wire-contract mistake.
type nonWebSocketTransportPayload struct {
	Source          string `json:"source"`
	Stage           string `json:"stage"`
	Kind            string `json:"kind"`
	Signal          string `json:"signal"`
	RawErrorSnippet string `json:"raw_error_snippet,omitempty"`
}

// buildNonWebSocketSessionEvidence converts the session-level observation
// into an `session_evidence_json` payload. Returns nil when the derivation
// decides the observation has no transport fact to report (pure client
// cancel, status failover, non-SSE success, etc.) — the caller then leaves
// the DB column NULL, which keeps `session_evidence_json` clean of
// noise-only rows.
func buildNonWebSocketSessionEvidence(facts nonWebSocketRuntimeFacts) *string {
	return marshalNonWebSocketEvidence(deriveNonWebSocketTransportDiagnostic(facts), facts.InjectedCredential)
}

// buildNonWebSocketAttemptEvidence produces the attempt-level evidence. It
// is a sibling of buildWebSocketAttemptEvidence — callers invoke it
// explicitly after recordAttempt rather than folding it into recordAttempt
// itself, so the HTTP attempt abstraction stays protocol-agnostic.
func buildNonWebSocketAttemptEvidence(facts nonWebSocketRuntimeFacts) *string {
	return marshalNonWebSocketEvidence(deriveNonWebSocketTransportDiagnostic(facts), facts.InjectedCredential)
}

// deriveNonWebSocketTransportDiagnostic adapts `nonWebSocketRuntimeFacts` to
// the foundation-level `transportObservation` and delegates to the shared
// `deriveTransportDiagnostic`. It is the single adapter point so the SSE
// path never writes its own classification table.
//
// Non-SSE traffic bypasses derivation entirely — plain HTTP requests carry
// no streaming transport signal worth logging to the evidence column. If a
// future non-SSE protocol needs its own observation, add a sibling adapter
// instead of extending this one (YAGNI and orthogonality beat over-fitting).
func deriveNonWebSocketTransportDiagnostic(facts nonWebSocketRuntimeFacts) *transportDiagnostic {
	if !facts.IsSSE || errors.Is(facts.TerminalErr, errClientDisguiseFailed) {
		return nil
	}
	return deriveTransportDiagnostic(transportObservation{
		protocol: transportProtocolSSE,
		err:      facts.TerminalErr,
		ctxErr:   facts.CtxErr,
		sse: sseObservation{
			firstByteVisible:   facts.FirstByteVisible,
			headerCommitted:    facts.ResponseCommitted,
			isStatusFailover:   facts.IsStatusFailover,
			isClientWriteError: facts.IsClientWriteError,
		},
	})
}

func marshalNonWebSocketEvidence(diag *transportDiagnostic, injectedCredential string) *string {
	if diag == nil {
		return nil
	}
	// The derivation function preserves raw fact text so unit tests can
	// round-trip it. Serialization is the single boundary where the explicitly
	// injected switch-a credential is replaced; all other provider diagnostics,
	// URLs, and token-shaped values remain available for debugging.
	diag.RawErrorSnippet = sanitizeEvidenceSnippet(diag.RawErrorSnippet, injectedCredential)
	payload := nonWebSocketEvidence{
		Version: nonWebSocketEvidenceSchemaVersion,
		Transport: &nonWebSocketTransportPayload{
			Source:          diag.Source,
			Stage:           diag.Stage,
			Kind:            diag.Kind,
			Signal:          diag.Signal,
			RawErrorSnippet: diag.RawErrorSnippet,
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	s := string(encoded)
	return &s
}

// sanitizeEvidenceSnippet keeps transport and semantic evidence on one
// explicit-key boundary, so diagnostics do not drift into separate heuristics.
func sanitizeEvidenceSnippet(value, injectedCredential string) string {
	return attemptevidence.SanitizeSnippet(value, injectedCredential)
}

// attachHTTPAttemptEvidence is the single HTTP attempt-observability boundary.
// The runtime hands it frozen values only, so persistence and trace emission can
// never retain response bodies, analyzer observations, or provider leases.
func (h *Handler) attachHTTPAttemptEvidence(
	pctx *proxyContext,
	result forwardResult,
	facts nonWebSocketRuntimeFacts,
) {
	if pctx == nil || len(pctx.attempts) == 0 {
		return
	}
	attempt := &pctx.attempts[len(pctx.attempts)-1]
	defer func() { attempt.AttemptEvidenceJSON = h.mergeDisguiseEvidence(pctx, attempt.AttemptEvidenceJSON) }()
	applyHTTPAttemptAxes(attempt, result)

	transportEvidence := buildNonWebSocketAttemptEvidence(facts)
	if result.semantic == nil {
		encoded, err := attemptevidence.EncodeString(transportEvidence, nil)
		if err == nil {
			attempt.AttemptEvidenceJSON = encoded
		} else {
			attempt.AttemptEvidenceJSON = transportEvidence
		}
		return
	}

	semantic, err := buildSemanticAttemptEvidence(result, facts.InjectedCredential)
	if err != nil {
		h.logSemanticEvidenceFailure(result.semantic, err)
		attempt.AttemptEvidenceJSON = transportEvidence
		return
	}
	emitSemanticTrace(h.logger, semantic, result.peakProcessBytes)
	encoded, err := attemptevidence.EncodeString(transportEvidence, &semantic)
	if err != nil {
		h.logSemanticEvidenceFailure(result.semantic, err)
		attempt.AttemptEvidenceJSON = transportEvidence
		return
	}
	attempt.AttemptEvidenceJSON = encoded
}

func applyHTTPAttemptAxes(attempt *model.RequestAttempt, result forwardResult) {
	if attempt == nil {
		return
	}
	visible := result.responseCommitted
	attempt.ResultVisibleToClient = &visible
	if visible {
		statusCode := result.statusCode
		attempt.ClientTransportStatusCode = &statusCode
	}
	outcome := classifyHTTPAttemptOutcome(result)
	attempt.Outcome = &outcome
	verdict := model.RequestAttemptHealthNeutral
	cause := model.RequestAttemptHealthCauseIncomplete
	if result.healthAvailable {
		verdict = model.RequestAttemptHealthVerdict(result.health.Verdict)
		cause = model.RequestAttemptHealthCause(result.health.Cause)
	}
	attempt.HealthVerdict = &verdict
	attempt.HealthCause = &cause
}

func classifyHTTPAttemptOutcome(result forwardResult) model.RequestAttemptOutcome {
	switch {
	case result.failureKind == attemptFailurePreparation || result.failureKind == attemptFailureDisguise:
		return model.RequestAttemptOutcomeGatewayError
	case result.failureKind == attemptFailureTransport ||
		result.failureKind == attemptFailureUpstreamNoResponse ||
		result.failureKind == attemptFailureRead:
		return model.RequestAttemptOutcomeUpstreamTransportError
	case result.failureKind == attemptFailureStatus || result.isStatusFailover:
		return model.RequestAttemptOutcomeUpstreamHTTPStatusError
	case result.semantic != nil:
		return model.RequestAttemptOutcomeUpstreamSemanticError
	case result.success:
		return model.RequestAttemptOutcomeUpstreamCompleted
	default:
		return model.RequestAttemptOutcomeUpstreamIncomplete
	}
}

func buildSemanticAttemptEvidence(result forwardResult, injectedCredential string) (attemptevidence.SemanticError, error) {
	semantic := result.semantic
	if semantic == nil {
		return attemptevidence.SemanticError{}, errors.New("semantic attempt facts are required")
	}
	state := attemptevidence.ResponseStateForwarding
	if result.discarded {
		state = attemptevidence.ResponseStateDiscarded
	} else if !result.responseCommitted && semantic.windowState == responseanalysis.StateProbing {
		state = attemptevidence.ResponseStateProbing
	}
	matchTiming := attemptevidence.MatchTimingForwarding
	if semantic.windowState == responseanalysis.StateProbing {
		matchTiming = attemptevidence.MatchTimingProbing
	}
	boundaryReason := result.boundaryReason
	if boundaryReason == "" {
		boundaryReason = semantic.releaseCause
	}
	return attemptevidence.NewSemanticError(attemptevidence.Facts{
		Identity: attemptevidence.IdentityFacts{
			RequestID: semantic.requestID, OperationID: semantic.operationID,
			ProviderID: semantic.providerID, LogicalAttempt: semantic.logicalAttempt,
			ProviderAttempt: semantic.providerAttempt, CredentialPhase: semantic.credentialPhase,
		},
		Response: attemptevidence.ResponseFacts{
			ProtocolID: semantic.protocolID, State: state, MatchTiming: matchTiming,
			BoundaryReason:         boundaryReason,
			ElapsedMilliseconds:    nonNegativeUint64(result.elapsedMs),
			PeakProbeBytes:         nonNegativeIntUint64(result.peakRequestBytes),
			RawProbeBytes:          nonNegativeUint64(result.upstreamBytes),
			DecodedProbeBytes:      nonNegativeUint64(result.decodedBytes),
			UpstreamBytesRead:      nonNegativeUint64(result.upstreamBytes),
			ClientBodyBytesWritten: nonNegativeUint64(result.responseBytes),
			HeadersCommitted:       result.responseCommitted, VisibleToClient: result.responseCommitted,
		},
		Rule: attemptevidence.RuleFacts{
			Revision: semantic.revision, Winner: semantic.winner, Matches: semantic.matches,
		},
		Retry: attemptevidence.RetryFacts{
			GlobalAttemptsStarted:   semantic.globalAttemptsStarted,
			GlobalAttemptsRemaining: semantic.globalAttemptsRemaining,
			GlobalAttemptsUnlimited: semantic.globalAttemptsUnlimited,
			RuleRetriesScheduled:    semantic.ruleRetriesScheduled, RuleRetryLimit: semantic.ruleRetryLimit,
		},
		Alternate: attemptevidence.AlternateFacts{
			Outcome: semantic.alternateOutcome, ProviderID: semantic.alternateProviderID,
			SwitchMode: semantic.alternateSwitchMode, SwitchReason: semantic.alternateSwitchReason,
		},
		Decision: semantic.decision,
		Health: attemptevidence.HealthFacts{
			Assessment: result.health, CircuitOpened: result.healthCircuitOpened,
		},
	}, injectedCredential)
}

func nonNegativeUint64(value int64) uint64 {
	if value <= 0 {
		return 0
	}
	return uint64(value)
}

func nonNegativeIntUint64(value int) uint64 {
	if value <= 0 {
		return 0
	}
	return uint64(value)
}

func (h *Handler) logSemanticEvidenceFailure(semantic *semanticAttemptFacts, err error) {
	if h == nil || h.logger == nil {
		return
	}
	fields := []zap.Field{zap.Error(err)}
	if semantic != nil {
		fields = append(fields,
			zap.String("request_id", semantic.requestID),
			zap.String("operation_id", semantic.operationID),
			zap.String("provider_id", semantic.providerID),
			zap.String("rule_id", string(semantic.winner.Rule.ID)),
		)
	}
	h.logger.Error("internal-error attempt evidence unavailable", fields...)
}

func emitSemanticTrace(logger *zap.Logger, semantic attemptevidence.SemanticError, peakProcessBytes int) {
	if logger == nil {
		return
	}
	for _, event := range attemptevidence.TraceEvents(semantic) {
		logger.Debug(string(event.Name), semanticTraceFields(event.Semantic, peakProcessBytes)...)
	}
}

func semanticTraceFields(semantic attemptevidence.SemanticError, peakProcessBytes int) []zap.Field {
	remaining := ""
	if semantic.Retry.GlobalAttemptsRemaining != nil {
		remaining = *semantic.Retry.GlobalAttemptsRemaining
	}
	return []zap.Field{
		zap.String("request_id", semantic.Identity.RequestID),
		zap.String("operation_id", semantic.Identity.OperationID),
		zap.String("provider_id", semantic.Identity.ProviderID),
		zap.String("logical_attempt", semantic.Identity.LogicalAttempt),
		zap.String("provider_attempt", semantic.Identity.ProviderAttempt),
		zap.String("rule_revision", semantic.Rule.Revision),
		zap.String("protocol_id", string(semantic.Response.ProtocolID)),
		zap.String("rule_id", string(semantic.Rule.WinnerID)),
		zap.String("decision", string(semantic.Decision.Value)),
		zap.String("decision_reason", string(semantic.Decision.Reason)),
		zap.String("response_state", string(semantic.Response.State)),
		zap.String("boundary_reason", string(semantic.Response.BoundaryReason)),
		zap.String("elapsed_ms", semantic.Response.ElapsedMilliseconds),
		zap.String("peak_probe_bytes", semantic.Response.PeakProbeBytes),
		zap.Uint64("peak_process_bytes", nonNegativeIntUint64(peakProcessBytes)),
		zap.String("raw_probe_bytes", semantic.Response.RawProbeBytes),
		zap.String("decoded_probe_bytes", semantic.Response.DecodedProbeBytes),
		zap.String("upstream_bytes_read", semantic.Response.UpstreamBytesRead),
		zap.String("client_body_bytes_written", semantic.Response.ClientBodyBytesWritten),
		zap.Bool("headers_committed", semantic.Response.HeadersCommitted),
		zap.Bool("visible_to_client", semantic.Response.VisibleToClient),
		zap.String("global_attempts_started", semantic.Retry.GlobalAttemptsStarted),
		zap.String("global_attempts_remaining", remaining),
		zap.Bool("global_attempts_unlimited", semantic.Retry.GlobalAttemptsUnlimited),
		zap.String("rule_retries_scheduled", semantic.Retry.RuleRetriesScheduled),
		zap.Int("rule_retry_limit", semantic.Retry.RuleRetryLimit),
		zap.String("health_verdict", string(semantic.Health.Verdict)),
		zap.String("health_cause", string(semantic.Health.Cause)),
		zap.Bool("circuit_opened", semantic.Health.CircuitOpened),
	}
}
