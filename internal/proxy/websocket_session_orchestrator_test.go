package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/selector"

	"github.com/coder/websocket"
	"go.uber.org/zap"
)

func TestWebSocketAttemptResult_ClientAcceptedUsesExplicitBoundary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		attempt WebSocketAttemptResult
		want    bool
	}{
		{
			name: "explicit client accept survives later relay error",
			attempt: WebSocketAttemptResult{
				Result: &WebSocketResult{
					HandshakeAccepted: true,
					ClientAccepted:    true,
				},
				ForwardErr: errors.New("relay terminated after accept"),
			},
			want: true,
		},
		{
			name: "legacy handshake fallback still reports accepted",
			attempt: WebSocketAttemptResult{
				Result: &WebSocketResult{
					HandshakeAccepted: true,
				},
			},
			want: true,
		},
		{
			name: "nil result stays not accepted",
			attempt: WebSocketAttemptResult{
				ForwardErr: errors.New("dial failed"),
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.attempt.clientAccepted(); got != tt.want {
				t.Fatalf("clientAccepted() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWebSocketAttemptResult_HelperProjectionFallbacks(t *testing.T) {
	t.Parallel()

	upstreamErr := errors.New("upstream failed")
	attempt := WebSocketAttemptResult{
		Result: &WebSocketResult{
			HandshakeAccepted: true,
			ClientAccepted:    true,
			UpstreamError: &WebSocketUpstreamError{
				StatusCode: http.StatusForbidden,
				Raw:        "semantic payload",
			},
			Err: upstreamErr,
		},
	}

	if got := attempt.statusCode(); got != http.StatusSwitchingProtocols {
		t.Fatalf("statusCode() = %d, want %d", got, http.StatusSwitchingProtocols)
	}
	if got := attempt.bodySnippet(); got != "semantic payload" {
		t.Fatalf("bodySnippet() = %q, want %q", got, "semantic payload")
	}
	if got := attempt.terminalErr(); !errors.Is(got, upstreamErr) {
		t.Fatalf("terminalErr() = %v, want %v", got, upstreamErr)
	}

	nilResult := WebSocketAttemptResult{}
	if got := nilResult.statusCode(); got != StatusCodeNoResponse {
		t.Fatalf("statusCode() with nil result = %d, want %d", got, StatusCodeNoResponse)
	}
	if got := nilResult.bodySnippet(); got != "" {
		t.Fatalf("bodySnippet() with nil result = %q, want empty", got)
	}
	if got := nilResult.terminalErr(); got != nil {
		t.Fatalf("terminalErr() with nil result = %v, want nil", got)
	}
}

func TestWebSocketAttemptResult_ShouldFailoverBeforeClientAccept(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		attempt WebSocketAttemptResult
		want    bool
	}{
		{
			name: "handshake rejection stays eligible",
			attempt: WebSocketAttemptResult{
				Result: &WebSocketResult{TerminalCause: model.TerminalUpstreamHandshakeRejected},
			},
			want: true,
		},
		{
			name: "transport failure before accept stays eligible",
			attempt: WebSocketAttemptResult{
				Result: &WebSocketResult{TerminalCause: model.TerminalUpstreamTransportError},
			},
			want: true,
		},
		{
			name: "provider configuration failure stays eligible",
			attempt: WebSocketAttemptResult{
				Result: &WebSocketResult{TerminalCause: model.TerminalProviderConfigurationError},
			},
			want: true,
		},
		{
			name: "accepted handshake closes pre-visible replacement window",
			attempt: WebSocketAttemptResult{
				Result: &WebSocketResult{HandshakeAccepted: true},
			},
			want: false,
		},
		{
			name: "transport crash without lifecycle result cannot switch",
			attempt: WebSocketAttemptResult{
				ForwardErr: errors.New("connection reset"),
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.attempt.shouldReplaceBeforeClientVisible(); got != tt.want {
				t.Fatalf("shouldReplaceBeforeClientVisible() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWebSocketSessionResult_RetryCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		attempts []WebSocketAttemptResult
		want     int
	}{
		{
			name: "no attempts means zero retries",
			want: 0,
		},
		{
			name: "single attempt means zero retries",
			attempts: []WebSocketAttemptResult{
				{Provider: &model.Provider{ID: "provider-a"}},
			},
			want: 0,
		},
		{
			name: "multiple attempts count retries beyond first",
			attempts: []WebSocketAttemptResult{
				{Provider: &model.Provider{ID: "provider-a"}},
				{Provider: &model.Provider{ID: "provider-b"}},
				{Provider: &model.Provider{ID: "provider-c"}},
			},
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			session := &WebSocketSessionResult{Attempts: tt.attempts}
			if got := session.RetryCount(); got != tt.want {
				t.Fatalf("RetryCount() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestWebSocketSessionResult_RequestAttemptsProjectsSemanticAttemptMetadata(t *testing.T) {
	t.Parallel()

	now := time.Now()
	session := &WebSocketSessionResult{
		RequestID: "req-ws",
		Attempts: []WebSocketAttemptResult{
			{
				Provider: &model.Provider{ID: "provider-a"},
				Attempt:  0,
				Result: &WebSocketResult{
					HandshakeAccepted: true,
					ClientAccepted:    true,
					TerminalCause:     model.TerminalUpstreamSemanticError,
					UpstreamError: &WebSocketUpstreamError{
						StatusCode: http.StatusForbidden,
						Raw:        `{"type":"error","error":{"type":"model_not_allowed"}}`,
					},
				},
				LatencyMs:    42,
				SwitchReason: "provider_scoped_semantic_error",
				CreatedAt:    now,
			},
			{
				Provider: &model.Provider{ID: "provider-b"},
				Attempt:  1,
				Result: &WebSocketResult{
					HandshakeAccepted: true,
					ClientAccepted:    true,
					ClientVisible:     true,
					SessionCommitted:  true,
					TerminalCause:     model.TerminalCleanClose,
				},
				LatencyMs: 84,
				CreatedAt: now.Add(time.Second),
			},
		},
	}

	attempts := session.RequestAttempts()
	if len(attempts) != 2 {
		t.Fatalf("RequestAttempts() count = %d, want 2", len(attempts))
	}

	if attempts[0].Phase == nil || *attempts[0].Phase != model.RequestAttemptPhasePostUpgradePreVisible {
		t.Fatalf("first Phase = %#v, want %q", attempts[0].Phase, model.RequestAttemptPhasePostUpgradePreVisible)
	}
	if attempts[0].Outcome == nil || *attempts[0].Outcome != model.RequestAttemptOutcomeUpstreamSemanticError {
		t.Fatalf("first Outcome = %#v, want %q", attempts[0].Outcome, model.RequestAttemptOutcomeUpstreamSemanticError)
	}
	if attempts[0].ResultVisibleToClient == nil || *attempts[0].ResultVisibleToClient {
		t.Fatalf("first ResultVisibleToClient = %#v, want false", attempts[0].ResultVisibleToClient)
	}
	if attempts[0].StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("first StatusCode = %d, want %d", attempts[0].StatusCode, http.StatusSwitchingProtocols)
	}
	if attempts[0].AttemptEvidenceJSON == nil || *attempts[0].AttemptEvidenceJSON == "" {
		t.Fatal("expected first AttemptEvidenceJSON to preserve suppressed semantic evidence")
	}
	if attempts[0].BodySnippet == "" {
		t.Fatal("expected first BodySnippet to preserve suppressed semantic payload")
	}

	if attempts[1].Phase == nil || *attempts[1].Phase != model.RequestAttemptPhaseVisible {
		t.Fatalf("second Phase = %#v, want %q", attempts[1].Phase, model.RequestAttemptPhaseVisible)
	}
	if attempts[1].Outcome == nil || *attempts[1].Outcome != model.RequestAttemptOutcomeVisibleSession {
		t.Fatalf("second Outcome = %#v, want %q", attempts[1].Outcome, model.RequestAttemptOutcomeVisibleSession)
	}
	if attempts[1].ResultVisibleToClient == nil || !*attempts[1].ResultVisibleToClient {
		t.Fatalf("second ResultVisibleToClient = %#v, want true", attempts[1].ResultVisibleToClient)
	}
	if attempts[1].StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("second StatusCode = %d, want %d", attempts[1].StatusCode, http.StatusSwitchingProtocols)
	}
}

func TestWebSocketSessionResult_RequestAttemptsProjectsPreAcceptFailures(t *testing.T) {
	t.Parallel()

	now := time.Now()
	handshakeRejected := WebSocketAttemptResult{
		Provider: &model.Provider{ID: "provider-handshake"},
		Attempt:  0,
		Result: &WebSocketResult{
			HandshakeStatusCode:  http.StatusUnauthorized,
			HandshakeBodySnippet: "denied",
			TerminalCause:        model.TerminalUpstreamHandshakeRejected,
			Err:                  errors.New("unauthorized"),
		},
		CreatedAt: now,
	}
	transportFailed := WebSocketAttemptResult{
		Provider:   &model.Provider{ID: "provider-transport"},
		Attempt:    1,
		ForwardErr: errors.New("dial timeout"),
		CreatedAt:  now.Add(time.Second),
	}

	session := &WebSocketSessionResult{
		RequestID: "req-pre-accept",
		Attempts:  []WebSocketAttemptResult{handshakeRejected, transportFailed},
	}

	attempts := session.RequestAttempts()
	if len(attempts) != 2 {
		t.Fatalf("RequestAttempts() count = %d, want 2", len(attempts))
	}

	if attempts[0].Phase == nil || *attempts[0].Phase != model.RequestAttemptPhasePreAccept {
		t.Fatalf("first Phase = %#v, want %q", attempts[0].Phase, model.RequestAttemptPhasePreAccept)
	}
	if attempts[0].Outcome == nil || *attempts[0].Outcome != model.RequestAttemptOutcomeUpstreamHandshakeRejected {
		t.Fatalf("first Outcome = %#v, want %q", attempts[0].Outcome, model.RequestAttemptOutcomeUpstreamHandshakeRejected)
	}
	if attempts[0].BodySnippet != "denied" {
		t.Fatalf("first BodySnippet = %q, want denied", attempts[0].BodySnippet)
	}
	if attempts[0].ResultVisibleToClient == nil || *attempts[0].ResultVisibleToClient {
		t.Fatalf("first ResultVisibleToClient = %#v, want false", attempts[0].ResultVisibleToClient)
	}

	if attempts[1].Phase == nil || *attempts[1].Phase != model.RequestAttemptPhasePreAccept {
		t.Fatalf("second Phase = %#v, want %q", attempts[1].Phase, model.RequestAttemptPhasePreAccept)
	}
	if attempts[1].Outcome == nil || *attempts[1].Outcome != model.RequestAttemptOutcomeUpstreamTransportError {
		t.Fatalf("second Outcome = %#v, want %q", attempts[1].Outcome, model.RequestAttemptOutcomeUpstreamTransportError)
	}
	if attempts[1].StatusCode != StatusCodeNoResponse {
		t.Fatalf("second StatusCode = %d, want %d", attempts[1].StatusCode, StatusCodeNoResponse)
	}
}

func TestNewWebSocketProviderConfigurationAttemptMapsGatewayFailure(t *testing.T) {
	t.Parallel()

	attempt := newWebSocketProviderConfigurationAttempt(
		&model.Provider{ID: "provider-config"},
		"codex",
		2,
		providerSwitchModeReplacement,
		selector.SelectionMetadata{},
		&webSocketProviderConfigError{
			missingField: "credentials",
			err:          errors.New("missing managed credential"),
		},
		2*time.Second,
	)

	if attempt.GatewayStatusCode != http.StatusBadGateway {
		t.Fatalf("GatewayStatusCode = %d, want %d", attempt.GatewayStatusCode, http.StatusBadGateway)
	}
	if attempt.GatewayErrorCode != ErrCodeWebSocketUpgrade {
		t.Fatalf("GatewayErrorCode = %q, want %q", attempt.GatewayErrorCode, ErrCodeWebSocketUpgrade)
	}
	if attempt.GatewayMessage == "" {
		t.Fatal("expected GatewayMessage to describe the missing provider field")
	}

	session := &WebSocketSessionResult{
		RequestID: "req-config",
		Attempts:  []WebSocketAttemptResult{attempt},
	}
	projected := session.RequestAttempts()
	if len(projected) != 1 {
		t.Fatalf("RequestAttempts() count = %d, want 1", len(projected))
	}
	if projected[0].Outcome == nil || *projected[0].Outcome != model.RequestAttemptOutcomeUpstreamTransportError {
		t.Fatalf("Outcome = %#v, want %q", projected[0].Outcome, model.RequestAttemptOutcomeUpstreamTransportError)
	}
}

func TestNewWebSocketSelectionFailureSessionPreservesGatewayMetadata(t *testing.T) {
	t.Parallel()

	attempts := []WebSocketAttemptResult{
		{
			Provider: &model.Provider{ID: "provider-a"},
			Attempt:  0,
		},
	}
	session := newWebSocketSelectionFailureSession(
		"req-selection-failure",
		true,
		attempts,
		http.StatusServiceUnavailable,
		model.TerminalProviderUnavailable,
		ErrCodeProviderUnavailable,
		"no providers available",
		internalErrNoProvider(),
	)

	if session.RequestID != "req-selection-failure" {
		t.Fatalf("RequestID = %q, want %q", session.RequestID, "req-selection-failure")
	}
	if !session.IsSticky {
		t.Fatal("expected sticky state to be preserved")
	}
	if session.GatewayStatusCode != http.StatusServiceUnavailable {
		t.Fatalf("GatewayStatusCode = %d, want %d", session.GatewayStatusCode, http.StatusServiceUnavailable)
	}
	if session.GatewayErrorCode != ErrCodeProviderUnavailable {
		t.Fatalf("GatewayErrorCode = %q, want %q", session.GatewayErrorCode, ErrCodeProviderUnavailable)
	}
	if session.GatewayMessage != "no providers available" {
		t.Fatalf("GatewayMessage = %q, want %q", session.GatewayMessage, "no providers available")
	}
	if session.FinalResult == nil || session.FinalResult.TerminalCause != model.TerminalProviderUnavailable {
		t.Fatalf("FinalResult.TerminalCause = %v, want %q", session.FinalResult.TerminalCause, model.TerminalProviderUnavailable)
	}
	if len(session.Attempts) != 1 || session.Attempts[0].Provider.ID != "provider-a" {
		t.Fatalf("Attempts = %#v, want preserved attempt slice", session.Attempts)
	}
}

func TestNewWebSocketForwardAttemptResultCapturesHandshakeGatewayFailure(t *testing.T) {
	t.Parallel()

	result := &WebSocketResult{
		HandshakeStatusCode:  http.StatusForbidden,
		HandshakeBodySnippet: "upstream denied",
		TerminalCause:        model.TerminalUpstreamHandshakeRejected,
		Err:                  errors.New("forbidden"),
	}
	attempt := newWebSocketForwardAttemptResult(
		&model.Provider{ID: "provider-a"},
		3,
		providerSwitchModeReplacement,
		selector.SelectionMetadata{},
		result,
		nil,
		1500*time.Millisecond,
	)

	if attempt.GatewayStatusCode != http.StatusForbidden {
		t.Fatalf("GatewayStatusCode = %d, want %d", attempt.GatewayStatusCode, http.StatusForbidden)
	}
	if attempt.GatewayErrorCode != ErrCodeWebSocketUpgrade {
		t.Fatalf("GatewayErrorCode = %q, want %q", attempt.GatewayErrorCode, ErrCodeWebSocketUpgrade)
	}
	if attempt.GatewayMessage != "upstream denied" {
		t.Fatalf("GatewayMessage = %q, want %q", attempt.GatewayMessage, "upstream denied")
	}
	if attempt.LatencyMs != 1500 {
		t.Fatalf("LatencyMs = %d, want 1500", attempt.LatencyMs)
	}
}

func TestWebSocketSessionOrchestrator_SelectionProbeDecision(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                      string
		apiType                   string
		req                       *model.SelectRequest
		configure                 func(*mockStore)
		newSelectionProbeObserver webSocketSelectionProbeObserverFactory
		probeOn                   bool
		want                      webSocketSelectionProbeDecision
		wantErr                   string
	}{
		{
			name:    "handshake model bypasses probe",
			apiType: APITypeCodex,
			req: &model.SelectRequest{
				APIType:    APITypeCodex,
				Model:      "handshake-model",
				StickyMode: model.StickyModeModel,
			},
			probeOn: true,
			want: webSocketSelectionProbeDecision{
				outcome: webSocketSelectionProbeOutcomeBypassed,
			},
		},
		{
			name:    "probe disabled bypasses hidden-model demand",
			apiType: APITypeCodex,
			req: &model.SelectRequest{
				APIType:    APITypeCodex,
				Model:      ModelUnknown,
				StickyMode: model.StickyModeModel,
			},
			probeOn: false,
			want: webSocketSelectionProbeDecision{
				outcome: webSocketSelectionProbeOutcomeBypassed,
			},
		},
		{
			name:    "no hidden-model consumer bypasses probe",
			apiType: APITypeCodex,
			req: &model.SelectRequest{
				APIType:    APITypeCodex,
				Model:      ModelUnknown,
				StickyMode: model.StickyModeOff,
			},
			probeOn: true,
			want: webSocketSelectionProbeDecision{
				outcome: webSocketSelectionProbeOutcomeBypassed,
			},
		},
		{
			name:    "unsupported replay-safe capability is explicit",
			apiType: "claude",
			req: &model.SelectRequest{
				APIType:    "claude",
				Model:      ModelUnknown,
				StickyMode: model.StickyModeModel,
			},
			probeOn: true,
			want: webSocketSelectionProbeDecision{
				outcome: webSocketSelectionProbeOutcomeUnsupported,
			},
		},
		{
			name:    "non-codex api can probe when capability seam supports it",
			apiType: "claude",
			req: &model.SelectRequest{
				APIType:    "claude",
				Model:      ModelUnknown,
				StickyMode: model.StickyModeModel,
			},
			newSelectionProbeObserver: func(apiType, initialModel string) WebSocketMessageObserver {
				if apiType != "claude" {
					return nil
				}
				return &stubObserver{snapshot: WebSocketObservation{Model: initialModel}}
			},
			probeOn: true,
			want: webSocketSelectionProbeDecision{
				outcome:     webSocketSelectionProbeOutcomeCompletedWithoutUsableModel,
				shouldProbe: true,
			},
		},
		{
			name:    "model sticky demand survives routing policy lookup failure",
			apiType: APITypeCodex,
			req: &model.SelectRequest{
				APIType:    APITypeCodex,
				Model:      ModelUnknown,
				StickyMode: model.StickyModeModel,
			},
			configure: func(store *mockStore) {
				store.routingPolicyErr = errors.New("routing policy store unavailable")
			},
			probeOn: true,
			want: webSocketSelectionProbeDecision{
				outcome:     webSocketSelectionProbeOutcomeCompletedWithoutUsableModel,
				shouldProbe: true,
			},
		},
		{
			name:    "routing policy model-only rule enables probe",
			apiType: APITypeCodex,
			req: &model.SelectRequest{
				APIType:    APITypeCodex,
				Model:      ModelUnknown,
				StickyMode: model.StickyModeOff,
			},
			configure: func(store *mockStore) {
				store.routingPolicies = []model.RoutingPolicy{
					{
						Enabled:         true,
						APIType:         APITypeCodex,
						ModelMatchType:  model.RoutingPolicyModelMatchTypePrefix,
						ModelMatchValue: "gpt-",
					},
				}
			},
			probeOn: true,
			want: webSocketSelectionProbeDecision{
				outcome:     webSocketSelectionProbeOutcomeCompletedWithoutUsableModel,
				shouldProbe: true,
			},
		},
		{
			name:    "routing policy lookup failure stays explicit when probe gating depends on it",
			apiType: APITypeCodex,
			req: &model.SelectRequest{
				APIType:    APITypeCodex,
				Model:      ModelUnknown,
				StickyMode: model.StickyModeOff,
			},
			configure: func(store *mockStore) {
				store.routingPolicyErr = errors.New("routing policy store unavailable")
			},
			probeOn: true,
			want: webSocketSelectionProbeDecision{
				outcome: webSocketSelectionProbeOutcomeDemandResolutionFailed,
			},
			wantErr: "routing policy store unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newMockStore()
			if tt.configure != nil {
				tt.configure(store)
			}
			orchestrator := newWebSocketSessionOrchestrator(&Handler{
				store:  store,
				logger: zap.NewNop(),
			}, webSocketSessionOrchestratorConfig{
				apiType:                   tt.apiType,
				selectReq:                 tt.req,
				probeClientModel:          tt.probeOn,
				newSelectionProbeObserver: tt.newSelectionProbeObserver,
			})

			got, err := orchestrator.selectionProbeDecision(context.Background())
			if got != tt.want {
				t.Fatalf("selectionProbeDecision() = %#v, want %#v", got, tt.want)
			}
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("selectionProbeDecision() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("selectionProbeDecision() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestWebSocketSessionOrchestrator_ProbeClientSelectionContextOutcomes(t *testing.T) {
	idleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		<-r.Context().Done()
	}))
	defer idleServer.Close()

	newClientConn := func(t *testing.T) *websocket.Conn {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		t.Cleanup(cancel)
		conn := connectWSClient(t, ctx, wsURL(idleServer))
		t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })
		return conn
	}

	t.Run("completed without usable model", func(t *testing.T) {
		ch := make(chan webSocketInitialReadResult, 1)
		ch <- webSocketInitialReadResult{
			messageType: websocket.MessageText,
			data:        []byte(`{"type":"response.create","response":{"instructions":"hello"}}`),
		}

		orchestrator := &WebSocketSessionOrchestrator{
			requestID:           "req-no-model",
			apiType:             APITypeCodex,
			clientConn:          newClientConn(t),
			initialClientReadCh: ch,
			replayBuffer:        newPreVisibleClientMessageBuffer(preVisibleClientReplayBufferLimitBytes),
			lifecycle:           newWebSocketLifecycleState(),
		}

		session, modelName, outcome := orchestrator.probeClientSelectionContext(context.Background())
		if session != nil {
			t.Fatalf("probeClientSelectionContext() session = %#v, want nil", session)
		}
		if modelName != "" {
			t.Fatalf("probeClientSelectionContext() model = %q, want empty", modelName)
		}
		if outcome != webSocketSelectionProbeOutcomeCompletedWithoutUsableModel {
			t.Fatalf("probeClientSelectionContext() outcome = %q, want %q", outcome, webSocketSelectionProbeOutcomeCompletedWithoutUsableModel)
		}
	})

	t.Run("transport failure is terminal", func(t *testing.T) {
		probeErr := io.EOF
		ch := make(chan webSocketInitialReadResult, 1)
		ch <- webSocketInitialReadResult{err: probeErr}

		orchestrator := &WebSocketSessionOrchestrator{
			requestID:           "req-transport-failure",
			apiType:             APITypeCodex,
			clientConn:          newClientConn(t),
			initialClientReadCh: ch,
			replayBuffer:        newPreVisibleClientMessageBuffer(preVisibleClientReplayBufferLimitBytes),
			lifecycle:           newWebSocketLifecycleState(),
		}
		orchestrator.lifecycle.MarkClientAccepted()

		session, modelName, outcome := orchestrator.probeClientSelectionContext(context.Background())
		if session == nil {
			t.Fatal("probeClientSelectionContext() session = nil, want terminal session")
		}
		if modelName != "" {
			t.Fatalf("probeClientSelectionContext() model = %q, want empty", modelName)
		}
		if outcome != webSocketSelectionProbeOutcomeTransportFailed {
			t.Fatalf("probeClientSelectionContext() outcome = %q, want %q", outcome, webSocketSelectionProbeOutcomeTransportFailed)
		}
		if session.ProbeOutcome != webSocketSelectionProbeOutcomeTransportFailed {
			t.Fatalf("session.ProbeOutcome = %q, want %q", session.ProbeOutcome, webSocketSelectionProbeOutcomeTransportFailed)
		}
		wantCause := classifyRelayTerminalCause(probeErr, webSocketPeerClient)
		if session.FinalResult == nil || session.FinalResult.TerminalCause != wantCause {
			t.Fatalf("FinalResult.TerminalCause = %v, want %q", session.FinalResult.TerminalCause, wantCause)
		}
		if !errors.Is(session.FinalErr, probeErr) {
			t.Fatalf("FinalErr = %v, want %v", session.FinalErr, probeErr)
		}
	})

	t.Run("custom capability supports non-codex probe", func(t *testing.T) {
		ch := make(chan webSocketInitialReadResult, 1)
		ch <- webSocketInitialReadResult{
			messageType: websocket.MessageText,
			data:        []byte(`{"type":"response.create","response":{"model":"claude-realtime","instructions":"hello"}}`),
		}

		orchestrator := &WebSocketSessionOrchestrator{
			requestID:           "req-custom-capability",
			apiType:             "claude",
			info:                RequestInfo{Model: ModelUnknown},
			clientConn:          newClientConn(t),
			initialClientReadCh: ch,
			replayBuffer:        newPreVisibleClientMessageBuffer(preVisibleClientReplayBufferLimitBytes),
			lifecycle:           newWebSocketLifecycleState(),
			newSelectionProbeObserver: func(apiType, initialModel string) WebSocketMessageObserver {
				if apiType != "claude" {
					return nil
				}
				return &stubObserver{snapshot: WebSocketObservation{Model: "claude-realtime"}}
			},
		}

		session, modelName, outcome := orchestrator.probeClientSelectionContext(context.Background())
		if session != nil {
			t.Fatalf("probeClientSelectionContext() session = %#v, want nil", session)
		}
		if modelName != "claude-realtime" {
			t.Fatalf("probeClientSelectionContext() model = %q, want %q", modelName, "claude-realtime")
		}
		if outcome != webSocketSelectionProbeOutcomeObservedUsableModel {
			t.Fatalf("probeClientSelectionContext() outcome = %q, want %q", outcome, webSocketSelectionProbeOutcomeObservedUsableModel)
		}
	})
}

func TestWebSocketSwitchReasonUsesPermanentStatusesAndTerminalCause(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		attempt WebSocketAttemptResult
		want    string
	}{
		{
			name: "permanent auth status uses canonical reason",
			attempt: WebSocketAttemptResult{
				Result: &WebSocketResult{
					HandshakeStatusCode: http.StatusUnauthorized,
				},
			},
			want: formatPermanentErrorReason(http.StatusUnauthorized),
		},
		{
			name: "usage-limit handshake uses canonical reason",
			attempt: WebSocketAttemptResult{
				Result: &WebSocketResult{
					HandshakeStatusCode: http.StatusTooManyRequests,
					HandshakeHeaders: http.Header{
						headerCodexPrimaryUsedPercent: []string{"100"},
						headerCodexPrimaryResetAt:     []string{strconv.FormatInt(time.Date(2026, time.March, 26, 12, 5, 0, 0, time.UTC).Unix(), 10)},
					},
					HandshakeBodySnippet: `{"type":"error","error":{"type":"usage_limit_reached","resets_at":` +
						strconv.FormatInt(time.Date(2026, time.March, 26, 12, 5, 0, 0, time.UTC).Unix(), 10) + `}}`,
					HandshakeObservedAt: time.Date(2026, time.March, 26, 12, 0, 0, 0, time.UTC),
				},
			},
			want: SwitchReasonUsageLimitReached,
		},
		{
			name: "other terminal causes fall back to lifecycle cause",
			attempt: WebSocketAttemptResult{
				Result: &WebSocketResult{
					ClientAccepted: true,
					TerminalCause:  model.TerminalUpstreamTransportError,
				},
			},
			want: string(model.TerminalUpstreamTransportError),
		},
		{
			name: "missing status and cause stays empty",
			attempt: WebSocketAttemptResult{
				Result: &WebSocketResult{},
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := websocketSwitchReason(tt.attempt); got != tt.want {
				t.Fatalf("websocketSwitchReason() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWebSocketSwitchReasonUsesProviderScopedSemanticConstant(t *testing.T) {
	t.Parallel()

	attempt := WebSocketAttemptResult{
		Result: &WebSocketResult{
			ClientAccepted: true,
			UpstreamError: &WebSocketUpstreamError{
				EventType:  "auth_error",
				Code:       "invalid_api_key",
				StatusCode: http.StatusUnauthorized,
			},
		},
	}
	if got := websocketSwitchReason(attempt); got != model.RequestAttemptSwitchReasonProviderScopedSemanticError {
		t.Fatalf("websocketSwitchReason() = %q, want %q", got, model.RequestAttemptSwitchReasonProviderScopedSemanticError)
	}
}

func TestWebSocketSwitchReasonUsesUsageLimitSwitchReasonForSemanticQuotaErrors(t *testing.T) {
	t.Parallel()

	observedAt := time.Date(2026, time.March, 26, 17, 0, 0, 0, time.UTC)
	resetAt := observedAt.Add(25 * time.Minute).Truncate(time.Second)
	attempt := WebSocketAttemptResult{
		Result: &WebSocketResult{
			ClientAccepted: true,
			UpstreamError: &WebSocketUpstreamError{
				EventType:  codexUsageLimitErrorType,
				StatusCode: http.StatusTooManyRequests,
				ObservedAt: observedAt,
				ResetAt:    &resetAt,
			},
		},
	}
	if got := websocketSwitchReason(attempt); got != SwitchReasonUsageLimitReached {
		t.Fatalf("websocketSwitchReason() = %q, want %q", got, SwitchReasonUsageLimitReached)
	}
}

func TestWebSocketSessionOrchestrator_AppliesLifecycleSnapshot(t *testing.T) {
	t.Parallel()

	orchestrator := &WebSocketSessionOrchestrator{
		lifecycle: newWebSocketLifecycleState(),
	}
	orchestrator.lifecycle.MarkClientAccepted()
	orchestrator.lifecycle.MarkClientVisible()

	result := &WebSocketResult{}
	orchestrator.applySessionLifecycleToResult(result)
	if !result.ClientAccepted || !result.ClientVisible {
		t.Fatalf("applySessionLifecycleToResult() = accepted:%v visible:%v, want true/true", result.ClientAccepted, result.ClientVisible)
	}

	attempt := WebSocketAttemptResult{Result: &WebSocketResult{}}
	orchestrator.applySessionLifecycleToAttempt(&attempt)
	if !attempt.Result.ClientAccepted || !attempt.Result.ClientVisible {
		t.Fatalf("applySessionLifecycleToAttempt() = accepted:%v visible:%v, want true/true", attempt.Result.ClientAccepted, attempt.Result.ClientVisible)
	}
}

func TestWebSocketSessionOrchestrator_ReplaysBufferedMessages(t *testing.T) {
	replayReceived := make(chan webSocketReplayMessage, 1)
	replayServer := newRecordingWSServer(t, replayReceived)
	defer replayServer.Close()

	orchestrator := &WebSocketSessionOrchestrator{
		suppressedAttempt: &webSocketSuppressedAttempt{
			provider: &model.Provider{ID: "provider-a"},
		},
		replayBuffer: newPreVisibleClientMessageBuffer(preVisibleClientReplayBufferLimitBytes),
	}
	orchestrator.replayBuffer.Record(websocket.MessageText, []byte("replay me"), false)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn := connectWSClient(t, ctx, wsURL(replayServer))
	defer conn.Close(websocket.StatusNormalClosure, "")

	replayedBytes, replayed, err := orchestrator.replayBufferedMessages(
		ctx,
		conn,
		newBytesTrackingObserver(newCodexWebSocketMessageObserver(ModelUnknown, nil, nil, nil), &LiveBytesTracker{}),
	)
	if err != nil {
		t.Fatalf("replayBufferedMessages() error = %v", err)
	}
	if !replayed {
		t.Fatal("replayBufferedMessages() = false, want true")
	}
	if replayedBytes != int64(len("replay me")) {
		t.Fatalf("replayBufferedMessages() bytes = %d, want %d", replayedBytes, len("replay me"))
	}

	select {
	case message := <-replayReceived:
		if message.MessageType != websocket.MessageText || string(message.Data) != "replay me" {
			t.Fatalf("replayed message = %#v, want text/replay me", message)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for replayed message")
	}
}

func TestWebSocketSessionOrchestrator_ReplayBufferedMessages_EmptyBufferIsNoOp(t *testing.T) {
	t.Parallel()

	orchestrator := &WebSocketSessionOrchestrator{
		suppressedAttempt: &webSocketSuppressedAttempt{
			provider: &model.Provider{ID: "provider-a"},
		},
		replayBuffer: newPreVisibleClientMessageBuffer(preVisibleClientReplayBufferLimitBytes),
	}

	replayedBytes, replayed, err := orchestrator.replayBufferedMessages(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("replayBufferedMessages() error = %v, want nil when no client messages were buffered", err)
	}
	if replayed {
		t.Fatal("replayBufferedMessages() = true, want false when there is nothing to replay")
	}
	if replayedBytes != 0 {
		t.Fatalf("replayBufferedMessages() bytes = %d, want 0 when there is nothing to replay", replayedBytes)
	}
}

func TestWebSocketSessionOrchestrator_SwitchAndFallbackPredicates(t *testing.T) {
	t.Parallel()

	suppressed := &webSocketSuppressedAttempt{
		provider: &model.Provider{ID: "provider-origin"},
		upstreamError: &WebSocketUpstreamError{
			EventType:  "auth_error",
			Code:       "invalid_api_key",
			StatusCode: http.StatusUnauthorized,
		},
	}
	orchestrator := &WebSocketSessionOrchestrator{
		suppressedAttempt: suppressed,
	}

	semanticAttempt := WebSocketAttemptResult{
		Result: &WebSocketResult{
			ClientAccepted: true,
			TerminalCause:  model.TerminalUpstreamSemanticError,
			UpstreamError:  suppressed.upstreamError.Clone(),
		},
	}
	if !orchestrator.shouldSwitchProvider(semanticAttempt) {
		t.Fatal("shouldSwitchProvider() = false, want true for suppressed provider-scoped semantic error")
	}

	genericProviderAttempt := WebSocketAttemptResult{
		Result: &WebSocketResult{
			ClientAccepted: true,
			TerminalCause:  model.TerminalUpstreamSemanticError,
			UpstreamError: &WebSocketUpstreamError{
				EventType:  "error",
				StatusCode: http.StatusInternalServerError,
				Message:    "upstream crashed",
			},
		},
	}
	if !orchestrator.shouldSwitchProvider(genericProviderAttempt) {
		t.Fatal("shouldSwitchProvider() = false, want true for generic provider-scoped semantic 5xx")
	}

	orchestrator.suppressedAttempt = &webSocketSuppressedAttempt{provider: suppressed.provider}
	fallbackAttempt := WebSocketAttemptResult{
		Result: &WebSocketResult{
			ClientAccepted: true,
			TerminalCause:  model.TerminalUpstreamSemanticError,
			UpstreamError: &WebSocketUpstreamError{
				EventType:  "invalid_request_error",
				Code:       "invalid_request_error",
				StatusCode: http.StatusBadRequest,
			},
		},
	}
	if !orchestrator.shouldFallbackToSuppressedPayload(fallbackAttempt) {
		t.Fatal("shouldFallbackToSuppressedPayload() = false, want true after fallback attempt stays pre-visible")
	}

	replayFailed := WebSocketAttemptResult{
		Result: &WebSocketResult{
			ClientAccepted: true,
			TerminalCause:  model.TerminalUpstreamTransportError,
		},
		ReplayFailed: true,
	}
	if !orchestrator.shouldFallbackToSuppressedPayload(replayFailed) {
		t.Fatal("shouldFallbackToSuppressedPayload() = false, want true after replay failure")
	}

	orchestrator.suppressedAttempt = nil
	if orchestrator.shouldSwitchProvider(semanticAttempt) {
		t.Fatal("shouldSwitchProvider() = true, want false without suppressed attempt context")
	}
}
