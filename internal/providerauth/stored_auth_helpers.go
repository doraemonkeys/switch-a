package providerauth

import (
	"strings"
	"time"

	"switch-a/internal/model"
)

func normalizeStoredChatGPTAuthStatus(stored *storedChatGPTCredential) ProviderAuthStatus {
	if stored == nil {
		return ProviderAuthStatusNotConnected
	}
	if modelStatus := ProviderAuthStatus(strings.TrimSpace(string(stored.AuthStatus))); model.IsValidProviderAuthStatus(modelStatus) {
		return modelStatus
	}
	if stored.Ready() {
		return ProviderAuthStatusActive
	}
	return ProviderAuthStatusNotConnected
}

func reasonForStoredChatGPTAuthStatus(stored *storedChatGPTCredential, status ProviderAuthStatus) string {
	if stored == nil {
		if status == ProviderAuthStatusNotConnected {
			return ProviderAuthReasonLoginRequired
		}
		return ""
	}
	if reason := strings.TrimSpace(stored.AuthReason); reason != "" {
		return reason
	}
	switch status {
	case ProviderAuthStatusActive:
		return ""
	case ProviderAuthStatusReauthRequired:
		return ProviderAuthReasonCredentialInvalid
	case ProviderAuthStatusNotConnected:
		return ProviderAuthReasonLoginRequired
	default:
		return ""
	}
}

func buildChatGPTAuthView(stored *storedChatGPTCredential) *ProviderAuthView {
	status := normalizeStoredChatGPTAuthStatus(stored)
	var credential *model.ChatGPTProviderCredential
	var lastError string
	if stored != nil {
		credential = &stored.ChatGPTProviderCredential
		lastError = stored.LastError
	}
	return buildProviderAuthView(
		providerCredentialTypeChatGPT,
		buildChatGPTAuthState(
			"",
			nil,
			credential,
			status,
			reasonForStoredChatGPTAuthStatus(stored, status),
			lastError,
			cloneProviderUsageSnapshot(chatGPTUsageSnapshot(credential)),
			time.Time{},
		),
	)
}
