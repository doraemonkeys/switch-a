package proxy

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/selector"
	storepkg "github.com/doraemonkeys/switch-a/internal/store"

	"go.uber.org/zap"
)

const (
	credentialRefreshTestProviderID = "refresh-provider"
	credentialRefreshTestSessionID  = "refresh-session"
	credentialRefreshTestAccountID  = "refresh-account"
	credentialRefreshTestIssuer     = "https://auth.openai.com"
	credentialRefreshTestClientID   = "app_EMoamEEZ73f0CkXaXp7hrann"
)

func TestHTTPUnauthorizedRefreshAdoptsAdvancedCredentialRevision(t *testing.T) {
	now := time.Now().UTC()
	persistence, err := storepkg.NewSQLiteStore(
		filepath.Join(t.TempDir(), "credential-refresh.db"), internal.RealClock{}, nil,
	)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = persistence.Close() })

	createCredentialRefreshTestProvider(t, persistence, now)
	events := &x3EventLog{}
	limiter := selector.NewConcurrencyLimiter()
	providerSelector := selector.NewSelector(selector.Config{
		Store: persistence, Limiter: limiter, Clock: internal.RealClock{}, Logger: zap.NewNop(),
	})
	auth := providerauth.NewService(providerauth.Config{
		CredentialStore: persistence,
		Clock:           internal.RealClock{},
		Logger:          zap.NewNop(),
		HTTPClient: credentialRefreshOAuthDoer(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(
					`{"access_token":"access-new","refresh_token":"refresh-new","expires_in":3600}`,
				)),
			}, nil
		}),
	})

	unauthorized := []byte(`{"error":{"message":"expired"}}`)
	accepted := []byte(`{"id":"accepted"}`)
	authorizations := make([]string, 0, 2)
	first := x3HTTPResponseStep(
		http.StatusUnauthorized, "application/json", "",
		x3NewTrackedBody(unauthorized, "close:unauthorized-revision", events), len(unauthorized),
	)
	first.onRequest = func(request *http.Request) {
		authorizations = append(authorizations, request.Header.Get("Authorization"))
	}
	second := x3HTTPResponseStep(
		http.StatusOK, "application/json", "",
		x3NewTrackedBody(accepted, "close:accepted-revision", events), len(accepted),
	)
	second.onRequest = func(request *http.Request) {
		authorizations = append(authorizations, request.Header.Get("Authorization"))
	}
	transport := &x3ScriptedTransport{events: events, steps: []x3TransportStep{first, second}}
	rules := x3CompiledRuleSet(t, 1, x3RetryThenSwitchAction(t, 1), "unused")
	handler := newProxyCodexTestHandler(t, Config{
		Store: persistence, Selector: providerSelector, Auth: auth,
		RuleSetProvider:  &x3RuleProvider{current: rules},
		ResponseAnalyzer: x3AnalyzerSpyForTest(t), Logger: zap.NewNop(),
	})

	recorder, pctx := executeCredentialRefreshTestRequest(t, handler, transport)
	if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), accepted) {
		t.Fatalf("response = (%d, %q), want (200, %q)", recorder.Code, recorder.Body.Bytes(), accepted)
	}
	if transport.Count() != 2 || len(pctx.attempts) != 1 {
		t.Fatalf("upstream fetches = %d, logical attempts = %d; want 2 and 1", transport.Count(), len(pctx.attempts))
	}
	if len(authorizations) != 2 || authorizations[0] != "Bearer access-old" || authorizations[1] != "Bearer access-new" {
		t.Fatalf("upstream authorizations = %v, want old then refreshed credential", authorizations)
	}
	refreshed, err := persistence.GetCredentialSession(context.Background(), credentialRefreshTestSessionID)
	if err != nil {
		t.Fatalf("GetCredentialSession() error = %v", err)
	}
	if refreshed.Version != 2 {
		t.Fatalf("credential version = %d, want 2", refreshed.Version)
	}
}

