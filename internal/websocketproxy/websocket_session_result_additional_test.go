package websocketproxy

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/selector"

	"github.com/coder/websocket"
)

func TestWebSocketSessionResultRequestAttemptsSkipsNilProviderAndDerivesHandshakeRejectedOutcome(t *testing.T) {
	t.Parallel()

	createdAt := time.Unix(1_700_000_000, 0)
	session := &WebSocketSessionResult{
		RequestID: "req-attempts",
		Attempts: []WebSocketAttemptResult{
			{
				Attempt: 0,
				Result: &WebSocketResult{
					HandshakeAccepted: true,
					ClientAccepted:    true,
				},
			},
			{
				Provider: &model.Provider{ID: "provider-1"},
				Attempt:  1,
				Result: &WebSocketResult{
					HandshakeStatusCode:  http.StatusBadGateway,
					HandshakeBodySnippet: "upstream refused websocket",
				},
				CreatedAt: createdAt,
			},
		},
	}

	attempts := session.RequestAttempts()
	if len(attempts) != 1 {
		t.Fatalf("len(RequestAttempts()) = %d, want 1", len(attempts))
	}

	attempt := attempts[0]
	if attempt.RequestID != session.RequestID {
		t.Fatalf("RequestID = %q, want %q", attempt.RequestID, session.RequestID)
	}
	if attempt.ProviderID != "provider-1" {
		t.Fatalf("ProviderID = %q, want provider-1", attempt.ProviderID)
	}
	if attempt.StatusCode != http.StatusBadGateway {
		t.Fatalf("StatusCode = %d, want %d", attempt.StatusCode, http.StatusBadGateway)
	}
	if attempt.BodySnippet != "upstream refused websocket" {
		t.Fatalf("BodySnippet = %q, want upstream refused websocket", attempt.BodySnippet)
	}
	if attempt.CreatedAt != createdAt {
		t.Fatalf("CreatedAt = %v, want %v", attempt.CreatedAt, createdAt)
	}
	if attempt.Outcome == nil || *attempt.Outcome != model.RequestAttemptOutcomeUpstreamHandshakeRejected {
		t.Fatalf("Outcome = %#v, want %q", attempt.Outcome, model.RequestAttemptOutcomeUpstreamHandshakeRejected)
	}
	if attempt.ResultVisibleToClient == nil || *attempt.ResultVisibleToClient {
		t.Fatalf("ResultVisibleToClient = %#v, want pointer to false", attempt.ResultVisibleToClient)
	}
}

func TestWebSocketSessionOrchestratorFinalSessionFromLastAttemptEmitsGatewayErrorWhenClientAccepted(t *testing.T) {
	t.Parallel()

	resultCh := make(chan *WebSocketSessionResult, 1)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientConn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}

		lifecycle := newWebSocketLifecycleState()
		lifecycle.MarkClientAccepted()

		orchestrator := &WebSocketSessionOrchestrator{
			requestID:  "req-no-provider",
			apiType:    "openai",
			isSticky:   true,
			lifecycle:  lifecycle,
			clientConn: clientConn,
		}
		resultCh <- orchestrator.finalSessionFromLastAttempt(r.Context())
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))
	defer clientConn.Close(websocket.StatusNormalClosure, "")

	envelope := readTerminalGatewayErrorEvent(t, ctx, clientConn, http.StatusServiceUnavailable, ErrCodeProviderUnavailable)
	wantMessage := "No available provider for api_type: openai"
	if envelope.Error.Message != wantMessage {
		t.Fatalf("gateway message = %q, want %q", envelope.Error.Message, wantMessage)
	}

	select {
	case session := <-resultCh:
		if !errors.Is(session.FinalErr, internal.ErrNoProvider) {
			t.Fatalf("FinalErr = %v, want ErrNoProvider", session.FinalErr)
		}
		if session.FinalResult == nil {
			t.Fatal("FinalResult = nil, want gateway failure result")
		}
		if !session.ClientAccepted || !session.FinalResult.ClientAccepted {
			t.Fatalf("client accepted flags = (%v, %v), want true/true", session.ClientAccepted, session.FinalResult.ClientAccepted)
		}
		if session.FinalResult.ClientVisible {
			t.Fatal("FinalResult.ClientVisible = true, want false because gateway terminal events are not upstream visibility")
		}
		if session.FinalResult.BytesUpstreamToClient != 0 {
			t.Fatalf("BytesUpstreamToClient = %d, want 0 for gateway-owned terminal event", session.FinalResult.BytesUpstreamToClient)
		}
		if session.GatewayStatusCode != http.StatusServiceUnavailable {
			t.Fatalf("GatewayStatusCode = %d, want %d", session.GatewayStatusCode, http.StatusServiceUnavailable)
		}
		if session.GatewayErrorCode != ErrCodeProviderUnavailable {
			t.Fatalf("GatewayErrorCode = %q, want %q", session.GatewayErrorCode, ErrCodeProviderUnavailable)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for session result")
	}

	if _, _, err := clientConn.Read(ctx); err == nil || (!errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) && !isNormalClose(err)) {
		t.Fatalf("expected websocket close after terminal gateway event, got %v", err)
	}
}

