package websocketproxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/selector"

	"github.com/coder/websocket"
	"go.uber.org/zap/zaptest"
)

func TestWebSocketOrchestratorDefersParticipatingDialCaptureCompletion(t *testing.T) {
	providers := []requestcapture.ProviderIdentity{
		{ID: "direct", Name: "direct"},
		{ID: "rejected", Name: "rejected"},
		{ID: "refreshed", Name: "refreshed"},
	}
	manager, session := startCaptureTestManager(t, providers)
	defer manager.Close()

	gatewayCapture := manager.BeginGateway(requestcapture.GatewayStart{GatewayRequestID: "deferred-completion"})
	directRecorder := beginWebSocketCaptureTestRecord(gatewayCapture, "direct", requestcapture.SelectionModeInitial)
	rejectedRecorder := beginWebSocketCaptureTestRecord(gatewayCapture, "rejected", requestcapture.SelectionModeReplacement)
	refreshedRecorder := beginWebSocketCaptureTestRecord(gatewayCapture, "refreshed", requestcapture.SelectionModeInitial)

	var nilOrchestrator *WebSocketSessionOrchestrator
	nilOrchestrator.queueCaptureCompletion(DialExchange{}, requestcapture.Outcome{})
	nilOrchestrator.finishCaptureCompletions()

	orchestrator := &WebSocketSessionOrchestrator{}
	orchestrator.queueCaptureCompletion(DialExchange{}, requestcapture.Outcome{})
	orchestrator.queueCaptureCompletion(DialExchange{
		capture:     directRecorder,
		captureMode: captureModeForRecorder(directRecorder),
	}, requestcapture.Outcome{
		SourceCompletion:  requestcapture.SourceCompletionComplete,
		TerminationReason: requestcapture.TerminationReasonWebSocketRelayError,
	})
	orchestrator.queueRejectedDialCapture(context.Background(), DialExchange{
		HandshakeStatusCode: http.StatusBadGateway,
		Err:                 errors.New("handshake rejected"),
		capture:             rejectedRecorder,
		captureMode:         captureModeForRecorder(rejectedRecorder),
	}, WebSocketAttemptResult{
		Result: &WebSocketResult{TerminalCause: model.TerminalUpstreamHandshakeRejected},
	})
	orchestrator.queueCredentialRefreshDrainCapture(DialExchange{
		HandshakeStatusCode: http.StatusUnauthorized,
		Err:                 errors.New("expired credential"),
		capture:             refreshedRecorder,
		captureMode:         captureModeForRecorder(refreshedRecorder),
	})

	if len(orchestrator.captureCompletions) != 3 {
		t.Fatalf("queued capture completions = %d, want 3", len(orchestrator.captureCompletions))
	}
	for index, completion := range orchestrator.captureCompletions {
		if completion.outcome.CompletedAt.IsZero() {
			t.Fatalf("completion %d has no terminal timestamp", index)
		}
	}
	if got := orchestrator.captureCompletions[1].outcome.TerminationReason; got != requestcapture.TerminationReasonStatusFailoverDrain {
		t.Fatalf("rejected dial termination = %q, want status failover drain", got)
	}
	if got := orchestrator.captureCompletions[2].outcome.TerminationReason; got != requestcapture.TerminationReasonCredentialRefreshDrain {
		t.Fatalf("credential drain termination = %q, want credential refresh drain", got)
	}

	orchestrator.finishCaptureCompletions()
	if orchestrator.captureCompletions != nil {
		t.Fatalf("capture completions retained after finish: %#v", orchestrator.captureCompletions)
	}
	gatewayCapture.Finish(requestcapture.GatewayOutcome{})

	page, err := readCaptureTestPage(manager, session, requestcapture.ListQuery{Limit: 10})
	if err != nil {
		t.Fatalf("read capture records: %v", err)
	}
	if len(page.Records) != 3 {
		t.Fatalf("capture records = %d, want 3", len(page.Records))
	}
}

