package proxy

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/doraemonkeys/switch-a/internal/requestcapture"

	"github.com/coder/websocket"
)

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
	if options.SkipClientToUpstream {
		return
	}

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
			nil,
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
	var totalBytes int64
	processMessage := func(
		msgType websocket.MessageType,
		data []byte,
	) (webSocketPeer, webSocketRelayFailureOperation, error) {
		captured := captureWebSocketMessageRead(
			options,
			direction,
			msgType,
			data,
			requestcapture.MessageSourceLive,
			requestcapture.MessageLineage{},
			requestcapture.MessageLineage{},
		)
		var onWriteConfirmed func()
		if onRead != nil {
			onWriteConfirmed = onRead(msgType, data, captured)
		}
		if observe != nil {
			observe(msgType, data)
		}
		if preWrite != nil {
			decision := preWrite(msgType, data)
			if decision.Action == webSocketPreWriteActionSuppress {
				captureWebSocketMessageResult(options, captured, requestcapture.MessageDispositionSuppressed, false, nil)
				return srcPeer, webSocketRelayFailureOperationUnknown, &webSocketSuppressedUpstreamError{
					upstreamError: decision.SuppressedUpstreamError,
				}
			}
		}
		if err := dst.Write(ctx, msgType, data); err != nil {
			captureWebSocketMessageResult(options, captured, requestcapture.MessageDispositionWriteFailed, false, err)
			return dstPeer, webSocketRelayFailureOperationWrite, err
		}
		totalBytes += int64(len(data))
		if onWriteConfirmed != nil {
			onWriteConfirmed()
		}
		if onForwarded != nil {
			onForwarded(msgType, data)
		}
		captureWebSocketMessageResult(options, captured, requestcapture.MessageDispositionForwarded, true, nil)
		return webSocketPeerUnknown, webSocketRelayFailureOperationUnknown, nil
	}
	if initialRead != nil {
		if initialRead.err != nil {
			return totalBytes, srcPeer, webSocketRelayFailureOperationRead, initialRead.err
		}
		if failurePeer, failureOperation, err := processMessage(initialRead.messageType, initialRead.data); err != nil {
			return totalBytes, failurePeer, failureOperation, err
		}
	}
	for {
		msgType, data, err := src.Read(ctx)
		if err != nil {
			return totalBytes, srcPeer, webSocketRelayFailureOperationRead, err
		}
		if failurePeer, failureOperation, err := processMessage(msgType, data); err != nil {
			return totalBytes, failurePeer, failureOperation, err
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
