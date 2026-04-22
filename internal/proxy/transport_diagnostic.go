package proxy

import (
	"context"
	"errors"
	"io"

	"github.com/coder/websocket"
)

// Transport diagnostic schema constants.
//
// These values are the stable wire contract consumed by the frontend v2
// renderer: any rename here is a schema break. `kind` / `signal` / `stage` /
// `source` form three orthogonal axes — do not collapse them into a single
// enum, and do not add protocol-specific `_sse` / `_ws` suffixes. Every value
// is declared here rather than inline at call sites to keep grep-ability high
// and to force the typechecker to flag typos.
const (
	transportSourceUpstream = "upstream"
	transportSourceClient   = "client"

	// SSE stage semantics are driven by HTTP response commitment state; WS
	// stage semantics are driven by upgrade + first-frame state. The three
	// buckets are shared so the frontend can render one helper.
	transportStagePreConnectionVisible = "pre_connection_visible"
	transportStagePrePayloadVisible    = "pre_payload_visible"
	transportStagePostPayloadVisible   = "post_payload_visible"

	transportKindTimeout       = "timeout"
	transportKindDisconnect    = "disconnect"
	transportKindProtocolError = "protocol_error"
	transportKindLocalError    = "local_error"

	// SSE signal values.
	transportSignalSSEIdleTimeout    = "sse_idle_timeout"
	transportSignalUpstreamReadError = "upstream_read_error"
	transportSignalClientWriteError  = "client_write_error"

	// WS signal values.
	transportSignalEOF                = "eof"
	transportSignalUnexpectedEOF      = "unexpected_eof"
	transportSignalCloseWithoutStatus = "close_without_status"
	transportSignalCloseError         = "close_error"
	transportSignalTimeout            = "timeout"
	transportSignalCanceled           = "canceled"

	// Shared fallback.
	transportSignalUnknownTransport = "unknown_transport"
)

// transportProtocol is an internal discriminator so the derivation function
// can branch on protocol without the two observation sub-structs leaking into
// the public `transportDiagnostic` evidence payload.
type transportProtocol uint8

const (
	transportProtocolSSE transportProtocol = iota + 1
	transportProtocolWS
)

// transportRawErrorSnippetLimitRunes bounds raw_error_snippet.
//
// The existing evidence layer (websocket_assessment_evidence.go) truncates
// redacted snippets at 512 bytes; the diagnostic raw snippet is intentionally
// tighter at 256 runes because it is the *source* fact, not a post-redaction
// artifact — we want it to round-trip into JSON cheaply and never dominate
// the 4 KiB evidence JSON budget.
const transportRawErrorSnippetLimitRunes = 256

// sseObservation carries SSE-specific runtime facts.
//
// Field-level rationale:
//   - `firstByteVisible`: whether a body byte was actually committed to the
//     client; this is the sole determinant of `post_payload_visible`.
//   - `headerCommitted`: whether HTTP headers were flushed; together with
//     `firstByteVisible` this produces the three-state stage.
//   - `isStatusFailover`: the synthetic `upstream returned status %d` error
//     produced by failoverForwardResponse is a status-classification fact,
//     not a transport failure — bypass explicitly so renames of the error
//     string do not leak into diagnostic.
//   - `isClientWriteError`: the observation layer knows whether the error
//     came out of writing to the client vs reading from upstream; the
//     derivation function treats this as authoritative rather than trying
//     to sniff error text.
type sseObservation struct {
	firstByteVisible   bool
	headerCommitted    bool
	isStatusFailover   bool
	isClientWriteError bool
}

// wsObservation carries WebSocket-specific runtime facts.
//
// Field-level rationale:
//   - `closeError`: the real observed close frame. Use `*websocket.CloseError`
//     rather than a value type so presence is unambiguous (a zero-valued
//     CloseError{} with Code 0 is a legitimate observation).
//   - `closedWithoutStatus`: set by the relay layer when it observed
//     EOF/unexpected-EOF/no-status-received but could not extract a concrete
//     CloseError. The plan requires `close_without_status` to fire even when
//     `closeError == nil`, so this flag is the only way for the derivation
//     function to learn that fact without reading the error text.
//   - `upgradeCompleted` / `anyFrameDelivered`: stage discriminators for WS.
//     Dial-phase failures must produce `pre_connection_visible`; upgrade
//     completion alone lifts the stage to `pre_payload_visible`; the first
//     delivered frame lifts it to `post_payload_visible`.
//   - `failurePeer`: which side the reduction layer attributed the failure
//     to. Drives the `source` axis for disconnect-family signals so a
//     client-originated close is not misreported as upstream. The zero
//     value (`webSocketPeerUnknown`) is treated as upstream — this matches
//     the pre-attribution default and keeps any observation the reduce
//     layer never tagged from flipping to `client` by accident.
type wsObservation struct {
	closeError          *websocket.CloseError
	closedWithoutStatus bool
	upgradeCompleted    bool
	anyFrameDelivered   bool
	failurePeer         webSocketPeer
}

