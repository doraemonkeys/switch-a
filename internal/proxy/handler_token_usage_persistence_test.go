package proxy

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/tokenusage"
	"go.uber.org/zap"
)

func TestLogRequestPersistsObservedTokenPointersExactly(t *testing.T) {
	t.Parallel()
	fixture, err := os.ReadFile(filepath.Join("..", "responseanalysis", "testdata", "token-usage", "openai-cache-write-zero.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name           string
		payload        []byte
		wantTotal      *int64
		wantCacheWrite *int64
		wantReasoning  *int64
	}{
		{name: "explicit zeros", payload: fixture, wantTotal: tokenPointer(15), wantCacheWrite: tokenPointer(0), wantReasoning: tokenPointer(0)},
		{name: "missing optional and total", payload: []byte(`{"usage":{"input_tokens":4,"output_tokens":2}}`)},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store := newMockStore()
			handler := NewHandler(Config{Store: store, Logger: zap.NewNop()})
			usage := responseanalysis.ObserveUsage(test.payload, nil)
			if usage == nil {
				t.Fatal("expected usage")
			}
			pctx := &proxyContext{
				requestID: "usage-log", startTime: time.Now(),
				info: RequestInfo{APIType: APITypeCodex, Method: http.MethodPost, Path: "/v1/responses"},
			}
			handler.logRequest(pctx, logRequestInputs{
				Facts: nonWebSocketRuntimeFacts{
					ClientTransportStatusCode: http.StatusOK,
					Success:                   true, ResponseCommitted: true, ServiceStarted: true,
				},
				TokenUsage: usage,
			})
			store.mu.Lock()
			if len(store.logs) != 1 {
				store.mu.Unlock()
				t.Fatalf("logs = %d", len(store.logs))
			}
			log := store.logs[0]
			store.mu.Unlock()
			assertTokenPointer(t, "prompt", log.PromptTokens, 4, test.name == "missing optional and total")
			if test.name == "explicit zeros" {
				assertTokenPointer(t, "prompt", log.PromptTokens, 12, true)
				assertTokenPointer(t, "completion", log.CompletionTokens, 3, true)
			}
			assertOptionalTokenPointer(t, "total", log.TotalTokens, test.wantTotal)
			assertOptionalTokenPointer(t, "cache write", log.CacheCreationInputTokens, test.wantCacheWrite)
			assertOptionalTokenPointer(t, "reasoning", log.ReasoningTokens, test.wantReasoning)
		})
	}
}

func TestRetryStateRetainsOnlyTerminalAttemptUsage(t *testing.T) {
	t.Parallel()
	first := &tokenusage.TokenUsage{
		PromptTokens:  tokenusage.ObservedCount{Value: 100, Present: true},
		CacheCreation: &tokenusage.CacheCreation{InputTokens: tokenusage.ObservedCount{Value: 9, Present: true}},
	}
	winner := &tokenusage.TokenUsage{
		PromptTokens:  tokenusage.ObservedCount{Value: 2, Present: true},
		CacheCreation: &tokenusage.CacheCreation{InputTokens: tokenusage.ObservedCount{Value: 0, Present: true}},
	}
	state := &retryState{}
	handler := &Handler{}
	handler.applyForwardResult(state, forwardResult{tokenUsage: first})
	handler.applyForwardResult(state, forwardResult{tokenUsage: winner})
	if state.tokenUsage == nil || state.tokenUsage.PromptTokens != winner.PromptTokens ||
		state.tokenUsage.CacheCreation == nil || state.tokenUsage.CacheCreation.InputTokens != winner.CacheCreation.InputTokens {
		t.Fatalf("terminal usage = %#v, want winning attempt %#v", state.tokenUsage, winner)
	}
}

