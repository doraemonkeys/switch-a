package proxy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestTransportDiagnostic_NoSignal_ReturnsNil(t *testing.T) {
	t.Parallel()
	if diagnostic := deriveTransportDiagnostic(transportObservation{protocol: transportProtocolSSE}); diagnostic != nil {
		t.Fatalf("expected nil diagnostic, got %+v", diagnostic)
	}
}

func TestTransportDiagnostic_StatusFailover_ReturnsNil(t *testing.T) {
	t.Parallel()
	observation := transportObservation{
		protocol: transportProtocolSSE,
		err:      fmt.Errorf("upstream returned status 502"),
		sse:      sseObservation{isStatusFailover: true},
	}
	if diagnostic := deriveTransportDiagnostic(observation); diagnostic != nil {
		t.Fatalf("status failover must bypass diagnostic, got %+v", diagnostic)
	}
}

func TestTransportDiagnostic_PureClientCancel_SSE_ReturnsNil(t *testing.T) {
	t.Parallel()
	observation := transportObservation{protocol: transportProtocolSSE, ctxErr: context.Canceled}
	if diagnostic := deriveTransportDiagnostic(observation); diagnostic != nil {
		t.Fatalf("pure SSE context cancellation must return nil, got %+v", diagnostic)
	}
}

func TestTransportDiagnostic_CtxWithSSEIdleTimeout_EmitsDiagnostic(t *testing.T) {
	t.Parallel()
	observation := transportObservation{
		protocol: transportProtocolSSE,
		err:      ErrSSEIdleTimeout,
		ctxErr:   context.Canceled,
		sse:      sseObservation{headerCommitted: true},
	}
	diagnostic := deriveTransportDiagnostic(observation)
	if diagnostic == nil {
		t.Fatal("expected diagnostic, got nil")
	}
	if diagnostic.Signal != transportSignalSSEIdleTimeout || diagnostic.Kind != transportKindTimeout {
		t.Fatalf("expected sse_idle_timeout/timeout, got signal=%s kind=%s", diagnostic.Signal, diagnostic.Kind)
	}
}

func TestTransportDiagnostic_SSE_IdleTimeout(t *testing.T) {
	t.Parallel()
	observation := transportObservation{
		protocol: transportProtocolSSE,
		err:      ErrSSEIdleTimeout,
		sse:      sseObservation{firstByteVisible: true, headerCommitted: true},
	}
	diagnostic := deriveTransportDiagnostic(observation)
	if diagnostic == nil {
		t.Fatal("expected diagnostic")
	}
	assertEqual(t, "signal", diagnostic.Signal, transportSignalSSEIdleTimeout)
	assertEqual(t, "kind", diagnostic.Kind, transportKindTimeout)
	assertEqual(t, "source", diagnostic.Source, transportSourceUpstream)
	assertEqual(t, "stage", diagnostic.Stage, transportStagePostPayloadVisible)
}

func TestTransportDiagnostic_SSE_UpstreamReadError(t *testing.T) {
	t.Parallel()
	observation := transportObservation{
		protocol: transportProtocolSSE,
		err:      NewUpstreamReadError(errors.New("connection reset")),
		sse:      sseObservation{headerCommitted: true},
	}
	diagnostic := deriveTransportDiagnostic(observation)
	if diagnostic == nil {
		t.Fatal("expected diagnostic")
	}
	assertEqual(t, "signal", diagnostic.Signal, transportSignalUpstreamReadError)
	assertEqual(t, "kind", diagnostic.Kind, transportKindProtocolError)
	assertEqual(t, "source", diagnostic.Source, transportSourceUpstream)
	assertEqual(t, "stage", diagnostic.Stage, transportStagePrePayloadVisible)
	if !strings.Contains(diagnostic.RawErrorSnippet, "upstream read error") {
		t.Fatalf("raw_error_snippet should preserve wrapper text, got %q", diagnostic.RawErrorSnippet)
	}
}

