package websocketproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	codexheaders "github.com/doraemonkeys/switch-a/internal/codex/headers"
	codexhttp "github.com/doraemonkeys/switch-a/internal/codex/http"
	codexws "github.com/doraemonkeys/switch-a/internal/codex/websocket"
	"github.com/doraemonkeys/switch-a/internal/model"
	"go.uber.org/zap"
)

func TestGatewayRejectedHandshakeLeavesHTTPFallbackUnbound(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer primary.Close()
	config := testCodexRuntimeConfig(t)
	wsRuntime, err := codexws.New(config)
	if err != nil {
		t.Fatal(err)
	}
	httpRuntime, err := codexhttp.New(codexhttp.Config{
		ClientIdentities: config.ClientIdentities, Continuity: config.Continuity,
		ProviderCookies: config.ProviderCookies, ExternalScheme: config.ExternalScheme,
	})
	if err != nil {
		t.Fatal(err)
	}
	store := newMockStore()
	store.providers = []model.Provider{{
		ID: "primary", AuthMode: "bearer", Enabled: true,
		APITypes:           []model.ProviderAPIType{{ProviderID: "primary", APIType: APITypeCodex, BaseURL: primary.URL}},
		CredentialSessions: testCredentialSessions("primary", APITypeCodex, credentialsession.KindAPIKey, "primary-key"),
	}}
	gateway := newTestGateway(t, Config{Store: store, Codex: wsRuntime, Logger: zap.NewNop()})
	server := newGatewayIntegrationServer(gateway, RequestConfig{
		GlobalAuthMode: "bearer", GlobalMaxAttempts: 1,
		ConversationRecoveryPolicy: model.ConversationRecoveryPreserveConversation,
	}, "rejected-before-http-fallback")
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	options := codexDialOptions()
	options.HTTPHeader.Set("Thread-Id", "new-thread-http-fallback")
	options.HTTPHeader.Set("Session-Id", "new-session-http-fallback")
	conn, response, err := websocket.Dial(ctx, wsURL(server)+"/responses?model=gpt-5", options)
	if conn != nil {
		_ = conn.CloseNow()
	}
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusNotFound {
		t.Fatalf("expected rejected WebSocket handshake, response=%v error=%v", response, err)
	}
	waitFor(t, func() bool { return store.LastLog() != nil }, testPollTimeout)

	request := httptest.NewRequest(http.MethodPost, "http://gateway.test/responses", nil)
	request.Header = options.HTTPHeader.Clone()
	fallback, err := httpRuntime.Begin(ctx, request, APITypeCodex, "http-fallback",
		model.ConversationRecoveryPreserveConversation, codexheaders.ClientEvidence{})
	if err != nil {
		t.Fatal(err)
	}
	defer fallback.Discard()
	if authority, _ := fallback.RequiredAuthority(); authority != nil {
		t.Fatal("final rejected WebSocket attempt left HTTP fallback bound to the failed account")
	}
	second := disclosurePreparedDial(t, "http-replacement")
	upstreamRequest := request.Clone(ctx)
	upstreamRequest.URL = second.finalURL
	attempt, err := fallback.PrepareAttempt(ctx, upstreamRequest, second.candidate, second.applied)
	if err != nil {
		t.Fatalf("HTTP fallback could not use another account: %v", err)
	}
	if err := attempt.MarkDisclosed(ctx); err != nil {
		t.Fatal(err)
	}
}
