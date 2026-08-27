package websocketproxy

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/startup"
	"github.com/doraemonkeys/switch-a/internal/codex/websocket"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/selector"

	"github.com/coder/websocket"
	"go.uber.org/zap/zaptest"
)

func testCodexOperation(t *testing.T, subprotocol bool) *codexws.Operation {
	t.Helper()
	runtime := codexws.New(codexws.Config{Features: codexws.FeatureSourceFunc(func() codexstartup.Snapshot {
		return codexstartup.Snapshot{WebSocketSubprotocol: subprotocol}
	})})
	operation, err := runtime.Begin(context.Background(), nil, APITypeCodex, "ws-boundary-test")
	if err != nil {
		t.Fatal(err)
	}
	return operation
}

func TestCodexWebSocketBoundaryAdapters(t *testing.T) {
	ctx := context.Background()
	disabled := testCodexOperation(t, false)
	orchestrator := &WebSocketSessionOrchestrator{
		handler:        &Gateway{logger: zaptest.NewLogger(t)},
		lifecycle:      newWebSocketLifecycleState(),
		codexOperation: disabled,
		requestID:      "boundary",
	}

	request := httptest.NewRequest(http.MethodGet, "http://gateway.test/responses", nil)
	request.Header.Set("Sec-WebSocket-Protocol", "bad protocol")
	if result := orchestrator.initializeSubprotocol(request); result != nil {
		t.Fatalf("disabled subprotocol gate rejected legacy request: %#v", result)
	}
	if len(orchestrator.subprotocol.DialOffer()) != 0 {
		t.Fatalf("disabled subprotocol dial offer = %#v", orchestrator.subprotocol.DialOffer())
	}

	clientGate := orchestrator.codexClientPreWrite(ctx)
	if clientGate == nil {
		t.Fatal("codex client gate was not installed")
	}
	clientDecision := clientGate(webSocketPreWriteContext{MessageType: websocket.MessageBinary, Data: []byte{0, 1, 2}})
	if clientDecision.Action != webSocketPreWriteActionForward || clientDecision.OnWriteConfirmed != nil {
		t.Fatalf("client decision = %#v", clientDecision)
	}

	semanticCalls := 0
	serverGate := orchestrator.composeUpstreamPreWrite(ctx, func(webSocketPreWriteContext) webSocketPreWriteDecision {
		semanticCalls++
		return webSocketPreWriteDecision{Action: webSocketPreWriteActionSuppress}
	})
	if decision := serverGate(webSocketPreWriteContext{}); decision.Action != webSocketPreWriteActionSuppress || semanticCalls != 1 {
		t.Fatalf("semantic rejection decision=%#v calls=%d", decision, semanticCalls)
	}
	serverGate = orchestrator.composeUpstreamPreWrite(ctx, nil)
	if decision := serverGate(webSocketPreWriteContext{MessageType: websocket.MessageBinary}); decision.Action != webSocketPreWriteActionForward {
		t.Fatalf("server decision = %#v", decision)
	}

	plainSemantic := func(webSocketPreWriteContext) webSocketPreWriteDecision {
		return webSocketPreWriteDecision{Action: webSocketPreWriteActionForward}
	}
	if got := (*WebSocketSessionOrchestrator)(nil).composeUpstreamPreWrite(ctx, plainSemantic); got == nil {
		t.Fatal("nil orchestrator did not preserve semantic gate")
	}
	if (*WebSocketSessionOrchestrator)(nil).codexClientPreWrite(ctx) != nil {
		t.Fatal("nil orchestrator installed a client gate")
	}

	for _, test := range []struct {
		class       codexws.FailureClass
		disposition requestcapture.MessageDisposition
		status      websocket.StatusCode
	}{
		{codexws.FailureProtocol, requestcapture.MessageDispositionProtocolRejected, websocket.StatusPolicyViolation},
		{codexws.FailureIdentity, requestcapture.MessageDispositionIdentityRejected, websocket.StatusPolicyViolation},
		{codexws.FailureStorage, requestcapture.MessageDispositionStorageRejected, websocket.StatusInternalError},
	} {
		failure := &codexws.Failure{Class: test.class, Stage: "test", Cause: errors.New("rejected")}
		decision := codexRejectedWrite(failure)
		if decision.Action != webSocketPreWriteActionReject || decision.RejectionDisposition != test.disposition {
			t.Fatalf("class %s decision = %#v", test.class, decision)
		}
		var closeError websocket.CloseError
		if !errors.As(decision.Err, &closeError) || closeError.Code != test.status {
			t.Fatalf("class %s close error = %#v (%v)", test.class, closeError, decision.Err)
		}
		if websocketCloseStatusForCodexFailure(failure) != test.status {
			t.Fatalf("class %s close status mismatch", test.class)
		}
	}

	closeError := newWebSocketCodexCloseError(nil)
	if closeError.Error() == "" || !errors.Is(closeError, closeError) {
		t.Fatal("nil-cause close error is unstable")
	}
	var concrete *webSocketCodexCloseError
	if !errors.As(closeError, &concrete) || concrete.As(new(error)) {
		t.Fatal("close error As contract is unstable")
	}
}

