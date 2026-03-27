package providerauth

import (
	"testing"

	"switch-a/internal/model"
)

func mustApplyLegacyChatGPTCredential(
	t *testing.T,
	provider *model.Provider,
	raw string,
) {
	t.Helper()

	credential, err := decodeChatGPTCredential(raw)
	if err != nil {
		t.Fatalf("decodeChatGPTCredential returned error: %v", err)
	}
	if err := applyChatGPTCredential(provider, credential); err != nil {
		t.Fatalf("applyChatGPTCredential returned error: %v", err)
	}
}

func mustApplyStoredLegacyChatGPTCredential(
	t *testing.T,
	provider *model.Provider,
	raw string,
) {
	t.Helper()

	credential, err := decodeStoredChatGPTCredential(raw)
	if err != nil {
		t.Fatalf("decodeStoredChatGPTCredential returned error: %v", err)
	}
	if err := applyStoredChatGPTCredential(provider, credential); err != nil {
		t.Fatalf("applyStoredChatGPTCredential returned error: %v", err)
	}
}

func mustBuildProviderAuthView(
	t *testing.T,
	provider *model.Provider,
) *ProviderAuthView {
	t.Helper()

	view := BuildProviderAuthView(provider)
	if view == nil {
		t.Fatal("BuildProviderAuthView returned nil")
	}
	return view
}
