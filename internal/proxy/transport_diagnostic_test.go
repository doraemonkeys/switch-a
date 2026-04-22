package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

// All tests share the TransportDiagnostic prefix so the caller can run
// `go test ./internal/proxy/ -run TransportDiagnostic` and hit only this
// suite during Step #2 development.

// --- Short-circuit branches ------------------------------------------------

func TestTransportDiagnostic_NoSignal_ReturnsNil(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		obs  transportObservation
	}{
		{
			name: "sse no err no ctx",
			obs:  transportObservation{protocol: transportProtocolSSE},
		},
		{
			name: "ws no err no closeError no closedWithoutStatus",
			obs:  transportObservation{protocol: transportProtocolWS},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if diag := deriveTransportDiagnostic(tc.obs); diag != nil {
				t.Fatalf("expected nil diagnostic, got %+v", diag)
			}
		})
	}
}

func TestTransportDiagnostic_StatusFailover_ReturnsNil(t *testing.T) {
	t.Parallel()
	obs := transportObservation{
		protocol: transportProtocolSSE,
		err:      fmt.Errorf("upstream returned status 502"),
		sse:      sseObservation{isStatusFailover: true},
	}
	if diag := deriveTransportDiagnostic(obs); diag != nil {
		t.Fatalf("status failover must bypass diagnostic, got %+v", diag)
	}
}

func TestTransportDiagnostic_SuppressedSyntheticFinal_ReturnsNil(t *testing.T) {
	t.Parallel()
	// Even with a real closeError present, the synthetic final session must
	// not emit a diagnostic: its observation is inherited from a replaced
	// attempt and would misattribute the transport fact.
	closeErr := &websocket.CloseError{Code: websocket.StatusAbnormalClosure}
	obs := transportObservation{
		protocol:                   transportProtocolWS,
		isSuppressedSyntheticFinal: true,
		ws:                         wsObservation{closeError: closeErr, upgradeCompleted: true, anyFrameDelivered: true},
	}
	if diag := deriveTransportDiagnostic(obs); diag != nil {
		t.Fatalf("suppressed synthetic final must bypass diagnostic, got %+v", diag)
	}
}

func TestTransportDiagnostic_PureClientCancel_SSE_ReturnsNil(t *testing.T) {
	t.Parallel()
	obs := transportObservation{
		protocol: transportProtocolSSE,
		ctxErr:   context.Canceled,
	}
	if diag := deriveTransportDiagnostic(obs); diag != nil {
		t.Fatalf("pure SSE ctx cancel must return nil, got %+v", diag)
	}
}

func TestTransportDiagnostic_PureClientCancel_WS_ReturnsNil(t *testing.T) {
	t.Parallel()
	obs := transportObservation{
		protocol: transportProtocolWS,
		ctxErr:   context.Canceled,
	}
	if diag := deriveTransportDiagnostic(obs); diag != nil {
		t.Fatalf("pure WS ctx cancel must return nil, got %+v", diag)
	}
}

// --- ctx + real signal concurrency ----------------------------------------

func TestTransportDiagnostic_CtxWithSSEIdleTimeout_EmitsDiagnostic(t *testing.T) {
	t.Parallel()
	// ctx cancel racing with SSE idle timeout is the motivating case: the
	// transport axis must still record the upstream timeout even though the
	// request-level axis also observed cancel.
	obs := transportObservation{
		protocol: transportProtocolSSE,
		err:      ErrSSEIdleTimeout,
		ctxErr:   context.Canceled,
		sse:      sseObservation{headerCommitted: true},
	}
	diag := deriveTransportDiagnostic(obs)
	if diag == nil {
		t.Fatal("expected diagnostic, got nil")
	}
	if diag.Signal != transportSignalSSEIdleTimeout || diag.Kind != transportKindTimeout {
		t.Fatalf("expected sse_idle_timeout/timeout, got signal=%s kind=%s", diag.Signal, diag.Kind)
	}
}

func TestTransportDiagnostic_CtxWithWSCloseError_EmitsDiagnostic(t *testing.T) {
	t.Parallel()
	closeErr := &websocket.CloseError{Code: websocket.StatusInternalError, Reason: "oops"}
	obs := transportObservation{
		protocol: transportProtocolWS,
		ctxErr:   context.DeadlineExceeded,
		ws:       wsObservation{closeError: closeErr, upgradeCompleted: true, anyFrameDelivered: true},
	}
	diag := deriveTransportDiagnostic(obs)
	if diag == nil {
		t.Fatal("expected diagnostic despite ctxErr, got nil")
	}
	if diag.Signal != transportSignalCloseError {
		t.Fatalf("expected close_error signal, got %s", diag.Signal)
	}
}