func TestForwardObservedRejectsBeforeAndAfterUpstreamDial(t *testing.T) {
	forwarder := NewWebSocketForwarder(WebSocketForwarderConfig{})

	malformed := httptest.NewRequest(http.MethodGet, "http://gateway.test/responses", nil)
	malformed.Header.Set("Sec-WebSocket-Protocol", "bad protocol")
	result, err := forwarder.ForwardObserved(
		context.Background(), httptest.NewRecorder(), malformed, "ws://unused.test", nil, nil, nil, nil,
	)
	if err != nil || result == nil || result.HandshakeStatusCode != http.StatusBadRequest {
		t.Fatalf("malformed offer result=%#v err=%v", result, err)
	}

	noUpstream, err := forwarder.ForwardObserved(
		context.Background(),
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "http://gateway.test/responses", nil),
		"://invalid",
		nil,
		nil,
		nil,
		nil,
	)
	if err != nil || noUpstream == nil || noUpstream.Err == nil {
		t.Fatalf("invalid upstream result=%#v err=%v", noUpstream, err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		connection, acceptErr := websocket.Accept(w, request, nil)
		if acceptErr != nil {
			t.Errorf("accept upstream: %v", acceptErr)
			return
		}
		defer connection.CloseNow()
		_, _, _ = connection.Read(request.Context())
	}))
	defer upstream.Close()

	notUpgrade := httptest.NewRequest(http.MethodGet, "http://gateway.test/responses", nil)
	result, err = forwarder.ForwardObserved(
		context.Background(), httptest.NewRecorder(), notUpgrade, wsURL(upstream), nil, nil, nil, nil,
	)
	if err == nil || result == nil || result.TerminalCause != model.TerminalClientUpgradeRejected {
		t.Fatalf("downstream rejection result=%#v err=%v", result, err)
	}
}

func newCodexRelayPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	accepted := make(chan *websocket.Conn, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("accept relay pair: %v", err)
			return
		}
		accepted <- connection
		<-release
		_ = connection.CloseNow()
	}))
	peer, _, err := websocket.Dial(context.Background(), wsURL(server), nil)
	if err != nil {
		server.Close()
		t.Fatalf("dial relay pair: %v", err)
	}
	local := <-accepted
	t.Cleanup(func() {
		close(release)
		_ = peer.CloseNow()
		server.Close()
	})
	return local, peer
}

