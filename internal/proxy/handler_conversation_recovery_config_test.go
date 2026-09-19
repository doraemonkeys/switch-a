package proxy

import (
	"context"
	"github.com/doraemonkeys/switch-a/internal/model"
	"go.uber.org/zap"
	"testing"
)

func TestHandlerLoadConfigIgnoresRetiredConversationRecoverySetting(t *testing.T) {
	st := newMockStore()
	h := newProxyCodexTestHandler(t, Config{Store: st, Logger: zap.NewNop()})
	for _, value := range []string{"", "preserve_conversation", "switch_account_preserve_conversation", "invalid"} {
		st.configs[ConfigKeyConversationRecoveryPolicy] = value
		cfg, err := h.loadConfig(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if cfg.ConversationRecoveryPolicy != model.ConversationRecoverySwitchAccountPreserveConversation {
			t.Fatalf("retired global setting %q changed protocol provenance behavior", value)
		}
	}
}
