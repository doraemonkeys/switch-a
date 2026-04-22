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

	"switch-a/internal/model"
	"switch-a/internal/providerauth"

	"github.com/coder/websocket"
	"go.uber.org/zap"
)

func TestHandler_logWebSocketRequest_UsesHandshakeDiagnostics(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
	})

	const handshakeBody = `{"error":{"message":"Account quota exhausted","type":"billing_error"}}`
	info := RequestInfo{
		APIType:   "codex",
		Model:     "gpt-4o-realtime",
		ClientIP:  "127.0.0.1",
		UserID:    "user-1",
		Path:      "/responses",
		Method:    http.MethodGet,
		UserAgent: "codex-test",
		RequestID: "upstream-request-id",
	}
	result := &WebSocketResult{
		HandshakeStatusCode:  http.StatusPaymentRequired,
		HandshakeBodySnippet: handshakeBody,
		TerminalCause:        model.TerminalUpstreamHandshakeRejected,
		Err:                  errors.New("failed to WebSocket dial: expected handshake response status code 101 but got 402"),
	}

	handler.logWebSocketSession(info, &WebSocketSessionResult{
		RequestID:     "req-ws-handshake",
		FinalProvider: &model.Provider{ID: "ws-p1"},
		FinalResult:   result,
		FinalErr:      result.Err,
		ProbeOutcome:  model.WebSocketProbeOutcomeUnsupported,
	}, 250*time.Millisecond)

	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry")
	}
	if requestLogClientTransportStatusCode(log) != http.StatusPaymentRequired {
		t.Fatalf("ClientTransportStatusCode = %d, want %d", requestLogClientTransportStatusCode(log), http.StatusPaymentRequired)
	}
	if requestLogEvidenceMessage(t, log) != handshakeBody {
		t.Fatalf("evidence message = %q, want %q", requestLogEvidenceMessage(t, log), handshakeBody)
	}
	if requestLogServiceOutcome(log) == model.ServiceOutcomeCompleted {
		t.Fatal("expected non-completed outcome for failed handshake")
	}
	if !log.IsWebSocket {
		t.Fatal("expected IsWebSocket=true")
	}
	if requestLogTerminationReason(log) != model.TerminationReasonUpstreamHandshakeRejected {
		t.Fatalf("TerminationReason = %q, want %q", requestLogTerminationReason(log), model.TerminationReasonUpstreamHandshakeRejected)
	}
	if log.SessionCommitted == nil || *log.SessionCommitted {
		t.Fatal("SessionCommitted must be false for failed handshake")
	}
	if log.ClientVisible == nil || *log.ClientVisible {
		t.Fatal("ClientVisible must be false for failed handshake")
	}
	if log.CommitSource == nil || *log.CommitSource != model.CommitUnknown {
		t.Fatalf("CommitSource = %v, want %q", log.CommitSource, model.CommitUnknown)
	}
	if requestLogClientAction(log) != model.ClientActionNone {
		t.Fatalf("ClientAction = %q, want %q", requestLogClientAction(log), model.ClientActionNone)
	}
}