// --- SSE signal coverage --------------------------------------------------

func TestTransportDiagnostic_SSE_IdleTimeout(t *testing.T) {
	t.Parallel()
	obs := transportObservation{
		protocol: transportProtocolSSE,
		err:      ErrSSEIdleTimeout,
		sse:      sseObservation{firstByteVisible: true, headerCommitted: true},
	}
	diag := deriveTransportDiagnostic(obs)
	if diag == nil {
		t.Fatal("expected diagnostic")
	}
	assertEqual(t, "signal", diag.Signal, transportSignalSSEIdleTimeout)
	assertEqual(t, "kind", diag.Kind, transportKindTimeout)
	assertEqual(t, "source", diag.Source, transportSourceUpstream)
	assertEqual(t, "stage", diag.Stage, transportStagePostPayloadVisible)
}

func TestTransportDiagnostic_SSE_UpstreamReadError(t *testing.T) {
	t.Parallel()
	inner := errors.New("connection reset")
	obs := transportObservation{
		protocol: transportProtocolSSE,
		err:      NewUpstreamReadError(inner),
		sse:      sseObservation{headerCommitted: true},
	}
	diag := deriveTransportDiagnostic(obs)
	if diag == nil {
		t.Fatal("expected diagnostic")
	}
	assertEqual(t, "signal", diag.Signal, transportSignalUpstreamReadError)
	assertEqual(t, "kind", diag.Kind, transportKindProtocolError)
	assertEqual(t, "source", diag.Source, transportSourceUpstream)
	assertEqual(t, "stage", diag.Stage, transportStagePrePayloadVisible)
	if !strings.Contains(diag.RawErrorSnippet, "upstream read error") {
		t.Fatalf("raw_error_snippet should preserve wrapper text, got %q", diag.RawErrorSnippet)
	}
}

func TestTransportDiagnostic_SSE_ClientWriteError(t *testing.T) {
	t.Parallel()
	obs := transportObservation{
		protocol: transportProtocolSSE,
		err:      errors.New("broken pipe"),
		sse: sseObservation{
			firstByteVisible:   true,
			headerCommitted:    true,
			isClientWriteError: true,
		},
	}
	diag := deriveTransportDiagnostic(obs)
	if diag == nil {
		t.Fatal("expected diagnostic")
	}
	assertEqual(t, "signal", diag.Signal, transportSignalClientWriteError)
	assertEqual(t, "kind", diag.Kind, transportKindProtocolError)
	assertEqual(t, "source", diag.Source, transportSourceClient)
	assertEqual(t, "stage", diag.Stage, transportStagePostPayloadVisible)
}

func TestTransportDiagnostic_SSE_UnknownTransport(t *testing.T) {
	t.Parallel()
	obs := transportObservation{
		protocol: transportProtocolSSE,
		err:      errors.New("mystery failure"),
	}
	diag := deriveTransportDiagnostic(obs)
	if diag == nil {
		t.Fatal("expected diagnostic")
	}
	assertEqual(t, "signal", diag.Signal, transportSignalUnknownTransport)
	assertEqual(t, "kind", diag.Kind, transportKindLocalError)
	assertEqual(t, "stage", diag.Stage, transportStagePreConnectionVisible)
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
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			obs := transportObservation{
				protocol: transportProtocolSSE,
				err:      ErrSSEIdleTimeout,
				sse:      tc.sse,
			}
			diag := deriveTransportDiagnostic(obs)
			if diag == nil {
				t.Fatal("expected diagnostic")
			}
			assertEqual(t, "stage", diag.Stage, tc.stage)
		})
	}
}

// --- WS signal coverage ---------------------------------------------------

