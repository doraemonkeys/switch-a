package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"switch-a/internal/model"

	"github.com/coder/websocket"
	"go.uber.org/zap"
)

// WebSocket forwarding constants.
const (
	// wsReadLimit caps message size to prevent unbounded memory usage.
	// 16 MB accommodates large AI payloads (e.g., base64-encoded audio for Realtime API).
	wsReadLimit = 16 * 1024 * 1024
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

	// HandshakeStatusCode records the HTTP status observed before the bidirectional
	// session started, whether the rejection came from the gateway or upstream.
	HandshakeStatusCode int

	// HandshakeBodySnippet captures the upstream HTTP error body from a rejected
	// WebSocket upgrade so logs can show the provider's actual reason.
	HandshakeBodySnippet string

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

type webSocketRelayResult struct {
	bytes       int64
	err         error
	failurePeer webSocketPeer
	errorOrder  uint32
}

type webSocketRelayOutcome struct {
	closeCode     websocket.StatusCode
	err           error
	terminalCause model.TerminalCause
}

type webSocketPeer uint8

const (
	webSocketPeerUnknown webSocketPeer = iota
	webSocketPeerClient
	webSocketPeerUpstream
)

type webSocketCommitState struct {
	mu        sync.Mutex
	committed bool
	source    model.CommitSource
}

func newWebSocketCommitState() *webSocketCommitState {
	return &webSocketCommitState{
		source: model.CommitUnknown,
	}
}

func (s *webSocketCommitState) Commit(source model.CommitSource) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.committed {
		return false
	}
	s.committed = true
	s.source = source
	return true
}

func (s *webSocketCommitState) Snapshot() (bool, model.CommitSource) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.committed, s.source
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
	return f.ForwardObserved(ctx, w, r, upstreamURL, extraHeaders, nil, nil)
}

// ForwardObserved behaves like Forward but additionally feeds relayed messages into an
// observer and emits a fallback commitment signal when the first upstream message is the
// only safe commitment boundary.
func (f *WebSocketForwarder) ForwardObserved(ctx context.Context, w http.ResponseWriter, r *http.Request, upstreamURL string, extraHeaders http.Header, observer WebSocketMessageObserver, onFirstUpstreamMessage func(WebSocketObservation)) (*WebSocketResult, error) {
	start := time.Now()

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
		if resp != nil {
			handshakeStatusCode = resp.StatusCode
			handshakeBodySnippet = drainReadCloserWithSnippet(resp.Body, 0)
		}
		return &WebSocketResult{
			Duration:             time.Since(start),
			HandshakeStatusCode:  handshakeStatusCode,
			HandshakeBodySnippet: handshakeBodySnippet,
			Err:                  err,
			TerminalCause:        classifyDialFailure(resp),
			CommitSource:         model.CommitUnknown,
		}, nil
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	upstreamConn.SetReadLimit(wsReadLimit)

	// Accept the client's WebSocket upgrade only after the provider handshake succeeds.
	// This avoids hiding upstream handshake failures behind an already-open proxy socket.
	clientConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		_ = upstreamConn.Close(websocket.StatusGoingAway, "client websocket upgrade rejected")
		return &WebSocketResult{
			Duration:      time.Since(start),
			Err:           err,
			TerminalCause: model.TerminalClientUpgradeRejected,
			CommitSource:  model.CommitUnknown,
		}, err
	}
	clientConn.SetReadLimit(wsReadLimit)

	// Both connections established — relay messages bidirectionally.
	result := f.relay(ctx, clientConn, upstreamConn, observer, onFirstUpstreamMessage)
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

