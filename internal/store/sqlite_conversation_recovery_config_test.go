package store

import (
	"context"
	"github.com/doraemonkeys/switch-a/internal/defaults"
	"testing"
)

func TestRetiredConversationRecoveryConfigRemoved(t *testing.T) {
	st := setupTestStore(t)
	ctx := context.Background()
	key := defaults.ConfigKeyConversationRecoveryPolicy
	if _, exists := GetDefaultConfigs()[key]; exists {
		t.Fatal("retired policy has a global default")
	}
	if err := st.SetConfig(ctx, key, "switch_account_preserve_conversation"); err != nil {
		t.Fatal(err)
	}
	if err := st.InitDefaultConfig(ctx); err != nil {
		t.Fatal(err)
	}
	values, err := st.GetAllConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := values[key]; exists {
		t.Fatal("retired global policy was retained")
	}
}
