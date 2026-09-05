package websocketproxy

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/doraemonkeys/switch-a/internal/requestcapture"

	"github.com/coder/websocket"
)

var (
	errWebSocketClientReadHandoffUnavailable = errors.New("websocket client read handoff is unavailable")
	errWebSocketClientReadHandoffClosed      = errors.New("websocket client read handoff closed without a result")
)

// Pending first delivery is independent of optional replay retention. A frame
// remains here until its first successful physical write, even when replay closes.
type webSocketPendingDelivery struct {
	message     webSocketReplayMessage
	replayIndex int
}

func (o *WebSocketSessionOrchestrator) replayBufferedMessages(
	ctx context.Context,
	upstreamConn *websocket.Conn,
	observer WebSocketMessageObserver,
	captureOptions webSocketRelayOptions,
) (int64, bool, error) {
	captureOptions = captureOptions.withCaptureHooks()
	snapshot := o.replayBuffer.Snapshot()
	defer snapshot.Release()
	if !snapshot.Enabled && len(o.pendingDelivery) == 0 {
		if o.replayBuffer == nil || o.lifecycle.Snapshot().ClientVisible {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("pre-visible replay unavailable: %s", o.replayBuffer.Status().State)
	}
	var total int64
	attempted := false
	for index, message := range snapshot.Messages {
		if !message.Delivered && len(o.pendingDelivery) > 0 {
			continue
		}
		attempted = true
		n, err := o.deliverBufferedClientMessage(ctx, upstreamConn, observer, captureOptions, message, index)
		total += n
		if err != nil {
			return total, true, err
		}
	}
	for len(o.pendingDelivery) > 0 {
		pending := o.pendingDelivery[0]
		attempted = true
		n, err := o.deliverBufferedClientMessage(ctx, upstreamConn, observer, captureOptions, pending.message, pending.replayIndex)
		total += n
		if err != nil {
			return total, true, err
		}
		o.replayBuffer.releaseDelivery(pending.message)
		o.pendingDelivery[0] = webSocketPendingDelivery{}
		o.pendingDelivery = o.pendingDelivery[1:]
	}
	o.pendingDelivery = nil
	return total, attempted, nil
}

func (o *WebSocketSessionOrchestrator) deliverBufferedClientMessage(
	ctx context.Context, upstreamConn *websocket.Conn, observer WebSocketMessageObserver,
	captureOptions webSocketRelayOptions, message webSocketReplayMessage, replayIndex int,
) (int64, error) {
	source := requestcapture.MessageSourceReplay
	lineage := requestcapture.MessageLineage{}
	sourceLineage := message.Lineage
	if !message.Delivered {
		source = requestcapture.MessageSourceLive
		lineage = message.Lineage
		sourceLineage = requestcapture.MessageLineage{}
	}
	captured := captureWebSocketMessageRead(captureOptions, requestcapture.MessageDirectionClientToUpstream,
		message.MessageType, message.Data, source, lineage, sourceLineage)
	o.observeReplayClientMessage(observer, message.MessageType, message.Data)
	decision := message.Decision
	if decision.PrepareReplay != nil {
		decision = decision.PrepareReplay(message.Data)
	}
	if decision.Action == webSocketPreWriteActionReject {
		disposition := decision.RejectionDisposition
		if disposition == "" {
			disposition = requestcapture.MessageDispositionProtocolRejected
		}
		captureWebSocketMessageResult(captureOptions, captured, disposition, false, decision.Err)
		return 0, decision.Err
	}
	payload := decision.physicalPayload(message.Data)
	if err := upstreamConn.Write(ctx, message.MessageType, payload); err != nil {
		captureWebSocketMessageResult(captureOptions, captured, requestcapture.MessageDispositionWriteFailed, false, err)
		return 0, err
	}
	if !decision.ReplacementEligible {
		o.replayBuffer.CloseReplay(webSocketReplayNonReplayableFrame)
	}
	if decision.OnWriteConfirmed != nil {
		if err := decision.OnWriteConfirmed(); err != nil {
			captureWebSocketMessageResult(captureOptions, captured, requestcapture.MessageDispositionStorageRejected, true, err)
			return int64(len(payload)), err
		}
	}
	if !message.Delivered {
		o.replayBuffer.MarkDelivered(replayIndex, captured.Lineage)
	}
	captureWebSocketMessageResult(captureOptions, captured, requestcapture.MessageDispositionForwarded, true, nil)
	return int64(len(payload)), nil
}

func (o *WebSocketSessionOrchestrator) observeReplayClientMessage(
	observer WebSocketMessageObserver,
	messageType websocket.MessageType,
	data []byte,
) {
	switch tracked := observer.(type) {
	case *bytesTrackingObserver:
		if tracked.inner != nil {
			tracked.inner.ObserveClientMessage(messageType, data)
		}
	default:
		if observer != nil {
			observer.ObserveClientMessage(messageType, data)
		}
	}
}

func (f *WebSocketForwarder) startClientToUpstreamRelay(
	ctx context.Context,
	sessionCtx context.Context,
	cancel context.CancelFunc,
	wg *sync.WaitGroup,
	upstreamConn *websocket.Conn,
	clientConn *websocket.Conn,
	options webSocketRelayOptions,
	lifecycle *webSocketLifecycleState,
	clientReads *webSocketClientReadHandoff,
	observeClient func(websocket.MessageType, []byte),
	errorOrder *atomic.Uint32,
	result *webSocketRelayResult,
) {
	wg.Go(func() {
		n, failurePeer, failureOperation, err := relayMessages(
			ctx,
			upstreamConn,
			webSocketPeerUpstream,
			webSocketPeerClient,
			nil,
			options,
			requestcapture.MessageDirectionClientToUpstream,
			observeClient,
			nil,
			func(messageType websocket.MessageType, data []byte, captured webSocketCapturedRead, decision webSocketPreWriteDecision) func() {
				if options.PreVisibleReplayBuffer == nil {
					return nil
				}
				lifecycleSnapshot := lifecycle.Snapshot()
				if options.Observer != nil && options.Observer.ParseDegraded() {
					options.PreVisibleReplayBuffer.CloseReplay(webSocketReplayParseDegraded)
				}
				if !decision.ReplayEligible {
					return nil
				}
				bufferedMessageIndex := options.PreVisibleReplayBuffer.RecordDecision(
					messageType,
					data,
					lifecycleSnapshot.ClientVisible,
					captured.Lineage,
					decision,
				)
				return func() {
					// Replay classification follows the physical upstream write, not
					// whether debug capture happened to retain the message.
					options.PreVisibleReplayBuffer.MarkDelivered(bufferedMessageIndex, captured.Lineage)
				}
			},
			func(messageType websocket.MessageType, data []byte) webSocketPreWriteDecision {
				if options.PreWriteToUpstream == nil {
					return webSocketPreWriteDecision{Action: webSocketPreWriteActionForward}
				}
				lifecycleSnapshot := lifecycle.Snapshot()
				return options.PreWriteToUpstream(webSocketPreWriteContext{
					MessageType: messageType, Data: data,
					ClientAccepted: lifecycleSnapshot.ClientAccepted,
					ClientVisible:  lifecycleSnapshot.ClientVisible,
				})
			},
			func(attemptCtx context.Context) (websocket.MessageType, []byte, error) {
				return clientReads.Read(attemptCtx, sessionCtx, clientConn)
			},
		)
		*result = newWebSocketRelayResultForOperation(n, err, failurePeer, failureOperation, errorOrder)
		if err != nil {
			cancel()
		}
	})
}

// webSocketClientReadHandoff keeps the single downstream reader owned by the
// whole session rather than by one provider attempt. coder/websocket closes a
// connection when a Read context is canceled, so an attempt-scoped Read would
// destroy the downstream socket precisely when pre-visible failover needs to
// preserve it. A pending read remains here for the next attempt to consume.
type webSocketClientReadHandoff struct {
	mu      sync.Mutex
	pending <-chan webSocketInitialReadResult
}

func newWebSocketClientReadHandoff(initial <-chan webSocketInitialReadResult) *webSocketClientReadHandoff {
	return &webSocketClientReadHandoff{pending: initial}
}

func (h *webSocketClientReadHandoff) pendingRead(
	sessionCtx context.Context,
	clientConn *websocket.Conn,
) <-chan webSocketInitialReadResult {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pending == nil && clientConn != nil {
		h.pending = startWebSocketInitialRead(sessionCtx, clientConn)
	}
	return h.pending
}

func (h *webSocketClientReadHandoff) complete(readCh <-chan webSocketInitialReadResult) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pending == readCh {
		h.pending = nil
	}
}