func TestHandler_logWebSocketRequest_UsesSemanticUpstreamError(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
	})

	const errorPayload = `{"error":{"message":"Model 'gpt-5.4' is not allowed","type":"model_not_allowed"},"status":403,"type":"error"}`
	info := RequestInfo{
		APIType:   "codex",
		Model:     "gpt-5.4",
		ClientIP:  "127.0.0.1",
		UserID:    "user-1",
		Path:      "/responses",
		Method:    http.MethodGet,
		UserAgent: "codex-test",
		RequestID: "upstream-request-id",
	}
	result := &WebSocketResult{
		HandshakeAccepted: true,
		ClientAccepted:    true,
		TerminalCause:     model.TerminalUpstreamSemanticError,
		CloseCode:         websocket.StatusNoStatusRcvd,
		Err:               errors.New("failed to get reader: received close frame: status = StatusNoStatusRcvd and reason = \"\""),
		UpstreamError: &WebSocketUpstreamError{
			EventType:  "model_not_allowed",
			StatusCode: http.StatusForbidden,
			Message:    "Model 'gpt-5.4' is not allowed",
			Raw:        errorPayload,
		},
	}

	handler.logWebSocketSession(info, &WebSocketSessionResult{
		RequestID:     "req-ws-semantic-error",
		FinalProvider: &model.Provider{ID: "ws-p1"},
		FinalResult:   result,
		FinalErr:      result.Err,
	}, 250*time.Millisecond)

	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry")
	}
	if requestLogClientTransportStatusCode(log) != http.StatusSwitchingProtocols {
		t.Fatalf("ClientTransportStatusCode = %d, want %d", requestLogClientTransportStatusCode(log), http.StatusSwitchingProtocols)
	}
	if requestLogEvidenceMessage(t, log) != errorPayload {
		t.Fatalf("evidence message = %q, want %q", requestLogEvidenceMessage(t, log), errorPayload)
	}
	if requestLogServiceOutcome(log) == model.ServiceOutcomeCompleted {
		t.Fatal("expected non-completed outcome for semantic upstream error")
	}
	if !log.IsWebSocket {
		t.Fatal("expected IsWebSocket=true")
	}
	if requestLogTerminationReason(log) != model.TerminationReasonUpstreamSemanticError {
		t.Fatalf("TerminationReason = %q, want %q", requestLogTerminationReason(log), model.TerminationReasonUpstreamSemanticError)
	}
	if log.SessionCommitted == nil || *log.SessionCommitted {
		t.Fatal("SessionCommitted must be false for pre-commit semantic errors")
	}
	if log.ClientVisible == nil || *log.ClientVisible {
		t.Fatal("ClientVisible must be false for pre-commit semantic errors")
	}
	if log.CommitSource == nil || *log.CommitSource != model.CommitUnknown {
		t.Fatalf("CommitSource = %v, want %q", log.CommitSource, model.CommitUnknown)
	}
	if requestLogClientAction(log) != model.ClientActionNone {
		t.Fatalf("ClientAction = %q, want %q", requestLogClientAction(log), model.ClientActionNone)
	}
}

func TestHandler_logWebSocketRequest_PersistsCommitSource(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
	})

	committed := true
	info := RequestInfo{
		APIType:   "codex",
		Model:     "gpt-5.4",
		ClientIP:  "127.0.0.1",
		UserID:    "user-1",
		Path:      "/responses",
		Method:    http.MethodGet,
		UserAgent: "codex-test",
		RequestID: "upstream-request-id",
	}
	result := &WebSocketResult{
		HandshakeAccepted: true,
		ClientAccepted:    true,
		SessionCommitted:  committed,
		TerminalCause:     model.TerminalCleanClose,
		CommitSource:      model.CommitSemantic,
	}

	handler.logWebSocketSession(info, &WebSocketSessionResult{
		RequestID:     "req-ws-commit-source",
		FinalProvider: &model.Provider{ID: "ws-p1"},
		FinalResult:   result,
		StickyWritten: true,
		ProbeOutcome:  model.WebSocketProbeOutcomeObservedUsableModel,
	}, 250*time.Millisecond)

	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry")
	}
	if log.CommitSource == nil || *log.CommitSource != model.CommitSemantic {
		t.Fatalf("CommitSource = %v, want %q", log.CommitSource, model.CommitSemantic)
	}
	if log.SessionCommitted == nil || !*log.SessionCommitted {
		t.Fatalf("SessionCommitted = %v, want true", log.SessionCommitted)
	}
	if log.ClientVisible == nil || *log.ClientVisible {
		t.Fatalf("ClientVisible = %v, want false", log.ClientVisible)
	}
	if requestLogClientAction(log) != model.ClientActionNone {
		t.Fatalf("ClientAction = %q, want %q", requestLogClientAction(log), model.ClientActionNone)
	}
}

