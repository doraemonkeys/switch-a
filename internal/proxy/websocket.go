package proxy

import (
	"context"
	"net/http"
	"time"

	"switch-a/internal/model"

	"github.com/coder/websocket"
	"go.uber.org/zap"
)

// WebSocket forwarding constants.
const (
	// wsReadLimit caps message size to prevent unbounded memory usage.
	// 16 MB accommodates large AI payloads (e.g., base64-encoded audio for Realtime API).
	wsReadLimit = 16 * 1024 * 1024

	// preVisibleClientReplayBufferLimitBytes bounds buffered client application payloads
	// so semantic replacement never turns an invisible session window into unbounded memory.
	preVisibleClientReplayBufferLimitBytes = 1 * 1024 * 1024

	// preVisibleClientReplayBufferLimitMessages prevents an endless stream of tiny
	// pre-visible client frames from pinning memory even when the byte budget stays low.
	preVisibleClientReplayBufferLimitMessages = 128

	// webSocketCloseReasonByteLimit keeps propagated close reasons within RFC 6455's
	// 125-byte control-frame limit after reserving two bytes for the close status code.
	webSocketCloseReasonByteLimit = 123

	// webSocketFallbackWriteTimeout keeps the terminal original-payload fallback
	// write independent from a canceled upstream request context while still
	// bounding how long the proxy waits on a broken downstream socket.
	webSocketFallbackWriteTimeout = 5 * time.Second

	// webSocketTerminalCloseFlushTimeout gives a terminal gateway event a brief
	// window to flush its close handshake before the handler proceeds. Without a
	// bounded wait, reconnect-required can degrade into a raw socket reset; with
	// an unbounded wait, the handler can hang on a peer that never answers.
	webSocketTerminalCloseFlushTimeout = 50 * time.Millisecond

	// webSocketPreVisibleClientReadWindow bounds how long the serialized pre-visible
	// path waits for a prompt-first client message before falling back to normal
	// concurrent relay, which keeps server-first sessions moving.
	webSocketPreVisibleClientReadWindow = 50 * time.Millisecond

	webSocketSemanticReplacementCloseReason = "semantic replacement"
)

// WebSocketDialer abstracts upstream WebSocket connection establishment.
// Defined at the consumer site (not the implementation) per Go interface convention.
type WebSocketDialer interface {
	Dial(ctx context.Context, url string, opts *websocket.DialOptions) (*websocket.Conn, *http.Response, error)
}

// realDialer wraps coder/websocket's top-level Dial function.
type realDialer struct{}

func (realDialer) Dial(ctx context.Context, url string, opts *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
	return websocket.Dial(ctx, url, opts)
}

// WebSocketForwarder handles bidirectional WebSocket forwarding to upstream providers.
// Architecturally parallel to Transport (which wraps *http.Client for HTTP traffic):
// WebSocketForwarder wraps coder/websocket for WebSocket traffic.
type WebSocketForwarder struct {
	dialer WebSocketDialer
	logger *zap.Logger
}

// WebSocketForwarderConfig holds configuration for creating a WebSocketForwarder.
type WebSocketForwarderConfig struct {
	Dialer WebSocketDialer // nil defaults to real coder/websocket dialer
	Logger *zap.Logger
}

// NewWebSocketForwarder creates a new WebSocket forwarder.
func NewWebSocketForwarder(cfg WebSocketForwarderConfig) *WebSocketForwarder {
	dialer := cfg.Dialer
	if dialer == nil {
		dialer = realDialer{}
	}
	return &WebSocketForwarder{
		dialer: dialer,
		logger: cfg.Logger,
	}
}