func (h *webSocketClientReadHandoff) Read(
	attemptCtx context.Context,
	sessionCtx context.Context,
	clientConn *websocket.Conn,
) (websocket.MessageType, []byte, error) {
	readCh := h.pendingRead(sessionCtx, clientConn)
	if readCh == nil {
		return 0, nil, errWebSocketClientReadHandoffUnavailable
	}
	select {
	case read, ok := <-readCh:
		h.complete(readCh)
		if !ok {
			return 0, nil, errWebSocketClientReadHandoffClosed
		}
		return read.messageType, read.data, read.err
	case <-attemptCtx.Done():
		return 0, nil, attemptCtx.Err()
	}
}

type webSocketRelayMessageProcessor struct {
	ctx         context.Context
	dst         *websocket.Conn
	dstPeer     webSocketPeer
	srcPeer     webSocketPeer
	options     webSocketRelayOptions
	direction   requestcapture.MessageDirection
	observe     func(websocket.MessageType, []byte)
	onForwarded func(websocket.MessageType, []byte)
	onRead      func(websocket.MessageType, []byte, webSocketCapturedRead, webSocketPreWriteDecision) func()
	preWrite    func(websocket.MessageType, []byte) webSocketPreWriteDecision
	totalBytes  int64
}

func (p *webSocketRelayMessageProcessor) process(
	messageType websocket.MessageType,
	data []byte,
) (webSocketPeer, webSocketRelayFailureOperation, error) {
	captured := captureWebSocketMessageRead(
		p.options,
		p.direction,
		messageType,
		data,
		requestcapture.MessageSourceLive,
		requestcapture.MessageLineage{},
		requestcapture.MessageLineage{},
	)
	decision, failurePeer, failureOperation, err := p.prepareWrite(messageType, data, captured)
	if err != nil {
		return failurePeer, failureOperation, err
	}
	var onWriteConfirmed func()
	if p.onRead != nil {
		onWriteConfirmed = p.onRead(messageType, data, captured, decision)
	}
	if p.observe != nil {
		p.observe(messageType, data)
	}
	payload := decision.physicalPayload(data)
	if err := p.dst.Write(p.ctx, messageType, payload); err != nil {
		writeErr := clientFrameWriteError(decision, err)
		captureWebSocketMessageResult(p.options, captured, requestcapture.MessageDispositionWriteFailed, false, writeErr)
		return p.dstPeer, webSocketRelayFailureOperationWrite, writeErr
	}
	p.totalBytes += int64(len(payload))
	if !decision.ReplacementEligible && p.options.PreVisibleReplayBuffer != nil {
		p.options.PreVisibleReplayBuffer.CloseReplay(webSocketReplayNonReplayableFrame)
	}
	if onWriteConfirmed != nil {
		onWriteConfirmed()
	}
	if decision.OnWriteConfirmed != nil {
		if err := decision.OnWriteConfirmed(); err != nil {
			captureWebSocketMessageResult(p.options, captured, requestcapture.MessageDispositionStorageRejected, true, err)
			return webSocketPeerUnknown, webSocketRelayFailureOperationUnknown, err
		}
	}
	if p.onForwarded != nil {
		p.onForwarded(messageType, data)
	}
	captureWebSocketMessageResult(p.options, captured, requestcapture.MessageDispositionForwarded, true, nil)
	return webSocketPeerUnknown, webSocketRelayFailureOperationUnknown, nil
}

