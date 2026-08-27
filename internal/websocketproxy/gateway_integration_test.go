package websocketproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/model"

	"github.com/coder/websocket"
	"go.uber.org/zap/zaptest"
)

func TestNewGateway_NilCodexRuntimePanics(t *testing.T) {
	defer func() {
		panicValue := recover()
		message, ok := panicValue.(string)
		if !ok || message != "websocketproxy: Codex runtime is required but was nil" {
			t.Fatalf("panic = %v, want mandatory Codex runtime failure", panicValue)
		}
	}()
	NewGateway(Config{Store: newMockStore(), Logger: zaptest.NewLogger(t)})
}

func TestGateway_RelaysSessionAndPersistsLifecycle(t *testing.T) {
	const (
		providerID = "gateway-integration-provider"
		requestID  = "gateway-integration-request"
		clientData = `{"type":"response.create","response":{"model":"gpt-5"}}`
		serverData = `{"type":"response.created","response":{"model":"gpt-5"}}`
	)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "Bearer provider-secret" {
			t.Errorf("Authorization = %q, want provider credential", got)
		}
		connection, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("accept upstream websocket: %v", err)
			return
		}
		defer connection.CloseNow()
		messageType, payload, err := connection.Read(request.Context())
		if err != nil {
			t.Errorf("read client frame: %v", err)
			return
		}
		if messageType != websocket.MessageText || string(payload) != clientData {
			t.Errorf("client frame = (%v, %q), want text/%q", messageType, payload, clientData)
			return
		}
		if err := connection.Write(request.Context(), websocket.MessageText, []byte(serverData)); err != nil {
			t.Errorf("write upstream frame: %v", err)
			return
		}
		_ = connection.Close(websocket.StatusNormalClosure, "complete")
	}))
	defer upstream.Close()

	store := newMockStore()
	store.providers = []model.Provider{{
		ID: providerID, Name: "Gateway Integration Provider",
		AuthMode: "bearer", Enabled: true,
		APITypes:           []model.ProviderAPIType{{ProviderID: providerID, APIType: APITypeCodex, BaseURL: upstream.URL}},
		CredentialSessions: testCredentialSessions(providerID, APITypeCodex, credentialsession.KindAPIKey, "provider-secret"),
	}}
	gateway := newTestGateway(t, Config{Store: store, Logger: zaptest.NewLogger(t)})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		gateway.Handle(request.Context(), w, request, RequestConfig{
			GlobalAuthMode: "bearer", GlobalMaxAttempts: 1,
		}, APITypeCodex, requestID, time.Now())
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, wsURL(server)+"/responses?model=gpt-5", codexDialOptions())
	if err != nil {
		t.Fatalf("dial gateway websocket: %v", err)
	}
	if err := connection.Write(ctx, websocket.MessageText, []byte(clientData)); err != nil {
		t.Fatalf("write client frame: %v", err)
	}
	messageType, payload, err := connection.Read(ctx)
	if err != nil {
		t.Fatalf("read upstream frame: %v", err)
	}
	if messageType != websocket.MessageText || string(payload) != serverData {
		t.Fatalf("upstream frame = (%v, %q), want text/%q", messageType, payload, serverData)
	}
	_, _, _ = connection.Read(ctx)
	_ = connection.CloseNow()

	waitFor(t, func() bool { return store.LastLog() != nil }, testPollTimeout)
	log := store.LastLog()
	if log.ProviderID != providerID || !log.IsWebSocket || log.RequestID != requestID {
		t.Fatalf("request log = %#v, want provider=%q websocket request=%q", log, providerID, requestID)
	}
}

func TestGateway_NoProviderReturnsCanonicalGatewayFailure(t *testing.T) {
	t.Parallel()
	store := newMockStore()
	gateway := newTestGateway(t, Config{Store: store, Logger: zaptest.NewLogger(t)})
	request := httptest.NewRequest(http.MethodGet, "http://gateway.test/responses?model=gpt-5", nil)
	authorizeCodexRequest(request)
	recorder := httptest.NewRecorder()

	gateway.Handle(request.Context(), recorder, request, RequestConfig{GlobalMaxAttempts: 1}, APITypeCodex, "no-provider", time.Now())

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", contentType)
	}
	waitFor(t, func() bool { return store.LastLog() != nil }, testPollTimeout)
	if log := store.LastLog(); log == nil || log.ProviderID != "" || log.IsWebSocket != true {
		t.Fatalf("request log = %#v, want providerless websocket failure", log)
	}
}