func TestPreVisibleCodexBoundaryCommitsAfterPhysicalWrite(t *testing.T) {
	forwarder := &WebSocketForwarder{}
	lifecycle := newWebSocketLifecycleState()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	upstream, upstreamPeer := newCodexRelayPair(t)
	commitErr := errors.New("client commit")
	clientOptions := (webSocketRelayOptions{
		PreVisibleReplayBuffer: newPreVisibleClientMessageBuffer(preVisibleClientReplayBufferLimitBytes),
		PreWriteToUpstream: func(webSocketPreWriteContext) webSocketPreWriteDecision {
			return webSocketPreWriteDecision{
				Action:           webSocketPreWriteActionForward,
				OnWriteConfirmed: func() error { return commitErr },
			}
		},
	}).withCaptureHooks()
	clientPayload := []byte("client-byte-identical")
	clientProgress := forwarder.relayPreVisibleClientMessage(
		ctx,
		upstream,
		clientOptions,
		lifecycle,
		webSocketInitialReadResult{messageType: websocket.MessageBinary, data: clientPayload},
		nil,
		nil,
	)
	if clientProgress.Result == nil || !errors.Is(clientProgress.Result.Err, commitErr) ||
		clientProgress.BytesClientToUpstream != int64(len(clientPayload)) {
		t.Fatalf("client commit progress = %#v", clientProgress)
	}
	messageType, payload, err := upstreamPeer.Read(ctx)
	if err != nil || messageType != websocket.MessageBinary || !bytes.Equal(payload, clientPayload) {
		t.Fatalf("physically written client frame type=%d payload=%q err=%v", messageType, payload, err)
	}

	downstream, downstreamPeer := newCodexRelayPair(t)
	upstreamCommitErr := errors.New("upstream commit")
	visibleCalled := false
	upstreamOptions := (webSocketRelayOptions{
		PreVisibleReplayBuffer: newPreVisibleClientMessageBuffer(preVisibleClientReplayBufferLimitBytes),
		PreWriteToClient: func(webSocketPreWriteContext) webSocketPreWriteDecision {
			return webSocketPreWriteDecision{
				Action:           webSocketPreWriteActionForward,
				OnWriteConfirmed: func() error { return upstreamCommitErr },
			}
		},
	}).withCaptureHooks()
	serverPayload := []byte("server-byte-identical")
	upstreamProgress := forwarder.relayPreVisibleUpstreamMessage(
		ctx,
		downstream,
		upstream,
		upstreamOptions,
		lifecycle,
		webSocketInitialReadResult{messageType: websocket.MessageText, data: serverPayload},
		nil,
		func(websocket.MessageType, []byte) { visibleCalled = true },
		nil,
		9,
	)
	if visibleCalled || upstreamProgress.Result == nil || !errors.Is(upstreamProgress.Result.Err, upstreamCommitErr) ||
		upstreamProgress.BytesUpstreamToClient != int64(len(serverPayload)) {
		t.Fatalf("upstream commit progress = %#v visible=%v", upstreamProgress, visibleCalled)
	}
	messageType, payload, err = downstreamPeer.Read(ctx)
	if err != nil || messageType != websocket.MessageText || !bytes.Equal(payload, serverPayload) {
		t.Fatalf("physically written server frame type=%d payload=%q err=%v", messageType, payload, err)
	}

	successClientPayload := []byte("client-success")
	successClient := forwarder.relayPreVisibleClientMessage(
		ctx,
		upstream,
		(webSocketRelayOptions{
			PreVisibleReplayBuffer: newPreVisibleClientMessageBuffer(preVisibleClientReplayBufferLimitBytes),
		}).withCaptureHooks(),
		lifecycle,
		webSocketInitialReadResult{messageType: websocket.MessageText, data: successClientPayload},
		nil,
		nil,
	)
	if successClient.Result != nil || successClient.BytesClientToUpstream != int64(len(successClientPayload)) {
		t.Fatalf("successful client progress = %#v", successClient)
	}
	if _, payload, err = upstreamPeer.Read(ctx); err != nil || !bytes.Equal(payload, successClientPayload) {
		t.Fatalf("successful client frame payload=%q err=%v", payload, err)
	}

	successVisible := false
	successServerPayload := []byte("server-success")
	successUpstream := forwarder.relayPreVisibleUpstreamMessage(
		ctx,
		downstream,
		upstream,
		(webSocketRelayOptions{}).withCaptureHooks(),
		lifecycle,
		webSocketInitialReadResult{messageType: websocket.MessageBinary, data: successServerPayload},
		nil,
		func(websocket.MessageType, []byte) { successVisible = true },
		nil,
		0,
	)
	if successUpstream.Result != nil || !successVisible ||
		successUpstream.BytesUpstreamToClient != int64(len(successServerPayload)) {
		t.Fatalf("successful upstream progress = %#v visible=%v", successUpstream, successVisible)
	}
	if _, payload, err = downstreamPeer.Read(ctx); err != nil || !bytes.Equal(payload, successServerPayload) {
		t.Fatalf("successful server frame payload=%q err=%v", payload, err)
	}
}

