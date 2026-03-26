//nolint:revive // file-length-limit: websocket relay/failover behavior is intentionally colocated while the transport split stabilizes.
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

	// preVisibleClientReplayBufferLimitBytes bounds buffered client application payloads
	// so semantic failover never turns an invisible session window into unbounded memory.
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

	// webSocketPreVisibleClientReadWindow bounds how long the serialized pre-visible
	// path waits for a prompt-first client message before falling back to normal
	// concurrent relay, which keeps server-first sessions moving.
	webSocketPreVisibleClientReadWindow = 50 * time.Millisecond

	webSocketSemanticFailoverCloseReason = "semantic failover"
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
	bytes                   int64
	err                     error
	failurePeer             webSocketPeer
	errorOrder              uint32
	suppressedUpstreamError *WebSocketUpstreamError
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

type webSocketRelayDisposition uint8

const (
	webSocketRelayDispositionCompleted webSocketRelayDisposition = iota
	webSocketRelayDispositionSuppressedUpstreamError
)

type webSocketLifecycleState struct {
	mu             sync.Mutex
	clientAccepted bool
	clientVisible  bool
}

type webSocketLifecycleSnapshot struct {
	ClientAccepted bool
	ClientVisible  bool
}

type webSocketReplayMessage struct {
	MessageType websocket.MessageType
	Data        []byte
}

type preVisibleClientMessageBuffer struct {
	mu         sync.Mutex
	limitBytes int
	totalBytes int
	enabled    bool
	messages   []webSocketReplayMessage
}

type preVisibleClientMessageBufferSnapshot struct {
	Enabled    bool
	TotalBytes int
	Messages   []webSocketReplayMessage
}

type webSocketPreWriteContext struct {
	MessageType    websocket.MessageType
	Data           []byte
	Observation    WebSocketObservation
	ClientAccepted bool
	ClientVisible  bool
}

type webSocketPreWriteAction uint8

const (
	webSocketPreWriteActionForward webSocketPreWriteAction = iota
	webSocketPreWriteActionSuppress
)

type webSocketPreWriteDecision struct {
	Action                  webSocketPreWriteAction
	SuppressedUpstreamError *WebSocketUpstreamError
	SuppressedMessageType   websocket.MessageType
	SuppressedMessageData   []byte
}

type webSocketVisibleWriteContext struct {
	MessageType websocket.MessageType
	Data        []byte
	Observation WebSocketObservation
}

type webSocketRelayOptions struct {
	Observer                 WebSocketMessageObserver
	OnFirstUpstreamMessage   func(WebSocketObservation)
	PreWriteToClient         func(webSocketPreWriteContext) webSocketPreWriteDecision
	OnClientVisible          func(webSocketVisibleWriteContext)
	PreVisibleReplayBuffer   *preVisibleClientMessageBuffer
	Lifecycle                *webSocketLifecycleState
	PreserveClientOnSuppress bool
	SkipPreVisibleWindow     bool
	// PreserveClientOnPreVisibleFailure keeps the downstream socket open when a
	// fallback attempt dies before any upstream bytes become client-visible, so
	// the orchestrator can keep switching providers or surface the suppressed
	// original payload instead of collapsing the session into a transport close.
	PreserveClientOnPreVisibleFailure bool
}

type webSocketRelaySessionResult struct {
	Disposition             webSocketRelayDisposition
	SessionCommitted        bool
	CommitSource            model.CommitSource
	TerminalCause           model.TerminalCause
	CloseCode               websocket.StatusCode
	BytesClientToUpstream   int64
	BytesUpstreamToClient   int64
	Err                     error
	ClientAccepted          bool
	ClientVisible           bool
	SuppressedUpstreamError *WebSocketUpstreamError
	SuppressedMessageType   websocket.MessageType
	SuppressedMessageData   []byte
}

type webSocketPreVisibleRelayProgress struct {
	BytesClientToUpstream   int64
	BytesUpstreamToClient   int64
	Result                  *webSocketRelaySessionResult
	ConsumedInitialClient   bool
	ConsumedInitialUpstream bool
}

func (p *webSocketPreVisibleRelayProgress) merge(other webSocketPreVisibleRelayProgress) {
	p.BytesClientToUpstream += other.BytesClientToUpstream
	p.BytesUpstreamToClient += other.BytesUpstreamToClient
	if other.Result != nil {
		p.Result = other.Result
	}
	p.ConsumedInitialClient = p.ConsumedInitialClient || other.ConsumedInitialClient
	p.ConsumedInitialUpstream = p.ConsumedInitialUpstream || other.ConsumedInitialUpstream
}

func (p webSocketPreVisibleRelayProgress) preservesClient() bool {
	return p.Result != nil && p.Result.Disposition == webSocketRelayDispositionSuppressedUpstreamError
}

type webSocketInitialReadResult struct {
	messageType websocket.MessageType
	data        []byte
	err         error
}

