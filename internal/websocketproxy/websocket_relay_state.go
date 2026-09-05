package websocketproxy

import (
	"sync"

	"github.com/doraemonkeys/switch-a/internal/codex/recovery"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"

	"github.com/coder/websocket"
)

type webSocketRelayResult struct {
	bytes                   int64
	err                     error
	failurePeer             webSocketPeer
	failureOperation        webSocketRelayFailureOperation
	errorOrder              uint32
	suppressedUpstreamError *WebSocketUpstreamError
	// closeError preserves the original *websocket.CloseError pulled from the
	// reader layer so reduction can forward the frame (code + reason) into
	// WebSocketResult.TransportObservation unchanged. Without it, reduction
	// would have to re-extract via errors.As on the candidate's err, which
	// duplicates logic and loses presence semantics when multiple wrappers
	// are involved.
	closeError *websocket.CloseError
}

type webSocketRelayOutcome struct {
	closeCode     websocket.StatusCode
	err           error
	terminalCause model.TerminalCause
	// observedCloseError carries the per-peer CloseError through reduction so
	// the evidence builder can populate WebSocketResult.TransportObservation.
	// It is strictly an observation-layer signal: unlike closeCode (which can
	// be synthesized as StatusNoStatusRcvd for isUnexpectedPeerDisconnect),
	// this pointer is non-nil only when a real frame was observed.
	observedCloseError *websocket.CloseError
	// failurePeer records which side originated the error that survived
	// reduction. Evidence builders need this to attribute transport facts to
	// upstream vs client; close propagation does not.
	failurePeer      webSocketPeer
	failureOperation webSocketRelayFailureOperation
}

type webSocketRelayFailureOperation uint8

const (
	webSocketRelayFailureOperationUnknown webSocketRelayFailureOperation = iota
	webSocketRelayFailureOperationRead
	webSocketRelayFailureOperationWrite
)

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
	webSocketPreWriteActionReject
)

type webSocketPreWriteDecision struct {
	Action                  webSocketPreWriteAction
	SuppressedUpstreamError *WebSocketUpstreamError
	SuppressedMessageType   websocket.MessageType
	SuppressedMessageData   []byte
	Err                     error
	RejectionDisposition    requestcapture.MessageDisposition
	OnWriteConfirmed        func() error
	ReplayEligible          bool
	ReplacementEligible     bool
	CurrentConnection       bool
	TraceContext            webSocketClientFrameTrace
	PrepareReplay           func() webSocketPreWriteDecision
}

type webSocketClientFrameTrace struct {
	Kind      string
	EventType string
	Decision  string
}

func clientFrameWriteError(decision webSocketPreWriteDecision, err error) error {
	if err == nil || !decision.CurrentConnection {
		return err
	}
	// A connection-bound control that could not reach its current upstream must
	// return to the client for an explicit reconnect. Provider replacement cannot
	// silently omit or replay the frame on another physical connection.
	return newWebSocketCodexCloseError(codexrecovery.Mark(codexrecovery.ConditionReconnectRequired, err))
}

type webSocketVisibleWriteContext struct {
	MessageType websocket.MessageType
	Data        []byte
	Observation WebSocketObservation
}

type webSocketCapturedRead struct {
	Ref       requestcapture.MessageRef
	Lineage   requestcapture.MessageLineage
	Direction requestcapture.MessageDirection
}

type webSocketMessageReadCapture func(
	webSocketRelayOptions,
	requestcapture.MessageDirection,
	websocket.MessageType,
	[]byte,
	requestcapture.MessageSource,
	requestcapture.MessageLineage,
	requestcapture.MessageLineage,
) webSocketCapturedRead

type webSocketMessageResultCapture func(
	webSocketRelayOptions,
	webSocketCapturedRead,
	requestcapture.MessageDisposition,
	bool,
	error,
)

func captureWebSocketMessageRead(
	options webSocketRelayOptions,
	direction requestcapture.MessageDirection,
	messageType websocket.MessageType,
	data []byte,
	source requestcapture.MessageSource,
	lineage requestcapture.MessageLineage,
	sourceLineage requestcapture.MessageLineage,
) webSocketCapturedRead {
	return options.captureRead(
		options,
		direction,
		messageType,
		data,
		source,
		lineage,
		sourceLineage,
	)
}

func captureNoWebSocketMessageRead(
	webSocketRelayOptions,
	requestcapture.MessageDirection,
	websocket.MessageType,
	[]byte,
	requestcapture.MessageSource,
	requestcapture.MessageLineage,
	requestcapture.MessageLineage,
) webSocketCapturedRead {
	return webSocketCapturedRead{}
}

func captureWebSocketMessageLineage(
	options webSocketRelayOptions,
	direction requestcapture.MessageDirection,
	messageType websocket.MessageType,
	_ []byte,
	source requestcapture.MessageSource,
	lineage requestcapture.MessageLineage,
	_ requestcapture.MessageLineage,
) webSocketCapturedRead {
	if direction != requestcapture.MessageDirectionClientToUpstream || source != requestcapture.MessageSourceLive {
		return webSocketCapturedRead{}
	}
	if _, ok := captureWebSocketMessageType(messageType); !ok {
		return webSocketCapturedRead{}
	}
	if !lineage.Valid() {
		lineage = options.GatewayCapture.NewMessageID()
	}
	return webSocketCapturedRead{Lineage: lineage, Direction: direction}
}

