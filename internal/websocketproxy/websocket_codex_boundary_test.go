package websocketproxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/http"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/keyring"
	"github.com/doraemonkeys/switch-a/internal/codex/recovery"
	"github.com/doraemonkeys/switch-a/internal/codex/websocket"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/selector"
	"github.com/doraemonkeys/switch-a/internal/store"

	"github.com/coder/websocket"
	"go.uber.org/zap/zaptest"
)

func testCodexRuntime(t *testing.T) *codexws.Runtime {
	t.Helper()
	document, err := codexkeyring.GenerateDocument(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := codexkeyring.Parse(document, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	persistence, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "codex.db"), internal.RealClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = persistence.Close() })
	repositories, err := persistence.OpenCodexRepositories(context.Background(), keyring)
	if err != nil {
		t.Fatal(err)
	}
	digesterValue, err := codexidentity.NewDigester(keyring)
	if err != nil {
		t.Fatal(err)
	}
	limits := codexcontinuity.Limits{
		PendingTTL: 24 * time.Hour, CommittedIdleTTL: 30 * 24 * time.Hour,
		TombstoneTTL: 7 * 24 * time.Hour, MaxBindings: 100,
	}
	policy, err := codexcontinuity.NewPolicy(map[codexcontinuity.Kind]codexcontinuity.Limits{
		codexcontinuity.KindThreadID: limits, codexcontinuity.KindSessionID: limits,
		codexcontinuity.KindConversationID: limits, codexcontinuity.KindWindowID: limits,
		codexcontinuity.KindTurnState: limits, codexcontinuity.KindTurnMetadata: limits,
		codexcontinuity.KindResponseReference: limits,
	})
	if err != nil {
		t.Fatal(err)
	}
	continuity, err := codexcontinuity.NewService(codexcontinuity.Config{
		Store: repositories.Continuity, Digester: &digesterValue, Policy: policy, Clock: internal.RealClock{},
	})
	if err != nil {
		t.Fatal(err)
	}
	cookies, err := providercookie.NewService(providercookie.ServiceConfig{
		Repository: repositories.ProviderCookies, HandleDigester: keyring, Random: rand.Reader,
		Clock: internal.RealClock{}, HostCanonicalizer: providercookie.HostCanonicalizerFunc(codexidentity.CanonicalizeCookieHost),
		PublicSuffixList: codexidentity.PublicSuffixList{}, Policy: providercookie.DefaultPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := codexws.New(codexws.Config{
		ClientScopes: &digesterValue, Continuity: continuity, ProviderCookies: cookies,
		ExternalScheme: codexhttp.NewTrustedProxySchemeResolver(nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func testCodexOperation(t *testing.T) *codexws.Operation {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "http://gateway.test/responses", nil)
	request.Header.Set("Authorization", "Bearer client")
	operation, err := testCodexRuntime(t).Begin(context.Background(), request, APITypeCodex, "ws-boundary-test")
	if err != nil {
		t.Fatal(err)
	}
	return operation
}

func TestCodexWebSocketBoundaryAdapters(t *testing.T) {
	ctx := context.Background()
	operation := testCodexOperation(t)
	orchestrator := &WebSocketSessionOrchestrator{
		handler:        &Gateway{logger: zaptest.NewLogger(t)},
		lifecycle:      newWebSocketLifecycleState(),
		codexOperation: operation,
		requestID:      "boundary",
	}

	request := httptest.NewRequest(http.MethodGet, "http://gateway.test/responses", nil)
	request.Header.Set("Sec-WebSocket-Protocol", "bad protocol")
	if result := orchestrator.initializeSubprotocol(request); result == nil {
		t.Fatal("always-on subprotocol gate accepted an invalid offer")
	}
	request.Header.Set("Sec-WebSocket-Protocol", "realtime.v2, realtime.v1")
	if result := orchestrator.initializeSubprotocol(request); result != nil {
		t.Fatalf("valid subprotocol offer rejected: %#v", result)
	}
	if len(orchestrator.subprotocol.DialOffer()) != 2 {
		t.Fatalf("subprotocol dial offer = %#v", orchestrator.subprotocol.DialOffer())
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
		condition   codexrecovery.Condition
		disposition requestcapture.MessageDisposition
		status      websocket.StatusCode
	}{
		{codexws.FailureProtocol, codexrecovery.ConditionProtocolInvalid, requestcapture.MessageDispositionProtocolRejected, websocket.StatusPolicyViolation},
		{codexws.FailureIdentity, codexrecovery.ConditionStateConflict, requestcapture.MessageDispositionIdentityRejected, websocket.StatusPolicyViolation},
		{codexws.FailureStorage, codexrecovery.ConditionStateStoreUnavailable, requestcapture.MessageDispositionStorageRejected, websocket.StatusTryAgainLater},
	} {
		failure := &codexws.Failure{Class: test.class, Stage: "test", Cause: codexrecovery.Mark(test.condition, errors.New("rejected"))}
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

func TestCodexWebSocketRecoveryAdapterClassifiesRealBoundaryFailures(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	runtime := testCodexRuntime(t)
	begin := func(operationID string) *codexws.Operation {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "http://gateway.test/responses", nil)
		request.Header.Set("Authorization", "Bearer client")
		operation, err := runtime.Begin(ctx, request, APITypeCodex, operationID)
		if err != nil {
			t.Fatalf("begin %s: %v", operationID, err)
		}
		return operation
	}
	requireFailure := func(stage string, err error) error {
		t.Helper()
		if err == nil {
			t.Fatalf("%s unexpectedly succeeded", stage)
		}
		return err
	}

	_, protocolErr := runtime.Begin(ctx, nil, APITypeCodex, "protocol-invalid")
	protocolErr = requireFailure("Runtime.Begin protocol validation", protocolErr)

	var unavailableRuntime *codexws.Runtime
	unavailableRequest := httptest.NewRequest(http.MethodGet, "http://gateway.test/responses", nil)
	unavailableRequest.Header.Set("Authorization", "Bearer client")
	_, storageErr := unavailableRuntime.Begin(ctx, unavailableRequest, APITypeCodex, "storage-unavailable")
	storageErr = requireFailure("Runtime.Begin unavailable runtime", storageErr)

	unknownRequest := httptest.NewRequest(http.MethodGet, "http://gateway.test/responses", nil)
	unknownRequest.Header.Set("Authorization", "Bearer client")
	unknownRequest.Header.Set("X-Codex-Turn-State", "unknown-turn-state")
	_, newThreadErr := runtime.Begin(ctx, unknownRequest, APITypeCodex, "new-thread")
	newThreadErr = requireFailure("Runtime.Begin unknown state", newThreadErr)

	openConnectionErr := requireFailure("OpenConnection without provider", begin("open-connection").OpenConnection())

	reconnectOperation := begin("reconnect")
	reconnectFrame := reconnectOperation.ClassifyClientFrame(
		ctx, true, []byte(`{"type":"response.append"}`),
	)
	_, reconnectErr := reconnectFrame.PrepareDelivery(ctx)
	reconnectErr = requireFailure("append without current connection", reconnectErr)

	provider := &model.Provider{
		ID:                 "provider",
		CredentialSessions: testCredentialSessions("provider", APITypeCodex, credentialsession.KindAPIKey, "secret"),
	}
	preparedA := testPreparedProviderAttempt(t, provider, APITypeCodex, "https://provider-a.example/v1/responses")
	preparedB := testPreparedProviderAttempt(t, provider, APITypeCodex, "https://provider-b.example/v1/responses")
	subject, err := codexidentity.CredentialSubjectFromSession(preparedA.credential.Subject)
	if err != nil {
		t.Fatalf("credential subject: %v", err)
	}
	preparedA.applied, err = codexidentity.AppliedIdentityFromRequest(preparedA.candidate.Authority().Vendor(), preparedA.finalURL, subject)
	if err != nil {
		t.Fatalf("provider A applied identity: %v", err)
	}
	preparedB.applied, err = codexidentity.AppliedIdentityFromRequest(preparedB.candidate.Authority().Vendor(), preparedB.finalURL, subject)
	if err != nil {
		t.Fatalf("provider B applied identity: %v", err)
	}
	_, prepareDialErr := begin("prepare-dial").PrepareDial(
		ctx, preparedA.headers.Clone(), preparedA.candidate, preparedB.applied, preparedB.finalURL,
	)
	prepareDialErr = requireFailure("PrepareDial identity mismatch", prepareDialErr)

	commitOperation := begin("permit-commit")
	dialPermit, err := commitOperation.PrepareDial(
		ctx, preparedA.headers.Clone(), preparedA.candidate, preparedA.applied, preparedA.finalURL,
	)
	if err != nil {
		t.Fatalf("prepare permit-commit dial: %v", err)
	}
	if err := dialPermit.Commit(ctx); err != nil {
		t.Fatalf("commit permit-commit dial: %v", err)
	}
	responsePermit, err := commitOperation.PrepareServerFrame(
		ctx, true, []byte(`{"type":"response.created","response":{"id":"resp-permit-commit"}}`),
	)
	if err != nil {
		t.Fatalf("prepare response lifecycle: %v", err)
	}
	commitErr := requireFailure("response lifecycle permit commit", responsePermit.Commit(ctx))

	type expected struct {
		name      string
		err       error
		condition codexrecovery.Condition
		status    int
		code      codexrecovery.ErrorCode
		closeCode websocket.StatusCode
	}
	tests := []expected{
		{"state conflict from permit commit", commitErr, codexrecovery.ConditionStateConflict, http.StatusConflict, codexrecovery.ErrorCodeStateConflict, websocket.StatusPolicyViolation},
		{"reconnect from append delivery", reconnectErr, codexrecovery.ConditionReconnectRequired, http.StatusConflict, codexrecovery.ErrorCodeReconnectRequired, websocket.StatusServiceRestart},
		{"new Thread from Begin", newThreadErr, codexrecovery.ConditionNewThreadRequired, http.StatusGone, codexrecovery.ErrorCodeNewThreadRequired, websocket.StatusPolicyViolation},
		{"storage from nil Runtime Begin", storageErr, codexrecovery.ConditionStateStoreUnavailable, http.StatusServiceUnavailable, codexrecovery.ErrorCodeStateStoreUnavailable, websocket.StatusTryAgainLater},
		{"protocol from nil request Begin", protocolErr, codexrecovery.ConditionProtocolInvalid, http.StatusBadRequest, codexrecovery.ErrorCodeProtocolInvalid, websocket.StatusPolicyViolation},
		{"unclassified internal", errors.New("unclassified adapter failure"), codexrecovery.ConditionInternalFailure, http.StatusInternalServerError, codexrecovery.ErrorCodeInternal, websocket.StatusInternalError},
	}
	handler := &Gateway{logger: zaptest.NewLogger(t)}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			preUpgrade := codexWebSocketRecoveryDecision(test.err, codexrecovery.PhaseWebSocketPreUpgrade)
			if preUpgrade.Condition() != test.condition ||
				preUpgrade.HTTPStatus() != test.status ||
				preUpgrade.ErrorCode() != test.code {
				t.Fatalf("pre-upgrade decision = (%q, %d, %q), want (%q, %d, %q)",
					preUpgrade.Condition(), preUpgrade.HTTPStatus(), preUpgrade.ErrorCode(),
					test.condition, test.status, test.code,
				)
			}

			accepted := codexWebSocketRecoveryDecision(test.err, codexrecovery.PhaseWebSocketAccepted)
			if accepted.Condition() != test.condition ||
				accepted.WebSocketCloseCode() != test.closeCode ||
				accepted.ErrorCode() != test.code {
				t.Fatalf("accepted decision = (%q, %d, %q), want (%q, %d, %q)",
					accepted.Condition(), accepted.WebSocketCloseCode(), accepted.ErrorCode(),
					test.condition, test.closeCode, test.code,
				)
			}

			recorder := httptest.NewRecorder()
			handler.writeCodexWebSocketFailureForOperation(recorder, "real-boundary", test.err)
			if recorder.Code != test.status ||
				!bytes.Contains(recorder.Body.Bytes(), []byte(test.code)) {
				t.Fatalf("pre-upgrade envelope status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var closeError websocket.CloseError
			if err := newWebSocketCodexCloseError(test.err); !errors.As(err, &closeError) ||
				closeError.Code != test.closeCode || closeError.Reason != string(test.code) {
				t.Fatalf("accepted close = %#v (%v), want code=%d reason=%q",
					closeError, err, test.closeCode, test.code,
				)
			}
		})
	}

	for _, boundary := range []struct {
		name string
		err  error
	}{
		{"PrepareDial", prepareDialErr},
		{"OpenConnection", openConnectionErr},
	} {
		t.Run(boundary.name+" maps identity fallback", func(t *testing.T) {
			for _, phase := range []codexrecovery.CarrierPhase{
				codexrecovery.PhaseWebSocketPreUpgrade,
				codexrecovery.PhaseWebSocketAccepted,
			} {
				if got := codexWebSocketRecoveryDecision(boundary.err, phase).Condition(); got != codexrecovery.ConditionStateConflict {
					t.Fatalf("%s condition = %q, want %q", phase, got, codexrecovery.ConditionStateConflict)
				}
			}
		})
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
	if clientObserved || clientRejection.Result == nil || !errors.Is(clientRejection.Result.Err, clientRejected) {
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

func TestGatewayCodexNegotiatesSubprotocolAndLeavesInjectTargetValidationUpstream(t *testing.T) {
	clientPayload := []byte(`{"type":"response.create","model":"gpt-5"}`)
	serverPayload := []byte(`{"type":"response.created","response":{"id":"response-1"}}`)
	injectPayload := []byte(" \n{\"type\":\"response.inject\",\"response_id\":{\"unknown_shape\":[1,2,3]}}\t")
	targetRejection := []byte(`{"type":"error","error":{"type":"invalid_request_error","message":"unknown response target"}}`)
	upstreamOffer := make(chan string, 1)
	upstreamCookie := make(chan string, 1)
	upstreamPayload := make(chan []byte, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		upstreamOffer <- request.Header.Get("Sec-WebSocket-Protocol")
		upstreamCookie <- request.Header.Get("Cookie")
		connection, err := websocket.Accept(w, request, &websocket.AcceptOptions{Subprotocols: []string{"realtime.v2"}})
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
		if messageType != websocket.MessageText {
			t.Errorf("upstream message type = %d", messageType)
		}
		upstreamPayload <- append([]byte(nil), payload...)
		if err := connection.Write(request.Context(), websocket.MessageText, serverPayload); err != nil {
			t.Errorf("write upstream: %v", err)
			return
		}
		messageType, payload, err = connection.Read(request.Context())
		if err != nil {
			t.Errorf("read inject upstream: %v", err)
			return
		}
		if messageType != websocket.MessageText {
			t.Errorf("inject upstream message type = %d", messageType)
		}
		upstreamPayload <- append([]byte(nil), payload...)
		if err := connection.Write(request.Context(), websocket.MessageText, targetRejection); err != nil {
			t.Errorf("write target rejection: %v", err)
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
	runtime := testCodexRuntime(t)
	gateway := newTestGateway(t, Config{Store: store, Logger: zaptest.NewLogger(t), Codex: runtime})
	server := newGatewayIntegrationServer(gateway, RequestConfig{GlobalAuthMode: "bearer", GlobalMaxAttempts: 1}, "always-on-wire")
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, wsURL(server)+"/responses?model=gpt-5", &websocket.DialOptions{
		Subprotocols: []string{"realtime.v2", "realtime.v1"},
		HTTPHeader: http.Header{
			"Authorization": {"Bearer client"},
			"Cookie":        {providercookie.GatewayHandleName + "=must-not-leak; ordinary=kept"},
		},
	})
	if err != nil {
		t.Fatalf("dial gateway: %v", err)
	}
	defer connection.CloseNow()
	if got := connection.Subprotocol(); got != "realtime.v2" {
		t.Fatalf("downstream subprotocol = %q", got)
	}
	if err := connection.Write(ctx, websocket.MessageText, clientPayload); err != nil {
		t.Fatalf("write client frame: %v", err)
	}
	messageType, payload, err := connection.Read(ctx)
	if err != nil {
		t.Fatalf("read server frame: %v", err)
	}
	if messageType != websocket.MessageText || !bytes.Equal(payload, serverPayload) {
		t.Fatalf("server frame type=%d payload=%v", messageType, payload)
	}
	if err := connection.Write(ctx, websocket.MessageText, injectPayload); err != nil {
		t.Fatalf("write inject frame: %v", err)
	}
	messageType, payload, err = connection.Read(ctx)
	if err != nil {
		t.Fatalf("read target rejection: %v", err)
	}
	if messageType != websocket.MessageText || !bytes.Equal(payload, targetRejection) {
		t.Fatalf("target rejection type=%d payload=%v", messageType, payload)
	}
	if got := <-upstreamOffer; got != "realtime.v2,realtime.v1" {
		t.Fatalf("upstream subprotocol offer = %q", got)
	}
	if got := <-upstreamCookie; got != "" {
		t.Fatalf("upstream cookie = %q", got)
	}
	if got := <-upstreamPayload; !bytes.Equal(got, clientPayload) {
		t.Fatalf("upstream payload = %v", got)
	}
	if got := <-upstreamPayload; !bytes.Equal(got, injectPayload) {
		t.Fatalf("upstream inject payload = %v", got)
	}
}

func TestCodexWebSocketFailureAndAttemptAdapters(t *testing.T) {
	handler := &Gateway{logger: zaptest.NewLogger(t)}
	for _, test := range []struct {
		class     codexws.FailureClass
		condition codexrecovery.Condition
		status    int
	}{
		{codexws.FailureProtocol, codexrecovery.ConditionProtocolInvalid, http.StatusBadRequest},
		{codexws.FailureStorage, codexrecovery.ConditionStateStoreUnavailable, http.StatusServiceUnavailable},
	} {
		recorder := httptest.NewRecorder()
		handler.writeCodexWebSocketFailure(recorder, &codexws.Failure{
			Class: test.class, Stage: "test", Cause: codexrecovery.Mark(test.condition, errors.New("failed")),
		})
		if recorder.Code != test.status {
			t.Fatalf("class %s status=%d want=%d", test.class, recorder.Code, test.status)
		}
	}

	applyCodexWebSocketRouteConstraint(nil, nil)
	selectRequest := &model.SelectRequest{}
	applyCodexWebSocketRouteConstraint(selectRequest, testCodexOperation(t))
	if selectRequest.RequiredAuthority != nil || selectRequest.PreferredRouteTargetID != "" {
		t.Fatalf("unexpected owner-free route constraint: %#v", selectRequest)
	}

	orchestrator := &WebSocketSessionOrchestrator{
		handler:        handler,
		lifecycle:      newWebSocketLifecycleState(),
		codexOperation: testCodexOperation(t),
		requestID:      "attempt",
	}
	if err := orchestrator.prepareCodexPhysicalDial(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	prepared := webSocketPreparedProviderAttempt{}
	if err := orchestrator.finishCodexPhysicalDial(context.Background(), prepared, DialExchange{}); codexws.Classify(err) != codexws.FailureStorage {
		t.Fatalf("missing cookie authority error = %v", err)
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
	prepared.applied, err = codexidentity.AppliedIdentityFromRequest(prepared.candidate.Authority().Vendor(), prepared.finalURL, subject)
	if err != nil {
		t.Fatal(err)
	}
	prepared.headers = http.Header{"Cookie": {providercookie.GatewayHandleName + "=private; ordinary=kept"}}
	if err := orchestrator.prepareCodexPhysicalDial(context.Background(), &prepared); err != nil {
		t.Fatalf("prepare physical dial: %v", err)
	}
	if prepared.boundaryPermit == nil || prepared.headers.Get("Cookie") != "" {
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
	operation := testCodexOperation(t)
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
		prepared.applied, err = codexidentity.AppliedIdentityFromRequest(prepared.candidate.Authority().Vendor(), prepared.finalURL, subject)
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

	decision := orchestrator.composeUpstreamPreWrite(context.Background(), nil)(webSocketPreWriteContext{
		MessageType: websocket.MessageText, Data: []byte(`{"type":"future.application.frame"}`),
	})
	if decision.OnWriteConfirmed == nil {
		t.Fatal("first upstream application frame has no visibility commit")
	}
	applyCodexWebSocketRouteConstraint(selectRequest, operation)
	if selectRequest.RequiredAuthority != nil || selectRequest.PreferredRouteTargetID != "" {
		t.Fatalf("first upstream frame pinned before physical write confirmation: %#v", selectRequest)
	}
	if err := decision.OnWriteConfirmed(); err != nil {
		t.Fatal(err)
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
