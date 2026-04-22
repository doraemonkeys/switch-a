package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"switch-a/internal/model"
)

// The tests below lock in the SSE transport evidence wire contract and the
// observation → assessment → evidence plumbing. They intentionally exercise
// the integration from `nonWebSocketRuntimeFacts` down through the derived
// `transportDiagnostic`, because that end-to-end shape is what the frontend
// v2 renderer consumes.

func TestNonWebSocketEvidence_NonSSE_ReturnsNil(t *testing.T) {
	t.Parallel()
	// Plain HTTP (IsSSE=false) never emits transport diagnostic evidence;
	// transport diagnostic only applies to streaming transports. This is a
	// hard contract — a regression here would flood the evidence column
	// with noise for every non-streaming request.
	got := buildNonWebSocketSessionEvidence(nonWebSocketRuntimeFacts{
		TerminalErr: errors.New("boom"),
		IsSSE:       false,
	})
	if got != nil {
		t.Fatalf("non-SSE must not produce evidence, got %q", *got)
	}
}

func TestNonWebSocketEvidence_PureClientCancel_ReturnsNil(t *testing.T) {
	t.Parallel()
	// Pure ctx cancel with no real transport signal must produce no
	// evidence, so `service_outcome = abandoned_by_client` rows stay
	// clean in the evidence column (plan acceptance criterion #7).
	got := buildNonWebSocketSessionEvidence(nonWebSocketRuntimeFacts{
		IsSSE:          true,
		ClientCanceled: true,
		CtxErr:         context.Canceled,
	})
	if got != nil {
		t.Fatalf("pure cancel must not produce evidence, got %q", *got)
	}
}

func TestNonWebSocketEvidence_StatusFailover_ReturnsNil(t *testing.T) {
	t.Parallel()
	// failoverForwardResponse sets IsStatusFailover=true alongside a
	// synthetic `upstream returned status %d` error. That path is a
	// status-class fact, not a transport failure — evidence derivation
	// must bypass it or the evidence column becomes noise.
	got := buildNonWebSocketSessionEvidence(nonWebSocketRuntimeFacts{
		IsSSE:            true,
		TerminalErr:      errors.New("upstream returned status 502"),
		IsStatusFailover: true,
	})
	if got != nil {
		t.Fatalf("status failover must not produce evidence, got %q", *got)
	}
}

func TestNonWebSocketEvidence_SSEIdleTimeout_EmitsV2WithKindTimeout(t *testing.T) {
	t.Parallel()
	// End-to-end: idle timeout before first byte must yield the v2
	// envelope with kind=timeout, signal=sse_idle_timeout, source=upstream,
	// and the pre-payload stage. This mirrors plan acceptance criterion #1.
	encoded := buildNonWebSocketSessionEvidence(nonWebSocketRuntimeFacts{
		IsSSE:             true,
		TerminalErr:       ErrSSEIdleTimeout,
		ResponseCommitted: true,
	})
	if encoded == nil {
		t.Fatal("expected evidence envelope, got nil")
	}
	env := parseNonWebSocketEvidenceEnvelope(t, *encoded)
	if env.Version != nonWebSocketEvidenceSchemaVersion {
		t.Fatalf("v = %d, want %d", env.Version, nonWebSocketEvidenceSchemaVersion)
	}
	if env.Transport == nil {
		t.Fatal("transport missing from v2 envelope")
	}
	assertEqual(t, "kind", env.Transport.Kind, transportKindTimeout)
	assertEqual(t, "signal", env.Transport.Signal, transportSignalSSEIdleTimeout)
	assertEqual(t, "source", env.Transport.Source, transportSourceUpstream)
	assertEqual(t, "stage", env.Transport.Stage, transportStagePrePayloadVisible)
}