func TestHandler_logWebSocketRequest_PersistsVisibilityAndRecoveryAction(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
	})

	info := RequestInfo{
		APIType:   "codex",
		Model:     "gpt-5.4",
		ClientIP:  "127.0.0.1",
		UserID:    "user-1",
		Path:      "/responses",
		Method:    http.MethodGet,
		UserAgent: "codex-test",
		RequestID: "upstream-request-id",
	}
	result := &WebSocketResult{
		HandshakeAccepted: true,
		ClientAccepted:    true,
		ClientVisible:     true,
		SessionCommitted:  false,
		TerminalCause:     model.TerminalUpstreamSemanticError,
		CommitSource:      model.CommitUnknown,
		UpstreamError: &WebSocketUpstreamError{
			EventType:  codexUsageLimitErrorType,
			StatusCode: http.StatusTooManyRequests,
			Message:    "usage window exhausted",
			Raw:        `{"type":"error"}`,
		},
	}

	handler.logWebSocketSession(info, &WebSocketSessionResult{
		RequestID:     "req-ws-reconnect",
		FinalProvider: &model.Provider{ID: "ws-p1"},
		FinalResult:   result,
		ProbeOutcome:  model.WebSocketProbeOutcomeTransportFailed,
	}, 250*time.Millisecond)

	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry")
	}
	if log.ClientVisible == nil || !*log.ClientVisible {
		t.Fatalf("ClientVisible = %v, want true", log.ClientVisible)
	}
	if log.SessionCommitted == nil || *log.SessionCommitted {
		t.Fatalf("SessionCommitted = %v, want false", log.SessionCommitted)
	}
	if requestLogClientAction(log) != model.ClientActionReconnectRequired {
		t.Fatalf("ClientAction = %q, want %q", requestLogClientAction(log), model.ClientActionReconnectRequired)
	}
}

func TestHandler_logWebSocketRequest_UsesLifecycleProviderAttributionAndPersistsAttempts(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
	})

	info := RequestInfo{
		APIType:   "codex",
		Model:     "gpt-5.4",
		ClientIP:  "127.0.0.1",
		UserID:    "user-1",
		Path:      "/responses",
		Method:    http.MethodGet,
		UserAgent: "codex-test",
		RequestID: "upstream-request-id",
	}
	attempts := []WebSocketAttemptResult{
		{
			Provider:   &model.Provider{ID: "provider-origin"},
			Attempt:    0,
			Result:     newWebSocketGatewayFailureResult(http.StatusForbidden, model.TerminalUpstreamHandshakeRejected, errors.New("provider-scoped semantic error")),
			ForwardErr: errors.New("provider-scoped semantic error"),
			CreatedAt:  time.Now(),
		},
		{
			Provider:  &model.Provider{ID: "provider-final"},
			Attempt:   1,
			Result:    &WebSocketResult{HandshakeAccepted: true},
			CreatedAt: time.Now().Add(time.Millisecond),
		},
	}
	result := &WebSocketResult{
		HandshakeAccepted: true,
		ClientAccepted:    true,
		SessionCommitted:  true,
		TerminalCause:     model.TerminalCleanClose,
		CommitSource:      model.CommitSemantic,
	}

	handler.logWebSocketSession(info, &WebSocketSessionResult{
		RequestID:     "req-ws-attribution",
		FinalProvider: &model.Provider{ID: "provider-final"},
		FinalResult:   result,
		Attempts:      attempts,
		StickyWritten: true,
	}, 250*time.Millisecond)

	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry")
	}
	if log.ProviderID != "provider-final" {
		t.Fatalf("ProviderID = %q, want %q", log.ProviderID, "provider-final")
	}
	if log.RetryCount != 1 {
		t.Fatalf("RetryCount = %d, want 1", log.RetryCount)
	}

	storedAttempts := store.LastAttempts(2)
	if len(storedAttempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(storedAttempts))
	}
	if storedAttempts[0].ProviderID != "provider-origin" || storedAttempts[1].ProviderID != "provider-final" {
		t.Fatalf("attempt provider order = [%s %s], want [provider-origin provider-final]", storedAttempts[0].ProviderID, storedAttempts[1].ProviderID)
	}
}