// transportObservation is the input to deriveTransportDiagnostic.
//
// The protocol discriminator drives stage derivation and signal
// classification. SSE/WS sub-structs keep the shape tight: a WS observation
// cannot accidentally set `firstByteVisible` (there is no such concept for
// framed protocols) and an SSE observation cannot accidentally set
// `closeError`.
type transportObservation struct {
	protocol transportProtocol

	// Shared runtime signals.
	err                         error
	ctxErr                      error
	isSuppressedSyntheticFinal  bool

	sse sseObservation
	ws  wsObservation
}

// transportDiagnostic is the evidence-layer projection of a
// transportObservation. JSON tags are part of the wire contract — do not
// reorder casually.
//
// `CloseCode` is `*int` (not `int` with omitempty) because 0 is a legitimate
// observed close code and the presence semantics must never collapse into
// "not observed". `CloseReasonSnippet` carries the human-readable close
// frame reason after sanitization by the evidence builder — the derivation
// function captures the raw reason; redaction lives one layer up.
type transportDiagnostic struct {
	Source             string `json:"source"`
	Stage              string `json:"stage"`
	Kind               string `json:"kind"`
	Signal             string `json:"signal"`
	RawErrorSnippet    string `json:"raw_error_snippet,omitempty"`
	CloseCode          *int   `json:"close_code,omitempty"`
	CloseReasonSnippet string `json:"close_reason_snippet,omitempty"`
}

// deriveTransportDiagnostic is the single point of truth for translating a
// runtime observation into an evidence-level diagnostic. It is a pure
// function: no IO, no ctx reads, no package-level state. Every branch is
// driven solely by `obs`.
//
// Short-circuit order is load-bearing and mirrors the plan:
//  1. No transport signal at all — nothing to report. This also absorbs the
//     "pure client cancel" case (plan rule #4): if `ctxErr` is set but there
//     is no `err` / `closeError` / `closedWithoutStatus`, the observation has
//     no transport signal by definition.
//  2. Status failover — status-class fact, explicitly not a transport failure.
//  3. Suppressed synthetic final — the synthetic session must not inherit an
//     attempt's observation; the builder enforces zeroing, and this flag is
//     belt-and-braces on the derivation side.
//  4. Otherwise — even if ctxErr is set, a real transport signal wins.
func deriveTransportDiagnostic(obs transportObservation) *transportDiagnostic {
	if !observationHasTransportSignal(obs) {
		return nil
	}
	if obs.protocol == transportProtocolSSE && obs.sse.isStatusFailover {
		return nil
	}
	if obs.isSuppressedSyntheticFinal {
		return nil
	}

	switch obs.protocol {
	case transportProtocolSSE:
		return deriveSSETransportDiagnostic(obs)
	case transportProtocolWS:
		return deriveWSTransportDiagnostic(obs)
	default:
		// Unknown protocol is a programming error; fail closed (no evidence)
		// rather than emit a diagnostic with uninitialized stage/source.
		return nil
	}
}

// observationHasTransportSignal captures the "is there any real transport
// fact to report?" gate. Keeping it as a helper makes the short-circuit
// reasoning in deriveTransportDiagnostic self-documenting and keeps the WS
// triple-signal (`err`, `closeError`, `closedWithoutStatus`) in one place.
func observationHasTransportSignal(obs transportObservation) bool {
	if obs.err != nil {
		return true
	}
	if obs.protocol == transportProtocolWS {
		if obs.ws.closeError != nil {
			return true
		}
		if obs.ws.closedWithoutStatus {
			return true
		}
	}
	return false
}

func deriveSSETransportDiagnostic(obs transportObservation) *transportDiagnostic {
	signal, kind, source := classifySSESignal(obs)
	return &transportDiagnostic{
		Source:          source,
		Stage:           sseStage(obs.sse),
		Kind:            kind,
		Signal:          signal,
		RawErrorSnippet: truncateRawErrorSnippet(errorText(obs.err)),
	}
}

func classifySSESignal(obs transportObservation) (signal, kind, source string) {
	switch {
	case errors.Is(obs.err, ErrSSEIdleTimeout):
		return transportSignalSSEIdleTimeout, transportKindTimeout, transportSourceUpstream
	case IsUpstreamReadError(obs.err):
		return transportSignalUpstreamReadError, transportKindProtocolError, transportSourceUpstream
	case obs.sse.isClientWriteError:
		return transportSignalClientWriteError, transportKindProtocolError, transportSourceClient
	default:
		return transportSignalUnknownTransport, transportKindLocalError, transportSourceUpstream
	}
}