func TestGateway_ReplacesProviderAfterPreVisibleSemanticFailure(t *testing.T) {
	const (
		clientData   = `{"type":"response.create","response":{"model":"gpt-5"}}`
		semanticData = `{"type":"error","status":403,"error":{"type":"auth_error","code":"model_not_allowed","message":"model access denied"}}`
		fallbackData = `{"type":"response.created","provider":"fallback"}`
		subprotocol  = "realtime.v1"
	)
	primaryProtocol := make(chan string, 1)
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(w, request, &websocket.AcceptOptions{Subprotocols: []string{subprotocol}})
		if err != nil {
			t.Errorf("accept primary websocket: %v", err)
			return
		}
		defer connection.CloseNow()
		primaryProtocol <- connection.Subprotocol()
		if _, _, err := connection.Read(request.Context()); err != nil {
			t.Errorf("read primary client frame: %v", err)
			return
		}
		if err := connection.Write(request.Context(), websocket.MessageText, []byte(semanticData)); err != nil {
			t.Errorf("write primary semantic failure: %v", err)
		}
	}))
	defer primary.Close()
	fallbackProtocol := make(chan string, 1)
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Sec-WebSocket-Protocol"); got != subprotocol {
			t.Errorf("replacement offer = %q, want fixed %q", got, subprotocol)
		}
		connection, err := websocket.Accept(w, request, &websocket.AcceptOptions{Subprotocols: []string{subprotocol}})
		if err != nil {
			t.Errorf("accept fallback websocket: %v", err)
			return
		}
		defer connection.CloseNow()
		fallbackProtocol <- connection.Subprotocol()
		messageType, payload, err := connection.Read(request.Context())
		if err != nil {
			t.Errorf("read replayed client frame: %v", err)
			return
		}
		if messageType != websocket.MessageText || string(payload) != clientData {
			t.Errorf("replayed frame = (%v, %q), want text/%q", messageType, payload, clientData)
			return
		}
		if err := connection.Write(request.Context(), websocket.MessageText, []byte(fallbackData)); err != nil {
			t.Errorf("write fallback response: %v", err)
			return
		}
		_ = connection.Close(websocket.StatusNormalClosure, "fallback complete")
	}))
	defer fallback.Close()

	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "primary", AuthMode: "bearer", Enabled: true, APITypes: []model.ProviderAPIType{{ProviderID: "primary", APIType: APITypeCodex, BaseURL: primary.URL}}, CredentialSessions: testCredentialSessions("primary", APITypeCodex, credentialsession.KindAPIKey, "primary-key")},
		{ID: "fallback", AuthMode: "bearer", Enabled: true, APITypes: []model.ProviderAPIType{{ProviderID: "fallback", APIType: APITypeCodex, BaseURL: fallback.URL}}, CredentialSessions: testCredentialSessions("fallback", APITypeCodex, credentialsession.KindAPIKey, "fallback-key")},
	}
	gateway := newTestGateway(t, Config{Store: store, Logger: zaptest.NewLogger(t)})
	server := newGatewayIntegrationServer(gateway, RequestConfig{GlobalAuthMode: "bearer", GlobalMaxAttempts: 3}, "semantic-replacement")
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(
		ctx,
		wsURL(server)+"/responses?model=gpt-5",
		codexDialOptions("realtime.v2", subprotocol),
	)
	if err != nil {
		t.Fatalf("dial gateway websocket: %v", err)
	}
	if got := connection.Subprotocol(); got != subprotocol {
		t.Fatalf("downstream Subprotocol() = %q, want %q", got, subprotocol)
	}
	if err := connection.Write(ctx, websocket.MessageText, []byte(clientData)); err != nil {
		t.Fatalf("write client frame: %v", err)
	}
	messageType, payload, err := connection.Read(ctx)
	if err != nil {
		t.Fatalf("read fallback response: %v", err)
	}
	if messageType != websocket.MessageText || string(payload) != fallbackData {
		t.Fatalf("client-visible frame = (%v, %q), want fallback response", messageType, payload)
	}
	if got := <-primaryProtocol; got != subprotocol {
		t.Fatalf("primary Subprotocol() = %q, want %q", got, subprotocol)
	}
	if got := <-fallbackProtocol; got != subprotocol {
		t.Fatalf("fallback Subprotocol() = %q, want %q", got, subprotocol)
	}
	_, _, _ = connection.Read(ctx)
	_ = connection.CloseNow()

	waitFor(t, func() bool { return len(store.LastAttempts(2)) == 2 }, testPollTimeout)
	attempts := store.LastAttempts(2)
	if attempts[0].ProviderID != "primary" || attempts[1].ProviderID != "fallback" {
		t.Fatalf("attempt providers = [%s %s], want [primary fallback]", attempts[0].ProviderID, attempts[1].ProviderID)
	}
}

