package proxy

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"switch-a/internal/model"

	"github.com/coder/websocket"
)

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
	InitialClientReadCh      <-chan webSocketInitialReadResult
	PreVisibleReplayBuffer   *preVisibleClientMessageBuffer
	Lifecycle                *webSocketLifecycleState
	PreserveClientOnSuppress bool
	SkipPreVisibleWindow     bool
	// SkipClientToUpstream is used for post-visible failover handoff when the
	// downstream connection must remain open for upstream-driven continuity but
	// the old relay must not poison the client socket by canceling a blocked read.
	SkipClientToUpstream bool
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
	return webSocketSemanticReplacementCloseReason
}

func (e *webSocketSuppressedUpstreamError) UpstreamError() *WebSocketUpstreamError {
	if e == nil {
		return nil
	}
	return e.upstreamError.Clone()
}

func closeWebSocketForSemanticReplacement(conn *websocket.Conn) {
	if conn == nil {
		return
	}
	// Pre-visible semantic replacement must hand control back to the orchestrator immediately.
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
		if ctx.Observation.ParseDegraded {
			return webSocketPreWriteDecision{Action: webSocketPreWriteActionForward}
		}
		classification := classifyWebSocketUpstreamMessage(ctx.MessageType, ctx.Data, ctx.Observation.ParseDegraded)
		suppressedUpstreamError := canonicalPreWriteUpstreamError(ctx)
		if suppressedUpstreamError != nil {
			classification = classifyWebSocketUpstreamError(suppressedUpstreamError)
		}
		if classification != webSocketSemanticClassificationProviderScopedAllowlisted {
			return webSocketPreWriteDecision{Action: webSocketPreWriteActionForward}
		}
		if !ctx.ClientVisible {
			if buffer == nil {
				return webSocketPreWriteDecision{Action: webSocketPreWriteActionForward}
			}
			snapshot := buffer.Snapshot()
			// An empty replay snapshot still means failover is safe: the provider failed
			// before any replayable client payload crossed the pre-visible boundary, so
			// there is nothing to resend to the replacement provider.
			if !snapshot.Enabled {
				return webSocketPreWriteDecision{Action: webSocketPreWriteActionForward}
			}
		}
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

func canonicalPreWriteUpstreamError(ctx webSocketPreWriteContext) *WebSocketUpstreamError {
	upstreamErr := ctx.Observation.UpstreamError
	if upstreamErr == nil || upstreamErr.Raw != string(ctx.Data) {
		return nil
	}
	return upstreamErr.Clone()
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
