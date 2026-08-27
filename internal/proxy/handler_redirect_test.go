package proxy

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"

	"go.uber.org/zap"
)

func TestHandlerReturnsRedirectResponseToAttemptCoordinator(t *testing.T) {
	for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var redirectedRequests atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				redirectedRequests.Add(1)
			}))
			defer target.Close()
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", target.URL+"/unselected-hop")
				w.WriteHeader(status)
			}))
			defer upstream.Close()

			store := newMockStore()
			store.configs[ConfigKeyGlobalMaxAttempts] = "1"
			store.providers = []model.Provider{withTestStaticCredential(model.Provider{
				ID: "redirect-provider", Name: "Redirect Provider", AuthMode: AuthModeBearer, Enabled: true,
				APITypes: []model.ProviderAPIType{{
					ProviderID: "redirect-provider", APIType: APITypeClaude, BaseURL: upstream.URL,
				}},
			}, APITypeClaude, "provider-key")}
			handler := NewHandler(Config{Store: store, Logger: zap.NewNop()})
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, RouteClaudeMessages, nil)
			handler.ServeHTTP(response, request)

			if response.Code != status || response.Header().Get("Location") != target.URL+"/unselected-hop" {
				t.Fatalf("coordinator response = %d/%q", response.Code, response.Header().Get("Location"))
			}
			if got := redirectedRequests.Load(); got != 0 {
				t.Fatalf("implicit redirect requests = %d, want zero", got)
			}
		})
	}
}