func TestTransportDiagnostic_WS_CloseError_WithRealCode(t *testing.T) {
	t.Parallel()
	// StatusNormalClosure (1000) is explicitly covered to lock in that a
	// "real code including 0" presence rule is honored — the classifier
	// keys on closeError presence, not on the numeric value.
	closeErr := &websocket.CloseError{Code: websocket.StatusNormalClosure, Reason: "bye"}
	obs := transportObservation{
		protocol: transportProtocolWS,
		ws: wsObservation{
			closeError:        closeErr,
			upgradeCompleted:  true,
			anyFrameDelivered: true,
		},
	}
	diag := deriveTransportDiagnostic(obs)
	if diag == nil {
		t.Fatal("expected diagnostic")
	}
	assertEqual(t, "signal", diag.Signal, transportSignalCloseError)
	assertEqual(t, "kind", diag.Kind, transportKindDisconnect)
	assertEqual(t, "stage", diag.Stage, transportStagePostPayloadVisible)
	if diag.CloseCode == nil {
		t.Fatal("expected close_code present when closeError set")
	}
	if *diag.CloseCode != int(websocket.StatusNormalClosure) {
		t.Fatalf("expected close code 1000, got %d", *diag.CloseCode)
	}
	assertEqual(t, "close_reason_snippet", diag.CloseReasonSnippet, "bye")
}

func TestTransportDiagnostic_WS_CloseError_ZeroCodeStillPresent(t *testing.T) {
	t.Parallel()
	// Zero is a legitimate observed code — the presence pointer must fire.
	closeErr := &websocket.CloseError{Code: 0}
	obs := transportObservation{
		protocol: transportProtocolWS,
		ws:       wsObservation{closeError: closeErr, upgradeCompleted: true},
	}
	diag := deriveTransportDiagnostic(obs)
	if diag == nil || diag.CloseCode == nil {
		t.Fatalf("zero-coded CloseError must still set CloseCode pointer, got %+v", diag)
	}
	if *diag.CloseCode != 0 {
		t.Fatalf("expected close code 0, got %d", *diag.CloseCode)
	}
}

func TestTransportDiagnostic_WS_CloseWithoutStatus_NoCloseCode(t *testing.T) {
	t.Parallel()
	// `isUnexpectedPeerDisconnect` path: the reduction layer recognized the
	// disconnect but could not recover a CloseError; presence pointer must
	// stay nil.
	obs := transportObservation{
		protocol: transportProtocolWS,
		ws:       wsObservation{closedWithoutStatus: true, upgradeCompleted: true, anyFrameDelivered: true},
	}
	diag := deriveTransportDiagnostic(obs)
	if diag == nil {
		t.Fatal("expected diagnostic")
	}
	assertEqual(t, "signal", diag.Signal, transportSignalCloseWithoutStatus)
	assertEqual(t, "kind", diag.Kind, transportKindDisconnect)
	if diag.CloseCode != nil {
		t.Fatalf("close_without_status must leave CloseCode nil, got %d", *diag.CloseCode)
	}
}

func TestTransportDiagnostic_WS_EOF(t *testing.T) {
	t.Parallel()
	obs := transportObservation{
		protocol: transportProtocolWS,
		err:      io.EOF,
		ws:       wsObservation{upgradeCompleted: true, anyFrameDelivered: true},
	}
	diag := deriveTransportDiagnostic(obs)
	if diag == nil {
		t.Fatal("expected diagnostic")
	}
	assertEqual(t, "signal", diag.Signal, transportSignalEOF)
	assertEqual(t, "kind", diag.Kind, transportKindDisconnect)
	assertEqual(t, "stage", diag.Stage, transportStagePostPayloadVisible)
	if diag.CloseCode != nil {
		t.Fatalf("no CloseError → CloseCode must be nil")
	}
}

func TestTransportDiagnostic_WS_UnexpectedEOF(t *testing.T) {
	t.Parallel()
	obs := transportObservation{
		protocol: transportProtocolWS,
		err:      io.ErrUnexpectedEOF,
		ws:       wsObservation{upgradeCompleted: true},
	}
	diag := deriveTransportDiagnostic(obs)
	if diag == nil {
		t.Fatal("expected diagnostic")
	}
	assertEqual(t, "signal", diag.Signal, transportSignalUnexpectedEOF)
	assertEqual(t, "kind", diag.Kind, transportKindDisconnect)
	assertEqual(t, "stage", diag.Stage, transportStagePrePayloadVisible)
}

func TestTransportDiagnostic_WS_Timeout(t *testing.T) {
	t.Parallel()
	obs := transportObservation{
		protocol: transportProtocolWS,
		err:      context.DeadlineExceeded,
		ws:       wsObservation{upgradeCompleted: true},
	}
	diag := deriveTransportDiagnostic(obs)
	if diag == nil {
		t.Fatal("expected diagnostic")
	}
	assertEqual(t, "signal", diag.Signal, transportSignalTimeout)
	assertEqual(t, "kind", diag.Kind, transportKindTimeout)
	assertEqual(t, "source", diag.Source, transportSourceUpstream)
}

