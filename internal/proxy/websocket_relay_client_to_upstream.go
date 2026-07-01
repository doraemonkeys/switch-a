package proxy

import (
	"context"
	"sync"
	"sync/atomic"

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
		*result = newWebSocketRelayResult(n, err, failurePeer, errorOrder)
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