func shouldRunPreVisibleSuppressionWindow(options webSocketRelayOptions) bool {
	return options.PreserveClientOnSuppress && !options.SkipPreVisibleWindow
}

func newSinglePeerRelaySessionResult(
	err error,
	failurePeer webSocketPeer,
	fallbackCommit *webSocketCommitState,
	lifecycle *webSocketLifecycleState,
	bytesClientToUpstream, bytesUpstreamToClient int64,
) *webSocketRelaySessionResult {
	var errorOrder atomic.Uint32
	return newWebSocketRelaySessionResultFromOutcome(
		reduceOrderedWebSocketRelayResults(
			newWebSocketRelayResult(0, err, failurePeer, &errorOrder),
			webSocketRelayResult{},
		),
		fallbackCommit,
		lifecycle,
		bytesClientToUpstream,
		bytesUpstreamToClient,
	)
}

func disablePreVisibleReplayBufferIfNeeded(options webSocketRelayOptions) {
	if options.Observer != nil && options.Observer.ParseDegraded() && options.PreVisibleReplayBuffer != nil {
		options.PreVisibleReplayBuffer.Disable()
	}
}

func currentWebSocketObservation(observer WebSocketMessageObserver) WebSocketObservation {
	if observer == nil {
		return WebSocketObservation{}
	}
	return observer.Snapshot()
}

func waitForInitialWebSocketRead(
	ctx context.Context,
	initialReadCh <-chan webSocketInitialReadResult,
) webSocketInitialReadResult {
	select {
	case initialRead := <-initialReadCh:
		return initialRead
	case <-ctx.Done():
		return webSocketInitialReadResult{err: ctx.Err()}
	}
}

type webSocketSuppressedUpstreamError struct {
	upstreamError *WebSocketUpstreamError
}

func (e *webSocketSuppressedUpstreamError) Error() string {
	return webSocketSemanticFailoverCloseReason
}

func (e *webSocketSuppressedUpstreamError) UpstreamError() *WebSocketUpstreamError {
	if e == nil {
		return nil
	}
	return e.upstreamError.Clone()
}

func closeWebSocketForSemanticFailover(conn *websocket.Conn) {
	if conn == nil {
		return
	}
	// Semantic failover must hand control back to the orchestrator immediately.
	// A graceful close waits for the peer's close handshake, which can stall forever
	// on the failed provider path we are intentionally abandoning.
	_ = conn.CloseNow()
}

func newWebSocketCommitState() *webSocketCommitState {
	return &webSocketCommitState{
		source: model.CommitUnknown,
	}
}

func newWebSocketLifecycleState() *webSocketLifecycleState {
	return &webSocketLifecycleState{}
}

func (s *webSocketLifecycleState) MarkClientAccepted() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clientAccepted = true
}

func (s *webSocketLifecycleState) MarkClientVisible() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.clientVisible {
		return false
	}
	s.clientVisible = true
	return true
}

func (s *webSocketLifecycleState) Snapshot() webSocketLifecycleSnapshot {
	if s == nil {
		return webSocketLifecycleSnapshot{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return webSocketLifecycleSnapshot{
		ClientAccepted: s.clientAccepted,
		ClientVisible:  s.clientVisible,
	}
}

func newPreVisibleClientMessageBuffer(limitBytes int) *preVisibleClientMessageBuffer {
	if limitBytes <= 0 {
		limitBytes = preVisibleClientReplayBufferLimitBytes
	}
	return &preVisibleClientMessageBuffer{
		limitBytes: limitBytes,
		enabled:    true,
	}
}

func (b *preVisibleClientMessageBuffer) Enabled() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.enabled
}

func (b *preVisibleClientMessageBuffer) Disable() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.disableLocked()
}

