package websocketproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/recovery"
	"github.com/doraemonkeys/switch-a/internal/codex/websocket"
	"github.com/doraemonkeys/switch-a/internal/model"
	"go.uber.org/zap"
)

func TestGatewayUpgradeHandleReusesOnlyFinalPersistedCookies(t *testing.T) {
	incoming := make(chan string, 3)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		incoming <- r.Header.Get("Cookie")
		w.Header().Set("Set-Cookie", "provider_session=retained; Path=/")
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		if _, _, err := conn.Read(r.Context()); err != nil {
			return
		}
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.created","provider":"cookie-test"}`))
		_ = conn.Close(websocket.StatusNormalClosure, "complete")
	}))
	defer upstream.Close()
	store := newMockStore()
	store.providers = []model.Provider{{
		ID: "cookie-provider", AuthMode: "bearer", Enabled: true,
		APITypes:           []model.ProviderAPIType{{ProviderID: "cookie-provider", APIType: APITypeCodex, BaseURL: upstream.URL, Transport: "websocket"}},
		CredentialSessions: testCredentialSessions("cookie-provider", APITypeCodex, credentialsession.KindAPIKey, "provider-key"),
	}}
	gateway := newTestGateway(t, Config{Store: store, Logger: zap.NewNop()})
	server := newGatewayIntegrationServer(gateway, RequestConfig{
		GlobalAuthMode: "bearer", GlobalMaxAttempts: 1,
		ConversationRecoveryPolicy: model.ConversationRecoveryPreserveConversation,
	}, "cookie-lifecycle")
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	handle := ""
	for _, returned := range []bool{false, true, false} {
		options := codexDialOptions()
		if returned {
			options.HTTPHeader.Set("Cookie", providercookie.GatewayHandleName+"="+handle)
		}
		conn, response, err := websocket.Dial(ctx, wsURL(server)+"/responses?model=gpt-5", options)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.CloseNow() })
		if !returned {
			cookies := response.Cookies()
			if len(cookies) != 1 || cookies[0].Name != providercookie.GatewayHandleName {
				t.Fatalf("upgrade cookies = %+v", cookies)
			}
			handle = cookies[0].Value
		}
		if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.create","model":"gpt-5","input":[]}`)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := conn.Read(ctx); err != nil {
			t.Fatal(err)
		}
		_, _, _ = conn.Read(ctx)
		select {
		case got := <-incoming:
			want := ""
			if returned {
				want = "provider_session=retained"
			}
			if got != want {
				t.Fatalf("returned=%v: upstream Cookie = %q, want %q", returned, got, want)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

type invalidCookieUpgradeScheme struct{}

func (invalidCookieUpgradeScheme) ResolveExternalScheme(*http.Request) (providercookie.ResolvedExternalScheme, error) {
	return providercookie.ResolvedExternalScheme{}, nil
}

func TestGatewayCookieReservationFailureReturnsStructuredHTTPError(t *testing.T) {
	config := testCodexRuntimeConfig(t)
	config.ExternalScheme = invalidCookieUpgradeScheme{}
	runtime, err := codexws.New(config)
	if err != nil {
		t.Fatal(err)
	}
	gateway := newTestGateway(t, Config{Store: newMockStore(), Logger: zap.NewNop(), Codex: runtime})
	for _, probe := range []bool{false, true} {
		request := httptest.NewRequest(http.MethodGet, "https://gateway.test/responses", nil)
		authorizeCodexRequest(request)
		request.Header.Set("Connection", "Upgrade")
		request.Header.Set("Upgrade", "websocket")
		response := httptest.NewRecorder()
		gateway.Handle(t.Context(), response, request, RequestConfig{ProbeClientModel: probe}, APITypeCodex, "cookie-reservation-error", time.Now())
		if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), string(codexrecovery.ErrorCodeStateStoreUnavailable)) {
			t.Fatalf("probe=%v: response = %d %s", probe, response.Code, response.Body.String())
		}
		if response.Header().Get("Set-Cookie") != "" {
			t.Fatal("rejected upgrade published a reserved handle")
		}
	}
}