func TestWebSocketSessionOrchestratorFinalizeSelectionFailureSessionHandlesNilInputs(t *testing.T) {
	t.Parallel()

	var nilOrchestrator *WebSocketSessionOrchestrator
	session := &WebSocketSessionResult{RequestID: "req-nil-guard"}
	if got := nilOrchestrator.finalizeSelectionFailureSession(session); got != session {
		t.Fatalf("nil receiver returned %#v, want original session", got)
	}

	orchestrator := &WebSocketSessionOrchestrator{}
	if got := orchestrator.finalizeSelectionFailureSession(nil); got != nil {
		t.Fatalf("finalizeSelectionFailureSession(nil) = %#v, want nil", got)
	}
}

func TestWebSocketSessionOrchestratorEmitTerminalGatewayErrorDoesNotMutateResultOnClosedClient(t *testing.T) {
	t.Parallel()

	resultCh := make(chan *WebSocketResult, 1)
	orchestratorCh := make(chan *WebSocketSessionOrchestrator, 1)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientConn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}

		orchestrator := &WebSocketSessionOrchestrator{
			clientConn: clientConn,
			lifecycle:  newWebSocketLifecycleState(),
		}
		orchestratorCh <- orchestrator
		if err := orchestrator.clientConn.CloseNow(); err != nil {
			t.Errorf("CloseNow() error = %v", err)
			return
		}

		result := &WebSocketResult{
			Err:           errors.New("original failure"),
			TerminalCause: model.TerminalUpstreamHandshakeRejected,
		}
		_ = orchestrator.emitTerminalGatewayErrorIfNeeded(result, http.StatusBadGateway, ErrCodeWebSocketUpgrade, "terminal failure")
		resultCh <- result
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))
	defer clientConn.CloseNow()
	orchestrator := <-orchestratorCh

	select {
	case result := <-resultCh:
		if result.Err == nil || result.Err.Error() != "original failure" {
			t.Fatalf("Result.Err = %v, want original failure preserved", result.Err)
		}
		if result.TerminalCause != model.TerminalUpstreamHandshakeRejected {
			t.Fatalf("TerminalCause = %q, want %q", result.TerminalCause, model.TerminalUpstreamHandshakeRejected)
		}
		if orchestrator.clientConn != nil {
			t.Fatal("clientConn should be cleared after failed terminal event write")
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for terminal event result")
	}
}