func TestTransportDiagnostic_WS_Canceled(t *testing.T) {
	t.Parallel()
	// context.Canceled co-observed with no real transport frame (but some
	// other err equals Canceled, e.g. wrapped by the relay layer) — signal
	// classifies as `canceled`, source is client since cancel originates
	// there.
	obs := transportObservation{
		protocol: transportProtocolWS,
		err:      fmt.Errorf("relay aborted: %w", context.Canceled),
		ws:       wsObservation{upgradeCompleted: true},
	}
	diag := deriveTransportDiagnostic(obs)
	if diag == nil {
		t.Fatal("expected diagnostic")
	}
	assertEqual(t, "signal", diag.Signal, transportSignalCanceled)
	assertEqual(t, "kind", diag.Kind, transportKindLocalError)
	assertEqual(t, "source", diag.Source, transportSourceClient)
}

func TestTransportDiagnostic_WS_UnknownTransport(t *testing.T) {
	t.Parallel()
	obs := transportObservation{
		protocol: transportProtocolWS,
		err:      errors.New("tls: bad handshake"),
	}
	diag := deriveTransportDiagnostic(obs)
	if diag == nil {
		t.Fatal("expected diagnostic")
	}
	assertEqual(t, "signal", diag.Signal, transportSignalUnknownTransport)
	assertEqual(t, "kind", diag.Kind, transportKindLocalError)
	assertEqual(t, "stage", diag.Stage, transportStagePreConnectionVisible)
}

func TestTransportDiagnostic_WS_StageTransitions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		ws    wsObservation
		stage string
	}{
		{"dial failure no upgrade", wsObservation{}, transportStagePreConnectionVisible},
		{"upgrade completed no frame", wsObservation{upgradeCompleted: true}, transportStagePrePayloadVisible},
		{"frame delivered", wsObservation{upgradeCompleted: true, anyFrameDelivered: true}, transportStagePostPayloadVisible},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			obs := transportObservation{
				protocol: transportProtocolWS,
				err:      io.EOF,
				ws:       tc.ws,
			}
			diag := deriveTransportDiagnostic(obs)
			if diag == nil {
				t.Fatal("expected diagnostic")
			}
			assertEqual(t, "stage", diag.Stage, tc.stage)
		})
	}
}

// --- Close-code presence invariants ---------------------------------------

func TestTransportDiagnostic_WS_CloseCodePresenceMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		obs     transportObservation
		present bool
		want    int
	}{
		{
			name: "real close code 1000",
			obs: transportObservation{
				protocol: transportProtocolWS,
				ws:       wsObservation{closeError: &websocket.CloseError{Code: 1000}},
			},
			present: true,
			want:    1000,
		},
		{
			name: "real close code 4000 custom",
			obs: transportObservation{
				protocol: transportProtocolWS,
				ws:       wsObservation{closeError: &websocket.CloseError{Code: 4000}},
			},
			present: true,
			want:    4000,
		},
		{
			name: "close_without_status → nil pointer",
			obs: transportObservation{
				protocol: transportProtocolWS,
				ws:       wsObservation{closedWithoutStatus: true},
			},
			present: false,
		},
		{
			name: "plain EOF → nil pointer",
			obs: transportObservation{
				protocol: transportProtocolWS,
				err:      io.EOF,
			},
			present: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			diag := deriveTransportDiagnostic(tc.obs)
			if diag == nil {
				t.Fatal("expected diagnostic")
			}
			if tc.present {
				if diag.CloseCode == nil {
					t.Fatalf("expected CloseCode=%d, got nil", tc.want)
				}
				if *diag.CloseCode != tc.want {
					t.Fatalf("CloseCode = %d, want %d", *diag.CloseCode, tc.want)
				}
			} else if diag.CloseCode != nil {
				t.Fatalf("expected nil CloseCode, got %d", *diag.CloseCode)
			}
		})
	}
}

// --- Kind mapping ---------------------------------------------------------