// WebSocketResult reports the outcome of a WebSocket forwarding session.
// The caller uses this for health tracking, request logging, and active registry cleanup.
type WebSocketResult struct {
	// HandshakeAccepted indicates whether the selected provider completed the
	// upstream WebSocket handshake. Client accept can still have succeeded when
	// this is false because the proxy upgrades the client before dialing upstream.
	HandshakeAccepted bool

	// ClientAccepted marks the explicit boundary where the proxy upgraded the
	// downstream connection. Session orchestration must not infer this from
	// provider handshake state because those boundaries diverge during failover.
	ClientAccepted bool

	// ClientVisible marks the first successful upstream application frame write to
	// the client. This closes the cross-provider failover window independently of
	// semantic commitment or billing attribution.
	ClientVisible bool

	// SessionCommitted marks the point where the provider delivered meaningful
	// upstream service. Handshake success alone is not sufficient for sticky or
	// health policy because semantic failures can arrive immediately after 101.
	SessionCommitted bool

	// TerminalCause records why the session ended so callers can derive sticky,
	// health, and logging decisions from an explicit lifecycle model.
	TerminalCause model.TerminalCause

	// CommitSource explains whether commitment came from a semantic observer or
	// from the first upstream message fallback path.
	CommitSource model.CommitSource

	// RecoveryAction is the client-facing next step for the session as a whole.
	// It intentionally does not live on individual attempts because reconnect
	// guidance only becomes meaningful after the gateway resolves recovery.
	RecoveryAction model.RecoveryAction

	// HandshakeStatusCode records the HTTP status observed before the bidirectional
	// session started, whether the rejection came from the gateway or upstream.
	HandshakeStatusCode int

	// HandshakeBodySnippet captures the upstream HTTP error body from a rejected
	// WebSocket upgrade so logs can show the provider's actual reason.
	HandshakeBodySnippet string

	// HandshakeHeaders preserves the upstream handshake response headers so the
	// handler can apply the same provider-failure semantics it uses on HTTP.
	HandshakeHeaders http.Header

	// HandshakeObservedAt fixes the response timestamp for relative reset-window
	// headers. WebSocket health handling runs after orchestration, so recomputing
	// these windows from a later wall clock would incorrectly extend cooldowns.
	HandshakeObservedAt time.Time

	// CloseCode is the WebSocket close status code, if available.
	CloseCode websocket.StatusCode

	// Duration is the total connection lifetime (from upgrade to close).
	Duration time.Duration

	// BytesClientToUpstream is total bytes relayed client → upstream.
	BytesClientToUpstream int64

	// BytesUpstreamToClient is total bytes relayed upstream → client.
	BytesUpstreamToClient int64

	// Err captures any error that terminated the session (nil for clean close).
	Err error

	// Model is the semantic model observed from relayed events when the upgrade request
	// itself did not carry enough information to identify the billed model.
	Model string

	// TokenUsage aggregates billable usage emitted during the WebSocket session.
	TokenUsage *TokenUsage

	// UpstreamError captures an error event emitted after the WebSocket upgrade
	// succeeded. This preserves the provider's semantic failure when the transport
	// itself only reports a generic close frame.
	UpstreamError *WebSocketUpstreamError
}

// Clone returns an isolated snapshot so session-level gateway fallback handling
// cannot rewrite the provider-attempt facts that drive persistence and health.
func (r *WebSocketResult) Clone() *WebSocketResult {
	if r == nil {
		return nil
	}
	clone := *r
	if r.HandshakeHeaders != nil {
		clone.HandshakeHeaders = r.HandshakeHeaders.Clone()
	}
	clone.TokenUsage = r.TokenUsage.Clone()
	clone.UpstreamError = r.UpstreamError.Clone()
	return &clone
}

// Forward accepts a client WebSocket upgrade, dials the upstream, and relays messages
// bidirectionally until either side closes or the context is cancelled.
//
// Error contract (two channels):
//   - err != nil: client accept failed. The caller should treat this as a client-side
//     issue (not a provider failure) and must NOT write any further HTTP response.
//   - err == nil, result.HandshakeAccepted == false: upstream dial failed before the proxy
//     upgraded the client. The caller still owns the HTTP response and can surface the
//     real handshake status (for example 401/426) instead of collapsing it into a close frame.
//   - err == nil, result.HandshakeAccepted == true: relay completed. result.Err is non-nil
//     only for abnormal terminations (not for normal close codes).
//
// The caller is responsible for provider selection, health reporting, and cleanup.
// Forward only handles the transport-level concerns: accept, dial, relay, close.
func (f *WebSocketForwarder) Forward(ctx context.Context, w http.ResponseWriter, r *http.Request, upstreamURL string, extraHeaders http.Header) (*WebSocketResult, error) {
	return f.ForwardObserved(ctx, w, r, upstreamURL, extraHeaders, nil, nil, nil)
}