func TestWebSocketSessionOrchestratorSessionFromAttemptKeepsProviderAttemptInvisibleAfterGatewayErrorDelivery(t *testing.T) {
	t.Parallel()

	sessionCh := make(chan *WebSocketSessionResult, 1)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientConn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}

		providerErr := errors.New("upstream denied")
		attempt := WebSocketAttemptResult{
			Provider: &model.Provider{ID: "provider-1"},
			Attempt:  0,
			Result: &WebSocketResult{
				ClientAccepted:       true,
				HandshakeStatusCode:  http.StatusForbidden,
				HandshakeBodySnippet: "upstream denied",
				TerminalCause:        model.TerminalUpstreamHandshakeRejected,
				Err:                  providerErr,
			},
			GatewayStatusCode: http.StatusForbidden,
			GatewayErrorCode:  ErrCodeWebSocketUpgrade,
			GatewayMessage:    "upstream denied",
		}
		orchestrator := &WebSocketSessionOrchestrator{
			requestID:  "req-attempt-visible-boundary",
			lifecycle:  newWebSocketLifecycleState(),
			clientConn: clientConn,
			attempts:   []WebSocketAttemptResult{attempt},
		}
		sessionCh <- orchestrator.sessionFromAttempt(attempt)
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))
	defer clientConn.Close(websocket.StatusNormalClosure, "")

	envelope := readTerminalGatewayErrorEvent(t, ctx, clientConn, http.StatusForbidden, ErrCodeWebSocketUpgrade)
	if envelope.Error.Message != "upstream denied" {
		t.Fatalf("gateway message = %q, want upstream denied", envelope.Error.Message)
	}

	select {
	case session := <-sessionCh:
		if session.FinalResult == nil {
			t.Fatal("FinalResult = nil, want cloned provider result")
		}
		if session.FinalResult == session.Attempts[0].Result {
			t.Fatal("FinalResult aliases attempt result, want isolated snapshot")
		}
		if session.FinalErr == nil || session.FinalErr.Error() != "upstream denied" {
			t.Fatalf("FinalErr = %v, want upstream denied", session.FinalErr)
		}
		if session.FinalResult.ClientVisible {
			t.Fatal("FinalResult.ClientVisible = true, want false for gateway terminal event")
		}
		if session.FinalResult.BytesUpstreamToClient != 0 {
			t.Fatalf("BytesUpstreamToClient = %d, want 0 for provider attempt", session.FinalResult.BytesUpstreamToClient)
		}
		if session.Attempts[0].Result == nil || session.Attempts[0].Result.ClientVisible {
			t.Fatalf("attempt result = %#v, want provider attempt to remain invisible", session.Attempts[0].Result)
		}
		projected := session.RequestAttempts()
		if len(projected) != 1 {
			t.Fatalf("len(RequestAttempts()) = %d, want 1", len(projected))
		}
		if projected[0].Phase == nil || *projected[0].Phase != model.RequestAttemptPhasePostUpgradePreVisible {
			t.Fatalf("Phase = %#v, want %q", projected[0].Phase, model.RequestAttemptPhasePostUpgradePreVisible)
		}
		if projected[0].Outcome == nil || *projected[0].Outcome != model.RequestAttemptOutcomeUpstreamHandshakeRejected {
			t.Fatalf("Outcome = %#v, want %q", projected[0].Outcome, model.RequestAttemptOutcomeUpstreamHandshakeRejected)
		}
		if projected[0].ResultVisibleToClient == nil || *projected[0].ResultVisibleToClient {
			t.Fatalf("ResultVisibleToClient = %#v, want false", projected[0].ResultVisibleToClient)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for session result")
	}
}

func TestWebSocketSessionOrchestratorFinalizeSelectionFailureSessionLeavesRecoveryActionUnsetForNeverStartedOutcome(t *testing.T) {
	t.Parallel()

	attemptResult := &WebSocketResult{
		TerminalCause: model.TerminalUpstreamSemanticError,
	}
	orchestrator := &WebSocketSessionOrchestrator{
		lifecycle: newWebSocketLifecycleState(),
	}
	session := newWebSocketSelectionFailureSession(
		"req-transparent-retry",
		true,
		[]WebSocketAttemptResult{{
			Provider:     &model.Provider{ID: "provider-1"},
			Attempt:      0,
			Result:       attemptResult,
			SwitchReason: SwitchReasonUsageLimitReached,
		}},
		http.StatusServiceUnavailable,
		model.TerminalProviderUnavailable,
		ErrCodeProviderUnavailable,
		"no providers remain",
		internal.ErrNoProvider,
	)

	finalized := orchestrator.finalizeSelectionFailureSession(session)
	if finalized.FinalResult == nil {
		t.Fatal("FinalResult = nil, want session lifecycle result")
	}
	if finalized.FinalResult.RecoveryAction != model.RecoveryActionNone {
		t.Fatalf("RecoveryAction = %q, want %q", finalized.FinalResult.RecoveryAction, model.RecoveryActionNone)
	}
	if finalized.Attempts[0].Result == nil {
		t.Fatal("attempt Result = nil, want original provider attempt")
	}
	if finalized.Attempts[0].Result.RecoveryAction != "" {
		t.Fatalf("attempt RecoveryAction = %q, want empty provider-attempt lifecycle", finalized.Attempts[0].Result.RecoveryAction)
	}
}

