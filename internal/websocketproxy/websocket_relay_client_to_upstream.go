package websocketproxy

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/doraemonkeys/switch-a/internal/requestcapture"

	"github.com/coder/websocket"
)

// replayBufferedMessages is part of the client-to-upstream transport boundary:
// it preserves the original wire bytes and applies the same capture and
// write-confirmation hooks as live client traffic.
func (o *WebSocketSessionOrchestrator) replayBufferedMessages(
	ctx context.Context,
	upstreamConn *websocket.Conn,
	observer WebSocketMessageObserver,
	captureOptions webSocketRelayOptions,
) (int64, bool, error) {
	captureOptions = captureOptions.withCaptureHooks()
	if o.replayBuffer == nil {
		return 0, false, nil
	}

	snapshot := o.replayBuffer.Snapshot()
	if !snapshot.Enabled {
		if o.lifecycle != nil && o.lifecycle.Snapshot().ClientVisible {
			// Once the session is already visible, a disabled pre-visible replay buffer
			// is expected rather than fatal. Post-visible failover reuses the live
			// downstream socket without trying to resurrect the pre-visible window.
			return 0, false, nil
		}
		return 0, false, errors.New("pre-visible replay buffer disabled")
	}
	if len(snapshot.Messages) == 0 {
		// Suppression can happen before the client sends any replayable frame. In that
		// case the replacement provider should continue with a clean socket rather than
		// treating "nothing to replay" as a synthetic transport failure.
		return 0, false, nil
	}

	var replayedBytes int64
	for index, message := range snapshot.Messages {
		source := requestcapture.MessageSourceReplay
		lineage := requestcapture.MessageLineage{}
		sourceLineage := message.Lineage
		if !message.Delivered {
			// The bootstrap selector read this frame before a provider was chosen. Its
			// first physical delivery is still the live event; only later attempts are
			// replay events linked back to that stable original message identity.
			source = requestcapture.MessageSourceLive
			lineage = message.Lineage
			sourceLineage = requestcapture.MessageLineage{}
		}
		captured := captureWebSocketMessageRead(
			captureOptions,
			requestcapture.MessageDirectionClientToUpstream,
			message.MessageType,
			message.Data,
			source,
			lineage,
			sourceLineage,
		)
		o.observeReplayClientMessage(observer, message.MessageType, message.Data)
		var boundaryConfirmed func() error
		if captureOptions.PreWriteToUpstream != nil {
			decision := captureOptions.PreWriteToUpstream(webSocketPreWriteContext{
				MessageType: message.MessageType, Data: message.Data,
				ClientAccepted: o.lifecycle.Snapshot().ClientAccepted,
				ClientVisible:  o.lifecycle.Snapshot().ClientVisible,
			})
			if decision.Action == webSocketPreWriteActionReject {
				disposition := decision.RejectionDisposition
				if disposition == "" {
					disposition = requestcapture.MessageDispositionProtocolRejected
				}
				captureWebSocketMessageResult(captureOptions, captured, disposition, false, decision.Err)
				return replayedBytes, true, decision.Err
			}
			boundaryConfirmed = decision.OnWriteConfirmed
		}
		if err := upstreamConn.Write(ctx, message.MessageType, message.Data); err != nil {
			captureWebSocketMessageResult(
				captureOptions,
				captured,
				requestcapture.MessageDispositionWriteFailed,
				false,
				err,
			)
			return replayedBytes, true, err
		}
		if boundaryConfirmed != nil {
			if err := boundaryConfirmed(); err != nil {
				captureWebSocketMessageResult(
					captureOptions, captured, requestcapture.MessageDispositionStorageRejected, true, err,
				)
				return replayedBytes + int64(len(message.Data)), true, err
			}
		}
		if !message.Delivered {
			o.replayBuffer.MarkDelivered(index, captured.Lineage)
		}
		captureWebSocketMessageResult(
			captureOptions,
			captured,
			requestcapture.MessageDispositionForwarded,
			true,
			nil,
		)
		replayedBytes += int64(len(message.Data))
	}
	return replayedBytes, true, nil
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
	cancel context.CancelFunc,
	wg *sync.WaitGroup,
	upstreamConn *websocket.Conn,
	clientConn *websocket.Conn,
	options webSocketRelayOptions,
	lifecycle *webSocketLifecycleState,
	initialClientReadCh <-chan webSocketInitialReadResult,
	observeClient func(websocket.MessageType, []byte),
	errorOrder *atomic.Uint32,
	result *webSocketRelayResult,
) {
	wg.Go(func() {
		initialClientRead := awaitInitialWebSocketRead(ctx, initialClientReadCh)
		n, failurePeer, failureOperation, err := relayMessages(
			ctx,
			upstreamConn,
			webSocketPeerUpstream,
			clientConn,
			webSocketPeerClient,
			initialClientRead,
			options,
			requestcapture.MessageDirectionClientToUpstream,
			observeClient,
			nil,
			func(messageType websocket.MessageType, data []byte, captured webSocketCapturedRead) func() {
				if options.PreVisibleReplayBuffer == nil {
					return nil
				}
				lifecycleSnapshot := lifecycle.Snapshot()
				if options.Observer != nil && options.Observer.ParseDegraded() {
					options.PreVisibleReplayBuffer.Disable()
				}
				bufferedMessageIndex := options.PreVisibleReplayBuffer.RecordWithLineage(
					messageType,
					data,
					lifecycleSnapshot.ClientVisible,
					captured.Lineage,
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
		)
		*result = newWebSocketRelayResultForOperation(n, err, failurePeer, failureOperation, errorOrder)
		if err != nil {
			cancel()
		}
	})
}

func awaitInitialWebSocketRead(
	ctx context.Context,
	readCh <-chan webSocketInitialReadResult,
) *webSocketInitialReadResult {
	if readCh == nil {
		return nil
	}

	select {
	case read := <-readCh:
		return &read
	case <-ctx.Done():
		return &webSocketInitialReadResult{err: ctx.Err()}
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
	onRead      func(websocket.MessageType, []byte, webSocketCapturedRead) func()
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
	var onWriteConfirmed func()
	if p.onRead != nil {
		onWriteConfirmed = p.onRead(messageType, data, captured)
	}
	if p.observe != nil {
		p.observe(messageType, data)
	}
	boundaryConfirmed, failurePeer, failureOperation, err := p.prepareWrite(messageType, data, captured)
	if err != nil {
		return failurePeer, failureOperation, err
	}
	if err := p.dst.Write(p.ctx, messageType, data); err != nil {
		captureWebSocketMessageResult(p.options, captured, requestcapture.MessageDispositionWriteFailed, false, err)
		return p.dstPeer, webSocketRelayFailureOperationWrite, err
	}
	p.totalBytes += int64(len(data))
	if onWriteConfirmed != nil {
		onWriteConfirmed()
	}
	if boundaryConfirmed != nil {
		if err := boundaryConfirmed(); err != nil {
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
) (func() error, webSocketPeer, webSocketRelayFailureOperation, error) {
	if p.preWrite == nil {
		return nil, webSocketPeerUnknown, webSocketRelayFailureOperationUnknown, nil
	}
	decision := p.preWrite(messageType, data)
	if decision.Action == webSocketPreWriteActionSuppress {
		captureWebSocketMessageResult(p.options, captured, requestcapture.MessageDispositionSuppressed, false, nil)
		return nil, p.srcPeer, webSocketRelayFailureOperationUnknown, &webSocketSuppressedUpstreamError{
			upstreamError: decision.SuppressedUpstreamError,
		}
	}
	if decision.Action == webSocketPreWriteActionReject {
		disposition := decision.RejectionDisposition
		if disposition == "" {
			disposition = requestcapture.MessageDispositionProtocolRejected
		}
		captureWebSocketMessageResult(p.options, captured, disposition, false, decision.Err)
		return nil, p.srcPeer, webSocketRelayFailureOperationUnknown, decision.Err
	}
	return decision.OnWriteConfirmed, webSocketPeerUnknown, webSocketRelayFailureOperationUnknown, nil
}

func relayMessages(
	ctx context.Context,
	dst *websocket.Conn,
	dstPeer webSocketPeer,
	src *websocket.Conn,
	srcPeer webSocketPeer,
	initialRead *webSocketInitialReadResult,
	options webSocketRelayOptions,
	direction requestcapture.MessageDirection,
	observe func(messageType websocket.MessageType, data []byte),
	onForwarded func(messageType websocket.MessageType, data []byte),
	onRead func(messageType websocket.MessageType, data []byte, captured webSocketCapturedRead) func(),
	preWrite func(messageType websocket.MessageType, data []byte) webSocketPreWriteDecision,
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
		messageType, data, err := src.Read(ctx)
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
