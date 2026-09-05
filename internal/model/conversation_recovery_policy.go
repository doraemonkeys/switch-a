package model

// ConversationRecoveryPolicy controls whether state provenance constrains account
// selection. It is independent of routing eligibility and sticky preferences.
type ConversationRecoveryPolicy string

const (
	ConversationRecoveryPreserveConversation              ConversationRecoveryPolicy = "preserve_conversation"
	ConversationRecoverySwitchAccountPreserveConversation ConversationRecoveryPolicy = "switch_account_preserve_conversation"
)

func (p ConversationRecoveryPolicy) IsValid() bool {
	return p == ConversationRecoveryPreserveConversation ||
		p == ConversationRecoverySwitchAccountPreserveConversation
}

// NormalizeConversationRecoveryPolicy keeps existing account isolation when a
// runtime value is absent or invalid; management APIs reject invalid writes.
func NormalizeConversationRecoveryPolicy(value string) ConversationRecoveryPolicy {
	policy := ConversationRecoveryPolicy(value)
	if !policy.IsValid() {
		return ConversationRecoveryPreserveConversation
	}
	return policy
}
