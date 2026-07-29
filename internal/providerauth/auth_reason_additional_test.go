package providerauth

import (
	"errors"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestProviderAuthStateErrorAndReasonFallbacks(t *testing.T) {
	t.Parallel()

	if got := (&ProviderAuthStateError{
		ProviderID: "chatgpt",
		Status:     ProviderAuthStatusReauthRequired,
	}).Error(); got != `provider "chatgpt" requires reauthentication` {
		t.Fatalf("Error() = %q, want %q", got, `provider "chatgpt" requires reauthentication`)
	}

	if got := providerAuthReason(providerCredentialTypeChatGPT, nil); got != "" {
		t.Fatalf("providerAuthReason(nil) = %q, want empty string", got)
	}
	if got := providerAuthReason(providerCredentialTypeChatGPT, &model.ProviderAuthState{
		Status: ProviderAuthStatusActive,
	}); got != "" {
		t.Fatalf("providerAuthReason(active) = %q, want empty string", got)
	}
	if got := providerAuthReason(providerCredentialTypeChatGPT, &model.ProviderAuthState{
		Status:       ProviderAuthStatusReauthRequired,
		StatusReason: " invalid_grant ",
	}); got != ProviderAuthReasonInvalidGrant {
		t.Fatalf("providerAuthReason(explicit) = %q, want %q", got, ProviderAuthReasonInvalidGrant)
	}
	if got := providerAuthReason(providerCredentialTypeChatGPT, &model.ProviderAuthState{
		Status: ProviderAuthStatusNotConnected,
	}); got != ProviderAuthReasonLoginRequired {
		t.Fatalf("providerAuthReason(chatgpt fallback) = %q, want %q", got, ProviderAuthReasonLoginRequired)
	}
	if got := providerAuthReason(providerCredentialTypeAPIKey, &model.ProviderAuthState{
		Status: ProviderAuthStatusNotConnected,
	}); got != "" {
		t.Fatalf("providerAuthReason(api key fallback) = %q, want empty string", got)
	}
}

func TestClassifyChatGPTRefreshFailureToProviderUsageWindowAndFirstNonEmpty(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name       string
		err        error
		wantReason string
		wantTerm   bool
	}{
		{name: "nil", err: nil, wantReason: "", wantTerm: false},
		{name: "refresh token reused", err: errors.New("refresh_token_reused by provider"), wantReason: ProviderAuthReasonRefreshTokenReused, wantTerm: true},
		{name: "invalid grant", err: errors.New("invalid_grant from oauth"), wantReason: ProviderAuthReasonInvalidGrant, wantTerm: true},
		{name: "interaction required", err: errors.New("login_required before re-auth"), wantReason: ProviderAuthReasonInteractionRequired, wantTerm: true},
		{name: "unknown", err: errors.New("temporary upstream timeout"), wantReason: "", wantTerm: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotReason, gotTerm := classifyChatGPTRefreshFailure(tc.err)
			if gotReason != tc.wantReason || gotTerm != tc.wantTerm {
				t.Fatalf("classifyChatGPTRefreshFailure(%v) = (%q, %v), want (%q, %v)", tc.err, gotReason, gotTerm, tc.wantReason, tc.wantTerm)
			}
		})
	}

	if got := firstNonEmpty("", "", ""); got != "" {
		t.Fatalf("firstNonEmpty(all empty) = %q, want empty string", got)
	}

	if got := toProviderUsageWindow(nil); got != nil {
		t.Fatalf("toProviderUsageWindow(nil) = %#v, want nil", got)
	}

	window := toProviderUsageWindow(&chatGPTUsageWindowRaw{
		UsedPercent:        12.5,
		LimitWindowSeconds: 3600,
		ResetAt:            1_770_000_000,
	})
	if window == nil {
		t.Fatal("toProviderUsageWindow(raw) = nil, want window")
	}
	if window.UsedPercent != 12.5 || window.WindowSeconds != 3600 {
		t.Fatalf("window = %#v, want usage percent and seconds copied", window)
	}
	expectedResetAt := time.Unix(1_770_000_000, 0).UTC()
	if window.ResetAt == nil || !window.ResetAt.Equal(expectedResetAt) {
		t.Fatalf("window.ResetAt = %#v, want %v", window.ResetAt, expectedResetAt)
	}
}
