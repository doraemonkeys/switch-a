package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth"
	"go.uber.org/zap"
)

func TestRedirectExecutionPolicyFollowsClaudeAndExposesManagedCodex(t *testing.T) {
	statuses := []int{
		http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect,
	}
	tests := []struct {
		name       string
		apiType    string
		route      string
		wantFollow bool
	}{
		{name: "ordinary Claude", apiType: APITypeClaude, route: RouteClaudeMessages, wantFollow: true},
		{name: "server-managed Codex", apiType: APITypeCodex, route: RouteCodexResponses, wantFollow: false},
	}
	for _, test := range tests {
		for _, status := range statuses {
			t.Run(test.name+"/"+http.StatusText(status), func(t *testing.T) {
				var targetRequests atomic.Int32
				target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					targetRequests.Add(1)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusNoContent)
				}))
				t.Cleanup(target.Close)
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Location", target.URL+"/target")
					w.Header().Add("Set-Cookie", "provider_session=server-only; Path=/")
					w.WriteHeader(status)
				}))
				t.Cleanup(upstream.Close)

				handler := newRedirectTestHandler(t, test.apiType, upstream.URL)
				response := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, test.route, strings.NewReader("{}"))
				request.Header.Set("Authorization", "Bearer client-scope-key")
				request.Header.Set("Content-Type", "application/json")
				handler.ServeHTTP(response, request)

				if test.wantFollow {
					if response.Code != http.StatusNoContent || targetRequests.Load() != 1 {
						t.Fatalf("followed response/target requests = %d/%d", response.Code, targetRequests.Load())
					}
					return
				}
				if response.Code != status || response.Header().Get("Location") != target.URL+"/target" {
					t.Fatalf("raw coordinator response = %d/%q", response.Code, response.Header().Get("Location"))
				}
				if got := targetRequests.Load(); got != 0 {
					t.Fatalf("managed-Codex redirect target requests = %d, want zero", got)
				}
				if strings.Contains(strings.Join(response.Header().Values("Set-Cookie"), ";"), "provider_session") {
					t.Fatalf("raw provider Set-Cookie reached client: %#v", response.Header().Values("Set-Cookie"))
				}
			})
		}
	}
}

func newRedirectTestHandler(t *testing.T, apiType, baseURL string) *Handler {
	t.Helper()
	store := newMockStore()
	store.configs[ConfigKeyGlobalMaxAttempts] = "1"
	store.providers = []model.Provider{withTestStaticCredential(model.Provider{
		ID: "redirect-provider", Name: "Redirect Provider", AuthMode: "bearer", Enabled: true,
		APITypes: []model.ProviderAPIType{{
			ProviderID: "redirect-provider", APIType: apiType, BaseURL: baseURL,
		}},
	}, apiType, "provider-key")}
	return newProxyCodexTestHandler(t, Config{
		Store: store, Auth: providerauth.NewService(providerauth.Config{}),
		CodexHTTP: newProxyCodexFixture(t).runtime, Logger: zap.NewNop(),
	})
}