func TestHandlerPersistsHTTPAndInferredSSEUsage(t *testing.T) {
	for _, test := range []struct {
		name                string
		responseContentType string
		accept              string
		body                string
		wantPrompt          int64
		wantCompletion      int64
		wantTotal           int64
		wantCacheRead       *int64
		wantCacheWrite      *int64
		wantReasoning       *int64
	}{
		{
			name: "HTTP JSON", responseContentType: "application/json",
			body:       `{"id":"resp_1","status":"completed","usage":{"input_tokens":12,"input_tokens_details":{"cached_tokens":2,"cache_write_tokens":0},"output_tokens":3,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":15}}`,
			wantPrompt: 12, wantCompletion: 3, wantTotal: 15,
			wantCacheRead: tokenPointer(2), wantCacheWrite: tokenPointer(0), wantReasoning: tokenPointer(0),
		},
		{
			name: "SSE inferred from Accept", accept: "text/event-stream",
			body: "event: response.completed\n" +
				`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":12,"input_tokens_details":{"cached_tokens":2,"cache_write_tokens":0},"output_tokens":3,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":15}}}` + "\n\n",
			wantPrompt: 12, wantCompletion: 3, wantTotal: 15,
			wantCacheRead: tokenPointer(2), wantCacheWrite: tokenPointer(0), wantReasoning: tokenPointer(0),
		},
		{
			name: "SSE burst preserves later partial fields", responseContentType: "text/event-stream",
			body: "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1}}\n\n" +
				"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":2}}\n\n" +
				"data: {\"choices\":[],\"usage\":{\"completion_tokens\":3}}\n\n" +
				"data: {\"choices\":[],\"usage\":{\"total_tokens\":4}}\n\n" +
				"data: {\"choices\":[],\"usage\":{\"completion_tokens\":5}}\n\n" +
				"data: [DONE]\n\n",
			wantPrompt: 2, wantCompletion: 5, wantTotal: 4,
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if test.responseContentType == "" {
					w.Header()["Content-Type"] = nil
				} else {
					w.Header().Set("Content-Type", test.responseContentType)
				}
				_, _ = w.Write([]byte(test.body))
			}))
			defer upstream.Close()

			store := newMockStore()
			store.providers = []model.Provider{{
				ID: "p1", Name: "provider", APIKey: "test", AuthMode: "bearer", Enabled: true,
				APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: APITypeCodex, BaseURL: upstream.URL}},
			}}
			handler := NewHandler(Config{Store: store, Logger: zap.NewNop()})
			request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-test","input":"hello"}`))
			request.Header.Set("Content-Type", "application/json")
			if test.accept != "" {
				request.Header.Set("Accept", test.accept)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d", response.Code)
			}
			waitFor(t, func() bool { return store.LogsLen() == 1 }, testPollTimeout)
			log := store.LastLog()
			if log == nil {
				t.Fatal("expected persisted log")
			}
			assertTokenPointer(t, "prompt", log.PromptTokens, test.wantPrompt, true)
			assertTokenPointer(t, "completion", log.CompletionTokens, test.wantCompletion, true)
			assertTokenPointer(t, "total", log.TotalTokens, test.wantTotal, true)
			assertOptionalTokenPointer(t, "cache read", log.CacheReadInputTokens, test.wantCacheRead)
			assertOptionalTokenPointer(t, "cache write", log.CacheCreationInputTokens, test.wantCacheWrite)
			assertOptionalTokenPointer(t, "reasoning", log.ReasoningTokens, test.wantReasoning)
		})
	}
}

func tokenPointer(value int64) *int64 { return &value }

func assertTokenPointer(t *testing.T, name string, got *int64, want int64, check bool) {
	t.Helper()
	if check && (got == nil || *got != want) {
		t.Fatalf("%s = %v, want %d", name, got, want)
	}
}

func assertOptionalTokenPointer(t *testing.T, name string, got, want *int64) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Fatalf("%s = %v, want nil", name, *got)
		}
		return
	}
	if got == nil || *got != *want {
		t.Fatalf("%s = %v, want %d", name, got, *want)
	}
}