func sseStage(sse sseObservation) string {
	switch {
	case sse.firstByteVisible:
		return transportStagePostPayloadVisible
	case sse.headerCommitted:
		return transportStagePrePayloadVisible
	default:
		return transportStagePreConnectionVisible
	}
}

func deriveWSTransportDiagnostic(obs transportObservation) *transportDiagnostic {
	signal, kind, source := classifyWSSignal(obs)
	diag := &transportDiagnostic{
		Source:          source,
		Stage:           wsStage(obs.ws),
		Kind:            kind,
		Signal:          signal,
		RawErrorSnippet: truncateRawErrorSnippet(errorText(obs.err)),
	}
	// Close code presence is strictly tied to a real observed CloseError.
	// The synthetic `StatusNoStatusRcvd` value that reduction writes into
	// WebSocketResult.CloseCode is intentionally not read here — only the
	// real frame counts. 0 is a legitimate code, so presence is carried by
	// the pointer.
	if obs.ws.closeError != nil {
		code := int(obs.ws.closeError.Code)
		diag.CloseCode = &code
		diag.CloseReasonSnippet = truncateRawErrorSnippet(obs.ws.closeError.Reason)
	}
	return diag
}

func classifyWSSignal(obs transportObservation) (signal, kind, source string) {
	// Ordering matters: a CloseError that is also an EOF wrapper should be
	// classified by its frame code first, since `close_error` with a real
	// code is strictly more informative than `eof`.
	//
	// Disconnect-family source attribution is driven by `failurePeer`, not
	// hardcoded to upstream: a client-originated close_error still carries
	// `kind = disconnect` but the `source` axis must reflect who tore the
	// connection down. Non-disconnect branches (`timeout`, `canceled`,
	// `unknown_transport`) retain their fixed attribution — timeout is
	// server-side by convention, cancel is already driven by ctx semantics,
	// and unknown_transport has no peer signal to rely on.
	if obs.ws.closeError != nil {
		return transportSignalCloseError, transportKindDisconnect, wsDisconnectSource(obs)
	}
	switch {
	case errors.Is(obs.err, context.DeadlineExceeded):
		return transportSignalTimeout, transportKindTimeout, transportSourceUpstream
	case errors.Is(obs.err, context.Canceled):
		return transportSignalCanceled, transportKindLocalError, transportSourceClient
	case errors.Is(obs.err, io.ErrUnexpectedEOF):
		return transportSignalUnexpectedEOF, transportKindDisconnect, wsDisconnectSource(obs)
	case errors.Is(obs.err, io.EOF):
		return transportSignalEOF, transportKindDisconnect, wsDisconnectSource(obs)
	case obs.ws.closedWithoutStatus:
		// Fall through to close_without_status only after EOF-style checks,
		// since close_without_status is the "we know the peer dropped but
		// have no frame" bucket — EOF wrappers carry strictly more info.
		return transportSignalCloseWithoutStatus, transportKindDisconnect, wsDisconnectSource(obs)
	default:
		return transportSignalUnknownTransport, transportKindLocalError, transportSourceUpstream
	}
}

// wsDisconnectSource centralizes the peer-to-source projection for
// disconnect-family signals. Extracted as a helper so the four call sites
// above stay aligned and the upstream-default convention (zero-value
// `failurePeer` → upstream) is declared in one spot rather than replicated.
func wsDisconnectSource(obs transportObservation) string {
	if obs.ws.failurePeer == webSocketPeerClient {
		return transportSourceClient
	}
	return transportSourceUpstream
}

func wsStage(ws wsObservation) string {
	switch {
	case ws.anyFrameDelivered:
		return transportStagePostPayloadVisible
	case ws.upgradeCompleted:
		return transportStagePrePayloadVisible
	default:
		return transportStagePreConnectionVisible
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// truncateRawErrorSnippet caps a raw fact at a rune-safe boundary. Redaction
// (bearer tokens, API keys) is applied by the evidence builder one layer up;
// the derivation layer intentionally does not sanitize so unit tests can
// assert round-tripping without pulling in regex fixtures.
//
// The rune-indexed for-range loop doubles as both counter and cut-point
// finder: when `runes` reaches the limit, `i` is already positioned at the
// byte index of the next rune — a rune-safe slice boundary by construction.
func truncateRawErrorSnippet(s string) string {
	runes := 0
	for i := range s {
		if runes == transportRawErrorSnippetLimitRunes {
			return s[:i]
		}
		runes++
	}
	return s
}