func TestTransportDiagnostic_KindMapping(t *testing.T) {
	t.Parallel()
	// The signal→kind projection is part of the wire contract; lock it in
	// with a direct table so a future refactor cannot silently reshuffle.
	cases := []struct {
		name   string
		obs    transportObservation
		signal string
		kind   string
	}{
		{
			name: "timeout ← sse_idle_timeout",
			obs: transportObservation{
				protocol: transportProtocolSSE,
				err:      ErrSSEIdleTimeout,
			},
			signal: transportSignalSSEIdleTimeout,
			kind:   transportKindTimeout,
		},
		{
			name: "timeout ← ws timeout",
			obs: transportObservation{
				protocol: transportProtocolWS,
				err:      context.DeadlineExceeded,
			},
			signal: transportSignalTimeout,
			kind:   transportKindTimeout,
		},
		{
			name: "disconnect ← eof",
			obs: transportObservation{
				protocol: transportProtocolWS,
				err:      io.EOF,
			},
			signal: transportSignalEOF,
			kind:   transportKindDisconnect,
		},
		{
			name: "disconnect ← unexpected_eof",
			obs: transportObservation{
				protocol: transportProtocolWS,
				err:      io.ErrUnexpectedEOF,
			},
			signal: transportSignalUnexpectedEOF,
			kind:   transportKindDisconnect,
		},
		{
			name: "disconnect ← close_without_status",
			obs: transportObservation{
				protocol: transportProtocolWS,
				ws:       wsObservation{closedWithoutStatus: true},
			},
			signal: transportSignalCloseWithoutStatus,
			kind:   transportKindDisconnect,
		},
		{
			name: "disconnect ← close_error",
			obs: transportObservation{
				protocol: transportProtocolWS,
				ws:       wsObservation{closeError: &websocket.CloseError{Code: 1011}},
			},
			signal: transportSignalCloseError,
			kind:   transportKindDisconnect,
		},
		{
			name: "protocol_error ← upstream_read_error",
			obs: transportObservation{
				protocol: transportProtocolSSE,
				err:      NewUpstreamReadError(errors.New("reset")),
			},
			signal: transportSignalUpstreamReadError,
			kind:   transportKindProtocolError,
		},
		{
			name: "protocol_error ← client_write_error",
			obs: transportObservation{
				protocol: transportProtocolSSE,
				err:      errors.New("broken pipe"),
				sse:      sseObservation{isClientWriteError: true},
			},
			signal: transportSignalClientWriteError,
			kind:   transportKindProtocolError,
		},
		{
			name: "local_error ← canceled",
			obs: transportObservation{
				protocol: transportProtocolWS,
				err:      fmt.Errorf("wrapped: %w", context.Canceled),
			},
			signal: transportSignalCanceled,
			kind:   transportKindLocalError,
		},
		{
			name: "local_error ← unknown_transport (sse)",
			obs: transportObservation{
				protocol: transportProtocolSSE,
				err:      errors.New("???"),
			},
			signal: transportSignalUnknownTransport,
			kind:   transportKindLocalError,
		},
		{
			name: "local_error ← unknown_transport (ws)",
			obs: transportObservation{
				protocol: transportProtocolWS,
				err:      errors.New("tls bad"),
			},
			signal: transportSignalUnknownTransport,
			kind:   transportKindLocalError,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			diag := deriveTransportDiagnostic(tc.obs)
			if diag == nil {
				t.Fatal("expected diagnostic")
			}
			assertEqual(t, "signal", diag.Signal, tc.signal)
			assertEqual(t, "kind", diag.Kind, tc.kind)
		})
	}
}

// --- Raw snippet truncation ------------------------------------------------

func TestTransportDiagnostic_RawErrorSnippet_TruncatedAtRuneBoundary(t *testing.T) {
	t.Parallel()
	// Build a string well past the limit with multi-byte runes to prove the
	// truncation is rune-safe rather than byte-indexed.
	rune4Byte := "𝕏" // 4-byte UTF-8 (U+1D54F)
	long := strings.Repeat(rune4Byte, transportRawErrorSnippetLimitRunes+50)
	obs := transportObservation{
		protocol: transportProtocolSSE,
		err:      errors.New(long),
	}
	diag := deriveTransportDiagnostic(obs)
	if diag == nil {
		t.Fatal("expected diagnostic")
	}
	if !utf8ValidAndRuneCountAtMost(diag.RawErrorSnippet, transportRawErrorSnippetLimitRunes) {
		t.Fatalf("snippet violates rune-boundary truncation: runes=%d bytes=%d",
			runeCount(diag.RawErrorSnippet), len(diag.RawErrorSnippet))
	}
}

