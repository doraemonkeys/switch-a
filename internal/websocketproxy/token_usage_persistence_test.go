package websocketproxy

import (
	"net/http"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/clientaccess"
	"github.com/doraemonkeys/switch-a/internal/model"
	"go.uber.org/zap"
)

func TestWebSocketSessionPersistsObservedCacheWriteZero(t *testing.T) {
	t.Parallel()
	usage := ParseWithLogger([]byte(`{"type":"response.completed","response":{"id":"resp_1","usage":{"input_tokens":8,"input_tokens_details":{"cached_tokens":2,"cache_write_tokens":0},"output_tokens":1,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":9}}}`), nil)
	if usage == nil {
		t.Fatal("expected usage")
	}
	store := newMockStore()
	gateway := newTestGateway(t, Config{Store: store, Logger: zap.NewNop()})
	clientKey := clientaccess.IdentifyKey([]byte("unregistered-ws-client-key"))
	gateway.logWebSocketSession(RequestInfo{ClientAPIKey: clientKey, APIType: "codex", Method: http.MethodGet, Path: "/v1/responses"}, &WebSocketSessionResult{
		RequestID: "ws-token-usage",
		FinalResult: &WebSocketResult{
			HandshakeStatusCode: http.StatusSwitchingProtocols,
			TerminalCause:       model.TerminalCleanClose,
			TokenUsage:          usage,
		},
	}, time.Millisecond)
	log := store.LastLog()
	if log == nil {
		t.Fatal("expected log")
	}
	if log.ClientAPIKeyFingerprint != clientKey.Fingerprint || log.ClientAPIKeyMasked != clientKey.MaskedKey {
		t.Fatalf("client key attribution = %q/%q", log.ClientAPIKeyFingerprint, log.ClientAPIKeyMasked)
	}
	if log.PromptTokens == nil || *log.PromptTokens != 8 || log.CompletionTokens == nil || *log.CompletionTokens != 1 ||
		log.TotalTokens == nil || *log.TotalTokens != 9 || log.CacheReadInputTokens == nil || *log.CacheReadInputTokens != 2 ||
		log.CacheCreationInputTokens == nil || *log.CacheCreationInputTokens != 0 || log.ReasoningTokens == nil || *log.ReasoningTokens != 0 {
		t.Fatalf("persisted token pointers = %#v", log)
	}
}
