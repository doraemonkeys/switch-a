package proxy

import (
	"encoding/json"
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
	return marshalNonWebSocketEvidence(deriveNonWebSocketTransportDiagnostic(facts))
}

// buildNonWebSocketAttemptEvidence produces the attempt-level evidence. It
// is a sibling of buildWebSocketAttemptEvidence — callers invoke it
// explicitly after recordAttempt rather than folding it into recordAttempt
// itself, so the HTTP attempt abstraction stays protocol-agnostic.
func buildNonWebSocketAttemptEvidence(facts nonWebSocketRuntimeFacts) *string {
	return marshalNonWebSocketEvidence(deriveNonWebSocketTransportDiagnostic(facts))
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
	if !facts.IsSSE {
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

func marshalNonWebSocketEvidence(diag *transportDiagnostic) *string {
	if diag == nil {
		return nil
	}
	// Redaction lives in the evidence layer (see transport_diagnostic.go doc
	// comment on `truncateRawErrorSnippet`): the derivation function
	// preserves raw fact text so unit tests can round-trip it, and every
	// serializer is responsible for scrubbing secrets before the snippet
	// hits the DB. SSE wrappers like `UpstreamReadError` readily quote
	// upstream URLs with query-string credentials, so skipping this step
	// leaks `api_key=...` verbatim into `attempt_evidence_json`.
	diag.RawErrorSnippet = sanitizeEvidenceSnippet(diag.RawErrorSnippet)
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

// attachSSEAttemptEvidence writes an `attempt_evidence_json` onto the most
// recently appended attempt in pctx.attempts. It mirrors the WS pattern
// (buildWebSocketAttemptEvidence) and is intentionally a sibling to
// recordAttempt rather than folded inside it: recordAttempt stays a thin
// persistence helper for HTTP-level fields, and evidence derivation lives
// on the observation-aware caller path.
//
// The helper is a no-op when no attempt has been recorded yet (defensive
// against accidental call order bugs) and when the derivation returns nil
// (nothing to report). It uses slice-tail mutation rather than an explicit
// attempt handle because pctx.attempts is batched-inserted at finalizeProxy
// time — there is no DB-assigned ID yet. Using the tail index matches how
// recordAttempt appends and keeps the contract "call attach immediately
// after recordAttempt for the same logical attempt."
func attachSSEAttemptEvidence(pctx *proxyContext, facts nonWebSocketRuntimeFacts) {
	if pctx == nil || len(pctx.attempts) == 0 {
		return
	}
	payload := buildNonWebSocketAttemptEvidence(facts)
	if payload == nil {
		return
	}
	pctx.attempts[len(pctx.attempts)-1].AttemptEvidenceJSON = payload
}