// relay copies messages between client and upstream until one side closes.
// Uses a context-derived cancel to ensure both goroutines exit promptly.
func (f *WebSocketForwarder) relay(ctx context.Context, clientConn, upstreamConn *websocket.Conn, observer WebSocketMessageObserver, onFirstUpstreamMessage func(WebSocketObservation)) *WebSocketResult {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		clientToUpstream webSocketRelayResult
		upstreamToClient webSocketRelayResult
		wg               sync.WaitGroup
		errorOrder       atomic.Uint32
	)
	fallbackCommit := newWebSocketCommitState()

	var observeClient func(websocket.MessageType, []byte)
	var observeUpstream func(websocket.MessageType, []byte)
	if observer != nil {
		observeClient = observer.ObserveClientMessage
		observeUpstream = observer.ObserveUpstreamMessage
	}
	onUpstreamForwarded := func(_ websocket.MessageType, _ []byte) {
		observation := WebSocketObservation{SessionCommitted: true}
		if observer != nil {
			observation = observer.Snapshot()
			if observation.SessionCommitted {
				return
			}
			if observer.HasSemanticObservation() && !observation.ParseDegraded {
				return
			}
		}
		if !fallbackCommit.Commit(model.CommitUpstreamMessage) {
			return
		}
		if onFirstUpstreamMessage == nil {
			return
		}
		observation.SessionCommitted = true
		onFirstUpstreamMessage(observation)
	}

	wg.Add(2)

	// client → upstream
	go func() {
		defer wg.Done()
		n, failurePeer, err := relayMessages(
			ctx,
			upstreamConn,
			webSocketPeerUpstream,
			clientConn,
			webSocketPeerClient,
			observeClient,
			nil,
		)
		clientToUpstream = newWebSocketRelayResult(n, err, failurePeer, &errorOrder)
		if err != nil {
			cancel()
		}
	}()

	// upstream → client
	go func() {
		defer wg.Done()
		n, failurePeer, err := relayMessages(
			ctx,
			clientConn,
			webSocketPeerClient,
			upstreamConn,
			webSocketPeerUpstream,
			observeUpstream,
			onUpstreamForwarded,
		)
		upstreamToClient = newWebSocketRelayResult(n, err, failurePeer, &errorOrder)
		if err != nil {
			cancel()
		}
	}()

	wg.Wait()

	outcome := reduceWebSocketRelayErrors(clientToUpstream, upstreamToClient)

	// Best-effort clean close: propagate the close code to both peers.
	// coder/websocket's Close is non-blocking — it writes the close frame
	// and returns immediately. No need to wait for close handshake completion.
	closeMsg := ""
	if outcome.err != nil {
		closeMsg = outcome.err.Error()
		// Truncate to a valid UTF-8 boundary to avoid splitting multi-byte codepoints.
		// RFC 6455 §5.5 limits close reason to 125 bytes.
		closeMsg = truncateUTF8(closeMsg, 123)
	}
	propagatedCloseCode := sanitizeWebSocketCloseCode(outcome.closeCode, outcome.err)

	_ = clientConn.Close(propagatedCloseCode, closeMsg)
	_ = upstreamConn.Close(propagatedCloseCode, closeMsg)

	sessionCommitted, commitSource := fallbackCommit.Snapshot()
	return &WebSocketResult{
		SessionCommitted:      sessionCommitted,
		TerminalCause:         outcome.terminalCause,
		CommitSource:          commitSource,
		CloseCode:             outcome.closeCode,
		BytesClientToUpstream: clientToUpstream.bytes,
		BytesUpstreamToClient: upstreamToClient.bytes,
		Err:                   outcome.err,
	}
}

// relayMessages copies messages from src to dst until src closes or ctx is cancelled.
// Returns total bytes relayed and any error that terminated the relay.
func relayMessages(
	ctx context.Context,
	dst *websocket.Conn,
	dstPeer webSocketPeer,
	src *websocket.Conn,
	srcPeer webSocketPeer,
	observe func(messageType websocket.MessageType, data []byte),
	onForwarded func(messageType websocket.MessageType, data []byte),
) (int64, webSocketPeer, error) {
	var totalBytes int64
	for {
		msgType, data, err := src.Read(ctx)
		if err != nil {
			return totalBytes, srcPeer, err
		}
		totalBytes += int64(len(data))
		if observe != nil {
			observe(msgType, data)
		}
		if err := dst.Write(ctx, msgType, data); err != nil {
			return totalBytes, dstPeer, err
		}
		if onForwarded != nil {
			onForwarded(msgType, data)
		}
	}
}

// httpToWSURL converts an HTTP(S) URL to a WS(S) URL.
// Provider BaseURLs are stored as https://... but WebSocket requires wss://... scheme.
// Uses case-insensitive scheme comparison for defensive robustness.
func httpToWSURL(rawURL string) string {
	lower := strings.ToLower(rawURL)
	if strings.HasPrefix(lower, "https://") {
		return "wss://" + rawURL[len("https://"):]
	}
	if strings.HasPrefix(lower, "http://") {
		return "ws://" + rawURL[len("http://"):]
	}
	// Already ws:// or wss://, pass through.
	return rawURL
}

// isNormalClose returns true if the error represents an orderly WebSocket close.
// StatusNormalClosure and StatusGoingAway are considered clean disconnects
// that should not be surfaced as errors to callers.
func isNormalClose(err error) bool {
	var closeErr websocket.CloseError
	if !errors.As(err, &closeErr) {
		return false
	}
	return closeErr.Code == websocket.StatusNormalClosure ||
		closeErr.Code == websocket.StatusGoingAway
}