func TestWebSocketSelectionBootstrapKeepsModelDiscoveryBeforeLeaseSelection(t *testing.T) {
	t.Run("demand resolution failure is terminal before selection", func(t *testing.T) {
		store := newMockStore()
		store.routingPolicyErr = errors.New("routing policy unavailable")
		orchestrator := newWebSocketSessionOrchestrator(&Gateway{
			store:  store,
			logger: zaptest.NewLogger(t),
		}, webSocketSessionOrchestratorConfig{
			requestID:        "demand-failure",
			apiType:          APITypeCodex,
			codexOperation:   testCodexOperation(t),
			probeClientModel: true,
			selectReq: &model.SelectRequest{
				APIType:    APITypeCodex,
				Model:      ModelUnknown,
				StickyMode: model.StickyModeOff,
			},
		})

		session := orchestrator.Run(
			context.Background(),
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodGet, "http://gateway.test/responses", nil),
		)
		if session == nil || session.ProbeOutcome != webSocketSelectionProbeOutcomeDemandResolutionFailed {
			t.Fatalf("bootstrap session = %#v, want demand-resolution failure", session)
		}
		if session.FinalErr == nil || !strings.Contains(session.FinalErr.Error(), "routing policy unavailable") {
			t.Fatalf("bootstrap error = %v, want routing policy failure", session.FinalErr)
		}
	})

	t.Run("client upgrade rejection does not enter provider selection", func(t *testing.T) {
		store := newMockStore()
		store.routingPolicies = []model.RoutingPolicy{{
			Enabled: true, APIType: APITypeCodex,
			ModelMatchType: model.RoutingPolicyModelMatchTypePrefix, ModelMatchValue: "gpt-",
		}}
		orchestrator := newWebSocketSessionOrchestrator(&Gateway{
			store:       store,
			wsForwarder: NewWebSocketForwarder(WebSocketForwarderConfig{Logger: zaptest.NewLogger(t)}),
			logger:      zaptest.NewLogger(t),
		}, webSocketSessionOrchestratorConfig{
			requestID:        "upgrade-rejected",
			apiType:          APITypeCodex,
			codexOperation:   testCodexOperation(t),
			probeClientModel: true,
			selectReq: &model.SelectRequest{
				APIType:    APITypeCodex,
				Model:      ModelUnknown,
				StickyMode: model.StickyModeModel,
			},
		})

		session := orchestrator.Run(
			context.Background(),
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodGet, "http://gateway.test/responses", nil),
		)
		if session == nil || session.FinalResult == nil || session.FinalResult.TerminalCause != model.TerminalClientUpgradeRejected {
			t.Fatalf("upgrade rejection session = %#v", session)
		}
		if session.ClientAccepted {
			t.Fatal("rejected downstream upgrade reported client acceptance")
		}
	})

	t.Run("observed model is replayed into the selection request", func(t *testing.T) {
		selectReq := &model.SelectRequest{
			APIType:    APITypeCodex,
			Model:      ModelUnknown,
			StickyMode: model.StickyModeModel,
		}
		store := newMockStore()
		store.routingPolicies = []model.RoutingPolicy{{
			Enabled: true, APIType: APITypeCodex,
			ModelMatchType: model.RoutingPolicyModelMatchTypePrefix, ModelMatchValue: "gpt-",
		}}
		orchestrator := newWebSocketSessionOrchestrator(&Gateway{
			store:  store,
			logger: zaptest.NewLogger(t),
		}, webSocketSessionOrchestratorConfig{
			requestID:        "model-observed",
			apiType:          APITypeCodex,
			codexOperation:   testCodexOperation(t),
			probeClientModel: true,
			selectReq:        selectReq,
			info:             RequestInfo{Model: ModelUnknown},
		})
		orchestrator.clientConn = &websocket.Conn{}
		orchestrator.lifecycle.MarkClientAccepted()
		initialRead := make(chan webSocketInitialReadResult, 1)
		initialRead <- webSocketInitialReadResult{
			messageType: websocket.MessageText,
			data:        []byte(`{"type":"response.create","response":{"model":"gpt-5-realtime"}}`),
		}
		orchestrator.initialClientReadCh = initialRead

		session := orchestrator.bootstrapSelectionContext(
			context.Background(),
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodGet, "http://gateway.test/responses", nil),
		)
		if session != nil {
			t.Fatalf("bootstrap returned terminal session: %#v", session)
		}
		if selectReq.Model != "gpt-5-realtime" || orchestrator.info.Model != "gpt-5-realtime" {
			t.Fatalf("resolved model = request:%q info:%q", selectReq.Model, orchestrator.info.Model)
		}
		if orchestrator.probeOutcome != webSocketSelectionProbeOutcomeObservedUsableModel {
			t.Fatalf("probe outcome = %q, want observed usable model", orchestrator.probeOutcome)
		}
		if snapshot := orchestrator.replayBuffer.Snapshot(); len(snapshot.Messages) != 1 || snapshot.Messages[0].Delivered {
			t.Fatalf("bootstrap replay snapshot = %#v", snapshot)
		}
	})

	t.Run("request cancellation terminates an in-flight probe", func(t *testing.T) {
		orchestrator := &WebSocketSessionOrchestrator{
			requestID:           "probe-canceled",
			clientConn:          &websocket.Conn{},
			initialClientReadCh: make(chan webSocketInitialReadResult),
			lifecycle:           newWebSocketLifecycleState(),
			replayBuffer:        newPreVisibleClientMessageBuffer(preVisibleClientReplayBufferLimitBytes),
		}
		orchestrator.lifecycle.MarkClientAccepted()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		session, modelName, outcome := orchestrator.probeClientSelectionContext(ctx)
		if session == nil || session.FinalResult == nil || !errors.Is(session.FinalErr, context.Canceled) {
			t.Fatalf("canceled probe session = %#v", session)
		}
		if modelName != "" || outcome != webSocketSelectionProbeOutcomeTransportFailed || !session.ClientAccepted {
			t.Fatalf("canceled probe = model:%q outcome:%q accepted:%v", modelName, outcome, session.ClientAccepted)
		}
	})

	var nilOrchestrator *WebSocketSessionOrchestrator
	if nilOrchestrator.supportsReplaySafeSelectionProbe() || nilOrchestrator.selectionProbeObserver(ModelUnknown) != nil {
		t.Fatal("nil orchestrator reported replay-safe probe support")
	}
	withoutClient := &WebSocketSessionOrchestrator{}
	if session, modelName, outcome := withoutClient.probeClientSelectionContext(context.Background()); session != nil || modelName != "" || outcome != webSocketSelectionProbeOutcomeCompletedWithoutUsableModel {
		t.Fatalf("probe without client = (%#v, %q, %q)", session, modelName, outcome)
	}
}