func TestHandler_logWebSocketRequest_PrefersLifecycleProviderOverLastAttempt(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
	})

	info := RequestInfo{
		APIType:   "codex",
		Model:     "gpt-5.4",
		ClientIP:  "127.0.0.1",
		UserID:    "user-1",
		Path:      "/responses",
		Method:    http.MethodGet,
		UserAgent: "codex-test",
		RequestID: "upstream-request-id",
	}
	attempts := []WebSocketAttemptResult{
		{
			Provider:   &model.Provider{ID: "provider-origin"},
			Attempt:    0,
			Result:     newWebSocketGatewayFailureResult(http.StatusForbidden, model.TerminalUpstreamHandshakeRejected, errors.New("provider-scoped semantic error")),
			ForwardErr: errors.New("provider-scoped semantic error"),
			CreatedAt:  time.Now(),
		},
		{
			Provider:   &model.Provider{ID: "provider-fallback"},
			Attempt:    1,
			Result:     newWebSocketGatewayFailureResult(http.StatusBadGateway, model.TerminalUpstreamTransportError, errors.New("fallback replay failed")),
			ForwardErr: errors.New("fallback replay failed"),
			CreatedAt:  time.Now().Add(time.Millisecond),
		},
	}
	result := &WebSocketResult{
		HandshakeAccepted: true,
		TerminalCause:     model.TerminalUpstreamSemanticError,
		Err:               errors.New("original provider semantic payload returned to client"),
	}

	handler.logWebSocketSession(info, &WebSocketSessionResult{
		RequestID:     "req-ws-origin-attribution",
		FinalProvider: &model.Provider{ID: "provider-origin"},
		FinalResult:   result,
		FinalErr:      result.Err,
		Attempts:      attempts,
	}, 250*time.Millisecond)

	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log entry")
	}
	if log.ProviderID != "provider-origin" {
		t.Fatalf("ProviderID = %q, want %q", log.ProviderID, "provider-origin")
	}

	storedAttempts := store.LastAttempts(2)
	if len(storedAttempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(storedAttempts))
	}
	if storedAttempts[1].ProviderID != "provider-fallback" {
		t.Fatalf("last attempt provider = %q, want %q", storedAttempts[1].ProviderID, "provider-fallback")
	}
}

func TestApplyWebSocketHealthOutcome_PostCommitTransportErrorMarksSuccess(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	healthMgr := newTrackingHealthManager()
	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
		Health: healthMgr,
	})
	provider := &model.Provider{ID: "ws-p1"}

	applyWebSocketHealthOutcome(context.Background(), handler, provider, &WebSocketResult{
		HandshakeAccepted: true,
		SessionCommitted:  true,
		TerminalCause:     model.TerminalUpstreamTransportError,
		Err:               io.EOF,
	})

	if len(healthMgr.getMarkFailureCalls()) != 0 {
		t.Fatalf("mark failure count = %d, want 0", len(healthMgr.getMarkFailureCalls()))
	}
	if successIDs := healthMgr.getMarkSuccessIDs(); len(successIDs) != 1 || successIDs[0] != "ws-p1" {
		t.Fatalf("mark success IDs = %v, want [ws-p1]", successIDs)
	}
}

func TestApplyWebSocketHealthOutcome_PostCommitSemanticErrorMarksFailure(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	healthMgr := newTrackingHealthManager()
	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
		Health: healthMgr,
	})
	provider := &model.Provider{ID: "ws-p1"}

	semanticErr := errors.New("model not allowed")
	applyWebSocketHealthOutcome(context.Background(), handler, provider, &WebSocketResult{
		HandshakeAccepted: true,
		SessionCommitted:  true,
		TerminalCause:     model.TerminalUpstreamSemanticError,
		Err:               semanticErr,
	})

	if successIDs := healthMgr.getMarkSuccessIDs(); len(successIDs) != 0 {
		t.Fatalf("mark success IDs = %v, want none", successIDs)
	}
	failures := healthMgr.getMarkFailureCalls()
	if len(failures) != 1 {
		t.Fatalf("mark failure count = %d, want 1", len(failures))
	}
	if failures[0].providerID != "ws-p1" {
		t.Fatalf("providerID = %q, want ws-p1", failures[0].providerID)
	}
	if !errors.Is(failures[0].err, semanticErr) {
		t.Fatalf("err = %v, want %v", failures[0].err, semanticErr)
	}
}

