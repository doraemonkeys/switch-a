package store

import (
	"context"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/defaults"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestConversationRecoveryConfigDefaultsAndPersistence(t *testing.T) {
	st := setupTestStore(t)
	ctx := context.Background()
	key := defaults.ConfigKeyConversationRecoveryPolicy
	if value, err := st.GetConfig(ctx, key); err != nil || value != defaults.DefaultConversationRecoveryPolicy {
		t.Fatalf("missing setting = %q, %v", value, err)
	}
	defaultValues := GetDefaultConfigs()
	defaultValues[key] = "modified"
	if GetDefaultConfigs()[key] != defaults.DefaultConversationRecoveryPolicy {
		t.Fatal("default map mutation escaped its caller")
	}
	want := string(model.ConversationRecoverySwitchAccountPreserveConversation)
	if err := st.SetConfigs(ctx, map[string]string{key: want}); err != nil {
		t.Fatal(err)
	}
	if err := st.InitDefaultConfig(ctx); err != nil {
		t.Fatal(err)
	}
	if value, err := st.GetConfig(ctx, key); err != nil || value != want {
		t.Fatalf("saved setting = %q, %v", value, err)
	}
	values, err := st.GetAllConfig(ctx)
	if err != nil || values[key] != want {
		t.Fatalf("all settings = %#v, %v", values, err)
	}
}
