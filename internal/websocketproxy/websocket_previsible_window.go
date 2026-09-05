package websocketproxy

import (
	"context"
	"github.com/coder/websocket"
	"time"
)

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

	timer := time.NewTimer(webSocketPreVisibleProviderFirstWindow)
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
	ctx, sessionCtx context.Context,
	clientConn, upstreamConn *websocket.Conn,
	options webSocketRelayOptions,
	lifecycle *webSocketLifecycleState,
	clientReads *webSocketClientReadHandoff,
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

	for {
		clientReadCh := clientReads.pendingRead(sessionCtx, clientConn)
		select {
		case clientRead, ok := <-clientReadCh:
			clientReads.complete(clientReadCh)
			if !ok {
				clientRead = webSocketInitialReadResult{err: errWebSocketClientReadHandoffClosed}
			}
			progress.merge(f.relayPreVisibleClientMessage(
				ctx,
				upstreamConn,
				options,
				lifecycle,
				clientRead,
				observeClient,
				fallbackCommit,
			))
			if progress.Result != nil {
				return progress
			}
		case initialUpstreamRead := <-initialUpstreamReadCh:
			progress.ConsumedInitialUpstream = true
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
				progress.BytesClientToUpstream,
			))
			return progress
		case <-ctx.Done():
			progress.Result = newSinglePeerRelaySessionResult(
				ctx.Err(),
				webSocketPeerClient,
				fallbackCommit,
				lifecycle,
				progress.BytesClientToUpstream,
				progress.BytesUpstreamToClient,
			)
			return progress
		}
	}
}