func TestWebSocketSelectProviderPreservesFailureSemanticsAfterAttempts(t *testing.T) {
	provider := routingTestProvider("attempted")
	lastErr := errors.New("last provider failed")
	orchestrator := newWebSocketSessionOrchestrator(&Gateway{
		selector: &routingTestSelector{initialErr: internal.ErrNoProvider},
		logger:   zaptest.NewLogger(t),
	}, webSocketSessionOrchestratorConfig{
		requestID:      "selection-exhausted",
		apiType:        APITypeCodex,
		codexOperation: testCodexOperation(t),
		selectReq:      &model.SelectRequest{APIType: APITypeCodex, Model: "gpt-5"},
	})
	orchestrator.attempts = []WebSocketAttemptResult{{
		Provider: &provider,
		Result: &WebSocketResult{
			Err:           lastErr,
			TerminalCause: model.TerminalUpstreamTransportError,
		},
		ForwardErr: lastErr,
	}}

	selection, mode, session := orchestrator.selectProvider(context.Background(), 0)
	if selection.Lease != nil || mode != providerSwitchModeInitial || session == nil {
		t.Fatalf("exhausted selection = (%#v, %q, %#v)", selection, mode, session)
	}
	if session.FinalProvider == nil || session.FinalProvider.ID != provider.ID || !errors.Is(session.FinalErr, lastErr) {
		t.Fatalf("terminal selection session = %#v", session)
	}

	selectionErr := errors.New("selector unavailable")
	orchestrator = newWebSocketSessionOrchestrator(&Gateway{
		selector: &routingTestSelector{initialErr: selectionErr},
		logger:   zaptest.NewLogger(t),
	}, webSocketSessionOrchestratorConfig{
		requestID:      "selection-error",
		apiType:        APITypeCodex,
		codexOperation: testCodexOperation(t),
		selectReq:      &model.SelectRequest{APIType: APITypeCodex, Model: "gpt-5"},
	})
	selection, mode, session = orchestrator.selectProvider(context.Background(), 0)
	if selection.Lease != nil || mode != providerSwitchModeInitial || session == nil {
		t.Fatalf("failed selection = (%#v, %q, %#v)", selection, mode, session)
	}
	if session.GatewayStatusCode != http.StatusInternalServerError || !errors.Is(session.FinalErr, selectionErr) {
		t.Fatalf("selection error session = %#v", session)
	}
}