func TestTransportDiagnostic_SSE_ClientWriteError(t *testing.T) {
	t.Parallel()
	observation := transportObservation{
		protocol: transportProtocolSSE,
		err:      errors.New("broken pipe"),
		sse: sseObservation{
			firstByteVisible:   true,
			headerCommitted:    true,
			isClientWriteError: true,
		},
	}
	diagnostic := deriveTransportDiagnostic(observation)
	if diagnostic == nil {
		t.Fatal("expected diagnostic")
	}
	assertEqual(t, "signal", diagnostic.Signal, transportSignalClientWriteError)
	assertEqual(t, "kind", diagnostic.Kind, transportKindProtocolError)
	assertEqual(t, "source", diagnostic.Source, transportSourceClient)
	assertEqual(t, "stage", diagnostic.Stage, transportStagePostPayloadVisible)
}

func TestTransportDiagnostic_SSE_UnknownTransport(t *testing.T) {
	t.Parallel()
	diagnostic := deriveTransportDiagnostic(transportObservation{
		protocol: transportProtocolSSE,
		err:      errors.New("mystery failure"),
	})
	if diagnostic == nil {
		t.Fatal("expected diagnostic")
	}
	assertEqual(t, "signal", diagnostic.Signal, transportSignalUnknownTransport)
	assertEqual(t, "kind", diagnostic.Kind, transportKindLocalError)
	assertEqual(t, "stage", diagnostic.Stage, transportStagePreConnectionVisible)
}

func TestTransportDiagnostic_SSE_StageTransitions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		sse   sseObservation
		stage string
	}{
		{"no header committed", sseObservation{}, transportStagePreConnectionVisible},
		{"header committed no first byte", sseObservation{headerCommitted: true}, transportStagePrePayloadVisible},
		{"first byte visible", sseObservation{firstByteVisible: true, headerCommitted: true}, transportStagePostPayloadVisible},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			diagnostic := deriveTransportDiagnostic(transportObservation{
				protocol: transportProtocolSSE,
				err:      ErrSSEIdleTimeout,
				sse:      test.sse,
			})
			if diagnostic == nil {
				t.Fatal("expected diagnostic")
			}
			assertEqual(t, "stage", diagnostic.Stage, test.stage)
		})
	}
}

func TestTransportDiagnostic_RawErrorSnippet_TruncatedAtRuneBoundary(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("𝕏", transportRawErrorSnippetLimitRunes+50)
	diagnostic := deriveTransportDiagnostic(transportObservation{
		protocol: transportProtocolSSE,
		err:      errors.New(long),
	})
	if diagnostic == nil {
		t.Fatal("expected diagnostic")
	}
	if !utf8ValidAndRuneCountAtMost(diagnostic.RawErrorSnippet, transportRawErrorSnippetLimitRunes) {
		t.Fatalf("snippet violates rune-boundary truncation: runes=%d bytes=%d", runeCount(diagnostic.RawErrorSnippet), len(diagnostic.RawErrorSnippet))
	}
}

func TestTransportDiagnostic_RawErrorSnippet_ShortPassthrough(t *testing.T) {
	t.Parallel()
	diagnostic := deriveTransportDiagnostic(transportObservation{
		protocol: transportProtocolSSE,
		err:      errors.New("short message"),
	})
	if diagnostic == nil {
		t.Fatal("expected diagnostic")
	}
	assertEqual(t, "raw_error_snippet", diagnostic.RawErrorSnippet, "short message")
}

func TestTransportDiagnostic_UnknownProtocol_ReturnsNil(t *testing.T) {
	t.Parallel()
	observation := transportObservation{protocol: transportProtocol(0), err: errors.New("boom")}
	if diagnostic := deriveTransportDiagnostic(observation); diagnostic != nil {
		t.Fatalf("unknown protocol must return nil, got %+v", diagnostic)
	}
}

func assertEqual(t *testing.T, field, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q, want %q", field, got, want)
	}
}

func utf8ValidAndRuneCountAtMost(value string, limit int) bool {
	return runeCount(value) <= limit
}

func runeCount(value string) int {
	count := 0
	for range value {
		count++
	}
	return count
}