func (b *preVisibleClientMessageBuffer) Record(messageType websocket.MessageType, data []byte, clientVisible bool) {
	if b == nil {
		return
	}

	if clientVisible {
		b.Disable()
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.enabled {
		return
	}
	if !isReplayableWebSocketMessageType(messageType) {
		b.disableLocked()
		return
	}
	if len(b.messages) >= preVisibleClientReplayBufferLimitMessages {
		b.disableLocked()
		return
	}
	nextTotalBytes := b.totalBytes + len(data)
	if nextTotalBytes > b.limitBytes {
		b.disableLocked()
		return
	}

	payload := append([]byte(nil), data...)
	b.messages = append(b.messages, webSocketReplayMessage{
		MessageType: messageType,
		Data:        payload,
	})
	b.totalBytes = nextTotalBytes
}

func (b *preVisibleClientMessageBuffer) Replay(ctx context.Context, upstreamConn *websocket.Conn) error {
	if b == nil {
		return nil
	}

	snapshot := b.Snapshot()
	if !snapshot.Enabled {
		return errors.New("pre-visible replay buffer disabled")
	}
	for _, message := range snapshot.Messages {
		if err := upstreamConn.Write(ctx, message.MessageType, message.Data); err != nil {
			return err
		}
	}
	return nil
}

func (b *preVisibleClientMessageBuffer) Snapshot() preVisibleClientMessageBufferSnapshot {
	if b == nil {
		return preVisibleClientMessageBufferSnapshot{}
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	snapshot := preVisibleClientMessageBufferSnapshot{
		Enabled:    b.enabled,
		TotalBytes: b.totalBytes,
		Messages:   make([]webSocketReplayMessage, 0, len(b.messages)),
	}
	for _, message := range b.messages {
		snapshot.Messages = append(snapshot.Messages, webSocketReplayMessage{
			MessageType: message.MessageType,
			Data:        append([]byte(nil), message.Data...),
		})
	}
	return snapshot
}

func (b *preVisibleClientMessageBuffer) disableLocked() {
	b.enabled = false
	b.totalBytes = 0
	b.messages = nil
}

func newAllowlistedProviderScopedSuppressDecision(buffer *preVisibleClientMessageBuffer) func(webSocketPreWriteContext) webSocketPreWriteDecision {
	return func(ctx webSocketPreWriteContext) webSocketPreWriteDecision {
		if ctx.ClientVisible || ctx.Observation.ParseDegraded || buffer == nil {
			return webSocketPreWriteDecision{Action: webSocketPreWriteActionForward}
		}
		snapshot := buffer.Snapshot()
		// An empty replay snapshot still means failover is safe: the provider failed
		// before any replayable client payload crossed the pre-visible boundary, so
		// there is nothing to resend to the replacement provider.
		if !snapshot.Enabled {
			return webSocketPreWriteDecision{Action: webSocketPreWriteActionForward}
		}
		classification := classifyWebSocketUpstreamMessage(ctx.MessageType, ctx.Data, ctx.Observation.ParseDegraded)
		if classification != webSocketSemanticClassificationProviderScopedAllowlisted {
			return webSocketPreWriteDecision{Action: webSocketPreWriteActionForward}
		}
		suppressedUpstreamError := ctx.Observation.UpstreamError.Clone()
		if suppressedUpstreamError == nil {
			suppressedUpstreamError = &WebSocketUpstreamError{
				Raw: string(ctx.Data),
			}
		}
		return webSocketPreWriteDecision{
			Action:                  webSocketPreWriteActionSuppress,
			SuppressedUpstreamError: suppressedUpstreamError,
			SuppressedMessageType:   ctx.MessageType,
			SuppressedMessageData:   append([]byte(nil), ctx.Data...),
		}
	}
}

func isReplayableWebSocketMessageType(messageType websocket.MessageType) bool {
	return messageType == websocket.MessageText || messageType == websocket.MessageBinary
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
		if resp != nil {
			handshakeStatusCode = resp.StatusCode
			handshakeBodySnippet = drainReadCloserWithSnippet(resp.Body, 0)
		}
		return nil, &WebSocketResult{
			HandshakeStatusCode:  handshakeStatusCode,
			HandshakeBodySnippet: handshakeBodySnippet,
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

// relay copies messages between client and upstream until one side closes.
// Uses a context-derived cancel to ensure both goroutines exit promptly.
//
//nolint:gocognit,gocyclo,funlen // The relay keeps both transport directions and failover hooks in one place until the refactor settles.
func (f *WebSocketForwarder) relay(ctx context.Context, clientConn, upstreamConn *websocket.Conn, options webSocketRelayOptions) *webSocketRelaySessionResult {
	ctx, cancel := context.WithCancel(ctx)
	preserveClient := false
	defer func() {
		if !preserveClient {
			cancel()
		}
	}()

	var (
		clientToUpstream webSocketRelayResult
		upstreamToClient webSocketRelayResult
		wg               sync.WaitGroup
		errorOrder       atomic.Uint32
	)
	fallbackCommit := newWebSocketCommitState()
	lifecycle := options.Lifecycle
	if lifecycle == nil {
		lifecycle = newWebSocketLifecycleState()
	}
	lifecycle.MarkClientAccepted()
	preVisibleProgress := webSocketPreVisibleRelayProgress{}
	var initialClientReadCh <-chan webSocketInitialReadResult
	var initialUpstreamReadCh <-chan webSocketInitialReadResult

	var observeClient func(websocket.MessageType, []byte)
	var observeUpstream func(websocket.MessageType, []byte)
	if options.Observer != nil {
		observeClient = options.Observer.ObserveClientMessage
		observeUpstream = options.Observer.ObserveUpstreamMessage
	}
	onUpstreamVisible := func(messageType websocket.MessageType, data []byte) {
		if !isReplayableWebSocketMessageType(messageType) {
			return
		}
		becameVisible := lifecycle.MarkClientVisible()
		if becameVisible && options.PreVisibleReplayBuffer != nil {
			options.PreVisibleReplayBuffer.Disable()
		}
		observation := WebSocketObservation{SessionCommitted: true}
		if options.Observer != nil {
			observation = options.Observer.Snapshot()
			if observation.SessionCommitted {
				goto publishVisibleHook
			}
			if options.Observer.HasSemanticObservation() && !observation.ParseDegraded {
				goto publishVisibleHook
			}
		}
		if !fallbackCommit.Commit(model.CommitUpstreamMessage) {
			goto publishVisibleHook
		}
		if options.OnFirstUpstreamMessage != nil {
			observation.SessionCommitted = true
			options.OnFirstUpstreamMessage(observation)
		}
	publishVisibleHook:
		if becameVisible && options.OnClientVisible != nil {
			options.OnClientVisible(webSocketVisibleWriteContext{
				MessageType: messageType,
				Data:        data,
				Observation: observation,
			})
		}
	}

	initialClientReadCh, initialUpstreamReadCh, preVisibleProgress = f.runPreVisibleSuppressionWindow(
		ctx,
		clientConn,
		upstreamConn,
		options,
		lifecycle,
		initialClientReadCh,
		initialUpstreamReadCh,
		observeClient,
		observeUpstream,
		onUpstreamVisible,
		fallbackCommit,
	)
	if preVisibleProgress.Result != nil {
		preserveClient = preVisibleProgress.preservesClient()
		return preVisibleProgress.Result
	}

	wg.Add(2)

	// client → upstream
	go func() {
		defer wg.Done()
		var initialClientRead *webSocketInitialReadResult
		if initialClientReadCh != nil {
			select {
			case read := <-initialClientReadCh:
				initialClientRead = &read
			case <-ctx.Done():
				initialClientRead = &webSocketInitialReadResult{err: ctx.Err()}
			}
		}
		n, failurePeer, err := relayMessages(
			ctx,
			upstreamConn,
			webSocketPeerUpstream,
			clientConn,
			webSocketPeerClient,
			initialClientRead,
			observeClient,
			nil,
			func(messageType websocket.MessageType, data []byte) {
				lifecycleSnapshot := lifecycle.Snapshot()
				if options.PreVisibleReplayBuffer != nil {
					if options.Observer != nil && options.Observer.ParseDegraded() {
						options.PreVisibleReplayBuffer.Disable()
					}
					options.PreVisibleReplayBuffer.Record(messageType, data, lifecycleSnapshot.ClientVisible)
				}
			},
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
		var initialUpstreamRead *webSocketInitialReadResult
		if initialUpstreamReadCh != nil {
			select {
			case read := <-initialUpstreamReadCh:
				initialUpstreamRead = &read
			case <-ctx.Done():
				initialUpstreamRead = &webSocketInitialReadResult{err: ctx.Err()}
			}
		}
		n, failurePeer, err := relayMessages(
			ctx,
			clientConn,
			webSocketPeerClient,
			upstreamConn,
			webSocketPeerUpstream,
			initialUpstreamRead,
			observeUpstream,
			onUpstreamVisible,
			nil,
			func(messageType websocket.MessageType, data []byte) webSocketPreWriteDecision {
				observation := WebSocketObservation{}
				if options.Observer != nil {
					observation = options.Observer.Snapshot()
					if observation.ParseDegraded && options.PreVisibleReplayBuffer != nil {
						options.PreVisibleReplayBuffer.Disable()
					}
				}
				lifecycleSnapshot := lifecycle.Snapshot()
				if options.PreWriteToClient == nil {
					return webSocketPreWriteDecision{Action: webSocketPreWriteActionForward}
				}
				return options.PreWriteToClient(webSocketPreWriteContext{
					MessageType:    messageType,
					Data:           data,
					Observation:    observation,
					ClientAccepted: lifecycleSnapshot.ClientAccepted,
					ClientVisible:  lifecycleSnapshot.ClientVisible,
				})
			},
		)
		upstreamToClient = newWebSocketRelayResult(n, err, failurePeer, &errorOrder)
		if err != nil {
			cancel()
		}
	}()

	wg.Wait()

	lifecycleSnapshot := lifecycle.Snapshot()
	if suppressedUpstreamError := firstSuppressedUpstreamError(clientToUpstream, upstreamToClient); suppressedUpstreamError != nil {
		if options.PreserveClientOnSuppress {
			preserveClient = true
			closeWebSocketForSemanticFailover(upstreamConn)
		} else {
			closeWebSocketForSemanticFailover(clientConn)
			closeWebSocketForSemanticFailover(upstreamConn)
		}
		sessionCommitted, commitSource := fallbackCommit.Snapshot()
		return &webSocketRelaySessionResult{
			Disposition:             webSocketRelayDispositionSuppressedUpstreamError,
			SessionCommitted:        sessionCommitted,
			TerminalCause:           model.TerminalUpstreamSemanticError,
			CommitSource:            commitSource,
			BytesClientToUpstream:   preVisibleProgress.BytesClientToUpstream + clientToUpstream.bytes,
			BytesUpstreamToClient:   preVisibleProgress.BytesUpstreamToClient + upstreamToClient.bytes,
			ClientAccepted:          lifecycleSnapshot.ClientAccepted,
			ClientVisible:           lifecycleSnapshot.ClientVisible,
			SuppressedUpstreamError: suppressedUpstreamError,
		}
	}

	outcome := reduceWebSocketRelayErrors(clientToUpstream, upstreamToClient)
	if shouldPreserveClientOnPreVisibleFailure(options, lifecycleSnapshot, outcome) {
		preserveClient = true
		closeWebSocketForSemanticFailover(upstreamConn)
		sessionCommitted, commitSource := fallbackCommit.Snapshot()
		return &webSocketRelaySessionResult{
			Disposition:           webSocketRelayDispositionCompleted,
			SessionCommitted:      sessionCommitted,
			TerminalCause:         outcome.terminalCause,
			CommitSource:          commitSource,
			CloseCode:             outcome.closeCode,
			BytesClientToUpstream: preVisibleProgress.BytesClientToUpstream + clientToUpstream.bytes,
			BytesUpstreamToClient: preVisibleProgress.BytesUpstreamToClient + upstreamToClient.bytes,
			Err:                   outcome.err,
			ClientAccepted:        lifecycleSnapshot.ClientAccepted,
			ClientVisible:         lifecycleSnapshot.ClientVisible,
		}
	}

	// Best-effort clean close: propagate the close code to both peers.
	// coder/websocket's Close waits for the close handshake, but at this point
	// both relay directions have already terminated so only post-session cleanup remains.
	closeMsg := ""
	if outcome.err != nil {
		closeMsg = outcome.err.Error()
		// Truncate to a valid UTF-8 boundary to avoid splitting multi-byte codepoints.
		// RFC 6455 §5.5 limits close reason to 125 bytes.
		closeMsg = truncateUTF8(closeMsg, webSocketCloseReasonByteLimit)
	}
	propagatedCloseCode := sanitizeWebSocketCloseCode(outcome.closeCode, outcome.err)

	_ = clientConn.Close(propagatedCloseCode, closeMsg)
	_ = upstreamConn.Close(propagatedCloseCode, closeMsg)

	sessionCommitted, commitSource := fallbackCommit.Snapshot()
	return &webSocketRelaySessionResult{
		Disposition:           webSocketRelayDispositionCompleted,
		SessionCommitted:      sessionCommitted,
		TerminalCause:         outcome.terminalCause,
		CommitSource:          commitSource,
		CloseCode:             outcome.closeCode,
		BytesClientToUpstream: preVisibleProgress.BytesClientToUpstream + clientToUpstream.bytes,
		BytesUpstreamToClient: preVisibleProgress.BytesUpstreamToClient + upstreamToClient.bytes,
		Err:                   outcome.err,
		ClientAccepted:        lifecycleSnapshot.ClientAccepted,
		ClientVisible:         lifecycleSnapshot.ClientVisible,
	}
}

// runPreVisibleSuppressionWindow serializes the provider-first and client-first
// pre-visible probes so failover sees exactly one authoritative first upstream read.
func (f *WebSocketForwarder) runPreVisibleSuppressionWindow(
	ctx context.Context,
	clientConn, upstreamConn *websocket.Conn,
	options webSocketRelayOptions,
	lifecycle *webSocketLifecycleState,
	initialClientReadCh, initialUpstreamReadCh <-chan webSocketInitialReadResult,
	observeClient, observeUpstream func(websocket.MessageType, []byte),
	onUpstreamVisible func(websocket.MessageType, []byte),
	fallbackCommit *webSocketCommitState,
) (<-chan webSocketInitialReadResult, <-chan webSocketInitialReadResult, webSocketPreVisibleRelayProgress) {
	progress := webSocketPreVisibleRelayProgress{}
	if !shouldRunPreVisibleSuppressionWindow(options) {
		return initialClientReadCh, initialUpstreamReadCh, progress
	}

	initialUpstreamReadCh = startWebSocketInitialRead(ctx, upstreamConn)
	progress = f.relayImmediatePreVisibleUpstreamWindow(
		ctx,
		options,
		lifecycle,
		initialUpstreamReadCh,
		clientConn,
		upstreamConn,
		observeUpstream,
		onUpstreamVisible,
		fallbackCommit,
	)
	if progress.Result != nil {
		return initialClientReadCh, initialUpstreamReadCh, progress
	}
	if lifecycle.Snapshot().ClientVisible {
		if progress.ConsumedInitialUpstream {
			initialUpstreamReadCh = nil
		}
		return initialClientReadCh, initialUpstreamReadCh, progress
	}

	initialClientReadCh = startWebSocketInitialRead(ctx, clientConn)
	progress.merge(f.relayPreVisibleWindow(
		ctx,
		clientConn,
		upstreamConn,
		options,
		lifecycle,
		initialClientReadCh,
		initialUpstreamReadCh,
		observeClient,
		observeUpstream,
		onUpstreamVisible,
		fallbackCommit,
	))
	if progress.ConsumedInitialClient {
		initialClientReadCh = nil
	}
	if progress.ConsumedInitialUpstream {
		initialUpstreamReadCh = nil
	}
	return initialClientReadCh, initialUpstreamReadCh, progress
}

func newSuppressedPreVisibleRelayResult(
	fallbackCommit *webSocketCommitState,
	lifecycleSnapshot webSocketLifecycleSnapshot,
	bytesClientToUpstream int64,
	decision webSocketPreWriteDecision,
) *webSocketRelaySessionResult {
	sessionCommitted, commitSource := fallbackCommit.Snapshot()
	return &webSocketRelaySessionResult{
		Disposition:             webSocketRelayDispositionSuppressedUpstreamError,
		SessionCommitted:        sessionCommitted,
		TerminalCause:           model.TerminalUpstreamSemanticError,
		CommitSource:            commitSource,
		BytesClientToUpstream:   bytesClientToUpstream,
		BytesUpstreamToClient:   0,
		ClientAccepted:          lifecycleSnapshot.ClientAccepted,
		ClientVisible:           lifecycleSnapshot.ClientVisible,
		SuppressedUpstreamError: decision.SuppressedUpstreamError,
		SuppressedMessageType:   decision.SuppressedMessageType,
		SuppressedMessageData:   append([]byte(nil), decision.SuppressedMessageData...),
	}
}

func (f *WebSocketForwarder) relayPreVisibleClientMessage(
	ctx context.Context,
	upstreamConn *websocket.Conn,
	options webSocketRelayOptions,
	lifecycle *webSocketLifecycleState,
	initialClientRead webSocketInitialReadResult,
	observeClient func(websocket.MessageType, []byte),
	fallbackCommit *webSocketCommitState,
) webSocketPreVisibleRelayProgress {
	progress := webSocketPreVisibleRelayProgress{}
	messageType, data, err := initialClientRead.messageType, initialClientRead.data, initialClientRead.err
	if err != nil {
		progress.Result = newSinglePeerRelaySessionResult(
			err,
			webSocketPeerClient,
			fallbackCommit,
			lifecycle,
			0,
			0,
		)
		return progress
	}
	disablePreVisibleReplayBufferIfNeeded(options)
	if options.PreVisibleReplayBuffer != nil {
		options.PreVisibleReplayBuffer.Record(messageType, data, false)
	}
	if observeClient != nil {
		observeClient(messageType, data)
	}
	if err := upstreamConn.Write(ctx, messageType, data); err != nil {
		progress.Result = newSinglePeerRelaySessionResult(
			err,
			webSocketPeerUpstream,
			fallbackCommit,
			lifecycle,
			0,
			0,
		)
		return progress
	}
	progress.BytesClientToUpstream = int64(len(data))
	return progress
}

func (f *WebSocketForwarder) relayPreVisibleUpstreamMessage(
	ctx context.Context,
	clientConn, upstreamConn *websocket.Conn,
	options webSocketRelayOptions,
	lifecycle *webSocketLifecycleState,
	initialUpstreamRead webSocketInitialReadResult,
	observeUpstream func(websocket.MessageType, []byte),
	onUpstreamVisible func(websocket.MessageType, []byte),
	fallbackCommit *webSocketCommitState,
	bytesClientToUpstream int64,
) webSocketPreVisibleRelayProgress {
	progress := webSocketPreVisibleRelayProgress{}
	upstreamMessageType, upstreamData, err := initialUpstreamRead.messageType, initialUpstreamRead.data, initialUpstreamRead.err
	if err != nil {
		progress.Result = newSinglePeerRelaySessionResult(
			err,
			webSocketPeerUpstream,
			fallbackCommit,
			lifecycle,
			bytesClientToUpstream,
			0,
		)
		return progress
	}
	if observeUpstream != nil {
		observeUpstream(upstreamMessageType, upstreamData)
	}
	disablePreVisibleReplayBufferIfNeeded(options)
	observation := currentWebSocketObservation(options.Observer)
	lifecycleSnapshot := lifecycle.Snapshot()
	if options.PreWriteToClient != nil {
		decision := options.PreWriteToClient(webSocketPreWriteContext{
			MessageType:    upstreamMessageType,
			Data:           upstreamData,
			Observation:    observation,
			ClientAccepted: lifecycleSnapshot.ClientAccepted,
			ClientVisible:  lifecycleSnapshot.ClientVisible,
		})
		if decision.Action == webSocketPreWriteActionSuppress {
			closeWebSocketForSemanticFailover(upstreamConn)
			progress.Result = newSuppressedPreVisibleRelayResult(
				fallbackCommit,
				lifecycleSnapshot,
				bytesClientToUpstream,
				decision,
			)
			return progress
		}
	}
	if err := clientConn.Write(ctx, upstreamMessageType, upstreamData); err != nil {
		progress.Result = newSinglePeerRelaySessionResult(
			err,
			webSocketPeerClient,
			fallbackCommit,
			lifecycle,
			bytesClientToUpstream,
			0,
		)
		return progress
	}
	progress.BytesUpstreamToClient = int64(len(upstreamData))
	onUpstreamVisible(upstreamMessageType, upstreamData)
	return progress
}

func (f *WebSocketForwarder) relayImmediatePreVisibleUpstreamWindow(
	ctx context.Context,
	options webSocketRelayOptions,
	lifecycle *webSocketLifecycleState,
	initialUpstreamReadCh <-chan webSocketInitialReadResult,
	clientConn, upstreamConn *websocket.Conn,
	observeUpstream func(websocket.MessageType, []byte),
	onUpstreamVisible func(websocket.MessageType, []byte),
	fallbackCommit *webSocketCommitState,
) webSocketPreVisibleRelayProgress {
	progress := webSocketPreVisibleRelayProgress{}
	if lifecycle == nil || lifecycle.Snapshot().ClientVisible {
		return progress
	}
	if options.PreVisibleReplayBuffer == nil {
		return progress
	}

	timer := time.NewTimer(webSocketPreVisibleClientReadWindow)
	defer timer.Stop()

	var initialUpstreamRead webSocketInitialReadResult
	select {
	case initialUpstreamRead = <-initialUpstreamReadCh:
		progress.ConsumedInitialUpstream = true
	case <-timer.C:
		return progress
	case <-ctx.Done():
		progress.Result = newSinglePeerRelaySessionResult(
			ctx.Err(),
			webSocketPeerClient,
			fallbackCommit,
			lifecycle,
			0,
			0,
		)
		return progress
	}

	progress.merge(f.relayPreVisibleUpstreamMessage(
		ctx,
		clientConn,
		upstreamConn,
		options,
		lifecycle,
		initialUpstreamRead,
		observeUpstream,
		onUpstreamVisible,
		fallbackCommit,
		0,
	))
	return progress
}

func (f *WebSocketForwarder) relayPreVisibleWindow(
	ctx context.Context,
	clientConn, upstreamConn *websocket.Conn,
	options webSocketRelayOptions,
	lifecycle *webSocketLifecycleState,
	initialClientReadCh <-chan webSocketInitialReadResult,
	initialUpstreamReadCh <-chan webSocketInitialReadResult,
	observeClient func(websocket.MessageType, []byte),
	observeUpstream func(websocket.MessageType, []byte),
	onUpstreamVisible func(websocket.MessageType, []byte),
	fallbackCommit *webSocketCommitState,
) webSocketPreVisibleRelayProgress {
	progress := webSocketPreVisibleRelayProgress{}
	if lifecycle == nil || lifecycle.Snapshot().ClientVisible {
		return progress
	}
	if options.PreVisibleReplayBuffer == nil {
		return progress
	}

	// After the provider-first probe stays silent, the remaining pre-visible window
	// only has to serialize the first client application message and the first
	// upstream response it triggers. Reusing the provider probe's one-shot upstream
	// read keeps that handshake deterministic without starting a second reader.
	timer := time.NewTimer(webSocketPreVisibleClientReadWindow)
	defer timer.Stop()

	select {
	case initialClientRead := <-initialClientReadCh:
		progress.ConsumedInitialClient = true
		progress.merge(f.relayPreVisibleClientMessage(
			ctx,
			upstreamConn,
			options,
			lifecycle,
			initialClientRead,
			observeClient,
			fallbackCommit,
		))
		if progress.Result != nil {
			return progress
		}

		progress.ConsumedInitialUpstream = true
		progress.merge(f.relayPreVisibleUpstreamMessage(
			ctx,
			clientConn,
			upstreamConn,
			options,
			lifecycle,
			waitForInitialWebSocketRead(ctx, initialUpstreamReadCh),
			observeUpstream,
			onUpstreamVisible,
			fallbackCommit,
			progress.BytesClientToUpstream,
		))
		return progress
	case <-timer.C:
		return progress
	case <-ctx.Done():
		progress.Result = newSinglePeerRelaySessionResult(
			ctx.Err(),
			webSocketPeerClient,
			fallbackCommit,
			lifecycle,
			0,
			0,
		)
		return progress
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
	initialRead *webSocketInitialReadResult,
	observe func(messageType websocket.MessageType, data []byte),
	onForwarded func(messageType websocket.MessageType, data []byte),
	onRead func(messageType websocket.MessageType, data []byte),
	preWrite func(messageType websocket.MessageType, data []byte) webSocketPreWriteDecision,
) (int64, webSocketPeer, error) {
	var totalBytes int64
	processMessage := func(msgType websocket.MessageType, data []byte) (webSocketPeer, error) {
		if onRead != nil {
			onRead(msgType, data)
		}
		if observe != nil {
			observe(msgType, data)
		}
		if preWrite != nil {
			decision := preWrite(msgType, data)
			if decision.Action == webSocketPreWriteActionSuppress {
				return srcPeer, &webSocketSuppressedUpstreamError{
					upstreamError: decision.SuppressedUpstreamError,
				}
			}
		}
		if err := dst.Write(ctx, msgType, data); err != nil {
			return dstPeer, err
		}
		totalBytes += int64(len(data))
		if onForwarded != nil {
			onForwarded(msgType, data)
		}
		return webSocketPeerUnknown, nil
	}
	if initialRead != nil {
		if initialRead.err != nil {
			return totalBytes, srcPeer, initialRead.err
		}
		if failurePeer, err := processMessage(initialRead.messageType, initialRead.data); err != nil {
			return totalBytes, failurePeer, err
		}
	}
	for {
		msgType, data, err := src.Read(ctx)
		if err != nil {
			return totalBytes, srcPeer, err
		}
		if failurePeer, err := processMessage(msgType, data); err != nil {
			return totalBytes, failurePeer, err
		}
	}
}

func startWebSocketInitialRead(ctx context.Context, conn *websocket.Conn) <-chan webSocketInitialReadResult {
	results := make(chan webSocketInitialReadResult, 1)
	go func() {
		messageType, data, err := conn.Read(ctx)
		results <- webSocketInitialReadResult{
			messageType: messageType,
			data:        data,
			err:         err,
		}
	}()
	return results
}

func newWebSocketRelaySessionResultFromOutcome(
	outcome webSocketRelayOutcome,
	fallbackCommit *webSocketCommitState,
	lifecycle *webSocketLifecycleState,
	bytesClientToUpstream int64,
	bytesUpstreamToClient int64,
) *webSocketRelaySessionResult {
	sessionCommitted := false
	commitSource := model.CommitUnknown
	if fallbackCommit != nil {
		sessionCommitted, commitSource = fallbackCommit.Snapshot()
	}
	lifecycleSnapshot := webSocketLifecycleSnapshot{}
	if lifecycle != nil {
		lifecycleSnapshot = lifecycle.Snapshot()
	}
	return &webSocketRelaySessionResult{
		Disposition:           webSocketRelayDispositionCompleted,
		SessionCommitted:      sessionCommitted,
		TerminalCause:         outcome.terminalCause,
		CommitSource:          commitSource,
		CloseCode:             outcome.closeCode,
		BytesClientToUpstream: bytesClientToUpstream,
		BytesUpstreamToClient: bytesUpstreamToClient,
		Err:                   outcome.err,
		ClientAccepted:        lifecycleSnapshot.ClientAccepted,
		ClientVisible:         lifecycleSnapshot.ClientVisible,
	}
}

func (r *webSocketRelaySessionResult) toWebSocketResult() *WebSocketResult {
	if r == nil {
		return &WebSocketResult{CommitSource: model.CommitUnknown}
	}
	return &WebSocketResult{
		ClientAccepted:        r.ClientAccepted,
		ClientVisible:         r.ClientVisible,
		SessionCommitted:      r.SessionCommitted,
		TerminalCause:         r.TerminalCause,
		CommitSource:          r.CommitSource,
		CloseCode:             r.CloseCode,
		BytesClientToUpstream: r.BytesClientToUpstream,
		BytesUpstreamToClient: r.BytesUpstreamToClient,
		Err:                   r.Err,
		UpstreamError:         r.SuppressedUpstreamError,
	}
}

func shouldPreserveClientOnPreVisibleFailure(
	options webSocketRelayOptions,
	lifecycleSnapshot webSocketLifecycleSnapshot,
	outcome webSocketRelayOutcome,
) bool {
	if !options.PreserveClientOnPreVisibleFailure || lifecycleSnapshot.ClientVisible {
		return false
	}

	switch outcome.terminalCause {
	case model.TerminalUpstreamTransportError, model.TerminalCleanClose:
		return true
	default:
		return false
	}
}

func firstSuppressedUpstreamError(results ...webSocketRelayResult) *WebSocketUpstreamError {
	for _, result := range results {
		if result.suppressedUpstreamError != nil {
			return result.suppressedUpstreamError.Clone()
		}
	}
	return nil
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
	var suppressedErr *webSocketSuppressedUpstreamError
	if errors.As(err, &suppressedErr) {
		result.suppressedUpstreamError = suppressedErr.UpstreamError()
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
