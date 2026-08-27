package proxy

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

var (
	ErrReadTimeout    = errors.New("upstream response idle timeout")
	ErrSSEIdleTimeout = errors.New("upstream SSE idle timeout")
)

// TransportConfig separates exchange-establishment deadlines from body idle
// deadlines. The latter are enforced by responseanalysis after body ownership
// moves out of the transport.
type TransportConfig struct {
	ConnectTimeout   time.Duration
	FirstByteTimeout time.Duration
	ReadTimeout      time.Duration
	SSEIdleTimeout   time.Duration
}

// Transport is intentionally fetch-only. Client writing, draining, decoding,
// and response classification belong to the pending-response coordinator.
type Transport struct {
	upstream *upstreamtransport.Transport
}

func NewTransport(config TransportConfig) *Transport {
	return &Transport{upstream: upstreamtransport.New(upstreamtransport.Config{
		ConnectTimeout:   config.ConnectTimeout,
		FirstByteTimeout: config.FirstByteTimeout,
	})}
}

func (t *Transport) FetchUpstream(ctx context.Context, request *http.Request) (*upstreamtransport.Response, error) {
	return t.upstream.Fetch(ctx, request)
}

func (t *Transport) CloseIdleConnections() {
	if t != nil && t.upstream != nil {
		t.upstream.CloseIdleConnections()
	}
}

func BuildUpstreamRequest(
	ctx context.Context,
	method string,
	upstreamURL string,
	body []byte,
	originalRequest *http.Request,
) (*http.Request, error) {
	return upstreamtransport.BuildRequest(ctx, method, upstreamURL, body, originalRequest)
}

func BuildUpstreamRequestWithPolicy(
	ctx context.Context,
	method string,
	upstreamURL string,
	body []byte,
	originalRequest *http.Request,
	policy upstreamtransport.RequestPolicy,
) (*http.Request, error) {
	return upstreamtransport.BuildRequestWithPolicy(ctx, method, upstreamURL, body, originalRequest, policy)
}

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

	// SSE stage semantics are driven by HTTP response commitment state. The
	// buckets match the cross-transport evidence contract consumed by the UI.
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

	// Shared fallback.
	transportSignalUnknownTransport = "unknown_transport"
)

// transportProtocol is an internal discriminator so the derivation function
// can branch on protocol without the two observation sub-structs leaking into
// the public `transportDiagnostic` evidence payload.
type transportProtocol uint8

const (
	transportProtocolSSE transportProtocol = iota + 1
)

// transportRawErrorSnippetLimitRunes bounds raw_error_snippet.
//
// The diagnostic raw snippet is intentionally capped at 256 runes because it
// is the source fact, not a post-redaction artifact; it must round-trip into
// JSON cheaply and never dominate the evidence JSON budget.
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

// transportObservation is the input to deriveTransportDiagnostic.
//
// The protocol discriminator makes unsupported transports fail closed while
// the nested SSE value keeps protocol-specific commitment facts explicit.
type transportObservation struct {
	protocol transportProtocol

	// Shared runtime signals.
	err                        error
	ctxErr                     error
	isSuppressedSyntheticFinal bool

	sse sseObservation
}

// transportDiagnostic is the evidence-layer projection of a
// transportObservation. JSON tags are part of the wire contract — do not
// reorder casually.
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
//     pure-client-cancel case: a context error without an observed transport
//     error is not transport evidence.
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
	default:
		// Unknown protocol is a programming error; fail closed (no evidence)
		// rather than emit a diagnostic with uninitialized stage/source.
		return nil
	}
}

// observationHasTransportSignal keeps context cancellation from becoming
// transport evidence unless the response path observed an actual error.
func observationHasTransportSignal(obs transportObservation) bool {
	return obs.err != nil
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