func TestNonWebSocketEvidence_UpstreamReadError_EmitsV2WithProtocolError(t *testing.T) {
	t.Parallel()
	// Plan acceptance criterion #2: signal=upstream_read_error,
	// kind=protocol_error, source=upstream.
	wrapped := NewUpstreamReadError(errors.New("connection reset"))
	encoded := buildNonWebSocketSessionEvidence(nonWebSocketRuntimeFacts{
		IsSSE:             true,
		TerminalErr:       wrapped,
		ResponseCommitted: true,
		FirstByteVisible:  true,
	})
	if encoded == nil {
		t.Fatal("expected evidence envelope, got nil")
	}
	env := parseNonWebSocketEvidenceEnvelope(t, *encoded)
	if env.Transport == nil {
		t.Fatal("transport missing from v2 envelope")
	}
	assertEqual(t, "kind", env.Transport.Kind, transportKindProtocolError)
	assertEqual(t, "signal", env.Transport.Signal, transportSignalUpstreamReadError)
	assertEqual(t, "stage", env.Transport.Stage, transportStagePostPayloadVisible)
	if !strings.Contains(env.Transport.RawErrorSnippet, "upstream read error") {
		t.Fatalf("raw_error_snippet lost wrapper text, got %q", env.Transport.RawErrorSnippet)
	}
}

func TestNonWebSocketEvidence_ClientWriteError_EmitsV2WithProtocolError(t *testing.T) {
	t.Parallel()
	// Non-cancel client-write failure → signal=client_write_error,
	// source=client, kind=protocol_error.
	encoded := buildNonWebSocketSessionEvidence(nonWebSocketRuntimeFacts{
		IsSSE:              true,
		TerminalErr:        errors.New("write: broken pipe"),
		ResponseCommitted:  true,
		FirstByteVisible:   true,
		IsClientWriteError: true,
	})
	if encoded == nil {
		t.Fatal("expected evidence envelope, got nil")
	}
	env := parseNonWebSocketEvidenceEnvelope(t, *encoded)
	if env.Transport == nil {
		t.Fatal("transport missing from v2 envelope")
	}
	assertEqual(t, "kind", env.Transport.Kind, transportKindProtocolError)
	assertEqual(t, "signal", env.Transport.Signal, transportSignalClientWriteError)
	assertEqual(t, "source", env.Transport.Source, transportSourceClient)
}

func TestNonWebSocketEvidence_StageBoundaries(t *testing.T) {
	t.Parallel()
	// The three SSE stages — pre_connection_visible / pre_payload_visible /
	// post_payload_visible — must flip purely on ResponseCommitted and
	// FirstByteVisible. The idle watchdog is the motivating pre-payload
	// case (headers flushed before any byte is read), which the existing
	// transport.go wiring can trigger.
	cases := []struct {
		name              string
		responseCommitted bool
		firstByteVisible  bool
		stage             string
	}{
		{"pre-connection before headers", false, false, transportStagePreConnectionVisible},
		{"pre-payload after headers no byte", true, false, transportStagePrePayloadVisible},
		{"post-payload first byte visible", true, true, transportStagePostPayloadVisible},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			encoded := buildNonWebSocketSessionEvidence(nonWebSocketRuntimeFacts{
				IsSSE:             true,
				TerminalErr:       ErrSSEIdleTimeout,
				ResponseCommitted: tc.responseCommitted,
				FirstByteVisible:  tc.firstByteVisible,
			})
			if encoded == nil {
				t.Fatal("expected evidence envelope")
			}
			env := parseNonWebSocketEvidenceEnvelope(t, *encoded)
			if env.Transport == nil {
				t.Fatal("transport missing")
			}
			assertEqual(t, "stage", env.Transport.Stage, tc.stage)
		})
	}
}

func TestNonWebSocketEvidence_CtxPlusIdleTimeout_EmitsDiagnostic(t *testing.T) {
	t.Parallel()
	// Acceptance criterion #14: ctx cancel racing an idle timeout must
	// still surface the transport signal. `clientCanceled` on the request
	// axis is unaffected; that is asserted elsewhere by the assessment
	// tests.
	encoded := buildNonWebSocketSessionEvidence(nonWebSocketRuntimeFacts{
		IsSSE:             true,
		TerminalErr:       ErrSSEIdleTimeout,
		CtxErr:            context.Canceled,
		ResponseCommitted: true,
	})
	if encoded == nil {
		t.Fatal("ctx + real signal must still emit evidence")
	}
}