func TestWebSocketProviderPreparationRetainsKnownCaptureTarget(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://gateway.test/responses?trace=1", nil)
	orchestrator := &WebSocketSessionOrchestrator{
		handler:             &Gateway{},
		apiType:             APITypeCodex,
		captureParticipates: true,
	}

	noBase := &model.Provider{ID: "no-base"}
	_, code, err := orchestrator.prepareProviderAttempt(
		context.Background(), request, noBase, orchestrator.handler.newFallbackProviderLease(noBase, APITypeCodex),
	)
	if err == nil || code != requestcapture.FailureCodeMissingBaseURL {
		t.Fatalf("missing base URL preparation = code:%q error:%v", code, err)
	}

	missingKey := model.Provider{
		ID:                 "missing-key",
		APITypes:           []model.ProviderAPIType{{ProviderID: "missing-key", APIType: APITypeCodex, BaseURL: "https://provider.example"}},
		CredentialSessions: testCredentialSessions("missing-key", APITypeCodex, credentialsession.KindAPIKey, ""),
	}
	preparedMissingKey, code, err := orchestrator.prepareProviderAttempt(
		context.Background(), request, &missingKey, orchestrator.handler.newFallbackProviderLease(&missingKey, APITypeCodex),
	)
	if err == nil || code != requestcapture.FailureCodeMissingAPIKey {
		t.Fatalf("missing key preparation = URL:%q code:%q error:%v", preparedMissingKey.upstreamURL, code, err)
	}
	if !strings.HasPrefix(preparedMissingKey.upstreamURL, "wss://provider.example/") || !strings.Contains(preparedMissingKey.upstreamURL, "trace=1") {
		t.Fatalf("known diagnostic target = %q", preparedMissingKey.upstreamURL)
	}

	applyErr := errors.New("credential application failed")
	provider := routingTestProvider("credential-error")
	orchestrator.handler.auth = &orchestratorCoverageAuthenticator{applyErr: applyErr}
	preparedCredentialError, code, err := orchestrator.prepareProviderAttempt(
		context.Background(), request, &provider, orchestrator.handler.newFallbackProviderLease(&provider, APITypeCodex),
	)
	if !errors.Is(err, applyErr) || code != requestcapture.FailureCodeCredentialApply || preparedCredentialError.upstreamURL == "" {
		t.Fatalf("credential preparation = URL:%q code:%q error:%v", preparedCredentialError.upstreamURL, code, err)
	}
	if got := preparedCredentialError.headers.Get("Authorization"); got != "Bearer coverage-token" {
		t.Fatalf("partial sanitized header context = %q", got)
	}

	tests := []struct {
		name string
		err  error
		want requestcapture.FailureCode
	}{
		{name: "ordinary error", err: errors.New("ordinary"), want: requestcapture.FailureCodeUnknown},
		{name: "api key", err: &webSocketProviderConfigError{missingField: "api_key", err: errors.New("missing")}, want: requestcapture.FailureCodeMissingAPIKey},
		{name: "managed credentials", err: &webSocketProviderConfigError{missingField: "credentials", err: errors.New("missing")}, want: requestcapture.FailureCodeMissingCredentials},
		{name: "unknown field", err: &webSocketProviderConfigError{missingField: "other", err: errors.New("missing")}, want: requestcapture.FailureCodeUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := webSocketConfigurationFailureCode(test.err); got != test.want {
				t.Fatalf("failure code = %q, want %q", got, test.want)
			}
		})
	}
	if metadata := webSocketCaptureAttemptMetadata(nil, APITypeCodex, providerSwitchModeInitial, selector.SelectionMetadata{}, requestcapture.CredentialPhaseInitial); metadata != (requestcapture.AttemptMetadata{}) {
		t.Fatalf("nil-provider capture metadata = %#v", metadata)
	}
}