// ForwardObserved behaves like Forward but additionally feeds relayed messages into an
// observer and emits a fallback commitment signal when the first client-visible upstream
// message is the only safe commitment boundary.
func (f *WebSocketForwarder) ForwardObserved(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	upstreamURL string,
	extraHeaders http.Header,
	observer WebSocketMessageObserver,
	onFirstUpstreamMessage func(WebSocketObservation),
	onClientVisible func(webSocketVisibleWriteContext),
) (*WebSocketResult, error) {
	start := time.Now()

	upstreamConn, dialResult := f.dialUpstream(ctx, upstreamURL, extraHeaders)
	if dialResult != nil {
		dialResult.Duration = time.Since(start)
		return dialResult, nil
	}
	clientConn, err := f.acceptClient(w, r)
	if err != nil {
		_ = upstreamConn.Close(websocket.StatusGoingAway, "client websocket upgrade rejected")
		return &WebSocketResult{
			Duration:          time.Since(start),
			HandshakeAccepted: true,
			Err:               err,
			TerminalCause:     model.TerminalClientUpgradeRejected,
			CommitSource:      model.CommitUnknown,
		}, err
	}

	lifecycle := newWebSocketLifecycleState()
	lifecycle.MarkClientAccepted()

	relayResult := f.relay(ctx, clientConn, upstreamConn, webSocketRelayOptions{
		Observer:               observer,
		OnFirstUpstreamMessage: onFirstUpstreamMessage,
		OnClientVisible:        onClientVisible,
		Lifecycle:              lifecycle,
	})
	result := relayResult.toWebSocketResult()
	result.HandshakeAccepted = true
	result.Duration = time.Since(start)
	if observer != nil {
		observation := observer.Snapshot()
		mergeWebSocketObservation(result, observation)
	}
	if result.UpstreamError != nil {
		result.TerminalCause = model.TerminalUpstreamSemanticError
	}
	return result, nil
}

func (f *WebSocketForwarder) dialUpstream(ctx context.Context, upstreamURL string, extraHeaders http.Header) (*websocket.Conn, *WebSocketResult) {
	// Dial the upstream WebSocket endpoint before accepting the client upgrade.
	// This preserves upstream handshake semantics for the caller, which is required for:
	//   - surfacing 426 so Codex CLI can fall back to HTTP
	//   - retrying 401 after refreshing provider-managed credentials
	dialHeaders := extraHeaders.Clone()
	if dialHeaders == nil {
		dialHeaders = make(http.Header)
	}
	EnsureExplicitUserAgentHeader(dialHeaders)
	upstreamConn, resp, err := f.dialer.Dial(ctx, upstreamURL, &websocket.DialOptions{
		HTTPHeader: dialHeaders,
	})
	if err != nil {
		var handshakeStatusCode int
		var handshakeBodySnippet string
		var handshakeHeaders http.Header
		var handshakeObservedAt time.Time
		if resp != nil {
			handshakeObservedAt = time.Now()
			handshakeStatusCode = resp.StatusCode
			handshakeHeaders = resp.Header.Clone()
			handshakeBodySnippet = drainReadCloserWithSnippet(resp.Body, 0)
		}
		return nil, &WebSocketResult{
			HandshakeStatusCode:  handshakeStatusCode,
			HandshakeBodySnippet: handshakeBodySnippet,
			HandshakeHeaders:     handshakeHeaders,
			HandshakeObservedAt:  handshakeObservedAt,
			Err:                  err,
			TerminalCause:        classifyDialFailure(resp),
			CommitSource:         model.CommitUnknown,
		}
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	upstreamConn.SetReadLimit(wsReadLimit)
	return upstreamConn, nil
}

func (f *WebSocketForwarder) acceptClient(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	// Accept the client's WebSocket upgrade only after the provider handshake succeeds.
	// This avoids hiding upstream handshake failures behind an already-open proxy socket.
	clientConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return nil, err
	}
	clientConn.SetReadLimit(wsReadLimit)
	return clientConn, nil
}