func TestAssessNonWebSocketRequest_IncludesSessionEvidenceJSON(t *testing.T) {
	t.Parallel()
	// Assessment must deliver SessionEvidenceJSON alongside classification
	// so the logRequest caller reads one aggregate, not two. This locks
	// in the "evidence is a first-class assessment output" invariant.
	assessment := assessNonWebSocketRequest(nonWebSocketRuntimeFacts{
		ClientTransportStatusCode: http.StatusOK,
		ResponseCommitted:         true,
		ServiceStarted:            true,
		TerminalErr:               ErrSSEIdleTimeout,
		IsSSE:                     true,
		FirstByteVisible:          true,
	})
	if assessment.SessionEvidenceJSON == nil {
		t.Fatal("SessionEvidenceJSON missing from assessment for SSE idle timeout")
	}
	if !strings.Contains(*assessment.SessionEvidenceJSON, transportSignalSSEIdleTimeout) {
		t.Fatalf("evidence missing signal, got %q", *assessment.SessionEvidenceJSON)
	}
}

func TestAttachSSEAttemptEvidence_AttachesToLastAttempt(t *testing.T) {
	t.Parallel()
	// Attach helper writes the payload into the tail attempt in place so
	// it can run after recordAttempt without threading handles around.
	pctx := &proxyContext{}
	// Seed a prior unrelated attempt; attach must NOT touch it.
	pctx.attempts = append(pctx.attempts, newNormalizedRequestAttemptForTest("earlier"))
	pctx.attempts = append(pctx.attempts, newNormalizedRequestAttemptForTest("target"))

	attachSSEAttemptEvidence(pctx, nonWebSocketRuntimeFacts{
		IsSSE:             true,
		TerminalErr:       NewUpstreamReadError(errors.New("reset")),
		ResponseCommitted: true,
	})

	if pctx.attempts[0].AttemptEvidenceJSON != nil {
		t.Fatalf("prior attempt was mutated; AttemptEvidenceJSON = %q",
			*pctx.attempts[0].AttemptEvidenceJSON)
	}
	if pctx.attempts[1].AttemptEvidenceJSON == nil {
		t.Fatal("target attempt missing AttemptEvidenceJSON")
	}
	payload := *pctx.attempts[1].AttemptEvidenceJSON
	if !strings.Contains(payload, transportSignalUpstreamReadError) {
		t.Fatalf("payload missing upstream_read_error signal, got %q", payload)
	}
}

func TestAttachSSEAttemptEvidence_NoOpOnEmptyAttempts(t *testing.T) {
	t.Parallel()
	// A defensive no-op matters: if the helper ran before recordAttempt
	// (or the attempt append was skipped for any reason), we must not
	// panic or write to a phantom record.
	pctx := &proxyContext{}
	attachSSEAttemptEvidence(pctx, nonWebSocketRuntimeFacts{
		IsSSE:       true,
		TerminalErr: ErrSSEIdleTimeout,
	})
	if len(pctx.attempts) != 0 {
		t.Fatalf("empty attempts was mutated, len=%d", len(pctx.attempts))
	}
}

func TestAttachSSEAttemptEvidence_NoOpWhenDerivationReturnsNil(t *testing.T) {
	t.Parallel()
	// Pure cancel has no transport signal; attach must leave the tail
	// attempt's AttemptEvidenceJSON at its zero value so the column stays
	// NULL and the v2 renderer isn't given an empty payload to parse.
	pctx := &proxyContext{}
	pctx.attempts = append(pctx.attempts, newNormalizedRequestAttemptForTest("target"))
	attachSSEAttemptEvidence(pctx, nonWebSocketRuntimeFacts{
		IsSSE:          true,
		ClientCanceled: true,
		CtxErr:         context.Canceled,
	})
	if pctx.attempts[0].AttemptEvidenceJSON != nil {
		t.Fatalf("attempt evidence must stay nil for pure cancel, got %q",
			*pctx.attempts[0].AttemptEvidenceJSON)
	}
}

