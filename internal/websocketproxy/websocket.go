package websocketproxy

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/attemptevidence"
	"github.com/doraemonkeys/switch-a/internal/codex/websocketprotocol"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"

	"github.com/coder/websocket"
	"go.uber.org/zap"
)

// WebSocket forwarding constants.
const (
	// wsReadLimit caps message size to prevent unbounded memory usage.
	// 16 MB accommodates large AI payloads (e.g., base64-encoded audio for Realtime API).
	wsReadLimit = 16 * 1024 * 1024

	webSocketSelectionProbeTotalDuration = 3 * time.Second

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

	// webSocketPreVisibleProviderFirstWindow gives a server-first provider a brief
	// opportunity to fail before client traffic is delivered. Downstream reading
	// starts concurrently, so this preference cannot block multi-frame protocols.
	webSocketPreVisibleProviderFirstWindow = 50 * time.Millisecond

	webSocketSemanticReplacementCloseReason = "semantic replacement"
	webSocketSubprotocolMismatchCloseReason = "websocket subprotocol mismatch"
	maxDrainBytes                           = 64 * 1024
	maxSnippetBytes                         = 512
)

type drainObservation struct {
	bytesRead    int64
	reachedEOF   bool
	limitReached bool
	readErr      error
}

func drainReadCloserWithSnippetObserved(body io.ReadCloser, maxSnippet int) (string, drainObservation) {
	if body == nil {
		return "", drainObservation{}
	}
	if maxSnippet <= 0 {
		maxSnippet = maxSnippetBytes
	}
	snippet := make([]byte, maxSnippet)
	n, snippetErr := io.ReadFull(body, snippet)
	remaining := maxDrainBytes - int64(n)
	var drained int64
	var drainErr error
	if remaining > 0 {
		drained, drainErr = io.Copy(io.Discard, io.LimitReader(body, remaining))
	}
	_ = body.Close()
	observation := drainObservation{
		bytesRead:    int64(n) + drained,
		reachedEOF:   drainErr == nil && drained < remaining,
		limitReached: drainErr == nil && remaining >= 0 && drained == remaining,
		readErr:      drainErr,
	}
	readCompleted := IsEOF(snippetErr) || IsUnexpectedEOF(snippetErr)
	if snippetErr != nil && !readCompleted {
		observation.readErr = snippetErr
		observation.reachedEOF = false
	}
	if readCompleted {
		observation.reachedEOF = observation.readErr == nil
		observation.limitReached = false
	}
	return string(snippet[:n]), observation
}

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

// WebSocketDialRequest keeps physical wire inputs and capture identity together.
// Capture begins only after headers reach their final wire shape, so the record
// describes the real dial without gaining authority over transport behavior.
type WebSocketDialRequest struct {
	HTTPClient          *http.Client
	URL                 string
	Headers             http.Header
	Subprotocols        []string
	InjectedCredential  string
	Capture             requestcapture.GatewayRecorder
	CaptureParticipates bool
	Attempt             requestcapture.AttemptMetadata
}

// DialExchange is the complete result of one physical upstream dial. A provider
// attempt may contain more than one exchange when managed credentials are refreshed,
// so this result deliberately does not share the provider-attempt lifecycle.
type DialExchange struct {
	Conn                     *websocket.Conn
	StartedAt                time.Time
	CompletedAt              time.Time
	HandshakeObservedAt      time.Time
	HandshakeStatusCode      int
	HandshakeProtocol        string
	HandshakeContentLength   int64
	HandshakeHeaders         http.Header
	NegotiatedSubprotocol    string
	HandshakeBodySnippet     string
	ObservedFailureBodyBytes int64
	FailureBodyPresent       bool
	FailureBodyReachedEOF    bool
	FailureBodyLimitReached  bool
	FailureBodyReadErr       error
	Err                      error
	capture                  requestcapture.Recorder
	captureMode              captureMode
	credentialEvidence       requestcapture.CredentialEvidence
}

func (e DialExchange) Accepted() bool {
	return e.Conn != nil && e.Err == nil
}

func (e DialExchange) toWebSocketResult() *WebSocketResult {
	return &WebSocketResult{
		HandshakeAccepted:     e.Accepted(),
		HandshakeStatusCode:   e.HandshakeStatusCode,
		HandshakeProtocol:     e.HandshakeProtocol,
		HandshakeBodySnippet:  e.HandshakeBodySnippet,
		HandshakeHeaders:      e.HandshakeHeaders,
		NegotiatedSubprotocol: e.NegotiatedSubprotocol,
		HandshakeObservedAt:   e.HandshakeObservedAt,
		HandshakeStartedAt:    e.StartedAt,
		HandshakeCompletedAt:  e.CompletedAt,
		Err:                   e.Err,
		TerminalCause:         classifyDialFailure(e.HandshakeStatusCode),
		CommitSource:          model.CommitUnknown,
	}
}

