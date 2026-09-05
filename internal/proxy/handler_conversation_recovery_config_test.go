package proxy

import (
	"context"
	"errors"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"
	"go.uber.org/zap"
)

type conversationRecoveryErrorStore struct{ *mockStore }

func (s *conversationRecoveryErrorStore) GetConfig(ctx context.Context, key string) (string, error) {
	if key == ConfigKeyConversationRecoveryPolicy {
		return string(model.ConversationRecoverySwitchAccountPreserveConversation), errors.New("config read failed")
	}
	return s.mockStore.GetConfig(ctx, key)
}

func TestHandlerLoadConfig_ConversationRecoverySnapshot(t *testing.T) {
	st := newMockStore()
	h := newProxyCodexTestHandler(t, Config{Store: st, Logger: zap.NewNop()})
	ctx := context.Background()
	initial, err := h.loadConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if initial.ConversationRecoveryPolicy != model.ConversationRecoveryPreserveConversation {
		t.Fatal("missing policy did not default")
	}

	st.configs[ConfigKeyConversationRecoveryPolicy] = string(model.ConversationRecoverySwitchAccountPreserveConversation)
	enabled, err := h.loadConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if enabled.ConversationRecoveryPolicy != model.ConversationRecoverySwitchAccountPreserveConversation {
		t.Fatal("next request did not use changed policy")
	}
	if initial.ConversationRecoveryPolicy != model.ConversationRecoveryPreserveConversation {
		t.Fatal("in-flight snapshot changed")
	}

	for _, value := range []string{string(model.ConversationRecoveryPreserveConversation), "client_decides"} {
		st.configs[ConfigKeyConversationRecoveryPolicy] = value
		cfg, err := h.loadConfig(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.ConversationRecoveryPolicy != model.ConversationRecoveryPreserveConversation {
			t.Fatalf("policy for %q = %q", value, cfg.ConversationRecoveryPolicy)
		}
	}
}

func TestHandlerLoadConfig_ConversationRecoveryReadError(t *testing.T) {
	h := newProxyCodexTestHandler(t, Config{
		Store:  &conversationRecoveryErrorStore{mockStore: newMockStore()},
		Logger: zap.NewNop(),
	})
	cfg, err := h.loadConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConversationRecoveryPolicy != model.ConversationRecoveryPreserveConversation {
		t.Fatal("read error did not use default")
	}
}