func TestApplyWebSocketHealthOutcome_UsageLimitHandshakeSuspendsProvider(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	healthMgr := newTrackingHealthManager()
	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
		Health: healthMgr,
	})
	provider := &model.Provider{
		ID:             "ws-p1",
		CredentialType: model.ProviderCredentialTypeChatGPT,
	}

	observedAt := time.Date(2026, time.March, 26, 12, 0, 0, 0, time.UTC)
	bodyReset := observedAt.Add(5 * time.Minute)
	laterHeaderReset := observedAt.Add(35 * time.Minute)
	usageLimitErr := errors.New("failed to WebSocket dial: expected handshake response status code 101 but got 429")

	applyWebSocketHealthOutcome(context.Background(), handler, provider, &WebSocketResult{
		HandshakeStatusCode: http.StatusTooManyRequests,
		HandshakeBodySnippet: `{"type":"error","error":{"type":"usage_limit_reached","resets_at":` +
			strconv.FormatInt(bodyReset.Unix(), 10) + `}}`,
		HandshakeHeaders: http.Header{
			headerCodexPrimaryUsedPercent:   []string{"100"},
			headerCodexSecondaryUsedPercent: []string{"100"},
			headerCodexPrimaryResetAt:       []string{strconv.FormatInt(bodyReset.Unix(), 10)},
			headerCodexSecondaryResetAt:     []string{strconv.FormatInt(laterHeaderReset.Unix(), 10)},
		},
		HandshakeObservedAt: observedAt,
		TerminalCause:       model.TerminalUpstreamHandshakeRejected,
		Err:                 usageLimitErr,
	})

	if successIDs := healthMgr.getMarkSuccessIDs(); len(successIDs) != 0 {
		t.Fatalf("mark success IDs = %v, want none", successIDs)
	}
	failures := healthMgr.getMarkFailureCalls()
	if len(failures) != 1 {
		t.Fatalf("mark failure count = %d, want 1", len(failures))
	}
	if failures[0].providerID != "ws-p1" {
		t.Fatalf("providerID = %q, want ws-p1", failures[0].providerID)
	}
	if !errors.Is(failures[0].err, usageLimitErr) {
		t.Fatalf("err = %v, want %v", failures[0].err, usageLimitErr)
	}

	suspensions := healthMgr.getSuspendCalls()
	if len(suspensions) != 1 {
		t.Fatalf("suspend call count = %d, want 1", len(suspensions))
	}
	if suspensions[0].providerID != "ws-p1" {
		t.Fatalf("suspend providerID = %q, want ws-p1", suspensions[0].providerID)
	}
	if suspensions[0].reason != usageLimitAutoDisableReason {
		t.Fatalf("suspend reason = %q, want %q", suspensions[0].reason, usageLimitAutoDisableReason)
	}
	if !suspensions[0].disabledUntil.Equal(laterHeaderReset) {
		t.Fatalf("disabledUntil = %v, want %v", suspensions[0].disabledUntil, laterHeaderReset)
	}
}