func TestPreVisibleCodexWindowsHonorDemandAndCancellation(t *testing.T) {
	forwarder := &WebSocketForwarder{}
	lifecycle := newWebSocketLifecycleState()
	options := (webSocketRelayOptions{
		PreVisibleReplayBuffer: newPreVisibleClientMessageBuffer(preVisibleClientReplayBufferLimitBytes),
	}).withCaptureHooks()
	commit := newWebSocketCommitState()

	if progress := forwarder.relayImmediatePreVisibleUpstreamWindow(
		context.Background(), options, nil, nil, nil, nil, nil, nil, commit,
	); progress.Result != nil {
		t.Fatalf("nil lifecycle progress = %#v", progress)
	}
	if progress := forwarder.relayImmediatePreVisibleUpstreamWindow(
		context.Background(), webSocketRelayOptions{}, lifecycle, nil, nil, nil, nil, nil, commit,
	); progress.Result != nil {
		t.Fatalf("disabled immediate window progress = %#v", progress)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if progress := forwarder.relayImmediatePreVisibleUpstreamWindow(
		cancelled, options, lifecycle, make(chan webSocketInitialReadResult), nil, nil, nil, nil, commit,
	); progress.Result == nil || !errors.Is(progress.Result.Err, context.Canceled) {
		t.Fatalf("cancelled immediate window = %#v", progress)
	}

	upstreamReads := make(chan webSocketInitialReadResult, 1)
	upstreamReads <- webSocketInitialReadResult{err: errors.New("upstream bootstrap")}
	if progress := forwarder.relayImmediatePreVisibleUpstreamWindow(
		context.Background(), options, lifecycle, upstreamReads, nil, nil, nil, nil, commit,
	); !progress.ConsumedInitialUpstream || progress.Result == nil {
		t.Fatalf("upstream bootstrap progress = %#v", progress)
	}

	if progress := forwarder.relayPreVisibleWindow(
		context.Background(), nil, nil, options, nil, nil, nil, nil, nil, nil, commit,
	); progress.Result != nil {
		t.Fatalf("nil pre-visible lifecycle = %#v", progress)
	}
	if progress := forwarder.relayPreVisibleWindow(
		context.Background(), nil, nil, webSocketRelayOptions{}, lifecycle, nil, nil, nil, nil, nil, commit,
	); progress.Result != nil {
		t.Fatalf("disabled pre-visible window = %#v", progress)
	}
	if progress := forwarder.relayPreVisibleWindow(
		cancelled, nil, nil, options, lifecycle, make(chan webSocketInitialReadResult), nil, nil, nil, nil, commit,
	); progress.Result == nil || !errors.Is(progress.Result.Err, context.Canceled) {
		t.Fatalf("cancelled pre-visible window = %#v", progress)
	}

	clientReads := make(chan webSocketInitialReadResult, 1)
	clientReads <- webSocketInitialReadResult{err: errors.New("client bootstrap")}
	if progress := forwarder.relayPreVisibleWindow(
		context.Background(), nil, nil, options, lifecycle, clientReads, nil, nil, nil, nil, commit,
	); !progress.ConsumedInitialClient || progress.Result == nil {
		t.Fatalf("client bootstrap progress = %#v", progress)
	}

	upstream, upstreamPeer := newCodexRelayPair(t)
	successfulClientReads := make(chan webSocketInitialReadResult, 1)
	successfulClientReads <- webSocketInitialReadResult{
		messageType: websocket.MessageBinary,
		data:        []byte("bootstrap-client"),
	}
	failedUpstreamReads := make(chan webSocketInitialReadResult, 1)
	failedUpstreamReads <- webSocketInitialReadResult{err: errors.New("bootstrap upstream failed")}
	progress := forwarder.relayPreVisibleWindow(
		context.Background(),
		nil,
		upstream,
		options,
		lifecycle,
		successfulClientReads,
		failedUpstreamReads,
		nil,
		nil,
		nil,
		commit,
	)
	if !progress.ConsumedInitialClient || !progress.ConsumedInitialUpstream || progress.Result == nil {
		t.Fatalf("combined bootstrap progress = %#v", progress)
	}
	ctx, cancelRead := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelRead()
	if _, payload, err := upstreamPeer.Read(ctx); err != nil || string(payload) != "bootstrap-client" {
		t.Fatalf("combined bootstrap payload=%q err=%v", payload, err)
	}
}

func TestPreVisibleCodexWriteFailureAndSuppression(t *testing.T) {
	forwarder := &WebSocketForwarder{}
	lifecycle := newWebSocketLifecycleState()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	brokenUpstream, _ := newCodexRelayPair(t)
	_ = brokenUpstream.CloseNow()
	clientFailure := forwarder.relayPreVisibleClientMessage(
		ctx,
		brokenUpstream,
		(webSocketRelayOptions{}).withCaptureHooks(),
		lifecycle,
		webSocketInitialReadResult{messageType: websocket.MessageText, data: []byte("write")},
		nil,
		nil,
	)
	if clientFailure.Result == nil || clientFailure.Result.Err == nil {
		t.Fatalf("client write failure = %#v", clientFailure)
	}

	brokenDownstream, _ := newCodexRelayPair(t)
	_ = brokenDownstream.CloseNow()
	upstreamFailure := forwarder.relayPreVisibleUpstreamMessage(
		ctx,
		brokenDownstream,
		nil,
		(webSocketRelayOptions{}).withCaptureHooks(),
		lifecycle,
		webSocketInitialReadResult{messageType: websocket.MessageText, data: []byte("write")},
		nil,
		func(websocket.MessageType, []byte) { t.Fatal("failed write became visible") },
		nil,
		0,
	)
	if upstreamFailure.Result == nil || upstreamFailure.Result.Err == nil {
		t.Fatalf("upstream write failure = %#v", upstreamFailure)
	}

	replaceableUpstream, _ := newCodexRelayPair(t)
	suppressed := forwarder.relayPreVisibleUpstreamMessage(
		ctx,
		nil,
		replaceableUpstream,
		(webSocketRelayOptions{
			PreWriteToClient: func(webSocketPreWriteContext) webSocketPreWriteDecision {
				return webSocketPreWriteDecision{
					Action:                  webSocketPreWriteActionSuppress,
					SuppressedUpstreamError: &WebSocketUpstreamError{Raw: "provider error"},
					SuppressedMessageType:   websocket.MessageText,
					SuppressedMessageData:   []byte("provider error"),
				}
			},
		}).withCaptureHooks(),
		lifecycle,
		webSocketInitialReadResult{messageType: websocket.MessageText, data: []byte("provider error")},
		nil,
		func(websocket.MessageType, []byte) { t.Fatal("suppressed frame became visible") },
		newWebSocketCommitState(),
		0,
	)
	if suppressed.Result == nil || suppressed.Result.Disposition != webSocketRelayDispositionSuppressedUpstreamError {
		t.Fatalf("suppressed progress = %#v", suppressed)
	}

	downstream, _ := newCodexRelayPair(t)
	failedUpstream, _ := newCodexRelayPair(t)
	_ = failedUpstream.CloseNow()
	session := forwarder.relay(ctx, downstream, failedUpstream, webSocketRelayOptions{
		Lifecycle:                         newWebSocketLifecycleState(),
		PreserveClientOnPreVisibleFailure: true,
		SkipPreVisibleWindow:              true,
	})
	if session == nil || session.ClientVisible || session.TerminalCause != model.TerminalUpstreamTransportError {
		t.Fatalf("preserved pre-visible session = %#v", session)
	}
}

func TestPreVisibleCodexGateRejectionsAreTypedAndDoNotWrite(t *testing.T) {
	forwarder := &WebSocketForwarder{}
	lifecycle := newWebSocketLifecycleState()
	readErr := errors.New("read failed")

	clientReadFailure := forwarder.relayPreVisibleClientMessage(
		context.Background(),
		nil,
		webSocketRelayOptions{},
		lifecycle,
		webSocketInitialReadResult{err: readErr},
		nil,
		nil,
	)
	if clientReadFailure.Result == nil || !errors.Is(clientReadFailure.Result.Err, readErr) {
		t.Fatalf("client read failure = %#v", clientReadFailure)
	}

	clientObserved := false
	clientRejected := errors.New("client boundary")
	clientOptions := (webSocketRelayOptions{
		PreVisibleReplayBuffer: newPreVisibleClientMessageBuffer(preVisibleClientReplayBufferLimitBytes),
		PreWriteToUpstream: func(webSocketPreWriteContext) webSocketPreWriteDecision {
			return webSocketPreWriteDecision{
				Action: webSocketPreWriteActionReject,
				Err:    clientRejected,
			}
		},
	}).withCaptureHooks()
	clientRejection := forwarder.relayPreVisibleClientMessage(
		context.Background(),
		nil,
		clientOptions,
		lifecycle,
		webSocketInitialReadResult{messageType: websocket.MessageText, data: []byte(`{"type":"response.create"}`)},
		func(websocket.MessageType, []byte) { clientObserved = true },
		nil,
	)
	if !clientObserved || clientRejection.Result == nil || !errors.Is(clientRejection.Result.Err, clientRejected) {
		t.Fatalf("client rejection = %#v observed=%v", clientRejection, clientObserved)
	}

	upstreamReadFailure := forwarder.relayPreVisibleUpstreamMessage(
		context.Background(),
		nil,
		nil,
		webSocketRelayOptions{},
		lifecycle,
		webSocketInitialReadResult{err: readErr},
		nil,
		nil,
		nil,
		7,
	)
	if upstreamReadFailure.Result == nil || !errors.Is(upstreamReadFailure.Result.Err, readErr) {
		t.Fatalf("upstream read failure = %#v", upstreamReadFailure)
	}

	upstreamObserved := false
	upstreamRejected := errors.New("upstream boundary")
	upstreamRejection := forwarder.relayPreVisibleUpstreamMessage(
		context.Background(),
		nil,
		nil,
		(webSocketRelayOptions{
			PreVisibleReplayBuffer: newPreVisibleClientMessageBuffer(preVisibleClientReplayBufferLimitBytes),
			PreWriteToClient: func(webSocketPreWriteContext) webSocketPreWriteDecision {
				return webSocketPreWriteDecision{
					Action:               webSocketPreWriteActionReject,
					Err:                  upstreamRejected,
					RejectionDisposition: requestcapture.MessageDispositionIdentityRejected,
				}
			},
		}).withCaptureHooks(),
		lifecycle,
		webSocketInitialReadResult{messageType: websocket.MessageText, data: []byte(`{"type":"response.created"}`)},
		func(websocket.MessageType, []byte) { upstreamObserved = true },
		func(websocket.MessageType, []byte) { t.Fatal("rejected frame became visible") },
		nil,
		11,
	)
	if !upstreamObserved || upstreamRejection.Result == nil || !errors.Is(upstreamRejection.Result.Err, upstreamRejected) {
		t.Fatalf("upstream rejection = %#v observed=%v", upstreamRejection, upstreamObserved)
	}
}

func TestGatewayCodexSubprotocolDisabledPreservesLegacyWireAndStripsHandle(t *testing.T) {
	clientPayload := []byte{0, 1, 2, 3, 0xff}
	serverPayload := []byte{0xfe, 4, 3, 2, 1}
	upstreamOffer := make(chan string, 1)
	upstreamCookie := make(chan string, 1)
	upstreamPayload := make(chan []byte, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		upstreamOffer <- request.Header.Get("Sec-WebSocket-Protocol")
		upstreamCookie <- request.Header.Get("Cookie")
		connection, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("accept upstream: %v", err)
			return
		}
		defer connection.CloseNow()
		messageType, payload, err := connection.Read(request.Context())
		if err != nil {
			t.Errorf("read upstream: %v", err)
			return
		}
		if messageType != websocket.MessageBinary {
			t.Errorf("upstream message type = %d", messageType)
		}
		upstreamPayload <- append([]byte(nil), payload...)
		if err := connection.Write(request.Context(), websocket.MessageBinary, serverPayload); err != nil {
			t.Errorf("write upstream: %v", err)
		}
	}))
	defer upstream.Close()

	store := newMockStore()
	store.providers = []model.Provider{{
		ID:      "provider",
		Enabled: true,
		APITypes: []model.ProviderAPIType{{
			ProviderID: "provider",
			APIType:    APITypeCodex,
			BaseURL:    upstream.URL,
		}},
		CredentialSessions: testCredentialSessions("provider", APITypeCodex, credentialsession.KindAPIKey, "secret"),
	}}
	runtime := codexws.New(codexws.Config{Features: codexws.FeatureSourceFunc(func() codexstartup.Snapshot {
		return codexstartup.Snapshot{}
	})})
	gateway := NewGateway(Config{Store: store, Logger: zaptest.NewLogger(t), Codex: runtime})
	server := newGatewayIntegrationServer(gateway, RequestConfig{GlobalAuthMode: "bearer", GlobalMaxAttempts: 1}, "disabled-wire")
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, wsURL(server)+"/responses?model=gpt-5", &websocket.DialOptions{
		Subprotocols: []string{"realtime.v2", "realtime.v1"},
		HTTPHeader: http.Header{
			"Cookie": {providercookie.GatewayHandleName + "=must-not-leak; ordinary=kept"},
		},
	})
	if err != nil {
		t.Fatalf("dial gateway: %v", err)
	}
	defer connection.CloseNow()
	if got := connection.Subprotocol(); got != "" {
		t.Fatalf("disabled downstream subprotocol = %q", got)
	}
	if err := connection.Write(ctx, websocket.MessageBinary, clientPayload); err != nil {
		t.Fatalf("write client frame: %v", err)
	}
	messageType, payload, err := connection.Read(ctx)
	if err != nil {
		t.Fatalf("read server frame: %v", err)
	}
	if messageType != websocket.MessageBinary || !bytes.Equal(payload, serverPayload) {
		t.Fatalf("server frame type=%d payload=%v", messageType, payload)
	}
	if got := <-upstreamOffer; got != "" {
		t.Fatalf("disabled upstream subprotocol offer = %q", got)
	}
	if got := <-upstreamCookie; got != "ordinary=kept" {
		t.Fatalf("upstream cookie = %q", got)
	}
	if got := <-upstreamPayload; !bytes.Equal(got, clientPayload) {
		t.Fatalf("upstream payload = %v", got)
	}
}

