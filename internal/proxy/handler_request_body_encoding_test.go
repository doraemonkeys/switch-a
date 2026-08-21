package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"

	"github.com/klauspost/compress/zstd"
	"go.uber.org/zap"
)

func TestHandlerDecodesZstdSemanticsAndForwardsOriginalWireBody(t *testing.T) {
	t.Parallel()
	semanticBody := []byte(`{"model":"gpt-5.6-sol","reasoning":{"effort":"xhigh","summary":"detailed"}}`)
	wireBody := encodeZstdRequestBody(t, semanticBody)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Content-Encoding"); got != "zstd" {
			t.Errorf("upstream Content-Encoding = %q, want zstd", got)
		}
		if request.ContentLength != int64(len(wireBody)) {
			t.Errorf("upstream ContentLength = %d, want %d", request.ContentLength, len(wireBody))
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read upstream request body: %v", err)
			return
		}
		if !bytes.Equal(body, wireBody) {
			t.Errorf("upstream body differs from original zstd wire body")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"response"}`))
	}))
	t.Cleanup(upstream.Close)

	store := newMockStore()
	store.providers = []model.Provider{{
		ID:       "provider-zstd",
		APIKey:   "test-key",
		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{
			ProviderID: "provider-zstd",
			APIType:    APITypeCodex,
			BaseURL:    upstream.URL,
		}},
	}}
	handler := NewHandler(Config{Store: store, Logger: zap.NewNop()})
	request := httptest.NewRequest(http.MethodPost, RouteCodexResponses, bytes.NewReader(wireBody))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Content-Encoding", "zstd")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%q", response.Code, http.StatusOK, response.Body.String())
	}
	waitFor(t, func() bool { return store.LogsLen() == 1 }, testPollTimeout)
	log := store.LastLog()
	if log.Model != "gpt-5.6-sol" {
		t.Fatalf("logged model = %q, want gpt-5.6-sol", log.Model)
	}
	if log.State == nil || *log.State != model.ReasoningObservationCaptured {
		t.Fatalf("reasoning state = %v, want %q", log.State, model.ReasoningObservationCaptured)
	}
	if log.Effort == nil || *log.Effort != "xhigh" {
		t.Fatalf("reasoning effort = %v, want xhigh", log.Effort)
	}
	if log.RequestBytes != int64(len(wireBody)) {
		t.Fatalf("request bytes = %d, want wire size %d", log.RequestBytes, len(wireBody))
	}
}

func encodeZstdRequestBody(t *testing.T, body []byte) []byte {
	t.Helper()
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatalf("create zstd encoder: %v", err)
	}
	t.Cleanup(func() {
		if err := encoder.Close(); err != nil {
			t.Errorf("close zstd encoder: %v", err)
		}
	})
	return encoder.EncodeAll(body, nil)
}