func TestApplyWebSocketHealthOutcome_UsageLimitSemanticErrorSuspendsProvider(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	healthMgr := newTrackingHealthManager()
	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
		Health: healthMgr,
	})
	provider := &model.Provider{
		ID:             "ws-p1",
		CredentialType: model.ProviderCredentialTypeChatGPT,
	}

	observedAt := time.Date(2026, time.March, 26, 14, 0, 0, 0, time.UTC)
	resetAt := observedAt.Add(40 * time.Minute).Truncate(time.Second)
	semanticErr := errors.New("provider reported usage limit reached")

	applyWebSocketHealthOutcome(context.Background(), handler, provider, &WebSocketResult{
		HandshakeAccepted: true,
		TerminalCause:     model.TerminalUpstreamSemanticError,
		Err:               semanticErr,
		UpstreamError: &WebSocketUpstreamError{
			EventType:  codexUsageLimitErrorType,
			StatusCode: http.StatusTooManyRequests,
			ObservedAt: observedAt,
			ResetAt:    &resetAt,
			Raw:        `{"type":"error"}`,
		},
	})

	failures := healthMgr.getMarkFailureCalls()
	if len(failures) != 1 {
		t.Fatalf("mark failure count = %d, want 1", len(failures))
	}
	if !errors.Is(failures[0].err, semanticErr) {
		t.Fatalf("err = %v, want %v", failures[0].err, semanticErr)
	}

	suspensions := healthMgr.getSuspendCalls()
	if len(suspensions) != 1 {
		t.Fatalf("suspend call count = %d, want 1", len(suspensions))
	}
	if suspensions[0].providerID != "ws-p1" {
		t.Fatalf("suspend providerID = %q, want ws-p1", suspensions[0].providerID)
	}
	if suspensions[0].reason != usageLimitAutoDisableReason {
		t.Fatalf("suspend reason = %q, want %q", suspensions[0].reason, usageLimitAutoDisableReason)
	}
	if !suspensions[0].disabledUntil.Equal(resetAt) {
		t.Fatalf("disabledUntil = %v, want %v", suspensions[0].disabledUntil, resetAt)
	}
}

func TestApplyWebSocketHealthOutcome_UsageLimitSemanticErrorSwitchOnlyDoesNotSuspendProvider(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	healthMgr := newTrackingHealthManager()
	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
		Health: healthMgr,
	})
	provider := &model.Provider{
		ID:             "ws-p1",
		CredentialType: model.ProviderCredentialTypeAPIKey,
	}

	observedAt := time.Date(2026, time.March, 26, 14, 0, 0, 0, time.UTC)
	resetAt := observedAt.Add(40 * time.Minute).Truncate(time.Second)
	semanticErr := errors.New("provider reported usage limit reached")

	applyWebSocketHealthOutcome(context.Background(), handler, provider, &WebSocketResult{
		HandshakeAccepted: true,
		TerminalCause:     model.TerminalUpstreamSemanticError,
		Err:               semanticErr,
		UpstreamError: &WebSocketUpstreamError{
			EventType:  codexUsageLimitErrorType,
			StatusCode: http.StatusTooManyRequests,
			ObservedAt: observedAt,
			ResetAt:    &resetAt,
			Raw:        `{"type":"error"}`,
		},
	})

	if suspensions := healthMgr.getSuspendCalls(); len(suspensions) != 0 {
		t.Fatalf("suspend calls = %v, want none", suspensions)
	}
	failures := healthMgr.getMarkFailureCalls()
	if len(failures) != 1 {
		t.Fatalf("mark failure count = %d, want 1", len(failures))
	}
	if !errors.Is(failures[0].err, semanticErr) {
		t.Fatalf("err = %v, want %v", failures[0].err, semanticErr)
	}
}

func TestApplyWebSocketHealthOutcome_ClientScopedSemanticErrorDoesNotSuspendProvider(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	healthMgr := newTrackingHealthManager()
	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
		Health: healthMgr,
	})
	provider := &model.Provider{ID: "ws-p1"}

	semanticErr := errors.New("client sent invalid request")
	applyWebSocketHealthOutcome(context.Background(), handler, provider, &WebSocketResult{
		HandshakeAccepted: true,
		TerminalCause:     model.TerminalUpstreamSemanticError,
		Err:               semanticErr,
		UpstreamError: &WebSocketUpstreamError{
			EventType:  "invalid_request_error",
			StatusCode: http.StatusBadRequest,
			Raw:        `{"type":"error"}`,
		},
	})

	if suspensions := healthMgr.getSuspendCalls(); len(suspensions) != 0 {
		t.Fatalf("suspend calls = %v, want none", suspensions)
	}
	failures := healthMgr.getMarkFailureCalls()
	if len(failures) != 0 {
		t.Fatalf("mark failure count = %d, want 0", len(failures))
	}
	if successes := healthMgr.getMarkSuccessIDs(); len(successes) != 0 {
		t.Fatalf("mark success IDs = %v, want none", successes)
	}
}

