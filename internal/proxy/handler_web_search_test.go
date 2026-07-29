package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"

	"go.uber.org/zap"
)

func TestHandler_ServeHTTP_CodexWebSearchForwardsOpaqueContract(t *testing.T) {
	type capturedRequest struct {
		method  string
		path    string
		query   string
		body    string
		headers http.Header
		readErr error
	}

	received := make(chan capturedRequest, 1)
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		received <- capturedRequest{
			method:  r.Method,
			path:    r.URL.Path,
			query:   r.URL.RawQuery,
			body:    string(body),
			headers: r.Header.Clone(),
			readErr: err,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"output":"search result","future_field":{"opaque":true}}`))
	}))
	defer upstreamServer.Close()

	store := newMockStore()
	store.providers = []model.Provider{{
		ID:       "codex-provider",
		Name:     "Codex Provider",
		APIKey:   "provider-key",
		AuthMode: AuthModeBearer,
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{
			ProviderID: "codex-provider",
			APIType:    APITypeCodex,
			BaseURL:    upstreamServer.URL + "/backend-api/codex",
		}},
	}}
	handler := NewHandler(Config{Store: store, Logger: zap.NewNop()})

	requestBody := `{"model":"gpt-5","input":"find docs","commands":{"search_query":[{"q":"switch-a"}]},"future_field":{"preserve":true}}`
	req := httptest.NewRequest(http.MethodPost, RouteCodexWebSearch+"?source=codex", strings.NewReader(requestBody))
	req.Header.Set("Authorization", "Bearer client-key")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Originator", "codex_cli_rs")
	req.Header.Set("Version", "0.144.3")
	req.Header.Set("X-OpenAI-Fedramp", "true")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if got := w.Body.String(); got != `{"output":"search result","future_field":{"opaque":true}}` {
		t.Fatalf("response body = %q, want opaque upstream body", got)
	}

	select {
	case upstream := <-received:
		if upstream.readErr != nil {
			t.Fatalf("read upstream body: %v", upstream.readErr)
		}
		if upstream.method != http.MethodPost {
			t.Errorf("method = %q, want %q", upstream.method, http.MethodPost)
		}
		if upstream.path != "/backend-api/codex/alpha/search" {
			t.Errorf("path = %q, want %q", upstream.path, "/backend-api/codex/alpha/search")
		}
		if upstream.query != "source=codex" {
			t.Errorf("query = %q, want %q", upstream.query, "source=codex")
		}
		if upstream.body != requestBody {
			t.Errorf("body = %q, want original opaque body", upstream.body)
		}
		if got := upstream.headers.Get("Authorization"); got != "Bearer provider-key" {
			t.Errorf("Authorization = %q, want provider credential", got)
		}
		for name, want := range map[string]string{
			"Content-Type":     "application/json",
			"Originator":       "codex_cli_rs",
			"Version":          "0.144.3",
			"X-OpenAI-Fedramp": "true",
		} {
			if got := upstream.headers.Get(name); got != want {
				t.Errorf("%s = %q, want %q", name, got, want)
			}
		}
	case <-time.After(testResponseMaxDur):
		t.Fatal("upstream did not receive Codex web-search request")
	}
}
