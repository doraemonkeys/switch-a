package proxy

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"switch-a/internal/model"

	"github.com/coder/websocket"
)

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
	initialClientReadCh := options.InitialClientReadCh
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

	closeMsg := ""
	if outcome.err != nil {
		closeMsg = truncateUTF8(outcome.err.Error(), webSocketCloseReasonByteLimit)
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

	if initialClientReadCh == nil {
		// The orchestrator may already own a pending first-client read from the
		// bootstrap selection probe. Replacing that channel would leave the
		// original goroutine consuming the first application frame off-path,
		// which makes the real relay miss the payload entirely.
		initialClientReadCh = startWebSocketInitialRead(ctx, clientConn)
	}
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
