package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/http"
	"github.com/doraemonkeys/switch-a/internal/codex/recovery"
	"github.com/doraemonkeys/switch-a/internal/model"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestCodexHTTPRecoveryAdapterPreservesRootConditionsAndSafeDiagnostics(t *testing.T) {
	const secretCause = "codeql-secret-state-value"
	tests := []struct {
		name      string
		err       error
		condition codexrecovery.Condition
		status    int
		code      codexrecovery.ErrorCode
		action    codexrecovery.RecoveryAction
	}{
		{
			name: "root conflict overrides coarse client kind",
			err: &codexhttp.Error{Kind: codexhttp.ErrorClientInput, Stage: "continuity_owner", Cause: &codexcontinuity.Error{
				Kind: codexcontinuity.ErrorConflict, Reason: secretCause,
			}},
			condition: codexrecovery.ConditionStateConflict, status: http.StatusConflict,
			code: codexrecovery.ErrorCodeStateConflict, action: codexrecovery.RecoveryActionNewThread,
		},
		{
			name: "unknown owner", err: wrappedContinuityHTTPError(codexcontinuity.ErrorUnknown, secretCause),
			condition: codexrecovery.ConditionNewThreadRequired, status: http.StatusGone,
			code: codexrecovery.ErrorCodeNewThreadRequired, action: codexrecovery.RecoveryActionNewThread,
		},
		{
			name: "expired owner", err: wrappedContinuityHTTPError(codexcontinuity.ErrorExpired, secretCause),
			condition: codexrecovery.ConditionNewThreadRequired, status: http.StatusGone,
			code: codexrecovery.ErrorCodeNewThreadRequired, action: codexrecovery.RecoveryActionNewThread,
		},
		{
			name: "inactive generation", err: wrappedContinuityHTTPError(codexcontinuity.ErrorInactiveGeneration, secretCause),
			condition: codexrecovery.ConditionReconnectRequired, status: http.StatusConflict,
			code: codexrecovery.ErrorCodeReconnectRequired, action: codexrecovery.RecoveryActionReconnect,
		},
		{
			name: "store unavailable", err: wrappedContinuityHTTPError(codexcontinuity.ErrorUnavailable, secretCause),
			condition: codexrecovery.ConditionStateStoreUnavailable, status: http.StatusServiceUnavailable,
			code: codexrecovery.ErrorCodeStateStoreUnavailable, action: codexrecovery.RecoveryActionRetry,
		},
		{
			name: "capacity", err: wrappedContinuityHTTPError(codexcontinuity.ErrorCapacity, secretCause),
			condition: codexrecovery.ConditionStateStoreUnavailable, status: http.StatusServiceUnavailable,
			code: codexrecovery.ErrorCodeStateStoreUnavailable, action: codexrecovery.RecoveryActionRetry,
		},
		{
			name:      "malformed recognized field fallback",
			err:       &codexhttp.Error{Kind: codexhttp.ErrorClientInput, Stage: "request_protocol", Cause: errors.New(secretCause)},
			condition: codexrecovery.ConditionProtocolInvalid, status: http.StatusBadRequest,
			code: codexrecovery.ErrorCodeProtocolInvalid, action: codexrecovery.RecoveryActionCorrectRequest,
		},
		{
			name:      "untyped state dependency fallback",
			err:       &codexhttp.Error{Kind: codexhttp.ErrorDependencyUnavailable, Stage: "provider_cookie", Cause: errors.New(secretCause)},
			condition: codexrecovery.ConditionStateStoreUnavailable, status: http.StatusServiceUnavailable,
			code: codexrecovery.ErrorCodeStateStoreUnavailable, action: codexrecovery.RecoveryActionRetry,
		},
		{
			name:      "known owner transition fallback",
			err:       &codexhttp.Error{Kind: codexhttp.ErrorIdentityMismatch, Stage: "required_authority", Cause: errors.New(secretCause)},
			condition: codexrecovery.ConditionStateConflict, status: http.StatusConflict,
			code: codexrecovery.ErrorCodeStateConflict, action: codexrecovery.RecoveryActionNewThread,
		},
		{
			name: "explicit internal condition is not replaced by coarse HTTP kind",
			err: &codexhttp.Error{Kind: codexhttp.ErrorDependencyUnavailable, Stage: "future", Cause: codexrecovery.Mark(
				codexrecovery.ConditionInternalFailure, errors.New(secretCause),
			)},
			condition: codexrecovery.ConditionInternalFailure, status: http.StatusInternalServerError,
			code: codexrecovery.ErrorCodeInternal, action: codexrecovery.RecoveryActionRetry,
		},
		{
			name: "future continuity kind keeps W0-R internal default",
			err: &codexhttp.Error{Kind: codexhttp.ErrorClientInput, Stage: "future", Cause: &codexcontinuity.Error{
				Kind: codexcontinuity.ErrorKind("future"), Reason: secretCause,
			}},
			condition: codexrecovery.ConditionInternalFailure, status: http.StatusInternalServerError,
			code: codexrecovery.ErrorCodeInternal, action: codexrecovery.RecoveryActionRetry,
		},
		{
			name: "future persistence kind keeps W0-R internal default",
			err: &codexhttp.Error{Kind: codexhttp.ErrorDependencyUnavailable, Stage: "future", Cause: &providercookie.PersistenceError{
				Kind: providercookie.PersistenceErrorKind("future"), Operation: "future", Cause: errors.New(secretCause),
			}},
			condition: codexrecovery.ConditionInternalFailure, status: http.StatusInternalServerError,
			code: codexrecovery.ErrorCodeInternal, action: codexrecovery.RecoveryActionRetry,
		},
		{
			name:      "unclassified adapter default",
			err:       &codexhttp.Error{Kind: codexhttp.ErrorKind("future"), Stage: "future", Cause: errors.New(secretCause)},
			condition: codexrecovery.ConditionInternalFailure, status: http.StatusInternalServerError,
			code: codexrecovery.ErrorCodeInternal, action: codexrecovery.RecoveryActionRetry,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			core, observed := observer.New(zap.WarnLevel)
			handler := &Handler{logger: zap.New(core)}
			response := httptest.NewRecorder()

			handler.handleCodexHTTPBeginError(response, "stable-operation-id", fmt.Errorf("adapter: %w", test.err))

			if response.Code != test.status {
				t.Fatalf("HTTP status = %d, want %d", response.Code, test.status)
			}
			var envelope model.GatewayError
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Error.Code != string(test.code) {
				t.Fatalf("error code = %q, want %q", envelope.Error.Code, test.code)
			}
			entries := observed.All()
			if len(entries) != 1 {
				t.Fatalf("log entries = %d, want 1", len(entries))
			}
			context := entries[0].ContextMap()
			if context["request_id"] != "stable-operation-id" || context["operation_id"] != "stable-operation-id" ||
				context["recovery_condition"] != string(test.condition) ||
				context["recovery_action"] != string(test.action) ||
				context["error_code"] != string(test.code) ||
				context["carrier_phase"] != string(codexrecovery.PhaseHTTP) ||
				context["recovery_http_status"] != int64(test.status) {
				t.Fatalf("recovery diagnostics = %#v", context)
			}
			encoded, err := json.Marshal(context)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(response.Body.String()+string(encoded), secretCause) {
				t.Fatalf("recovery surface leaked opaque state: body=%s context=%s", response.Body.String(), encoded)
			}
		})
	}
}