func TestWebSocketSessionOrchestratorSessionFromAttemptEmitsReconnectRequiredGatewayError(t *testing.T) {
	t.Parallel()

	sessionCh := make(chan *WebSocketSessionResult, 1)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientConn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}

		providerErr := errors.New("quota exhausted")
		resetAt := time.Date(2026, time.March, 26, 16, 0, 0, 0, time.UTC)
		attempt := WebSocketAttemptResult{
			Provider: &model.Provider{ID: "provider-1"},
			Attempt:  0,
			Result: &WebSocketResult{
				ClientAccepted: true,
				ClientVisible:  true,
				TerminalCause:  model.TerminalUpstreamSemanticError,
				Err:            providerErr,
				UpstreamError: &WebSocketUpstreamError{
					EventType:  codexUsageLimitErrorType,
					StatusCode: http.StatusTooManyRequests,
					ObservedAt: resetAt.Add(-15 * time.Minute),
					ResetAt:    &resetAt,
					Raw:        `{"type":"error"}`,
				},
			},
		}
		orchestrator := &WebSocketSessionOrchestrator{
			requestID:  "req-attempt-reconnect",
			lifecycle:  newWebSocketLifecycleState(),
			clientConn: clientConn,
			attempts:   []WebSocketAttemptResult{attempt},
		}
		sessionCh <- orchestrator.sessionFromAttempt(attempt)
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))
	defer clientConn.Close(websocket.StatusNormalClosure, "")

	envelope := readTerminalGatewayErrorEvent(
		t,
		ctx,
		clientConn,
		webSocketReconnectRequiredStatusCode,
		ErrCodeWebSocketReconnect,
	)
	if envelope.Error.Message != webSocketReconnectRequiredMessage {
		t.Fatalf("gateway message = %q, want %q", envelope.Error.Message, webSocketReconnectRequiredMessage)
	}

	select {
	case session := <-sessionCh:
		if session.FinalResult == nil {
			t.Fatal("FinalResult = nil, want cloned provider result")
		}
		if session.FinalResult.RecoveryAction != model.RecoveryActionReconnectRequired {
			t.Fatalf("RecoveryAction = %q, want %q", session.FinalResult.RecoveryAction, model.RecoveryActionReconnectRequired)
		}
		if !session.FinalResult.ClientVisible {
			t.Fatal("FinalResult.ClientVisible = false, want true for post-visible reconnect-required termination")
		}
		if session.GatewayStatusCode != webSocketReconnectRequiredStatusCode {
			t.Fatalf("GatewayStatusCode = %d, want %d", session.GatewayStatusCode, webSocketReconnectRequiredStatusCode)
		}
		if session.GatewayErrorCode != ErrCodeWebSocketReconnect {
			t.Fatalf("GatewayErrorCode = %q, want %q", session.GatewayErrorCode, ErrCodeWebSocketReconnect)
		}
		if session.Attempts[0].Result == nil || !session.Attempts[0].Result.ClientVisible {
			t.Fatalf("attempt result = %#v, want provider attempt to remain client-visible", session.Attempts[0].Result)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for reconnect-required session")
	}

	if _, _, err := clientConn.Read(ctx); err == nil || (!errors.Is(err, io.EOF) && !isNormalClose(err) && !errors.Is(err, net.ErrClosed)) {
		t.Fatalf("expected websocket close after reconnect-required gateway error, got %v", err)
	}
}

