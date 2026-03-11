package proxy

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

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
	// ConnectSuccess indicates whether the upstream handshake succeeded.
	// When false, the client was never upgraded (received an HTTP error).
	ConnectSuccess bool

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
}

// Forward accepts a client WebSocket upgrade, dials the upstream, and relays messages
// bidirectionally until either side closes or the context is cancelled.
//
// Error contract (two channels):
//   - err != nil: client accept failed. The caller should treat this as a client-side
//     issue (not a provider failure) and must NOT write any further HTTP response.
//   - err == nil, result.ConnectSuccess == false: upstream dial failed after the client
//     was already upgraded. The client received a close frame with StatusBadGateway.
//   - err == nil, result.ConnectSuccess == true: relay completed. result.Err is non-nil
//     only for abnormal terminations (not for normal close codes).
//
// The caller is responsible for provider selection, health reporting, and cleanup.
// Forward only handles the transport-level concerns: accept, dial, relay, close.
func (f *WebSocketForwarder) Forward(ctx context.Context, w http.ResponseWriter, r *http.Request, upstreamURL string, extraHeaders http.Header) (*WebSocketResult, error) {
	return f.ForwardObserved(ctx, w, r, upstreamURL, extraHeaders, nil)
}

// ForwardObserved behaves like Forward but additionally feeds relayed messages into an observer.
// This keeps transport responsibilities centralized while allowing higher layers to extract
// domain fields such as model resolution and usage from event-driven protocols.
func (f *WebSocketForwarder) ForwardObserved(ctx context.Context, w http.ResponseWriter, r *http.Request, upstreamURL string, extraHeaders http.Header, observer WebSocketMessageObserver) (*WebSocketResult, error) {
	start := time.Now()

	// Accept the client's WebSocket upgrade.
	// InsecureSkipVerify allows any Origin since this is an API proxy, not a browser-facing app.
	clientConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return &WebSocketResult{
			Duration: time.Since(start),
			Err:      err,
		}, err
	}
	clientConn.SetReadLimit(wsReadLimit)

	// Dial the upstream WebSocket endpoint.
	upstreamConn, resp, err := f.dialer.Dial(ctx, upstreamURL, &websocket.DialOptions{
		HTTPHeader: extraHeaders,
	})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		// Upstream unreachable — close client connection with a gateway error status.
		_ = clientConn.Close(websocket.StatusBadGateway, "upstream connection failed")
		return &WebSocketResult{
			Duration: time.Since(start),
			Err:      err,
		}, nil
	}
	upstreamConn.SetReadLimit(wsReadLimit)

	// Both connections established — relay messages bidirectionally.
	result := f.relay(ctx, clientConn, upstreamConn, observer)
	result.ConnectSuccess = true
	result.Duration = time.Since(start)
	if observer != nil {
		observation := observer.Snapshot()
		result.Model = observation.Model
		result.TokenUsage = observation.TokenUsage
	}
	return result, nil
}

// relay copies messages between client and upstream until one side closes.
// Uses a context-derived cancel to ensure both goroutines exit promptly.
func (f *WebSocketForwarder) relay(ctx context.Context, clientConn, upstreamConn *websocket.Conn, observer WebSocketMessageObserver) *WebSocketResult {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		clientToUpstreamBytes atomic.Int64
		upstreamToClientBytes atomic.Int64
		wg                    sync.WaitGroup
		// First error wins: the relay direction that fails first determines the close code.
		once     sync.Once
		firstErr error
	)

	var observeClient func(websocket.MessageType, []byte)
	var observeUpstream func(websocket.MessageType, []byte)
	if observer != nil {
		observeClient = observer.ObserveClientMessage
		observeUpstream = observer.ObserveUpstreamMessage
	}

	wg.Add(2)

	// client → upstream
	go func() {
		defer wg.Done()
		n, err := relayMessages(ctx, upstreamConn, clientConn, observeClient)
		clientToUpstreamBytes.Store(n)
		if err != nil {
			once.Do(func() { firstErr = err })
			cancel()
		}
	}()

	// upstream → client
	go func() {
		defer wg.Done()
		n, err := relayMessages(ctx, clientConn, upstreamConn, observeUpstream)
		upstreamToClientBytes.Store(n)
		if err != nil {
			once.Do(func() { firstErr = err })
			cancel()
		}
	}()

	wg.Wait()

	// Determine close code from the first error.
	closeCode := websocket.StatusNormalClosure
	if firstErr != nil {
		closeCode = extractCloseCode(firstErr)
	}

	// Best-effort clean close: propagate the close code to both peers.
	// coder/websocket's Close is non-blocking — it writes the close frame
	// and returns immediately. No need to wait for close handshake completion.
	closeMsg := ""
	if firstErr != nil {
		closeMsg = firstErr.Error()
		// Truncate to a valid UTF-8 boundary to avoid splitting multi-byte codepoints.
		// RFC 6455 §5.5 limits close reason to 125 bytes.
		closeMsg = truncateUTF8(closeMsg, 123)
	}

	_ = clientConn.Close(closeCode, closeMsg)
	_ = upstreamConn.Close(closeCode, closeMsg)

	// Normalize clean closes: a normal/going-away close is not an error.
	// Without this, the caller's Err field would contain a CloseError for every
	// orderly disconnect, polluting logs and health metrics.
	var resultErr error
	if firstErr != nil && !isNormalClose(firstErr) {
		resultErr = firstErr
	}

	return &WebSocketResult{
		CloseCode:             closeCode,
		BytesClientToUpstream: clientToUpstreamBytes.Load(),
		BytesUpstreamToClient: upstreamToClientBytes.Load(),
		Err:                   resultErr,
	}
}

// relayMessages copies messages from src to dst until src closes or ctx is cancelled.
// Returns total bytes relayed and any error that terminated the relay.
func relayMessages(ctx context.Context, dst, src *websocket.Conn, observe func(messageType websocket.MessageType, data []byte)) (int64, error) {
	var totalBytes int64
	for {
		msgType, data, err := src.Read(ctx)
		if err != nil {
			return totalBytes, err
		}
		totalBytes += int64(len(data))
		if observe != nil {
			observe(msgType, data)
		}
		if err := dst.Write(ctx, msgType, data); err != nil {
			return totalBytes, err
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
