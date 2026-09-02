package websocketproxy

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"

	"github.com/coder/websocket"
)

// relay copies messages between client and upstream until one side closes.
// Uses a context-derived cancel to ensure both goroutines exit promptly.
//
//nolint:gocognit,gocyclo,funlen // The relay keeps both transport directions and failover hooks in one place until the refactor settles.
func (f *WebSocketForwarder) relay(ctx context.Context, clientConn, upstreamConn *websocket.Conn, options webSocketRelayOptions) *webSocketRelaySessionResult {
	options = options.withCaptureHooks()
	sessionCtx := ctx
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
	clientReads := options.ClientReadHandoff
	if clientReads == nil {
		clientReads = newWebSocketClientReadHandoff(nil)
	}

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

	initialUpstreamReadCh, preVisibleProgress := f.runPreVisibleSuppressionWindow(
		ctx,
		sessionCtx,
		clientConn,
		upstreamConn,
		options,
		lifecycle,
		clientReads,
		nil,
		observeClient,
		observeUpstream,
		onUpstreamVisible,
		fallbackCommit,
	)
	if preVisibleProgress.Result != nil {
		preserveClient = preVisibleProgress.preservesClient()
		return preVisibleProgress.Result
	}

	f.startClientToUpstreamRelay(
		ctx,
		sessionCtx,
		cancel,
		&wg,
		upstreamConn,
		clientConn,
		options,
		lifecycle,
		clientReads,
		observeClient,
		&errorOrder,
		&clientToUpstream,
	)

	wg.Go(func() {
		var initialUpstreamRead *webSocketInitialReadResult
		if initialUpstreamReadCh != nil {
			select {
			case read := <-initialUpstreamReadCh:
				initialUpstreamRead = &read
			case <-ctx.Done():
				initialUpstreamRead = &webSocketInitialReadResult{err: ctx.Err()}
			}
		}
		n, failurePeer, failureOperation, err := relayMessages(
			ctx,
			clientConn,
			webSocketPeerClient,
			webSocketPeerUpstream,
			initialUpstreamRead,
			options,
			requestcapture.MessageDirectionUpstreamToClient,
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
			upstreamConn.Read,
		)
		upstreamToClient = newWebSocketRelayResultForOperation(n, err, failurePeer, failureOperation, &errorOrder)
		if err != nil {
			cancel()
		}
	})

	wg.Wait()

	lifecycleSnapshot := lifecycle.Snapshot()
	if suppressedUpstreamError := firstSuppressedUpstreamError(clientToUpstream, upstreamToClient); suppressedUpstreamError != nil {
		if options.PreserveClientOnSuppress {
			preserveClient = true
			closeWebSocketForSemanticReplacement(upstreamConn)
		} else {
			closeWebSocketForSemanticReplacement(clientConn)
			closeWebSocketForSemanticReplacement(upstreamConn)
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
		closeWebSocketForSemanticReplacement(upstreamConn)
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
			ObservedCloseError:    outcome.observedCloseError,
			FailurePeer:           outcome.failurePeer,
			FailureOperation:      outcome.failureOperation,
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
		ObservedCloseError:    outcome.observedCloseError,
		FailurePeer:           outcome.failurePeer,
		FailureOperation:      outcome.failureOperation,
	}
}

// runPreVisibleSuppressionWindow owns both directions until the first upstream
// application frame establishes whether the attempt is visible or replaceable.
// Client frames remain independently readable and are forwarded in order, while
// delivery decisions stay serialized so replay snapshots have an exact boundary.
func (f *WebSocketForwarder) runPreVisibleSuppressionWindow(
	ctx, sessionCtx context.Context,
	clientConn, upstreamConn *websocket.Conn,
	options webSocketRelayOptions,
	lifecycle *webSocketLifecycleState,
	clientReads *webSocketClientReadHandoff,
	initialUpstreamReadCh <-chan webSocketInitialReadResult,
	observeClient, observeUpstream func(websocket.MessageType, []byte),
	onUpstreamVisible func(websocket.MessageType, []byte),
	fallbackCommit *webSocketCommitState,
) (<-chan webSocketInitialReadResult, webSocketPreVisibleRelayProgress) {
	progress := webSocketPreVisibleRelayProgress{}
	if !shouldRunPreVisibleSuppressionWindow(options) {
		return initialUpstreamReadCh, progress
	}

	initialUpstreamReadCh = startWebSocketInitialRead(ctx, upstreamConn)
	// Begin the session-owned downstream read before the provider-first grace
	// period. A ready client frame is retained by the handoff and cannot be lost
	// if this provider fails before the frame is delivered.
	clientReads.pendingRead(sessionCtx, clientConn)
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
		return initialUpstreamReadCh, progress
	}
	if lifecycle.Snapshot().ClientVisible {
		if progress.ConsumedInitialUpstream {
			initialUpstreamReadCh = nil
		}
		return initialUpstreamReadCh, progress
	}

	progress.merge(f.relayPreVisibleWindow(
		ctx,
		sessionCtx,
		clientConn,
		upstreamConn,
		options,
		lifecycle,
		clientReads,
		initialUpstreamReadCh,
		observeClient,
		observeUpstream,
		onUpstreamVisible,
		fallbackCommit,
	))
	if progress.ConsumedInitialUpstream {
		initialUpstreamReadCh = nil
	}
	return initialUpstreamReadCh, progress
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
		progress.Result = newSinglePeerRelaySessionResultForOperation(
			err,
			webSocketPeerClient,
			webSocketRelayFailureOperationRead,
			fallbackCommit,
			lifecycle,
			0,
			0,
		)
		return progress
	}
	captured := captureWebSocketMessageRead(
		options,
		requestcapture.MessageDirectionClientToUpstream,
		messageType,
		data,
		requestcapture.MessageSourceLive,
		requestcapture.MessageLineage{},
		requestcapture.MessageLineage{},
	)
	decision := replayableClientFrameDecision()
	if options.PreWriteToUpstream != nil {
		decision = options.PreWriteToUpstream(webSocketPreWriteContext{
			MessageType: messageType, Data: data,
			ClientAccepted: lifecycle.Snapshot().ClientAccepted,
			ClientVisible:  lifecycle.Snapshot().ClientVisible,
		})
		if decision.Action == webSocketPreWriteActionReject {
			disposition := decision.RejectionDisposition
			if disposition == "" {
				disposition = requestcapture.MessageDispositionProtocolRejected
			}
			captureWebSocketMessageResult(options, captured, disposition, false, decision.Err)
			progress.Result = newSinglePeerRelaySessionResultForOperation(
				decision.Err, webSocketPeerClient, webSocketRelayFailureOperationUnknown,
				fallbackCommit, lifecycle, 0, 0,
			)
			return progress
		}
	}
	disablePreVisibleReplayBufferIfNeeded(options)
	bufferedMessageIndex := invalidWebSocketReplayMessageIndex
	if options.PreVisibleReplayBuffer != nil && decision.ReplayEligible {
		bufferedMessageIndex = options.PreVisibleReplayBuffer.RecordDecision(
			messageType,
			data,
			false,
			captured.Lineage,
			decision,
		)
	}
	if observeClient != nil {
		observeClient(messageType, data)
	}
	if err := upstreamConn.Write(ctx, messageType, data); err != nil {
		writeErr := clientFrameWriteError(decision, err)
		captureWebSocketMessageResult(options, captured, requestcapture.MessageDispositionWriteFailed, false, writeErr)
		progress.Result = newSinglePeerRelaySessionResultForOperation(
			writeErr,
			webSocketPeerUpstream,
			webSocketRelayFailureOperationWrite,
			fallbackCommit,
			lifecycle,
			0,
			0,
		)
		return progress
	}
	progress.BytesClientToUpstream = int64(len(data))
	if !decision.ReplacementEligible && options.PreVisibleReplayBuffer != nil {
		options.PreVisibleReplayBuffer.Disable()
	}
	if decision.OnWriteConfirmed != nil {
		if err := decision.OnWriteConfirmed(); err != nil {
			captureWebSocketMessageResult(options, captured, requestcapture.MessageDispositionStorageRejected, true, err)
			progress.Result = newSinglePeerRelaySessionResultForOperation(
				err, webSocketPeerUnknown, webSocketRelayFailureOperationUnknown,
				fallbackCommit, lifecycle, progress.BytesClientToUpstream, 0,
			)
			return progress
		}
	}
	if options.PreVisibleReplayBuffer != nil {
		options.PreVisibleReplayBuffer.MarkDelivered(bufferedMessageIndex, captured.Lineage)
	}
	captureWebSocketMessageResult(options, captured, requestcapture.MessageDispositionForwarded, true, nil)
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
		progress.Result = newSinglePeerRelaySessionResultForOperation(
			err,
			webSocketPeerUpstream,
			webSocketRelayFailureOperationRead,
			fallbackCommit,
			lifecycle,
			bytesClientToUpstream,
			0,
		)
		return progress
	}
	captured := captureWebSocketMessageRead(
		options,
		requestcapture.MessageDirectionUpstreamToClient,
		upstreamMessageType,
		upstreamData,
		requestcapture.MessageSourceLive,
		requestcapture.MessageLineage{},
		requestcapture.MessageLineage{},
	)
	if observeUpstream != nil {
		observeUpstream(upstreamMessageType, upstreamData)
	}
	disablePreVisibleReplayBufferIfNeeded(options)
	observation := currentWebSocketObservation(options.Observer)
	lifecycleSnapshot := lifecycle.Snapshot()
	var boundaryConfirmed func() error
	if options.PreWriteToClient != nil {
		decision := options.PreWriteToClient(webSocketPreWriteContext{
			MessageType:    upstreamMessageType,
			Data:           upstreamData,
			Observation:    observation,
			ClientAccepted: lifecycleSnapshot.ClientAccepted,
			ClientVisible:  lifecycleSnapshot.ClientVisible,
		})
		if decision.Action == webSocketPreWriteActionSuppress {
			captureWebSocketMessageResult(options, captured, requestcapture.MessageDispositionSuppressed, false, nil)
			closeWebSocketForSemanticReplacement(upstreamConn)
			progress.Result = newSuppressedPreVisibleRelayResult(
				fallbackCommit,
				lifecycleSnapshot,
				bytesClientToUpstream,
				decision,
			)
			return progress
		}
		if decision.Action == webSocketPreWriteActionReject {
			disposition := decision.RejectionDisposition
			if disposition == "" {
				disposition = requestcapture.MessageDispositionProtocolRejected
			}
			captureWebSocketMessageResult(options, captured, disposition, false, decision.Err)
			progress.Result = newSinglePeerRelaySessionResultForOperation(
				decision.Err, webSocketPeerUpstream, webSocketRelayFailureOperationUnknown,
				fallbackCommit, lifecycle, bytesClientToUpstream, 0,
			)
			return progress
		}
		boundaryConfirmed = decision.OnWriteConfirmed
	}
	if err := clientConn.Write(ctx, upstreamMessageType, upstreamData); err != nil {
		captureWebSocketMessageResult(options, captured, requestcapture.MessageDispositionWriteFailed, false, err)
		progress.Result = newSinglePeerRelaySessionResultForOperation(
			err,
			webSocketPeerClient,
			webSocketRelayFailureOperationWrite,
			fallbackCommit,
			lifecycle,
			bytesClientToUpstream,
			0,
		)
		return progress
	}
	progress.BytesUpstreamToClient = int64(len(upstreamData))
	if boundaryConfirmed != nil {
		if err := boundaryConfirmed(); err != nil {
			captureWebSocketMessageResult(options, captured, requestcapture.MessageDispositionStorageRejected, true, err)
			progress.Result = newSinglePeerRelaySessionResultForOperation(
				err, webSocketPeerUnknown, webSocketRelayFailureOperationUnknown,
				fallbackCommit, lifecycle, bytesClientToUpstream, progress.BytesUpstreamToClient,
			)
			return progress
		}
	}
	onUpstreamVisible(upstreamMessageType, upstreamData)
	captureWebSocketMessageResult(options, captured, requestcapture.MessageDispositionForwarded, true, nil)
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