func TestTransportDiagnostic_RawErrorSnippet_ShortPassthrough(t *testing.T) {
	t.Parallel()
	obs := transportObservation{
		protocol: transportProtocolSSE,
		err:      errors.New("short message"),
	}
	diag := deriveTransportDiagnostic(obs)
	if diag == nil {
		t.Fatal("expected diagnostic")
	}
	assertEqual(t, "raw_error_snippet", diag.RawErrorSnippet, "short message")
}

// truncateRawErrorSnippet handles the empty input path as a no-op; exercised
// via an unknown-transport observation that reports only a closeError reason
// (empty) to keep the surrounding shape realistic.
func TestTransportDiagnostic_RawErrorSnippet_Empty(t *testing.T) {
	t.Parallel()
	// WS close with no reason → CloseReasonSnippet must stay empty, and
	// RawErrorSnippet is empty because obs.err is nil.
	obs := transportObservation{
		protocol: transportProtocolWS,
		ws:       wsObservation{closeError: &websocket.CloseError{Code: 1011}},
	}
	diag := deriveTransportDiagnostic(obs)
	if diag == nil {
		t.Fatal("expected diagnostic")
	}
	assertEqual(t, "raw_error_snippet", diag.RawErrorSnippet, "")
	assertEqual(t, "close_reason_snippet", diag.CloseReasonSnippet, "")
}

// --- Peer-attribution on disconnect signals -------------------------------

// TestTransportDiagnostic_WS_DisconnectSourcePerPeer covers the four
// disconnect-family signals (`close_error`, `eof`, `unexpected_eof`,
// `close_without_status`) across the three `failurePeer` states. The
// contract being locked in: `source` flips to `client` iff the reduction
// layer attributed the failure to the client peer; the zero-value and
// the upstream-peer cases both resolve to `upstream` (upstream-default
// convention — any observation the reduce layer never tagged must stay
// upstream so we never silently misattribute a real upstream disconnect).
func TestTransportDiagnostic_WS_DisconnectSourcePerPeer(t *testing.T) {
	t.Parallel()
	closeErr := &websocket.CloseError{Code: websocket.StatusAbnormalClosure, Reason: "bye"}
	signals := []struct {
		name string
		ws   wsObservation
		err  error
	}{
		{name: "close_error", ws: wsObservation{closeError: closeErr, upgradeCompleted: true, anyFrameDelivered: true}},
		{name: "eof", ws: wsObservation{upgradeCompleted: true, anyFrameDelivered: true}, err: io.EOF},
		{name: "unexpected_eof", ws: wsObservation{upgradeCompleted: true}, err: io.ErrUnexpectedEOF},
		{name: "close_without_status", ws: wsObservation{closedWithoutStatus: true, upgradeCompleted: true, anyFrameDelivered: true}},
	}
	peers := []struct {
		name string
		peer webSocketPeer
		want string
	}{
		{name: "unknown→upstream", peer: webSocketPeerUnknown, want: transportSourceUpstream},
		{name: "upstream→upstream", peer: webSocketPeerUpstream, want: transportSourceUpstream},
		{name: "client→client", peer: webSocketPeerClient, want: transportSourceClient},
	}
	for _, sig := range signals {
		for _, p := range peers {
			sig, p := sig, p
			t.Run(sig.name+"/"+p.name, func(t *testing.T) {
				t.Parallel()
				ws := sig.ws
				ws.failurePeer = p.peer
				obs := transportObservation{
					protocol: transportProtocolWS,
					err:      sig.err,
					ws:       ws,
				}
				diag := deriveTransportDiagnostic(obs)
				if diag == nil {
					t.Fatal("expected diagnostic")
				}
				assertEqual(t, "source", diag.Source, p.want)
				// Kind stays `disconnect` regardless of peer attribution —
				// the `kind` axis is orthogonal to `source`.
				assertEqual(t, "kind", diag.Kind, transportKindDisconnect)
			})
		}
	}
}