func TestCodexWebSocketFailureAndAttemptAdapters(t *testing.T) {
	handler := &Gateway{logger: zaptest.NewLogger(t)}
	for _, test := range []struct {
		class  codexws.FailureClass
		status int
	}{
		{codexws.FailureProtocol, http.StatusBadRequest},
		{codexws.FailureStorage, http.StatusServiceUnavailable},
	} {
		recorder := httptest.NewRecorder()
		handler.writeCodexWebSocketFailure(recorder, &codexws.Failure{Class: test.class, Stage: "test", Cause: errors.New("failed")})
		if recorder.Code != test.status {
			t.Fatalf("class %s status=%d want=%d", test.class, recorder.Code, test.status)
		}
	}

	applyCodexWebSocketRouteConstraint(nil, nil)
	selectRequest := &model.SelectRequest{}
	applyCodexWebSocketRouteConstraint(selectRequest, testCodexOperation(t, false))
	if selectRequest.RequiredAuthority != nil || selectRequest.PreferredRouteTargetID != "" {
		t.Fatalf("unexpected disabled route constraint: %#v", selectRequest)
	}

	orchestrator := &WebSocketSessionOrchestrator{
		handler:        handler,
		lifecycle:      newWebSocketLifecycleState(),
		codexOperation: testCodexOperation(t, false),
		requestID:      "attempt",
	}
	if err := orchestrator.prepareCodexPhysicalDial(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	prepared := webSocketPreparedProviderAttempt{}
	if err := orchestrator.finishCodexPhysicalDial(context.Background(), prepared, DialExchange{}); err != nil {
		t.Fatal(err)
	}
	if err := (*WebSocketSessionOrchestrator)(nil).prepareCodexPhysicalDial(context.Background(), &prepared); err != nil {
		t.Fatal(err)
	}
	if err := (*WebSocketSessionOrchestrator)(nil).finishCodexPhysicalDial(context.Background(), prepared, DialExchange{}); err != nil {
		t.Fatal(err)
	}

	provider := &model.Provider{
		ID:                 "provider",
		CredentialSessions: testCredentialSessions("provider", APITypeCodex, credentialsession.KindAPIKey, "secret"),
	}
	prepared = testPreparedProviderAttempt(t, provider, APITypeCodex, "https://api.example.test/v1/responses")
	subject, err := codexidentity.CredentialSubjectFromSession(prepared.credential.Subject)
	if err != nil {
		t.Fatal(err)
	}
	prepared.applied, err = codexidentity.AppliedIdentityFromRequest("openai", prepared.finalURL, subject)
	if err != nil {
		t.Fatal(err)
	}
	prepared.headers = http.Header{"Cookie": {providercookie.GatewayHandleName + "=private; ordinary=kept"}}
	if err := orchestrator.prepareCodexPhysicalDial(context.Background(), &prepared); err != nil {
		t.Fatalf("prepare physical dial: %v", err)
	}
	if prepared.boundaryPermit == nil || prepared.headers.Get("Cookie") != "ordinary=kept" {
		t.Fatalf("prepared boundary=%#v headers=%v", prepared.boundaryPermit, prepared.headers)
	}
	if err := orchestrator.finishCodexPhysicalDial(context.Background(), prepared, DialExchange{}); err != nil {
		t.Fatalf("finish physical dial: %v", err)
	}

	invalid := prepared
	invalid.applied = codexidentity.AppliedIdentity{}
	if err := orchestrator.prepareCodexPhysicalDial(context.Background(), &invalid); codexws.Classify(err) != codexws.FailureIdentity {
		t.Fatalf("invalid applied identity error = %v", err)
	}

	replayErr := errors.New("replay failed")
	replayAttempt, replayOutcome := orchestrator.newReplayFailureAttempt(
		context.Background(),
		provider,
		DialExchange{captureMode: captureModeTransition},
		3,
		providerSwitchModeInitial,
		selector.SelectionMetadata{},
		time.Now(),
		true,
		"redacted",
		17,
		replayErr,
	)
	if replayAttempt.Result == nil || !errors.Is(replayAttempt.Result.Err, replayErr) ||
		!replayAttempt.ReplayFailed || !replayAttempt.RecoveryAttempted || replayOutcome == nil {
		t.Fatalf("replay failure attempt=%#v outcome=%#v", replayAttempt, replayOutcome)
	}

	provider = &model.Provider{ID: "provider"}
	boundaryErr := &codexws.Failure{Class: codexws.FailureStorage, Stage: "dial", Cause: errors.New("storage")}
	attempt := orchestrator.newCodexBoundaryAttempt(
		provider, DialExchange{}, 1, providerSwitchModeInitial, selector.SelectionMetadata{}, time.Now(), boundaryErr, "redacted",
	)
	if attempt.Result == nil || attempt.Result.Err != boundaryErr || attempt.injectedCredential != "redacted" {
		t.Fatalf("boundary attempt = %#v", attempt)
	}

	protocolErr := errors.New("subprotocol")
	attempt = orchestrator.newSubprotocolViolationAttempt(
		provider, DialExchange{}, 2, providerSwitchModeInitial, selector.SelectionMetadata{}, time.Now(), protocolErr, "redacted",
	)
	if attempt.Result == nil || attempt.Result.Err != protocolErr || attempt.injectedCredential != "redacted" {
		t.Fatalf("subprotocol attempt = %#v", attempt)
	}

	session := orchestrator.bootstrapProbeFailure(errors.New("bootstrap"), model.TerminalClientDisconnect)
	if session.FinalErr == nil || session.ProbeOutcome != webSocketSelectionProbeOutcomeTransportFailed {
		t.Fatalf("bootstrap failure session = %#v", session)
	}
}

func TestCodexOrchestratorPinsRouteOnlyAtClientVisibility(t *testing.T) {
	operation := testCodexOperation(t, false)
	selectRequest := &model.SelectRequest{APIType: APITypeCodex}
	visibleCallbacks := 0
	orchestrator := newWebSocketSessionOrchestrator(&Gateway{logger: zaptest.NewLogger(t)}, webSocketSessionOrchestratorConfig{
		apiType: APITypeCodex, selectReq: selectRequest, codexOperation: operation,
		onClientVisible: func(webSocketVisibleWriteContext) { visibleCallbacks++ },
	})

	prepare := func(id, target string) webSocketPreparedProviderAttempt {
		t.Helper()
		provider := &model.Provider{
			ID: id, CredentialSessions: testCredentialSessions(id, APITypeCodex, credentialsession.KindAPIKey, "secret"),
		}
		prepared := testPreparedProviderAttempt(t, provider, APITypeCodex, target)
		subject, err := codexidentity.CredentialSubjectFromSession(prepared.credential.Subject)
		if err != nil {
			t.Fatal(err)
		}
		prepared.applied, err = codexidentity.AppliedIdentityFromRequest("openai", prepared.finalURL, subject)
		if err != nil {
			t.Fatal(err)
		}
		return prepared
	}

	first := prepare("first", "https://first.example.test/v1/responses")
	second := prepare("second", "https://second.example.test/v1/responses")
	if err := orchestrator.prepareCodexPhysicalDial(context.Background(), &first); err != nil {
		t.Fatal(err)
	}
	if selectRequest.RequiredAuthority != nil || selectRequest.PreferredRouteTargetID != "" {
		t.Fatalf("first physical attempt constrained selection: %#v", selectRequest)
	}
	if err := orchestrator.prepareCodexPhysicalDial(context.Background(), &second); err != nil {
		t.Fatal("owner-free cross-authority replacement rejected:", err)
	}
	if selectRequest.RequiredAuthority != nil || selectRequest.PreferredRouteTargetID != "" {
		t.Fatalf("replacement physical attempt constrained selection: %#v", selectRequest)
	}

	orchestrator.onClientVisible(webSocketVisibleWriteContext{})
	applyCodexWebSocketRouteConstraint(selectRequest, operation)
	if visibleCallbacks != 1 || selectRequest.RequiredAuthority == nil ||
		!selectRequest.RequiredAuthority.Equal(second.candidate.Authority()) ||
		selectRequest.PreferredRouteTargetID != second.candidate.RouteTargetID() {
		t.Fatalf("visible route pin callbacks=%d request=%#v", visibleCallbacks, selectRequest)
	}
	if err := orchestrator.prepareCodexPhysicalDial(context.Background(), &first); codexws.Classify(err) != codexws.FailureIdentity {
		t.Fatalf("post-visible route replacement class=%q err=%v", codexws.Classify(err), err)
	}
}