type orchestratorCoverageAuthenticator struct {
	refresh    bool
	refreshErr error
	applyErr   error
	refreshes  int
	applies    int
}

func (a *orchestratorCoverageAuthenticator) ApplyProviderCredentials(
	_ context.Context,
	headers http.Header,
	_ codexidentity.CandidateSnapshot,
	_, _ string,
	_ *http.Request,
	_ *url.URL,
) (codexidentity.AppliedIdentity, error) {
	a.applies++
	headers.Set("Authorization", "Bearer coverage-token")
	return codexidentity.AppliedIdentity{}, a.applyErr
}

func (a *orchestratorCoverageAuthenticator) RefreshCredentialSession(context.Context, credentialsession.Snapshot) (bool, error) {
	a.refreshes++
	return a.refresh, a.refreshErr
}

func TestWebSocketCredentialRecoveryKeepsOneProviderAttempt(t *testing.T) {
	provider := routingTestProvider("recoverable")
	request := httptest.NewRequest(http.MethodGet, "http://gateway.test/responses", nil)
	prepared := testPreparedProviderAttempt(t, &provider, APITypeCodex, "wss://provider.example/responses")

	newOrchestrator := func(auth ProviderAuthenticator, dialer WebSocketDialer) *WebSocketSessionOrchestrator {
		return &WebSocketSessionOrchestrator{
			handler: &Gateway{
				auth: auth,
				wsForwarder: NewWebSocketForwarder(WebSocketForwarderConfig{
					Dialer: dialer,
					Logger: zaptest.NewLogger(t),
				}),
				logger: zaptest.NewLogger(t),
			},
			apiType:   APITypeCodex,
			lifecycle: newWebSocketLifecycleState(),
		}
	}

	t.Run("refresh not performed leaves the original unauthorized attempt terminal", func(t *testing.T) {
		auth := &orchestratorCoverageAuthenticator{}
		orchestrator := newOrchestrator(auth, nil)
		exchange, attempt, recovered := orchestrator.recoverUnauthorizedSameProvider(
			context.Background(), request, &provider, prepared, 0,
			providerSwitchModeInitial, selector.SelectionMetadata{}, time.Now(),
		)
		if recovered || exchange.Conn != nil || attempt.Result != nil || auth.refreshes != 1 || auth.applies != 0 {
			t.Fatalf("refresh-not-performed result = exchange:%#v attempt:%#v recovered:%v auth:%#v", exchange, attempt, recovered, auth)
		}
	})

	t.Run("refresh failure does not redial", func(t *testing.T) {
		refreshErr := errors.New("refresh failed")
		auth := &orchestratorCoverageAuthenticator{refresh: true, refreshErr: refreshErr}
		orchestrator := newOrchestrator(auth, nil)
		_, _, recovered := orchestrator.recoverUnauthorizedSameProvider(
			context.Background(), request, &provider, prepared, 0,
			providerSwitchModeInitial, selector.SelectionMetadata{}, time.Now(),
		)
		if recovered || auth.refreshes != 1 || auth.applies != 0 {
			t.Fatalf("failed refresh = recovered:%v auth:%#v", recovered, auth)
		}
	})

	t.Run("refreshed credential preparation failure is captured as the same attempt", func(t *testing.T) {
		manager, session := startCaptureTestManager(t, []requestcapture.ProviderIdentity{{ID: provider.ID, Name: provider.Name}})
		defer manager.Close()
		gatewayCapture := manager.BeginGateway(requestcapture.GatewayStart{GatewayRequestID: "recovery-config"})
		auth := &orchestratorCoverageAuthenticator{refresh: true, applyErr: errors.New("credential apply failed")}
		orchestrator := newOrchestrator(auth, nil)
		orchestrator.capture = gatewayCapture
		orchestrator.captureParticipates = true

		exchange, attempt, recovered := orchestrator.recoverUnauthorizedSameProvider(
			context.Background(), request, &provider, prepared, 2,
			providerSwitchModeReplacement, selector.SelectionMetadata{Source: selector.SelectionSourceStrategy}, time.Now(),
		)
		if !recovered || exchange.Conn != nil || !attempt.RecoveryAttempted || attempt.Result == nil || attempt.Result.TerminalCause != model.TerminalProviderConfigurationError {
			t.Fatalf("configuration recovery = exchange:%#v attempt:%#v recovered:%v", exchange, attempt, recovered)
		}
		gatewayCapture.Finish(requestcapture.GatewayOutcome{})
		page, err := readCaptureTestPage(manager, session, requestcapture.ListQuery{Limit: 10})
		if err != nil || len(page.Records) != 0 {
			t.Fatalf("configuration-only recovery invented %d physical dial records, error = %v", len(page.Records), err)
		}
	})

	t.Run("second rejected handshake replaces the first recovery result", func(t *testing.T) {
		redialErr := errors.New("refreshed handshake rejected")
		auth := &orchestratorCoverageAuthenticator{refresh: true}
		orchestrator := newOrchestrator(auth, &mockDialer{dialFunc: func(
			context.Context,
			string,
			*websocket.DialOptions,
		) (*websocket.Conn, *http.Response, error) {
			return nil, &http.Response{StatusCode: http.StatusBadGateway, Header: make(http.Header)}, redialErr
		}})
		resolution := orchestrator.resolveRejectedProviderDial(
			context.Background(), request, &provider,
			DialExchange{HandshakeStatusCode: http.StatusUnauthorized, Err: errors.New("unauthorized")},
			prepared, 0, providerSwitchModeInitial,
			selector.SelectionMetadata{}, time.Now(),
		)
		if resolution.accepted || !resolution.terminalAttempt.RecoveryAttempted || resolution.terminalAttempt.Result == nil {
			t.Fatalf("rejected recovery resolution = %#v", resolution)
		}
		if resolution.terminalAttempt.Result.HandshakeStatusCode != http.StatusBadGateway || !errors.Is(resolution.terminalAttempt.Result.Err, redialErr) {
			t.Fatalf("recovered handshake result = %#v", resolution.terminalAttempt.Result)
		}
	})

	t.Run("non-unauthorized rejection never invokes credential recovery", func(t *testing.T) {
		auth := &orchestratorCoverageAuthenticator{refresh: true}
		orchestrator := newOrchestrator(auth, nil)
		resolution := orchestrator.resolveRejectedProviderDial(
			context.Background(), request, &provider,
			DialExchange{HandshakeStatusCode: http.StatusForbidden, Err: errors.New("forbidden")},
			prepared, 0, providerSwitchModeInitial,
			selector.SelectionMetadata{}, time.Now(),
		)
		if resolution.accepted || resolution.terminalAttempt.Result == nil || auth.refreshes != 0 {
			t.Fatalf("non-unauthorized recovery resolution = %#v auth:%#v", resolution, auth)
		}
	})
}