func (e DialExchange) applyHandshake(result *WebSocketResult) {
	if result == nil {
		return
	}
	result.HandshakeAccepted = e.Accepted()
	result.HandshakeStatusCode = e.HandshakeStatusCode
	result.HandshakeProtocol = e.HandshakeProtocol
	result.HandshakeHeaders = e.HandshakeHeaders
	result.NegotiatedSubprotocol = e.NegotiatedSubprotocol
	result.HandshakeObservedAt = e.HandshakeObservedAt
	result.HandshakeStartedAt = e.StartedAt
	result.HandshakeCompletedAt = e.CompletedAt
}

type webSocketFailureBodyReader struct {
	io.ReadCloser
	capture requestcapture.Recorder
}

func (r *webSocketFailureBodyReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 {
		r.capture.ObserveUpstream(p[:n])
	}
	return n, err
}

func beginWebSocketDialCapture(
	request WebSocketDialRequest,
	dialHeaders http.Header,
) (
	requestcapture.Recorder,
	requestcapture.SensitiveHeaderEvidence,
	requestcapture.CredentialEvidence,
	captureMode,
) {
	if !request.CaptureParticipates {
		return requestcapture.Recorder{}, requestcapture.SensitiveHeaderEvidence{}, requestcapture.CredentialEvidence{}, captureModeNone
	}

	sensitiveHeaders, credentialEvidence := captureCredentialMaterial(request.InjectedCredential)
	recorder := request.Capture.BeginWebSocket(requestcapture.RawWebSocketStart{
		Attempt:   request.Attempt,
		TargetURL: request.URL,
		Request: requestcapture.RawRequest{
			Method:             http.MethodGet,
			Headers:            dialHeaders,
			ContentLength:      0,
			SensitiveHeaders:   sensitiveHeaders,
			CredentialEvidence: credentialEvidence,
		},
	})
	return recorder, sensitiveHeaders, credentialEvidence, captureModeForRecorder(recorder)
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
	ClientDisguise          *attemptevidence.ClientDisguise
	healthOutcomePublished  bool
	accountRecoveryNotified bool
	ReplayStatus            webSocketReplayStatus
	// HandshakeAccepted indicates whether the selected provider completed the
	// upstream WebSocket handshake. Client accept can still be true when a later
	// replacement or failover dial is rejected because the logical downstream
	// session outlives each individual provider attempt.
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

	// HandshakeProtocol preserves the HTTP protocol reported by the dial response.
	HandshakeProtocol string

	// HandshakeStartedAt and HandshakeCompletedAt bound the physical dial rather
	// than the longer logical provider attempt, which may include credential refresh.
	HandshakeStartedAt   time.Time
	HandshakeCompletedAt time.Time

	// HandshakeBodySnippet captures the upstream HTTP error body from a rejected
	// WebSocket upgrade so logs can show the provider's actual reason.
	HandshakeBodySnippet string

	// HandshakeHeaders preserves the upstream handshake response headers so the
	// handler can apply the same provider-failure semantics it uses on HTTP.
	HandshakeHeaders http.Header

	// NegotiatedSubprotocol is the protocol selected on the physical upstream
	// connection. A successful session exposes the same value downstream.
	NegotiatedSubprotocol string

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

	// CompletionObserved records whether the upstream runtime emitted an explicit
	// terminal completion event. Assessment uses this to distinguish "completed"
	// from "transport vanished after something visible happened".
	CompletionObserved bool

	// TransportObservation carries the real runtime transport facts observed by
	// the relay layer (CloseError frames, failing peer). It is evidence-layer
	// input only: session assessment and evidence derivation read it to build a
	// transport diagnostic, nothing else on this struct depends on it.
	//
	// The nested struct boundary is load-bearing: synthetic-final inheritance
	// paths (`applyLastAttemptToSuppressedPayload`) MUST zero this whole value
	// so the final session cannot be attributed to a replaced attempt's
	// transport observation. A flat field would have allowed silent "partial
	// inheritance" bugs.
	TransportObservation WebSocketTransportObservation
}