func TestGateway_ReplacesProviderConfigurationFailureBeforeUpgrade(t *testing.T) {
	const responseData = `{"type":"response.created","provider":"ready"}`
	ready := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("accept ready websocket: %v", err)
			return
		}
		defer connection.CloseNow()
		if _, _, err := connection.Read(request.Context()); err != nil {
			t.Errorf("read client frame: %v", err)
			return
		}
		if err := connection.Write(request.Context(), websocket.MessageText, []byte(responseData)); err != nil {
			t.Errorf("write ready response: %v", err)
			return
		}
		_ = connection.Close(websocket.StatusNormalClosure, "complete")
	}))
	defer ready.Close()

	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "misconfigured", AuthMode: "bearer", Enabled: true, APITypes: []model.ProviderAPIType{{ProviderID: "misconfigured", APIType: APITypeCodex}}, CredentialSessions: testCredentialSessions("misconfigured", APITypeCodex, credentialsession.KindAPIKey, "key")},
		{ID: "ready", AuthMode: "bearer", Enabled: true, APITypes: []model.ProviderAPIType{{ProviderID: "ready", APIType: APITypeCodex, BaseURL: ready.URL}}, CredentialSessions: testCredentialSessions("ready", APITypeCodex, credentialsession.KindAPIKey, "key")},
	}
	gateway := newTestGateway(t, Config{Store: store, Logger: zaptest.NewLogger(t)})
	server := newGatewayIntegrationServer(gateway, RequestConfig{GlobalAuthMode: "bearer", GlobalMaxAttempts: 2}, "configuration-replacement")
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, wsURL(server)+"/responses?model=gpt-5", codexDialOptions())
	if err != nil {
		t.Fatalf("dial gateway websocket: %v", err)
	}
	if err := connection.Write(ctx, websocket.MessageText, []byte(`{"type":"response.create"}`)); err != nil {
		t.Fatalf("write client frame: %v", err)
	}
	_, payload, err := connection.Read(ctx)
	if err != nil || string(payload) != responseData {
		t.Fatalf("ready response = %q, error = %v", payload, err)
	}
	_, _, _ = connection.Read(ctx)
	_ = connection.CloseNow()
	waitFor(t, func() bool { return len(store.LastAttempts(2)) == 2 }, testPollTimeout)
}

type rotatingGatewayAuthenticator struct {
	refreshed    atomic.Bool
	refreshCalls atomic.Int32
}

func (auth *rotatingGatewayAuthenticator) ApplyProviderCredentials(
	_ context.Context,
	headers http.Header,
	candidate codexidentity.CandidateSnapshot,
	_, _ string,
	_ *http.Request,
	finalURL *url.URL,
) (codexidentity.AppliedIdentity, error) {
	token := "initial-token"
	if auth.refreshed.Load() {
		token = "refreshed-token"
	}
	headers.Set("Authorization", "Bearer "+token)
	subject, err := codexidentity.CredentialSubjectFromSession(candidate.Credential().Subject)
	if err != nil {
		return codexidentity.AppliedIdentity{}, err
	}
	return codexidentity.AppliedIdentityFromRequest(candidate.Authority().Vendor(), finalURL, subject)
}

