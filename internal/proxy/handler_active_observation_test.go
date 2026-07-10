package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"switch-a/internal/model"

	"go.uber.org/zap"
)

func TestHandlerActiveRequestExposesReasoningAndHTTPTraffic(t *testing.T) {
	const requestBody = `{"model":"claude-test","output_config":{"effort":"high"}}`
	responseChunk := []byte("streaming-response")
	upstreamStarted := make(chan struct{}, 1)
	releaseUpstream := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseUpstream) }) }

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read upstream request body: %v", err)
			return
		}
		if string(body) != requestBody {
			t.Errorf("upstream request body = %q, want %q", body, requestBody)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(responseChunk); err != nil {
			t.Errorf("write upstream response chunk: %v", err)
			return
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		upstreamStarted <- struct{}{}
		<-releaseUpstream
	}))
	t.Cleanup(upstream.Close)
	// Release must run before server shutdown if an assertion aborts the test.
	t.Cleanup(release)

	store := newMockStore()
	store.providers = []model.Provider{{
		ID:       "provider-1",
		APIKey:   "test-key",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{
			ProviderID: "provider-1",
			APIType:    APITypeClaude,
			BaseURL:    upstream.URL,
		}},
	}}
	registry := NewActiveRequestRegistry()
	handler := NewHandler(Config{
		Store:          store,
		ActiveRegistry: registry,
		Logger:         zap.NewNop(),
	})
	req := httptest.NewRequest(http.MethodPost, RouteClaudeMessages, strings.NewReader(requestBody))
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(recorder, req)
		close(done)
	}()

	select {
	case <-upstreamStarted:
	case <-time.After(testResponseMaxDur):
		t.Fatal("timeout waiting for upstream HTTP response")
	}
	var active ActiveRequest
	waitFor(t, func() bool {
		requests := registry.List()
		if len(requests) != 1 {
			return false
		}
		active = requests[0]
		return active.BytesSent == int64(len(requestBody)) &&
			active.BytesReceived == int64(len(responseChunk))
	}, testResponseMaxDur)

	if active.State == nil || *active.State != model.ReasoningObservationCaptured {
		t.Fatalf("reasoning state = %v, want %q", active.State, model.ReasoningObservationCaptured)
	}
	if active.Effort == nil || *active.Effort != "high" {
		t.Fatalf("reasoning effort = %v, want high", active.Effort)
	}
	if active.IsWebSocket || active.IsSSE {
		t.Fatalf("transport flags = websocket:%t sse:%t, want regular HTTP", active.IsWebSocket, active.IsSSE)
	}
	if active.LastActivityAt == 0 {
		t.Fatal("LastActivityAt must reflect HTTP response traffic")
	}

	release()
	select {
	case <-done:
	case <-time.After(testResponseMaxDur):
		t.Fatal("timeout waiting for proxied HTTP request to finish")
	}
	if requests := registry.List(); len(requests) != 0 {
		t.Fatalf("active requests after completion = %d, want 0", len(requests))
	}
}