func TestApplyWebSocketHealthOutcome_WebSocketConnectionLimitWithoutCompletionStaysNeutral(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	healthMgr := newTrackingHealthManager()
	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
		Health: healthMgr,
	})
	provider := &model.Provider{ID: "ws-p1"}

	applyWebSocketHealthOutcome(context.Background(), handler, provider, &WebSocketResult{
		HandshakeAccepted: true,
		ClientVisible:     true,
		SessionCommitted:  true,
		TerminalCause:     model.TerminalUpstreamSemanticError,
		UpstreamError: &WebSocketUpstreamError{
			EventType:  webSocketConnectionLimitErrorType,
			StatusCode: http.StatusTooManyRequests,
			Message:    "connection limit reached",
			Raw:        `{"type":"error"}`,
		},
	})

	if failures := healthMgr.getMarkFailureCalls(); len(failures) != 0 {
		t.Fatalf("mark failure calls = %v, want none", failures)
	}
	if suspensions := healthMgr.getSuspendCalls(); len(suspensions) != 0 {
		t.Fatalf("suspend calls = %v, want none", suspensions)
	}
	if successes := healthMgr.getMarkSuccessIDs(); len(successes) != 0 {
		t.Fatalf("mark success IDs = %v, want none", successes)
	}
}

func TestApplyWebSocketHealthOutcome_WebSocketConnectionLimitAfterCompletionMarksSuccess(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	healthMgr := newTrackingHealthManager()
	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
		Health: healthMgr,
	})
	provider := &model.Provider{ID: "ws-p1"}

	applyWebSocketHealthOutcome(context.Background(), handler, provider, &WebSocketResult{
		HandshakeAccepted:  true,
		ClientVisible:      true,
		SessionCommitted:   true,
		CompletionObserved: true,
		TerminalCause:      model.TerminalUpstreamSemanticError,
		UpstreamError: &WebSocketUpstreamError{
			EventType:  webSocketConnectionLimitErrorType,
			StatusCode: http.StatusTooManyRequests,
			Message:    "connection limit reached",
			Raw:        `{"type":"error"}`,
		},
	})

	if failures := healthMgr.getMarkFailureCalls(); len(failures) != 0 {
		t.Fatalf("mark failure calls = %v, want none", failures)
	}
	if suspensions := healthMgr.getSuspendCalls(); len(suspensions) != 0 {
		t.Fatalf("suspend calls = %v, want none", suspensions)
	}
	if successes := healthMgr.getMarkSuccessIDs(); len(successes) != 1 || successes[0] != "ws-p1" {
		t.Fatalf("mark success IDs = %v, want [ws-p1]", successes)
	}
}

func TestApplyWebSocketHealthOutcome_PostVisibleTransportErrorWithoutCompletionDoesNotMarkProviderFailure(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	healthMgr := newTrackingHealthManager()
	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
		Health: healthMgr,
	})
	provider := &model.Provider{ID: "ws-p1"}

	applyWebSocketHealthOutcome(context.Background(), handler, provider, &WebSocketResult{
		HandshakeAccepted: true,
		ClientVisible:     true,
		SessionCommitted:  true,
		TerminalCause:     model.TerminalUpstreamTransportError,
		Err:               io.EOF,
	})

	if failures := healthMgr.getMarkFailureCalls(); len(failures) != 0 {
		t.Fatalf("mark failure calls = %v, want none", failures)
	}
	if suspensions := healthMgr.getSuspendCalls(); len(suspensions) != 0 {
		t.Fatalf("suspend calls = %v, want none", suspensions)
	}
	if successes := healthMgr.getMarkSuccessIDs(); len(successes) != 0 {
		t.Fatalf("mark success IDs = %v, want none", successes)
	}
}