// WebSocketTransportObservation isolates transport-layer runtime facts so they
// can be cleared as one unit when a result is copied across a semantic
// inheritance boundary (synthetic final session). CloseError is captured by
// pointer because a zero-valued CloseError{} with Code=0 is a legitimate
// observation — presence must be unambiguous.
type WebSocketTransportObservation struct {
	// CloseError is the real observed close frame, populated only when the
	// relay layer extracted one. A nil pointer means "no concrete frame" and
	// forces the derivation layer onto EOF / close_without_status paths.
	CloseError *websocket.CloseError
	// FailurePeer records which side first produced the error that drove
	// reduction. It lets the evidence builder distinguish upstream-originated
	// from client-originated transport failures without re-deriving that from
	// error text.
	FailurePeer webSocketPeer
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
	// CloseError is immutable once observed (coder/websocket never mutates the
	// frame fields after the read that produced it), so a pointer copy is safe
	// and avoids deep-copying a value whose identity does not matter for
	// evidence derivation.
	clone.TransportObservation = r.TransportObservation
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
	negotiation, err := parseWebSocketSubprotocolNegotiation(r.Header)
	if err != nil {
		return &WebSocketResult{
			HandshakeStatusCode: http.StatusBadRequest,
			Duration:            time.Since(start),
			Err:                 err,
			TerminalCause:       model.TerminalClientUpgradeRejected,
			CommitSource:        model.CommitUnknown,
		}, nil
	}

	dialExchange := f.dialUpstream(ctx, WebSocketDialRequest{
		URL:          upstreamURL,
		Headers:      extraHeaders,
		Subprotocols: negotiation.DialOffer(),
	})
	if !dialExchange.Accepted() {
		result := dialExchange.toWebSocketResult()
		if dialExchange.HandshakeStatusCode == http.StatusSwitchingProtocols {
			if _, protocolErr := negotiation.BindUpstream(dialExchange.NegotiatedSubprotocol); protocolErr != nil {
				result.HandshakeStatusCode = http.StatusBadGateway
				result.Err = protocolErr
				result.TerminalCause = model.TerminalInternalError
			}
		}
		result.Duration = time.Since(start)
		return result, nil
	}
	negotiation, err = negotiation.BindUpstream(dialExchange.NegotiatedSubprotocol)
	if err != nil {
		closeWebSocketSubprotocolViolation(dialExchange.Conn)
		result := dialExchange.toWebSocketResult()
		result.HandshakeAccepted = false
		result.HandshakeStatusCode = http.StatusBadGateway
		result.Duration = time.Since(start)
		result.Err = err
		result.TerminalCause = model.TerminalInternalError
		return result, nil
	}
	downstreamOffer, err := negotiation.DownstreamOffer()
	if err != nil {
		closeWebSocketSubprotocolViolation(dialExchange.Conn)
		result := dialExchange.toWebSocketResult()
		result.HandshakeAccepted = false
		result.HandshakeStatusCode = http.StatusBadGateway
		result.Duration = time.Since(start)
		result.Err = err
		result.TerminalCause = model.TerminalInternalError
		return result, nil
	}
	clientConn, err := f.acceptClient(w, r, downstreamOffer...)
	if err != nil {
		_ = dialExchange.Conn.Close(websocket.StatusGoingAway, "client websocket upgrade rejected")
		result := &WebSocketResult{
			Duration:      time.Since(start),
			Err:           err,
			TerminalCause: model.TerminalClientUpgradeRejected,
			CommitSource:  model.CommitUnknown,
		}
		dialExchange.applyHandshake(result)
		return result, err
	}
	if err := negotiation.ValidateDownstream(clientConn.Subprotocol()); err != nil {
		closeWebSocketSubprotocolViolation(clientConn)
		closeWebSocketSubprotocolViolation(dialExchange.Conn)
		result := &WebSocketResult{
			ClientAccepted: true,
			Duration:       time.Since(start),
			Err:            err,
			TerminalCause:  model.TerminalInternalError,
			CommitSource:   model.CommitUnknown,
		}
		dialExchange.applyHandshake(result)
		return result, nil
	}

	lifecycle := newWebSocketLifecycleState()
	lifecycle.MarkClientAccepted()

	relayResult := f.relay(ctx, clientConn, dialExchange.Conn, webSocketRelayOptions{
		Observer:               observer,
		OnFirstUpstreamMessage: onFirstUpstreamMessage,
		OnClientVisible:        onClientVisible,
		Lifecycle:              lifecycle,
	})
	result := relayResult.toWebSocketResult()
	dialExchange.applyHandshake(result)
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

func (f *WebSocketForwarder) dialUpstream(ctx context.Context, request WebSocketDialRequest) DialExchange {
	// Dial the upstream WebSocket endpoint before accepting the client upgrade.
	// This preserves upstream handshake semantics for the caller, which is required for:
	//   - surfacing 426 so Codex CLI can fall back to HTTP
	//   - retrying 401 after refreshing provider-managed credentials
	dialHeaders := request.Headers.Clone()
	if dialHeaders == nil {
		dialHeaders = make(http.Header)
	}
	EnsureExplicitUserAgentHeader(dialHeaders)

	exchange := DialExchange{StartedAt: time.Now()}
	captureHeaders := dialHeaders.Clone()
	if len(request.Subprotocols) > 0 {
		// coder/websocket materializes this header from DialOptions after capture
		// starts. Projecting the dedicated option into the capture-only snapshot
		// preserves wire fidelity without treating it as a passthrough header.
		captureHeaders.Set(websocketprotocol.HeaderName, strings.Join(request.Subprotocols, ","))
	}
	capture, sensitiveHeaders, credentialEvidence, captureMode := beginWebSocketDialCapture(request, captureHeaders)
	exchange.capture = capture
	exchange.captureMode = captureMode
	exchange.credentialEvidence = credentialEvidence
	upstreamConn, resp, err := f.dialer.Dial(ctx, request.URL, &websocket.DialOptions{
		HTTPClient:   request.HTTPClient,
		HTTPHeader:   dialHeaders,
		Subprotocols: append([]string(nil), request.Subprotocols...),
	})
	exchange.CompletedAt = time.Now()
	exchange.Err = err
	if resp != nil {
		exchange.HandshakeObservedAt = exchange.CompletedAt
		exchange.HandshakeStatusCode = resp.StatusCode
		exchange.HandshakeProtocol = resp.Proto
		exchange.HandshakeContentLength = resp.ContentLength
		exchange.HandshakeHeaders = resp.Header.Clone()
		exchange.NegotiatedSubprotocol = resp.Header.Get(websocketprotocol.HeaderName)
		if exchange.captureMode.CapturesPayload() {
			exchange.capture.ObserveWebSocketHandshake(requestcapture.WebSocketHandshake{
				StatusCode:         resp.StatusCode,
				Protocol:           resp.Proto,
				Headers:            resp.Header,
				SensitiveHeaders:   sensitiveHeaders,
				CredentialEvidence: credentialEvidence,
			})
		}
	}

	if err != nil {
		if resp != nil && resp.Body != nil {
			exchange.FailureBodyPresent = true
			body := resp.Body
			if exchange.captureMode.CapturesPayload() {
				body = &webSocketFailureBodyReader{
					ReadCloser: resp.Body,
					capture:    exchange.capture,
				}
			}
			snippet, observation := drainReadCloserWithSnippetObserved(body, 0)
			exchange.HandshakeBodySnippet = snippet
			exchange.ObservedFailureBodyBytes = observation.bytesRead
			exchange.FailureBodyReachedEOF = observation.reachedEOF
			exchange.FailureBodyLimitReached = observation.limitReached
			exchange.FailureBodyReadErr = observation.readErr
		}
		return exchange
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	upstreamConn.SetReadLimit(wsReadLimit)
	exchange.Conn = upstreamConn
	exchange.NegotiatedSubprotocol = upstreamConn.Subprotocol()
	return exchange
}

func (f *WebSocketForwarder) acceptClient(w http.ResponseWriter, r *http.Request, subprotocols ...string) (*websocket.Conn, error) {
	// Accept the client's WebSocket upgrade only after the provider handshake succeeds.
	// This avoids hiding upstream handshake failures behind an already-open proxy socket.
	clientConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
		Subprotocols:       append([]string(nil), subprotocols...),
	})
	if err != nil {
		return nil, err
	}
	clientConn.SetReadLimit(wsReadLimit)
	return clientConn, nil
}

func parseWebSocketSubprotocolNegotiation(headers http.Header) (websocketprotocol.Negotiation, error) {
	offer, err := websocketprotocol.ParseClientOffer(webSocketSubprotocolHeaderValues(headers))
	if err != nil {
		return websocketprotocol.Negotiation{}, err
	}
	return websocketprotocol.New(offer), nil
}

func webSocketSubprotocolHeaderValues(headers http.Header) []string {
	var values []string
	for name, headerValues := range headers {
		if strings.EqualFold(name, websocketprotocol.HeaderName) {
			values = append(values, headerValues...)
		}
	}
	return values
}

func closeWebSocketSubprotocolViolation(conn *websocket.Conn) {
	if conn == nil {
		return
	}
	_ = conn.Close(websocket.StatusProtocolError, webSocketSubprotocolMismatchCloseReason)
}
