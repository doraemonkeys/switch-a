package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
	"go.uber.org/zap"
)

func TestCodexHTTPAndWebSocketUseTheirOwnEndpointAndKey(t *testing.T) {
	httpAuth := make(chan string, 1)
	httpUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpAuth <- r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer httpUpstream.Close()
	wsAuth := make(chan string, 1)
	wsUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wsAuth <- r.Header.Get("Authorization")
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		typ, body, err := conn.Read(r.Context())
		if err == nil {
			_ = conn.Write(r.Context(), typ, body)
		}
	}))
	defer wsUpstream.Close()
	provider := withTestStaticCredential(model.Provider{
		ID: "dual", Enabled: true, AuthMode: "bearer", Priority: 10,
		APITypes: []model.ProviderAPIType{{ProviderID: "dual", APIType: "codex", Transport: "http", BaseURL: httpUpstream.URL}},
	}, "codex", "http-key")
	for i := range provider.APITypes {
		if provider.APITypes[i].Transport == "websocket" {
			provider.APITypes[i].BaseURL = wsUpstream.URL
		}
	}
	for i := range provider.CredentialSessions {
		route := &provider.CredentialSessions[i]
		if route.Transport == "websocket" {
			route.Credential.SessionID = "ws-credential"
			route.Credential.SecretData = "ws-key"
			route.Credential.Subject, route.Credential.AuthState = testCredentialIdentity("ws-key", "codex", credentialsession.KindAPIKey)
		}
	}
	store := newMockStore()
	store.providers = []model.Provider{provider}
	handler := newProxyCodexTestHandler(t, Config{Store: store, Logger: zap.NewNop()})
	request := httptest.NewRequest(http.MethodGet, "/codex/models", nil)
	request.Header.Set("Authorization", proxyCodexTestAuthorization)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("HTTP: %d %s", response.Code, response.Body)
	}
	if got := <-httpAuth; got != "Bearer http-key" {
		t.Fatal("HTTP credential", got)
	}

	var unsupportedCalls atomic.Int32
	unsupportedUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { unsupportedCalls.Add(1); w.WriteHeader(500) }))
	defer unsupportedUpstream.Close()
	unsupported := withTestStaticCredential(model.Provider{ID: "http-only", Enabled: true, Priority: 1, APITypes: []model.ProviderAPIType{{ProviderID: "http-only", APIType: "codex", BaseURL: unsupportedUpstream.URL}}}, "codex", "unused")
	unsupported.APITypes = unsupported.APITypes[:1]
	unsupported.CredentialSessions = unsupported.CredentialSessions[:1]
	store.mu.Lock()
	store.providers = []model.Provider{unsupported, provider}
	store.mu.Unlock()
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(server)+"/responses", proxyCodexDialOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := conn.Write(ctx, websocket.MessageText, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	_, body, err := conn.Read(ctx)
	if err != nil || string(body) != "hello" {
		t.Fatal(string(body), err)
	}
	if got := <-wsAuth; got != "Bearer ws-key" {
		t.Fatal("WS credential", got)
	}
	if unsupportedCalls.Load() != 0 {
		t.Fatal("unsupported provider was contacted")
	}
}