// TestTransportDiagnostic_WS_TimeoutSourceFixed locks in that the `timeout`
// branch retains its fixed upstream attribution regardless of `failurePeer`.
// Deadline-exceeded on the WS transport is a server-side convention (ctx
// deadline management belongs to the upstream side in this codebase) and
// peer attribution only governs disconnect-class signals.
func TestTransportDiagnostic_WS_TimeoutSourceFixed(t *testing.T) {
	t.Parallel()
	for _, peer := range []webSocketPeer{webSocketPeerUnknown, webSocketPeerClient, webSocketPeerUpstream} {
		peer := peer
		t.Run(fmt.Sprintf("peer=%d", peer), func(t *testing.T) {
			t.Parallel()
			obs := transportObservation{
				protocol: transportProtocolWS,
				err:      context.DeadlineExceeded,
				ws:       wsObservation{upgradeCompleted: true, failurePeer: peer},
			}
			diag := deriveTransportDiagnostic(obs)
			if diag == nil {
				t.Fatal("expected diagnostic")
			}
			assertEqual(t, "signal", diag.Signal, transportSignalTimeout)
			assertEqual(t, "source", diag.Source, transportSourceUpstream)
		})
	}
}

// TestTransportDiagnostic_WS_CanceledSourceFixed locks in the reverse
// invariant for `canceled`: ctx.Canceled always attributes to the client
// via ctx semantics. Upstream-tagged `failurePeer` on a cancel observation
// must not override that — cancel is a request-axis fact.
func TestTransportDiagnostic_WS_CanceledSourceFixed(t *testing.T) {
	t.Parallel()
	for _, peer := range []webSocketPeer{webSocketPeerUnknown, webSocketPeerClient, webSocketPeerUpstream} {
		peer := peer
		t.Run(fmt.Sprintf("peer=%d", peer), func(t *testing.T) {
			t.Parallel()
			obs := transportObservation{
				protocol: transportProtocolWS,
				err:      fmt.Errorf("relay aborted: %w", context.Canceled),
				ws:       wsObservation{upgradeCompleted: true, failurePeer: peer},
			}
			diag := deriveTransportDiagnostic(obs)
			if diag == nil {
				t.Fatal("expected diagnostic")
			}
			assertEqual(t, "signal", diag.Signal, transportSignalCanceled)
			assertEqual(t, "source", diag.Source, transportSourceClient)
		})
	}
}

// TestTransportDiagnostic_WS_ClientCloseErrorKeepsCloseCode guards the
// close-code + peer orthogonality: flipping `source` to `client` for a
// client-originated `close_error` must not drop the observed `CloseCode`
// pointer. The presence pointer is keyed on `closeError != nil`, not on
// attribution — a regression here would silently lose structured close
// codes for any client-originated close.
func TestTransportDiagnostic_WS_ClientCloseErrorKeepsCloseCode(t *testing.T) {
	t.Parallel()
	closeErr := &websocket.CloseError{Code: websocket.StatusGoingAway, Reason: "navigating away"}
	obs := transportObservation{
		protocol: transportProtocolWS,
		ws: wsObservation{
			closeError:        closeErr,
			upgradeCompleted:  true,
			anyFrameDelivered: true,
			failurePeer:       webSocketPeerClient,
		},
	}
	diag := deriveTransportDiagnostic(obs)
	if diag == nil {
		t.Fatal("expected diagnostic")
	}
	assertEqual(t, "source", diag.Source, transportSourceClient)
	assertEqual(t, "signal", diag.Signal, transportSignalCloseError)
	if diag.CloseCode == nil {
		t.Fatal("CloseCode pointer dropped on client-attributed close_error")
	}
	if *diag.CloseCode != int(websocket.StatusGoingAway) {
		t.Fatalf("CloseCode = %d, want %d", *diag.CloseCode, websocket.StatusGoingAway)
	}
}

// Unknown protocol must fail closed rather than emit uninitialized
// stage/source. This guards against a future enum value being added without
// updating the derivation switch.
func TestTransportDiagnostic_UnknownProtocol_ReturnsNil(t *testing.T) {
	t.Parallel()
	obs := transportObservation{
		protocol: transportProtocol(0), // zero value is not sse or ws
		err:      errors.New("boom"),
	}
	if diag := deriveTransportDiagnostic(obs); diag != nil {
		t.Fatalf("unknown protocol must return nil, got %+v", diag)
	}
}

// --- helpers ---------------------------------------------------------------

func assertEqual(t *testing.T, field, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q, want %q", field, got, want)
	}
}

func utf8ValidAndRuneCountAtMost(s string, limit int) bool {
	count := 0
	for range s {
		count++
	}
	return count <= limit
}

func runeCount(s string) int {
	count := 0
	for range s {
		count++
	}
	return count
}