func TestWebSocketPreVisibleRecoveryGuardsUnsafeTransitions(t *testing.T) {
	t.Run("disabled replay buffer is fatal only before visibility", func(t *testing.T) {
		withoutBuffer := &WebSocketSessionOrchestrator{}
		if bytes, replayed, err := withoutBuffer.replayBufferedMessages(context.Background(), nil, nil, webSocketRelayOptions{}); err != nil || replayed || bytes != 0 {
			t.Fatalf("nil replay buffer = bytes:%d replayed:%v error:%v", bytes, replayed, err)
		}

		disabled := newPreVisibleClientMessageBuffer(64)
		disabled.Disable()
		preVisible := &WebSocketSessionOrchestrator{replayBuffer: disabled, lifecycle: newWebSocketLifecycleState()}
		if _, _, err := preVisible.replayBufferedMessages(context.Background(), nil, nil, webSocketRelayOptions{}); err == nil {
			t.Fatal("disabled pre-visible replay buffer did not fail closed")
		}

		visible := &WebSocketSessionOrchestrator{replayBuffer: disabled, lifecycle: newWebSocketLifecycleState()}
		visible.lifecycle.MarkClientVisible()
		if bytes, replayed, err := visible.replayBufferedMessages(context.Background(), nil, nil, webSocketRelayOptions{}); err != nil || replayed || bytes != 0 {
			t.Fatalf("visible disabled replay buffer = bytes:%d replayed:%v error:%v", bytes, replayed, err)
		}
	})

	t.Run("suppressed payload retains reconstructable frame identity", func(t *testing.T) {
		provider := routingTestProvider("suppressed")
		orchestrator := &WebSocketSessionOrchestrator{}
		orchestrator.captureSuppressedAttempt(&provider, &webSocketRelaySessionResult{
			SuppressedUpstreamError: &WebSocketUpstreamError{Raw: `{"type":"error"}`},
		})
		if orchestrator.suppressedAttempt == nil || orchestrator.suppressedAttempt.messageType != websocket.MessageText {
			t.Fatalf("suppressed attempt = %#v", orchestrator.suppressedAttempt)
		}
		if got := string(orchestrator.suppressedAttempt.payload); got != `{"type":"error"}` {
			t.Fatalf("reconstructed payload = %q", got)
		}
	})

	t.Run("post-visible routing is pinned", func(t *testing.T) {
		switchable := &WebSocketUpstreamError{
			EventType:  "auth_error",
			Code:       "invalid_api_key",
			StatusCode: http.StatusUnauthorized,
		}
		nonSwitchable := &WebSocketUpstreamError{
			EventType:  "invalid_request_error",
			Code:       "invalid_request_error",
			StatusCode: http.StatusBadRequest,
		}
		orchestrator := &WebSocketSessionOrchestrator{
			suppressedAttempt: &webSocketSuppressedAttempt{upstreamError: switchable.Clone()},
		}

		if orchestrator.shouldSwitchProvider(WebSocketAttemptResult{}) {
			t.Fatal("nil attempt result switched provider")
		}
		if orchestrator.shouldSwitchProvider(WebSocketAttemptResult{Result: &WebSocketResult{}, ReplayFailed: true}) {
			t.Fatal("replay failure switched provider")
		}
		if orchestrator.shouldSwitchProvider(WebSocketAttemptResult{Result: &WebSocketResult{TerminalCause: model.TerminalCleanClose}}) {
			t.Fatal("clean pre-visible close switched provider")
		}

		for _, upstreamError := range []*WebSocketUpstreamError{nil, nonSwitchable, switchable} {
			visibleAttempt := WebSocketAttemptResult{Result: &WebSocketResult{
				ClientVisible: true, UpstreamError: upstreamError,
			}}
			orchestrator.switchTracker.continuityContext = &model.ProviderContinuityContext{}
			if orchestrator.shouldSwitchProvider(visibleAttempt) {
				t.Fatal("client-visible route target was handed off")
			}
		}

		internalFailure := WebSocketAttemptResult{Result: &WebSocketResult{
			ClientAccepted: true,
			TerminalCause:  model.TerminalInternalError,
		}}
		if orchestrator.shouldFallbackToSuppressedPayload(internalFailure) {
			t.Fatal("internal failure replayed a suppressed provider payload")
		}
	})
}

func TestWebSocketOrchestratorOwnershipGuardsAreNoOps(t *testing.T) {
	orchestrator := &WebSocketSessionOrchestrator{excludedProviders: make(map[string]bool)}
	if observer := orchestrator.newAttemptObserver(); observer != nil {
		t.Fatalf("observer without factory = %#v", observer)
	}
	orchestrator.applySessionLifecycleToResult(nil)
	orchestrator.excludeCurrentProvider()
	orchestrator.unregisterCurrentLease("no-owner")
	closeTerminalSuppressedClientConn(nil)
	if len(orchestrator.excludedProviders) != 0 || orchestrator.currentLease != nil || orchestrator.currentProvider != nil {
		t.Fatalf("ownership guards mutated state: %#v", orchestrator)
	}
}