func TestWebSocketSessionOrchestratorSessionFromAttemptPreservesProviderFailureWhenTerminalGatewayWriteFails(t *testing.T) {
	t.Parallel()

	sessionCh := make(chan *WebSocketSessionResult, 1)
	orchestratorCh := make(chan *WebSocketSessionOrchestrator, 1)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientConn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}

		providerErr := errors.New("missing managed credential")
		attempt := WebSocketAttemptResult{
			Provider: &model.Provider{ID: "provider-1"},
			Attempt:  0,
			Result: &WebSocketResult{
				ClientAccepted: true,
				TerminalCause:  model.TerminalProviderConfigurationError,
				Err:            providerErr,
			},
			ForwardErr:        providerErr,
			GatewayStatusCode: http.StatusBadGateway,
			GatewayErrorCode:  ErrCodeWebSocketUpgrade,
			GatewayMessage:    `Provider "provider-1" is not ready for websocket "openai": credentials`,
		}
		orchestrator := &WebSocketSessionOrchestrator{
			requestID:  "req-attempt-write-fail",
			lifecycle:  newWebSocketLifecycleState(),
			clientConn: clientConn,
			attempts:   []WebSocketAttemptResult{attempt},
		}
		orchestratorCh <- orchestrator
		if err := orchestrator.clientConn.CloseNow(); err != nil {
			t.Errorf("CloseNow() error = %v", err)
			return
		}
		sessionCh <- orchestrator.sessionFromAttempt(attempt)
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))
	defer clientConn.CloseNow()
	orchestrator := <-orchestratorCh

	select {
	case session := <-sessionCh:
		if !errors.Is(session.FinalErr, session.Attempts[0].ForwardErr) {
			t.Fatalf("FinalErr = %v, want original provider failure %v", session.FinalErr, session.Attempts[0].ForwardErr)
		}
		if session.FinalResult == nil {
			t.Fatal("FinalResult = nil, want cloned provider result")
		}
		if !errors.Is(session.FinalResult.Err, session.Attempts[0].ForwardErr) {
			t.Fatalf("FinalResult.Err = %v, want original provider failure %v", session.FinalResult.Err, session.Attempts[0].ForwardErr)
		}
		if session.FinalResult.TerminalCause != model.TerminalProviderConfigurationError {
			t.Fatalf("TerminalCause = %q, want %q", session.FinalResult.TerminalCause, model.TerminalProviderConfigurationError)
		}
		if session.FinalResult == session.Attempts[0].Result {
			t.Fatal("FinalResult aliases attempt result, want isolated snapshot")
		}
		projected := session.RequestAttempts()
		if len(projected) != 1 {
			t.Fatalf("len(RequestAttempts()) = %d, want 1", len(projected))
		}
		if projected[0].Error != "missing managed credential" {
			t.Fatalf("Error = %q, want original provider failure", projected[0].Error)
		}
		if projected[0].Outcome == nil || *projected[0].Outcome != model.RequestAttemptOutcomeUpstreamTransportError {
			t.Fatalf("Outcome = %#v, want %q", projected[0].Outcome, model.RequestAttemptOutcomeUpstreamTransportError)
		}
		if projected[0].ResultVisibleToClient == nil || *projected[0].ResultVisibleToClient {
			t.Fatalf("ResultVisibleToClient = %#v, want false", projected[0].ResultVisibleToClient)
		}
		if orchestrator.clientConn != nil {
			t.Fatal("clientConn should be cleared after failed terminal event write")
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for session result")
	}
}

func TestNewWebSocketProbeDecisionFailureSessionPreservesSelectionFailureShape(t *testing.T) {
	t.Parallel()

	probeErr := errors.New("probe demand resolution failed")
	attempts := []WebSocketAttemptResult{
		{
			Provider: &model.Provider{ID: "provider-1"},
			Attempt:  1,
		},
	}

	session := newWebSocketProbeDecisionFailureSession(
		"req-probe",
		true,
		attempts,
		webSocketSelectionProbeOutcomeDemandResolutionFailed,
		probeErr,
	)
	if session == nil {
		t.Fatal("newWebSocketProbeDecisionFailureSession() = nil, want session")
	}
	if session.RequestID != "req-probe" {
		t.Fatalf("RequestID = %q, want %q", session.RequestID, "req-probe")
	}
	if !session.IsSticky {
		t.Fatal("IsSticky = false, want true")
	}
	if session.ProbeOutcome != webSocketSelectionProbeOutcomeDemandResolutionFailed {
		t.Fatalf("ProbeOutcome = %q, want %q", session.ProbeOutcome, webSocketSelectionProbeOutcomeDemandResolutionFailed)
	}
	if session.GatewayStatusCode != http.StatusInternalServerError {
		t.Fatalf("GatewayStatusCode = %d, want %d", session.GatewayStatusCode, http.StatusInternalServerError)
	}
	if session.GatewayErrorCode != ErrCodeInternalError {
		t.Fatalf("GatewayErrorCode = %q, want %q", session.GatewayErrorCode, ErrCodeInternalError)
	}
	if session.GatewayMessage != webSocketProbeDemandResolutionFailureMessage {
		t.Fatalf("GatewayMessage = %q, want %q", session.GatewayMessage, webSocketProbeDemandResolutionFailureMessage)
	}
	if !errors.Is(session.FinalErr, probeErr) {
		t.Fatalf("FinalErr = %v, want %v", session.FinalErr, probeErr)
	}
	if session.FinalResult == nil {
		t.Fatal("FinalResult = nil, want gateway failure result")
	}
	if session.FinalResult.TerminalCause != model.TerminalInternalError {
		t.Fatalf("FinalResult.TerminalCause = %q, want %q", session.FinalResult.TerminalCause, model.TerminalInternalError)
	}
	if session.FinalResult.HandshakeStatusCode != http.StatusInternalServerError {
		t.Fatalf("FinalResult.HandshakeStatusCode = %d, want %d", session.FinalResult.HandshakeStatusCode, http.StatusInternalServerError)
	}

	attempts[0].Attempt = 99
	if session.Attempts[0].Attempt != 1 {
		t.Fatalf("session attempts mutated to %d after caller slice change, want preserved copy", session.Attempts[0].Attempt)
	}
}