func (auth *rotatingGatewayAuthenticator) RefreshCredentialSession(context.Context, credentialsession.Snapshot) (bool, error) {
	auth.refreshCalls.Add(1)
	auth.refreshed.Store(true)
	return true, nil
}

func TestGateway_RetriesSameManagedProviderAfterUnauthorizedHandshake(t *testing.T) {
	const (
		responseData = `{"type":"response.created","provider":"refreshed"}`
		subprotocol  = "realtime.v1"
	)
	var upstreamCalls atomic.Int32
	upstreamOffers := make(chan string, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		upstreamCalls.Add(1)
		upstreamOffers <- request.Header.Get("Sec-WebSocket-Protocol")
		if request.Header.Get("Authorization") == "Bearer initial-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		connection, err := websocket.Accept(w, request, &websocket.AcceptOptions{Subprotocols: []string{subprotocol}})
		if err != nil {
			t.Errorf("accept refreshed websocket: %v", err)
			return
		}
		defer connection.CloseNow()
		if got := connection.Subprotocol(); got != subprotocol {
			t.Errorf("upstream Subprotocol() = %q, want %q", got, subprotocol)
		}
		if _, _, err := connection.Read(request.Context()); err != nil {
			t.Errorf("read client frame: %v", err)
			return
		}
		if err := connection.Write(request.Context(), websocket.MessageText, []byte(responseData)); err != nil {
			t.Errorf("write refreshed response: %v", err)
			return
		}
		_ = connection.Close(websocket.StatusNormalClosure, "complete")
	}))
	defer upstream.Close()

	store := newMockStore()
	store.providers = []model.Provider{{
		ID:                 "managed",
		Enabled:            true,
		APITypes:           []model.ProviderAPIType{{ProviderID: "managed", APIType: APITypeCodex, BaseURL: upstream.URL}},
		CredentialSessions: testCredentialSessions("managed", APITypeCodex, credentialsession.KindChatGPT, `{"access_token":"initial-token"}`),
	}}
	auth := &rotatingGatewayAuthenticator{}
	gateway := newTestGateway(t, Config{Store: store, Auth: auth, Logger: zaptest.NewLogger(t)})
	server := newGatewayIntegrationServer(gateway, RequestConfig{GlobalAuthMode: "bearer", GlobalMaxAttempts: 1}, "managed-refresh")
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(
		ctx,
		wsURL(server)+"/responses?model=gpt-5",
		codexDialOptions("realtime.v2", subprotocol),
	)
	if err != nil {
		t.Fatalf("dial gateway websocket: %v", err)
	}
	if got := connection.Subprotocol(); got != subprotocol {
		t.Fatalf("downstream Subprotocol() = %q, want %q", got, subprotocol)
	}
	if err := connection.Write(ctx, websocket.MessageText, []byte(`{"type":"response.create"}`)); err != nil {
		t.Fatalf("write client frame: %v", err)
	}
	_, payload, err := connection.Read(ctx)
	if err != nil || string(payload) != responseData {
		t.Fatalf("refreshed response = %q, error = %v", payload, err)
	}
	_, _, _ = connection.Read(ctx)
	_ = connection.CloseNow()
	waitFor(t, func() bool { return store.LastLog() != nil }, testPollTimeout)
	if auth.refreshCalls.Load() != 1 || upstreamCalls.Load() != 2 {
		t.Fatalf("refresh calls = %d, upstream calls = %d, want 1/2", auth.refreshCalls.Load(), upstreamCalls.Load())
	}
	for attempt := 0; attempt < 2; attempt++ {
		if got := <-upstreamOffers; got != "realtime.v2,"+subprotocol {
			t.Fatalf("dial %d offer = %q, want full client offer", attempt+1, got)
		}
	}
}

func newGatewayIntegrationServer(gateway *Gateway, config RequestConfig, requestID string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		gateway.Handle(request.Context(), w, request, config, APITypeCodex, requestID, time.Now())
	}))
}