func (p *webSocketRelayMessageProcessor) prepareWrite(
	messageType websocket.MessageType,
	data []byte,
	captured webSocketCapturedRead,
) (webSocketPreWriteDecision, webSocketPeer, webSocketRelayFailureOperation, error) {
	if p.preWrite == nil {
		return replayableClientFrameDecision(), webSocketPeerUnknown, webSocketRelayFailureOperationUnknown, nil
	}
	decision := p.preWrite(messageType, data)
	if decision.Action == webSocketPreWriteActionSuppress {
		captureWebSocketMessageResult(p.options, captured, requestcapture.MessageDispositionSuppressed, false, nil)
		return decision, p.srcPeer, webSocketRelayFailureOperationUnknown, &webSocketSuppressedUpstreamError{
			upstreamError: decision.SuppressedUpstreamError,
		}
	}
	if decision.Action == webSocketPreWriteActionReject {
		disposition := decision.RejectionDisposition
		if disposition == "" {
			disposition = requestcapture.MessageDispositionProtocolRejected
		}
		captureWebSocketMessageResult(p.options, captured, disposition, false, decision.Err)
		return decision, p.srcPeer, webSocketRelayFailureOperationUnknown, decision.Err
	}
	return decision, webSocketPeerUnknown, webSocketRelayFailureOperationUnknown, nil
}

type webSocketMessageReadFunc func(context.Context) (websocket.MessageType, []byte, error)

func relayMessages(
	ctx context.Context,
	dst *websocket.Conn,
	dstPeer webSocketPeer,
	srcPeer webSocketPeer,
	initialRead *webSocketInitialReadResult,
	options webSocketRelayOptions,
	direction requestcapture.MessageDirection,
	observe func(messageType websocket.MessageType, data []byte),
	onForwarded func(messageType websocket.MessageType, data []byte),
	onRead func(messageType websocket.MessageType, data []byte, captured webSocketCapturedRead, decision webSocketPreWriteDecision) func(),
	preWrite func(messageType websocket.MessageType, data []byte) webSocketPreWriteDecision,
	read webSocketMessageReadFunc,
) (int64, webSocketPeer, webSocketRelayFailureOperation, error) {
	processor := webSocketRelayMessageProcessor{
		ctx: ctx, dst: dst, dstPeer: dstPeer, srcPeer: srcPeer,
		options: options, direction: direction, observe: observe,
		onForwarded: onForwarded, onRead: onRead, preWrite: preWrite,
	}
	if initialRead != nil {
		if initialRead.err != nil {
			return 0, srcPeer, webSocketRelayFailureOperationRead, initialRead.err
		}
		if failurePeer, failureOperation, err := processor.process(initialRead.messageType, initialRead.data); err != nil {
			return processor.totalBytes, failurePeer, failureOperation, err
		}
	}
	for {
		messageType, data, err := read(ctx)
		if err != nil {
			return processor.totalBytes, srcPeer, webSocketRelayFailureOperationRead, err
		}
		if failurePeer, failureOperation, err := processor.process(messageType, data); err != nil {
			return processor.totalBytes, failurePeer, failureOperation, err
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