func TestPopulateCanonicalWebSocketGatewayMetadataCoversCanonicalFallbacks(t *testing.T) {
	t.Parallel()

	t.Run("nil guards leave session unchanged", func(t *testing.T) {
		t.Parallel()

		populateCanonicalWebSocketGatewayMetadata(nil)

		session := &WebSocketSessionResult{}
		populateCanonicalWebSocketGatewayMetadata(session)
		if session.GatewayStatusCode != 0 || session.GatewayErrorCode != "" || session.GatewayMessage != "" {
			t.Fatalf("session = %#v, want zero-value gateway metadata", session)
		}
	})

	t.Run("reconnect required overrides gateway metadata", func(t *testing.T) {
		t.Parallel()

		session := &WebSocketSessionResult{
			FinalResult: &WebSocketResult{
				ClientVisible:  true,
				RecoveryAction: model.RecoveryActionReconnectRequired,
				TerminalCause:  model.TerminalUpstreamSemanticError,
			},
			GatewayStatusCode: http.StatusForbidden,
			GatewayErrorCode:  "IGNORED",
			GatewayMessage:    "ignored",
		}

		populateCanonicalWebSocketGatewayMetadata(session)
		if session.GatewayStatusCode != webSocketReconnectRequiredStatusCode {
			t.Fatalf("GatewayStatusCode = %d, want %d", session.GatewayStatusCode, webSocketReconnectRequiredStatusCode)
		}
		if session.GatewayErrorCode != ErrCodeWebSocketReconnect {
			t.Fatalf("GatewayErrorCode = %q, want %q", session.GatewayErrorCode, ErrCodeWebSocketReconnect)
		}
		if session.GatewayMessage != webSocketReconnectRequiredMessage {
			t.Fatalf("GatewayMessage = %q, want %q", session.GatewayMessage, webSocketReconnectRequiredMessage)
		}
	})

	t.Run("existing gateway status backfills canonical code and message", func(t *testing.T) {
		t.Parallel()

		configErr := errors.New("missing managed credential")
		session := &WebSocketSessionResult{
			FinalResult: &WebSocketResult{
				TerminalCause: model.TerminalProviderConfigurationError,
				Err:           configErr,
			},
			GatewayStatusCode: http.StatusBadGateway,
		}

		populateCanonicalWebSocketGatewayMetadata(session)
		if session.GatewayErrorCode != ErrCodeWebSocketUpgrade {
			t.Fatalf("GatewayErrorCode = %q, want %q", session.GatewayErrorCode, ErrCodeWebSocketUpgrade)
		}
		if session.GatewayMessage != configErr.Error() {
			t.Fatalf("GatewayMessage = %q, want %q", session.GatewayMessage, configErr.Error())
		}
	})

	t.Run("client visible failures do not synthesize pre-visible gateway errors", func(t *testing.T) {
		t.Parallel()

		session := &WebSocketSessionResult{
			FinalResult: &WebSocketResult{ClientVisible: true},
		}

		populateCanonicalWebSocketGatewayMetadata(session)
		if session.GatewayStatusCode != 0 || session.GatewayErrorCode != "" || session.GatewayMessage != "" {
			t.Fatalf("session = %#v, want client-visible failures to keep empty gateway metadata", session)
		}
	})

	t.Run("pre-visible failures use the canonical gateway fallback", func(t *testing.T) {
		t.Parallel()

		session := &WebSocketSessionResult{
			FinalResult: &WebSocketResult{
				TerminalCause: model.TerminalUpstreamTransportError,
				Err:           errors.New("transport down"),
			},
		}

		populateCanonicalWebSocketGatewayMetadata(session)
		if session.GatewayStatusCode != webSocketPreVisibleFailureStatusCode {
			t.Fatalf("GatewayStatusCode = %d, want %d", session.GatewayStatusCode, webSocketPreVisibleFailureStatusCode)
		}
		if session.GatewayErrorCode != ErrCodeWebSocketUpgrade {
			t.Fatalf("GatewayErrorCode = %q, want %q", session.GatewayErrorCode, ErrCodeWebSocketUpgrade)
		}
		if session.GatewayMessage != webSocketPreVisibleFailureMessage {
			t.Fatalf("GatewayMessage = %q, want %q", session.GatewayMessage, webSocketPreVisibleFailureMessage)
		}
	})
}