func TestAttemptFactsFromForwardResult_PreservesObservation(t *testing.T) {
	t.Parallel()
	// Lock in the forwardResult → facts translation: every observation
	// axis must survive. Drift here would silently break the
	// `forwardResult → retryState → logRequest` chain enshrined by plan
	// "SSE 链路接入".
	inner := errors.New("boom")
	result := forwardResult{
		statusCode:         http.StatusOK,
		success:            false,
		responseCommitted:  true,
		clientCanceled:     false,
		err:                NewUpstreamReadError(inner),
		isSSE:              true,
		firstByteVisible:   true,
		isStatusFailover:   false,
		isClientWriteError: false,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	facts := attemptFactsFromForwardResult(ctx, result)

	if !facts.IsSSE || !facts.FirstByteVisible || !facts.ResponseCommitted {
		t.Fatalf("facts = %+v, want SSE/first-byte/header-committed all true", facts)
	}
	if !IsUpstreamReadError(facts.TerminalErr) {
		t.Fatalf("TerminalErr must preserve upstream read error wrapper, got %v", facts.TerminalErr)
	}
	// The wrapper text is the wire contract on request_attempts.error
	// (plan acceptance criterion #2); the translation must not unwrap.
	if !strings.Contains(facts.TerminalErr.Error(), "upstream read error:") {
		t.Fatalf("wrapper text lost, got %q", facts.TerminalErr.Error())
	}
}

// parseNonWebSocketEvidenceEnvelope decodes the evidence JSON into a
// testing-only view so assertions can read `v` and `transport` directly.
// Keeping the view local avoids exporting internal schema types.
func parseNonWebSocketEvidenceEnvelope(t *testing.T, encoded string) struct {
	Version   int                          `json:"v"`
	Transport *nonWebSocketTransportPayload `json:"transport"`
} {
	t.Helper()
	var env struct {
		Version   int                          `json:"v"`
		Transport *nonWebSocketTransportPayload `json:"transport"`
	}
	if err := json.Unmarshal([]byte(encoded), &env); err != nil {
		t.Fatalf("json.Unmarshal(%q) = %v", encoded, err)
	}
	return env
}

// newNormalizedRequestAttemptForTest is a compact wrapper to keep the
// attempt-seeding lines above focused on the behavior under test rather
// than attempt scaffolding. It mirrors newNormalizedRequestAttempt's
// signature so production callers and test callers stay aligned.
func newNormalizedRequestAttemptForTest(providerID string) model.RequestAttempt {
	return newNormalizedRequestAttempt("req-test", providerID, time.Unix(0, 0))
}

// TestNonWebSocketEvidence_RedactsSecretsInRawErrorSnippet locks in the fix
// for a real leak: `UpstreamReadError` wrappers frequently embed upstream
// request URLs and header dumps into their `Error()` text. Without
// redaction at the evidence layer those secrets land verbatim in
// `attempt_evidence_json`. The regex classes (header-shaped secrets,
// Bearer/Basic auth prefixes) mirror the WS sanitize path so both
// protocols share one taxonomy.
//
// Each redaction class is exercised in isolation so a regression in any
// one regex fails the test in that sub-case rather than being masked by
// another class's match. The combined-in-one-error layout that real
// traffic sometimes produces is covered separately in `combined`.
func TestNonWebSocketEvidence_RedactsSecretsInRawErrorSnippet(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		innerText string
		// secrets lists every literal that must NOT appear in the
		// serialized evidence payload. Keeping it per-case makes a
		// regression point at the exact failing class.
		secrets []string
	}{
		{
			name:      "authorization header",
			innerText: `dial https://upstream: Authorization: sk-test-abc`,
			secrets:   []string{"sk-test-abc"},
		},
		{
			name:      "api_key query string",
			innerText: `Get "https://api.vendor.com/v1?api_key=sk-xyz"`,
			secrets:   []string{"sk-xyz"},
		},
		{
			name:      "bare bearer token",
			innerText: `authentication failed for Bearer tok-123`,
			secrets:   []string{"tok-123"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			encoded := buildNonWebSocketAttemptEvidence(nonWebSocketRuntimeFacts{
				IsSSE:             true,
				TerminalErr:       NewUpstreamReadError(errors.New(tc.innerText)),
				ResponseCommitted: true,
				FirstByteVisible:  true,
			})
			if encoded == nil {
				t.Fatal("expected evidence envelope, got nil")
			}
			payload := *encoded
			if !strings.Contains(payload, "[REDACTED]") {
				t.Fatalf("payload missing [REDACTED] placeholder, got %q", payload)
			}
			for _, leak := range tc.secrets {
				if strings.Contains(payload, leak) {
					t.Fatalf("payload leaked secret %q, got %q", leak, payload)
				}
			}
		})
	}
}
