package websocketproxy

import (
	"context"
	"io"
	"time"

	"github.com/coder/websocket"
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
			upload, upstreamRead := withWebSocketConcurrentRead(ctx, initialUpstreamReadCh, "pre_visible_upload",
				func(uploadCtx context.Context) webSocketPreVisibleRelayProgress {
					return f.relayPreVisibleClientMessage(uploadCtx, upstreamConn, options, lifecycle, clientRead, observeClient, fallbackCommit)
				})
			if upstreamRead != nil && upstreamRead.err != nil {
				// The upload's cancellation is a consequence, not the origin, of failure.
				upload.Result = nil
			}
			progress.merge(upload)
			if upstreamRead != nil {
				progress.ConsumedInitialUpstream = true
				if progress.Result == nil {
					progress.merge(f.relayPreVisibleUpstreamMessage(ctx, clientConn, upstreamConn,
						options, lifecycle, *upstreamRead, observeUpstream, onUpstreamVisible,
						fallbackCommit, progress.BytesClientToUpstream))
				}
				return progress
			}
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

// withWebSocketConcurrentRead keeps transport failure observable while a physical
// upload blocks. Join the uploader before applying read results: replay retention,
// delivery confirmation and lifecycle hooks still have one serialized owner.
func withWebSocketConcurrentRead[T any](
	ctx context.Context,
	reads <-chan webSocketInitialReadResult,
	phase string,
	upload func(context.Context) T,
) (T, *webSocketInitialReadResult) {
	uploadCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan T, 1)
	go func() { done <- upload(uploadCtx) }()
	select {
	case result := <-done:
		return result, nil
	case read, ok := <-reads:
		if !ok {
			read.err = io.ErrUnexpectedEOF
		}
		if read.err != nil {
			cancel()
		}
		return <-done, &read
	}
}

// A consumed initial read belongs to the relay, even when it arrived during replay.
func retainedWebSocketRead(read webSocketInitialReadResult) <-chan webSocketInitialReadResult {
	results := make(chan webSocketInitialReadResult, 1)
	results <- read
	return results
}