func TestCanonicalWebSocketGatewayMessageAndRecoveryFallbacks(t *testing.T) {
	t.Parallel()

	if got := canonicalWebSocketGatewayMessage(nil); got != webSocketPreVisibleFailureMessage {
		t.Fatalf("canonicalWebSocketGatewayMessage(nil) = %q, want %q", got, webSocketPreVisibleFailureMessage)
	}

	configErr := errors.New("provider credential missing")
	if got := canonicalWebSocketGatewayMessage(&WebSocketResult{
		TerminalCause: model.TerminalProviderConfigurationError,
		Err:           configErr,
	}); got != configErr.Error() {
		t.Fatalf("canonicalWebSocketGatewayMessage(provider config error) = %q, want %q", got, configErr.Error())
	}

	if got := canonicalWebSocketGatewayMessage(&WebSocketResult{
		TerminalCause: model.TerminalUpstreamTransportError,
		Err:           errors.New("dial failed"),
	}); got != webSocketPreVisibleFailureMessage {
		t.Fatalf("canonicalWebSocketGatewayMessage(transport error) = %q, want %q", got, webSocketPreVisibleFailureMessage)
	}

	if got := deriveWebSocketSessionRecoveryAction(nil); got != model.RecoveryActionNone {
		t.Fatalf("deriveWebSocketSessionRecoveryAction(nil) = %q, want %q", got, model.RecoveryActionNone)
	}

	session := &WebSocketSessionResult{
		FinalResult: &WebSocketResult{},
		Attempts: []WebSocketAttemptResult{
			{SwitchReason: "provider_switch"},
		},
	}
	if got := deriveWebSocketSessionRecoveryAction(session); got != model.RecoveryActionNone {
		t.Fatalf("deriveWebSocketSessionRecoveryAction() = %q, want %q", got, model.RecoveryActionNone)
	}

	replacedSession := &WebSocketSessionResult{
		FinalResult: &WebSocketResult{
			SessionCommitted:   true,
			CompletionObserved: true,
		},
		Attempts: []WebSocketAttemptResult{
			{
				Provider: &model.Provider{ID: "ws-origin"},
				Result: &WebSocketResult{
					HandshakeStatusCode: http.StatusTooManyRequests,
					TerminalCause:       model.TerminalUpstreamHandshakeRejected,
				},
				SwitchReason: SwitchReasonUsageLimitReached,
			},
			{SelectionMode: providerSwitchModeReplacement},
		},
	}
	if got := deriveWebSocketSessionRecoveryAction(replacedSession); got != model.RecoveryActionTransparentRetry {
		t.Fatalf("deriveWebSocketSessionRecoveryAction() for provider-scoped replacement completion = %q, want %q", got, model.RecoveryActionTransparentRetry)
	}
}

func TestWebSocketResultCloneDeepCopiesMutableFields(t *testing.T) {
	t.Parallel()

	original := &WebSocketResult{
		HandshakeAccepted: true,
		HandshakeHeaders: http.Header{
			"X-Test": []string{"original"},
		},
		TokenUsage: &TokenUsage{
			PromptTokens: observedTokenCount(1),
			CacheCreation: &CacheCreation{
				InputTokens: observedTokenCount(2),
			},
		},
		UpstreamError: &WebSocketUpstreamError{
			Code: "rate_limit_error",
			Raw:  `{"type":"error"}`,
		},
	}

	clone := original.Clone()
	if clone == nil {
		t.Fatal("Clone() = nil, want deep copy")
	}
	if clone == original {
		t.Fatal("Clone() returned original pointer")
	}

	clone.HandshakeHeaders.Set("X-Test", "clone")
	clone.TokenUsage.CacheCreation.InputTokens.Value = 9
	clone.UpstreamError.Code = "different"

	if got := original.HandshakeHeaders.Get("X-Test"); got != "original" {
		t.Fatalf("original header = %q, want original", got)
	}
	if got := original.TokenUsage.CacheCreation.InputTokens.Value; got != 2 {
		t.Fatalf("original cache creation = %d, want 2", got)
	}
	if got := original.UpstreamError.Code; got != "rate_limit_error" {
		t.Fatalf("original upstream error code = %q, want rate_limit_error", got)
	}
}