func TestCodexHTTPUnknownPreviousResponseMapsToNewThreadBeforeUpstream(t *testing.T) {
	var upstreamRequests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		upstreamRequests.Add(1)
	}))
	t.Cleanup(upstream.Close)
	handler := newRedirectTestHandler(t, APITypeCodex, upstream.URL)
	request := httptest.NewRequest(
		http.MethodPost,
		RouteCodexResponses,
		strings.NewReader(`{"type":"response.create","previous_response_id":"resp_missing"}`),
	)
	request.Header.Set("Authorization", "Bearer stable-client-scope")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusGone {
		t.Fatalf("HTTP status = %d, want %d; body=%s", response.Code, http.StatusGone, response.Body.String())
	}
	var envelope model.GatewayError
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != string(codexrecovery.ErrorCodeNewThreadRequired) {
		t.Fatalf("error code = %q, want %q", envelope.Error.Code, codexrecovery.ErrorCodeNewThreadRequired)
	}
	if got := upstreamRequests.Load(); got != 0 {
		t.Fatalf("upstream requests = %d, want zero", got)
	}
}

func TestHandleNoProviderReportsContinuityRoutingConflict(t *testing.T) {
	store := newMockStore()
	handler := newProxyCodexTestHandler(t, Config{Store: store, Logger: zap.NewNop()})
	response := httptest.NewRecorder()
	selectionErr := &internal.ProviderSelectionError{
		Reason:                        internal.ProviderSelectionFailureContinuityRoutingConflict,
		APIType:                       APITypeCodex,
		PreferredRouteTargetID:        "owner-route-sensitive",
		RoutingPolicyConstraint:       "exact_provider",
		RoutingPolicyTargetProviderID: "policy-route-sensitive",
	}
	pctx := &proxyContext{
		w:         response,
		apiType:   APITypeCodex,
		requestID: "continuity-routing-conflict",
		startTime: time.Now(),
		info: RequestInfo{
			APIType: APITypeCodex,
			Path:    RouteCodexResponses,
			Method:  http.MethodPost,
		},
	}

	handler.handleNoProvider(pctx, selectionErr)

	if response.Code != http.StatusConflict {
		t.Fatalf("HTTP status = %d, want %d", response.Code, http.StatusConflict)
	}
	var envelope model.GatewayError
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != string(codexrecovery.ErrorCodeContinuityRoutingConflict) {
		t.Fatalf("error code = %q, want %q", envelope.Error.Code, codexrecovery.ErrorCodeContinuityRoutingConflict)
	}
	wantMessage := codexrecovery.ClientMessage(codexrecovery.ConditionContinuityRoutingConflict)
	if envelope.Error.Message != wantMessage {
		t.Fatalf("error message = %q, want %q", envelope.Error.Message, wantMessage)
	}
	if strings.Contains(response.Body.String(), "owner-route-sensitive") ||
		strings.Contains(response.Body.String(), "policy-route-sensitive") {
		t.Fatalf("client response leaked route identifiers: %s", response.Body.String())
	}

	waitFor(t, func() bool { return store.LogsLen() == 1 }, testPollTimeout)
	requestLog := store.LastLog()
	if requestLogClientTransportStatusCode(requestLog) != http.StatusConflict {
		t.Fatalf("ClientTransportStatusCode = %d, want %d", requestLogClientTransportStatusCode(requestLog), http.StatusConflict)
	}
	if requestLogTerminationReason(requestLog) != model.TerminationReasonProviderConfigurationError {
		t.Fatalf("TerminationReason = %q, want %q", requestLogTerminationReason(requestLog), model.TerminationReasonProviderConfigurationError)
	}
	if requestLog.TerminationActor == nil || *requestLog.TerminationActor != model.TerminationActorGateway {
		t.Fatalf("TerminationActor = %v, want %q", requestLog.TerminationActor, model.TerminationActorGateway)
	}
}

func wrappedContinuityHTTPError(kind codexcontinuity.ErrorKind, reason string) error {
	return &codexhttp.Error{
		Kind:  codexhttp.ErrorClientInput,
		Stage: "continuity_owner",
		Cause: &codexcontinuity.Error{Kind: kind, Reason: reason},
	}
}