func TestPrepareWebSocketDialHeaders_ManagedAuthErrorAndLogHelpers(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
		Auth:   providerauth.NewService(providerauth.Config{}),
	})

	req := httptest.NewRequest(http.MethodGet, "http://proxy.test/v1/realtime?model=gpt-5.4", nil)
	provider := &model.Provider{
		ID:             "ws-chatgpt-invalid",
		AuthMode:       "bearer",
		CredentialType: model.ProviderCredentialTypeChatGPT,
	}

	headers, err := handler.prepareWebSocketDialHeaders(context.Background(), req, provider, APITypeCodex, "bearer")
	if err == nil {
		t.Fatal("prepareWebSocketDialHeaders() error = nil, want managed auth error")
	}
	if headers != nil {
		t.Fatalf("prepareWebSocketDialHeaders() headers = %v, want nil on error", headers)
	}

	if got := webSocketClientTransportStatusCode(0, nil); got != StatusCodeNoResponse {
		t.Fatalf("webSocketClientTransportStatusCode(0, nil) = %d, want %d", got, StatusCodeNoResponse)
	}
	evidence := buildWebSocketEvidence(webSocketGatewayEvidenceInput{}, nil, errors.New("fallback transport error"), false)
	if evidence == nil {
		t.Fatal("buildWebSocketEvidence() = nil, want transport evidence")
	}

	recorder := httptest.NewRecorder()
	statusCode, errorCode, message := websocketGatewayFailure(&WebSocketResult{
		HandshakeStatusCode: http.StatusUpgradeRequired,
		TerminalCause:       model.TerminalUpstreamHandshakeRejected,
	})
	handler.writeGatewayError(recorder, statusCode, errorCode, message)
	if recorder.Code != http.StatusUpgradeRequired {
		t.Fatalf("writeGatewayError status = %d, want %d", recorder.Code, http.StatusUpgradeRequired)
	}
	if !strings.Contains(recorder.Body.String(), "HTTP fallback") {
		t.Fatalf("writeGatewayError body = %q, want upgrade-required message", recorder.Body.String())
	}
}

func TestApplyWebSocketSessionHealthOutcomesAndBytesTrackingObserver_NoOpBranches(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	healthMgr := newTrackingHealthManager()
	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
		Health: healthMgr,
	})

	applyWebSocketSessionHealthOutcomes(context.Background(), handler, nil)
	applyWebSocketSessionHealthOutcomes(context.Background(), handler, &WebSocketSessionResult{
		Attempts: []WebSocketAttemptResult{
			{},
			{
				Provider: &model.Provider{ID: "ws-p1"},
			},
		},
	})
	applyWebSocketHealthOutcome(context.Background(), handler, &model.Provider{ID: "ws-p1"}, nil)
	if failures := healthMgr.getMarkFailureCalls(); len(failures) != 0 {
		t.Fatalf("mark failure calls = %v, want none", failures)
	}
	if successes := healthMgr.getMarkSuccessIDs(); len(successes) != 0 {
		t.Fatalf("mark success IDs = %v, want none", successes)
	}

	tracker := &LiveBytesTracker{}
	observer := newBytesTrackingObserver(nil, tracker)
	observer.ObserveClientMessage(websocket.MessageText, []byte("ping"))
	observer.ObserveUpstreamMessage(websocket.MessageText, []byte("pong"))
	if snapshot := observer.Snapshot(); snapshot != (WebSocketObservation{}) {
		t.Fatalf("Snapshot() = %+v, want zero observation without inner observer", snapshot)
	}
	if observer.ParseDegraded() {
		t.Fatal("ParseDegraded() = true, want false without inner observer")
	}
	if observer.HasSemanticObservation() {
		t.Fatal("HasSemanticObservation() = true, want false without inner observer")
	}
}