func isCloseWithoutStatus(err error) bool {
	return websocket.CloseStatus(err) == websocket.StatusNoStatusRcvd
}

func isUnexpectedPeerDisconnect(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || isCloseWithoutStatus(err)
}

func newWebSocketRelayResult(bytes int64, err error, failurePeer webSocketPeer, errorOrder *atomic.Uint32) webSocketRelayResult {
	result := webSocketRelayResult{
		bytes:       bytes,
		err:         err,
		failurePeer: failurePeer,
	}
	if err != nil {
		// Capture order before canceling the sibling relay leg so reduction can preserve
		// the actual trigger instead of whichever struct happens to be examined first.
		result.errorOrder = errorOrder.Add(1)
	}
	return result
}

func reduceWebSocketRelayErrors(clientToUpstream, upstreamToClient webSocketRelayResult) webSocketRelayOutcome {
	primary, secondary := orderWebSocketRelayResults(clientToUpstream, upstreamToClient)
	return reduceOrderedWebSocketRelayResults(primary, secondary)
}

func orderWebSocketRelayResults(first, second webSocketRelayResult) (webSocketRelayResult, webSocketRelayResult) {
	switch {
	case first.errorOrder == 0:
		return second, first
	case second.errorOrder == 0:
		return first, second
	case first.errorOrder <= second.errorOrder:
		return first, second
	default:
		return second, first
	}
}

func reduceOrderedWebSocketRelayResults(primary, secondary webSocketRelayResult) webSocketRelayOutcome {
	for _, candidate := range []webSocketRelayResult{primary, secondary} {
		if candidate.err == nil {
			continue
		}
		terminalCause := classifyRelayTerminalCause(candidate.err, candidate.failurePeer)
		if isNormalClose(candidate.err) {
			return webSocketRelayOutcome{
				closeCode:     extractCloseCode(candidate.err),
				terminalCause: terminalCause,
			}
		}
		if isUnexpectedPeerDisconnect(candidate.err) {
			return webSocketRelayOutcome{
				closeCode:     websocket.StatusNoStatusRcvd,
				terminalCause: terminalCause,
			}
		}
		return webSocketRelayOutcome{
			closeCode:     extractCloseCode(candidate.err),
			err:           candidate.err,
			terminalCause: terminalCause,
		}
	}
	return webSocketRelayOutcome{
		closeCode:     websocket.StatusNormalClosure,
		terminalCause: model.TerminalCleanClose,
	}
}

func mergeWebSocketObservation(result *WebSocketResult, observation WebSocketObservation) {
	result.Model = observation.Model
	result.TokenUsage = observation.TokenUsage
	result.UpstreamError = observation.UpstreamError
	if observation.SessionCommitted {
		result.SessionCommitted = true
		result.CommitSource = model.CommitSemantic
	}
}

func classifyDialFailure(resp *http.Response) model.TerminalCause {
	if resp != nil && resp.StatusCode > 0 {
		return model.TerminalUpstreamHandshakeRejected
	}
	return model.TerminalUpstreamTransportError
}

func classifyRelayTerminalCause(err error, failurePeer webSocketPeer) model.TerminalCause {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return model.TerminalInternalError
	}
	switch failurePeer {
	case webSocketPeerClient:
		return model.TerminalClientDisconnect
	case webSocketPeerUpstream:
		if isNormalClose(err) {
			return model.TerminalCleanClose
		}
		return model.TerminalUpstreamTransportError
	default:
		return model.TerminalInternalError
	}
}

func sanitizeWebSocketCloseCode(code websocket.StatusCode, sessionErr error) websocket.StatusCode {
	switch code {
	case websocket.StatusNoStatusRcvd, websocket.StatusAbnormalClosure, websocket.StatusTLSHandshake:
		if sessionErr == nil {
			return websocket.StatusNormalClosure
		}
		return websocket.StatusInternalError
	default:
		return code
	}
}

// truncateUTF8 truncates s to at most maxBytes without splitting multi-byte codepoints.
func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// Walk backwards from the limit to find the last valid rune boundary.
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}

// extractCloseCode extracts the WebSocket close status code from an error.
// Returns StatusNormalClosure if the error doesn't contain a close code.
func extractCloseCode(err error) websocket.StatusCode {
	var closeErr websocket.CloseError
	if errors.As(err, &closeErr) {
		return closeErr.Code
	}
	return websocket.StatusNormalClosure
}
