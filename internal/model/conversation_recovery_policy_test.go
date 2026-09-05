package model

import (
	"testing"

	"github.com/doraemonkeys/switch-a/internal/defaults"
)

func TestConversationRecoveryPolicy(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  ConversationRecoveryPolicy
		valid bool
	}{
		{string(ConversationRecoveryPreserveConversation), ConversationRecoveryPreserveConversation, true},
		{string(ConversationRecoverySwitchAccountPreserveConversation), ConversationRecoverySwitchAccountPreserveConversation, true},
		{"", ConversationRecoveryPreserveConversation, false},
		{"client_decides", ConversationRecoveryPreserveConversation, false},
		{" preserve_conversation", ConversationRecoveryPreserveConversation, false},
		{"PRESERVE_CONVERSATION", ConversationRecoveryPreserveConversation, false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			if got := ConversationRecoveryPolicy(tc.value).IsValid(); got != tc.valid {
				t.Fatalf("IsValid = %v, want %v", got, tc.valid)
			}
			if got := NormalizeConversationRecoveryPolicy(tc.value); got != tc.want {
				t.Fatalf("Normalize = %q, want %q", got, tc.want)
			}
		})
	}
	if defaults.DefaultConversationRecoveryPolicy != string(ConversationRecoveryPreserveConversation) {
		t.Fatal("runtime default must preserve the original account")
	}
}