func TestWebSocketSessionOrchestratorSessionFromSuppressedPayloadHandlesClosedClient(t *testing.T) {
	t.Parallel()

	sessionCh := make(chan *WebSocketSessionResult, 1)
	orchestratorCh := make(chan *WebSocketSessionOrchestrator, 1)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientConn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}

		orchestrator := &WebSocketSessionOrchestrator{
			requestID:  "req-suppressed-write-fail",
			lifecycle:  newWebSocketLifecycleState(),
			clientConn: clientConn,
			suppressedAttempt: &webSocketSuppressedAttempt{
				provider:    &model.Provider{ID: "provider-1"},
				messageType: websocket.MessageText,
				payload:     []byte(`{"type":"error"}`),
				upstreamError: &WebSocketUpstreamError{
					Code: "rate_limit_error",
					Raw:  `{"type":"error"}`,
				},
			},
		}
		orchestratorCh <- orchestrator
		if err := orchestrator.clientConn.CloseNow(); err != nil {
			t.Errorf("CloseNow() error = %v", err)
			return
		}
		sessionCh <- orchestrator.sessionFromSuppressedPayload(r.Context())
	}))
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn := connectWSClient(t, ctx, wsURL(proxyServer))
	defer clientConn.CloseNow()
	orchestrator := <-orchestratorCh

	select {
	case session := <-sessionCh:
		if session.FinalErr == nil {
			t.Fatal("FinalErr = nil, want write failure")
		}
		if session.FinalProvider == nil || session.FinalProvider.ID != "provider-1" {
			t.Fatalf("FinalProvider = %#v, want provider-1", session.FinalProvider)
		}
		if session.FinalResult == nil {
			t.Fatal("FinalResult = nil, want fallback result")
		}
		if session.FinalResult.TerminalCause != model.TerminalProviderUnavailable {
			t.Fatalf("TerminalCause = %q, want %q", session.FinalResult.TerminalCause, model.TerminalProviderUnavailable)
		}
		if orchestrator.clientConn != nil {
			t.Fatal("clientConn should be cleared after failed suppressed payload write")
		}
		if orchestrator.suppressedAttempt != nil {
			t.Fatal("suppressedAttempt should be cleared after session finalization")
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for suppressed payload session")
	}
}

func TestNewWebSocketProviderConfigurationAttemptUsesTypedMissingField(t *testing.T) {
	t.Parallel()

	baseErr := errors.New("missing managed credential")
	attempt := newWebSocketProviderConfigurationAttempt(
		&model.Provider{ID: "provider-1"},
		"openai",
		2,
		providerSwitchModeReplacement,
		selector.SelectionMetadata{},
		&webSocketProviderConfigError{
			missingField: "credentials",
			err:          baseErr,
		},
		1500*time.Millisecond,
	)

	if attempt.Provider == nil || attempt.Provider.ID != "provider-1" {
		t.Fatalf("Provider = %#v, want provider-1", attempt.Provider)
	}
	if attempt.Attempt != 2 {
		t.Fatalf("Attempt = %d, want 2", attempt.Attempt)
	}
	if !errors.Is(attempt.ForwardErr, baseErr) {
		t.Fatalf("ForwardErr = %v, want wrapped %v", attempt.ForwardErr, baseErr)
	}
	if attempt.LatencyMs != 1500 {
		t.Fatalf("LatencyMs = %d, want 1500", attempt.LatencyMs)
	}
	if attempt.GatewayStatusCode != http.StatusBadGateway {
		t.Fatalf("GatewayStatusCode = %d, want %d", attempt.GatewayStatusCode, http.StatusBadGateway)
	}
	if attempt.GatewayErrorCode != ErrCodeWebSocketUpgrade {
		t.Fatalf("GatewayErrorCode = %q, want %q", attempt.GatewayErrorCode, ErrCodeWebSocketUpgrade)
	}

	wantMessage := `Provider "provider-1" is not ready for websocket "openai": credentials`
	if attempt.GatewayMessage != wantMessage {
		t.Fatalf("GatewayMessage = %q, want %q", attempt.GatewayMessage, wantMessage)
	}
	if attempt.Result == nil {
		t.Fatal("Result = nil, want gateway failure result")
	}
	if attempt.Result.TerminalCause != model.TerminalProviderConfigurationError {
		t.Fatalf("TerminalCause = %q, want %q", attempt.Result.TerminalCause, model.TerminalProviderConfigurationError)
	}
	if !errors.Is(attempt.Result.Err, attempt.ForwardErr) {
		t.Fatalf("Result.Err = %v, want wrapped %v", attempt.Result.Err, attempt.ForwardErr)
	}
}