func captureWebSocketMessagePayload(
	options webSocketRelayOptions,
	direction requestcapture.MessageDirection,
	messageType websocket.MessageType,
	data []byte,
	source requestcapture.MessageSource,
	lineage requestcapture.MessageLineage,
	sourceLineage requestcapture.MessageLineage,
) webSocketCapturedRead {
	captureType, ok := captureWebSocketMessageType(messageType)
	if !ok {
		return webSocketCapturedRead{}
	}
	if !lineage.Valid() && direction == requestcapture.MessageDirectionClientToUpstream && source == requestcapture.MessageSourceLive {
		lineage = options.GatewayCapture.NewMessageID()
	}
	ref := options.Capture.MessageRead(requestcapture.MessageRead{
		Lineage:       lineage,
		Direction:     direction,
		Type:          captureType,
		Payload:       data,
		Source:        source,
		SourceLineage: sourceLineage,
	})
	return webSocketCapturedRead{Ref: ref, Lineage: ref.Lineage(), Direction: direction}
}

func captureWebSocketMessageResult(
	options webSocketRelayOptions,
	read webSocketCapturedRead,
	disposition requestcapture.MessageDisposition,
	writeConfirmed bool,
	err error,
) {
	options.captureResult(options, read, disposition, writeConfirmed, err)
}

func captureNoWebSocketMessageResult(
	webSocketRelayOptions,
	webSocketCapturedRead,
	requestcapture.MessageDisposition,
	bool,
	error,
) {
}

func captureWebSocketMessagePayloadResult(
	options webSocketRelayOptions,
	read webSocketCapturedRead,
	disposition requestcapture.MessageDisposition,
	writeConfirmed bool,
	err error,
) {
	peer := requestcapture.FailurePeerUnknown
	switch read.Direction {
	case requestcapture.MessageDirectionClientToUpstream:
		peer = requestcapture.FailurePeerUpstream
	case requestcapture.MessageDirectionUpstreamToClient:
		peer = requestcapture.FailurePeerClient
	}
	options.Capture.MessageResult(read.Ref, requestcapture.MessageResult{
		Disposition:        disposition,
		WriteConfirmed:     writeConfirmed,
		Failure:            webSocketMessageWrite(peer, err),
		CredentialEvidence: options.CredentialEvidence,
	})
}

func captureWebSocketMessageType(messageType websocket.MessageType) (requestcapture.MessageType, bool) {
	switch messageType {
	case websocket.MessageText:
		return requestcapture.MessageTypeText, true
	case websocket.MessageBinary:
		return requestcapture.MessageTypeBinary, true
	default:
		return "", false
	}
}

type webSocketRelayOptions struct {
	GatewayCapture           requestcapture.GatewayRecorder
	Capture                  requestcapture.Recorder
	CaptureMode              captureMode
	captureRead              webSocketMessageReadCapture
	captureResult            webSocketMessageResultCapture
	CredentialEvidence       requestcapture.CredentialEvidence
	Observer                 WebSocketMessageObserver
	OnFirstUpstreamMessage   func(WebSocketObservation)
	PreWriteToClient         func(webSocketPreWriteContext) webSocketPreWriteDecision
	PreWriteToUpstream       func(webSocketPreWriteContext) webSocketPreWriteDecision
	OnClientVisible          func(webSocketVisibleWriteContext)
	ClientReadHandoff        *webSocketClientReadHandoff
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

func (o webSocketRelayOptions) withCaptureHooks() webSocketRelayOptions {
	switch o.CaptureMode {
	case captureModeTransition:
		o.captureRead = captureWebSocketMessageLineage
		o.captureResult = captureNoWebSocketMessageResult
	case captureModePayload:
		o.captureRead = captureWebSocketMessagePayload
		o.captureResult = captureWebSocketMessagePayloadResult
	default:
		o.captureRead = captureNoWebSocketMessageRead
		o.captureResult = captureNoWebSocketMessageResult
	}
	return o
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
	// ObservedCloseError + FailurePeer pass the per-peer transport observation
	// through the relay session layer so it reaches WebSocketResult unchanged.
	// They are evidence-layer only — close propagation reads CloseCode, not
	// this pointer.
	ObservedCloseError *websocket.CloseError
	FailurePeer        webSocketPeer
	FailureOperation   webSocketRelayFailureOperation
}

type webSocketPreVisibleRelayProgress struct {
	BytesClientToUpstream   int64
	BytesUpstreamToClient   int64
	Result                  *webSocketRelaySessionResult
	ConsumedInitialUpstream bool
}

func (p *webSocketPreVisibleRelayProgress) merge(other webSocketPreVisibleRelayProgress) {
	p.BytesClientToUpstream += other.BytesClientToUpstream
	p.BytesUpstreamToClient += other.BytesUpstreamToClient
	if other.Result != nil {
		other.Result.BytesClientToUpstream = p.BytesClientToUpstream
		other.Result.BytesUpstreamToClient = p.BytesUpstreamToClient
		p.Result = other.Result
	}
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

func disablePreVisibleReplayBufferIfNeeded(options webSocketRelayOptions) {
	if options.Observer != nil && options.Observer.ParseDegraded() && options.PreVisibleReplayBuffer != nil {
		options.PreVisibleReplayBuffer.CloseReplay(webSocketReplayParseDegraded)
	}
}

func currentWebSocketObservation(observer WebSocketMessageObserver) WebSocketObservation {
	if observer == nil {
		return WebSocketObservation{}
	}
	return observer.Snapshot()
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
			status := buffer.Status()
			// An empty replay snapshot still means failover is safe: the provider failed
			// before any replayable client payload crossed the pre-visible boundary, so
			// there is nothing to resend to the replacement provider.
			if status.State != webSocketReplayable {
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