func createCredentialRefreshTestProvider(
	t *testing.T,
	persistence *storepkg.SQLiteStore,
	now time.Time,
) {
	t.Helper()
	secret, err := model.EncodeChatGPTProviderSecret(&model.ChatGPTProviderSecret{
		AccessToken: "access-old", RefreshToken: "refresh-old",
		OAuthIssuer: credentialRefreshTestIssuer, OAuthClientID: credentialRefreshTestClientID,
	})
	if err != nil {
		t.Fatalf("EncodeChatGPTProviderSecret() error = %v", err)
	}
	subject, err := credentialsession.AccountSubject(credentialRefreshTestAccountID)
	if err != nil {
		t.Fatalf("AccountSubject() error = %v", err)
	}
	expiresAt := now.Add(time.Hour)
	session := &credentialsession.Session{
		ID: credentialRefreshTestSessionID, Name: "Refresh session",
		Kind: credentialsession.KindChatGPT, SecretData: secret, Version: 1,
		AuthState: credentialsession.AuthState{
			Status: credentialsession.AuthStatusActive, AccountID: credentialRefreshTestAccountID,
			ExpiresAt: &expiresAt,
		},
	}
	if err := session.SetSubject(subject); err != nil {
		t.Fatalf("SetSubject() error = %v", err)
	}
	snapshot, err := session.Snapshot()
	if err != nil {
		t.Fatalf("Session.Snapshot() error = %v", err)
	}
	provider := &model.Provider{
		ID: credentialRefreshTestProviderID, Name: "Refresh provider", Enabled: true,
		AuthMode: "bearer", Concurrency: 1,
		APITypes: []model.ProviderAPIType{{
			ProviderID: credentialRefreshTestProviderID, APIType: APITypeCodex,
			BaseURL: "https://refresh-provider.example.test",
		}},
		CredentialSessions: []credentialsession.RouteSnapshot{{
			RouteTargetID: credentialRefreshTestProviderID, APIType: APITypeCodex,
			VendorScope: "openai", Credential: snapshot,
		}},
	}
	if err := persistence.CreateProviderWithCredentialSessions(
		context.Background(), provider, []*credentialsession.Session{session},
	); err != nil {
		t.Fatalf("CreateProviderWithCredentialSessions() error = %v", err)
	}
}

func executeCredentialRefreshTestRequest(
	t *testing.T,
	handler *Handler,
	transport HTTPTransport,
) (*httptest.ResponseRecorder, *proxyContext) {
	t.Helper()
	requestBody := []byte(`{"model":"refresh-model"}`)
	request := httptest.NewRequest(http.MethodPost, "/responses", bytes.NewReader(requestBody))
	authorizeProxyCodexTestRequest(request)
	codexOperation, err := handler.codexHTTP.Begin(
		request.Context(), request, APITypeCodex, "credential-refresh-request", testClientEvidence(requestBody, requestBody),
	)
	if err != nil {
		t.Fatalf("codex HTTP Begin() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	pctx := &proxyContext{
		handler: handler, r: request, w: recorder,
		cfg: &runtimeConfig{
			globalAuthMode: DefaultGlobalAuthMode, globalMaxAttempts: 1,
			readTimeout: time.Hour, sseIdleTimeout: time.Hour, stickyMode: model.StickyModeOff,
		},
		transport: transport, apiType: APITypeCodex, ingress: newTestIngress(t, requestBody),
		info: RequestInfo{
			Model: "refresh-model", APIType: APITypeCodex, Path: "/responses", Method: http.MethodPost,
		},
		selectReq: &model.SelectRequest{
			OperationID: "credential-refresh-request", APIType: APITypeCodex,
			Model: "refresh-model", StickyMode: model.StickyModeOff,
		},
		startTime: time.Now(), requestID: "credential-refresh-request", codex: codexOperation,
		liveBytes: &LiveBytesTracker{}, attempts: make([]model.RequestAttempt, 0),
	}
	pctx.upload = &ingressUpload{ingress: pctx.ingress, tracker: pctx.liveBytes}
	pctx.capture = handler.beginGatewayCapture(pctx.requestID, pctx.startTime)
	pctx.captureParticipates = pctx.capture.Valid()
	handler.executeProxy(request.Context(), pctx)
	if pctx.captureParticipates {
		pctx.capture.Finish(requestcapture.GatewayOutcome{})
	}
	return recorder, pctx
}

type credentialRefreshOAuthDoer func(*http.Request) (*http.Response, error)

func (do credentialRefreshOAuthDoer) Do(request *http.Request) (*http.Response, error) {
	return do(request)
}
