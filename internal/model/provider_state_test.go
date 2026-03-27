package model

import (
	"testing"
	"time"
)

func TestDefaultProviderAuthStatus_StateDefaults(t *testing.T) {
	t.Run("api key providers default to active", func(t *testing.T) {
		if got := DefaultProviderAuthStatus(ProviderCredentialTypeAPIKey); got != ProviderAuthStatusActive {
			t.Fatalf("DefaultProviderAuthStatus(api_key) = %q, want %q", got, ProviderAuthStatusActive)
		}
	})

	t.Run("chatgpt providers default to not connected", func(t *testing.T) {
		if got := DefaultProviderAuthStatus(ProviderCredentialTypeChatGPT); got != ProviderAuthStatusNotConnected {
			t.Fatalf("DefaultProviderAuthStatus(chatgpt) = %q, want %q", got, ProviderAuthStatusNotConnected)
		}
	})
}

func TestProviderCredentialFromLegacy_PreservesSecretAndBindingAccount(t *testing.T) {
	t.Parallel()

	raw, err := EncodeChatGPTProviderCredential(&ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		AccountID:    "acct_test",
	})
	if err != nil {
		t.Fatalf("EncodeChatGPTProviderCredential error: %v", err)
	}

	record := ProviderCredentialFromLegacy("provider-gpt", ProviderCredentialTypeChatGPT, raw)
	if record == nil {
		t.Fatal("ProviderCredentialFromLegacy = nil, want record")
	}
	if record.ProviderID != "provider-gpt" {
		t.Fatalf("ProviderID = %q, want provider-gpt", record.ProviderID)
	}
	if record.SecretData != raw {
		t.Fatalf("SecretData = %q, want original payload", record.SecretData)
	}
	if record.BindingAccountID == nil || *record.BindingAccountID != "acct_test" {
		t.Fatalf("BindingAccountID = %v, want acct_test", record.BindingAccountID)
	}
	if record.Version != 1 {
		t.Fatalf("Version = %d, want 1", record.Version)
	}
}

func TestProviderAuthStateFromCredential_ChatGPTReadyCredentialBecomesActive(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 27, 4, 0, 0, 0, time.UTC)
	resetAt := now.Add(2 * time.Hour)
	raw, err := EncodeChatGPTProviderCredential(&ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		AccountID:    "acct_test",
		Email:        "user@example.com",
		PlanType:     "pro",
		LastRefresh:  now.Add(-time.Minute),
		ExpiresAt:    now.Add(time.Hour),
		Usage: &ProviderUsageSnapshot{
			FetchedAt: &now,
			OneWeek: &ProviderUsageWindow{
				UsedPercent:   42,
				WindowSeconds: 7 * 24 * 60 * 60,
				ResetAt:       &resetAt,
			},
		},
	})
	if err != nil {
		t.Fatalf("EncodeChatGPTProviderCredential error: %v", err)
	}

	state := ProviderAuthStateFromCredential(
		"provider-gpt",
		ProviderCredentialTypeChatGPT,
		ProviderCredentialFromLegacy("provider-gpt", ProviderCredentialTypeChatGPT, raw),
	)
	if state == nil {
		t.Fatal("ProviderAuthStateFromCredential = nil, want state")
	}
	if state.Status != ProviderAuthStatusActive {
		t.Fatalf("Status = %q, want %q", state.Status, ProviderAuthStatusActive)
	}
	if state.AccountID != "acct_test" {
		t.Fatalf("AccountID = %q, want acct_test", state.AccountID)
	}
	if state.Email != "user@example.com" {
		t.Fatalf("Email = %q, want user@example.com", state.Email)
	}
	if state.PlanType != "pro" {
		t.Fatalf("PlanType = %q, want pro", state.PlanType)
	}
	if state.UsageSnapshot == nil || state.UsageSnapshot.OneWeek == nil {
		t.Fatalf("UsageSnapshot = %#v, want hydrated snapshot", state.UsageSnapshot)
	}
}

func TestProviderAuthStateFromCredential_ChatGPTIncompleteCredentialStaysNotConnected(t *testing.T) {
	t.Parallel()

	raw, err := EncodeChatGPTProviderCredential(&ChatGPTProviderCredential{
		AccountID: "acct_test",
		Email:     "user@example.com",
	})
	if err != nil {
		t.Fatalf("EncodeChatGPTProviderCredential error: %v", err)
	}

	state := ProviderAuthStateFromCredential(
		"provider-gpt",
		ProviderCredentialTypeChatGPT,
		ProviderCredentialFromLegacy("provider-gpt", ProviderCredentialTypeChatGPT, raw),
	)
	if state == nil {
		t.Fatal("ProviderAuthStateFromCredential = nil, want state")
	}
	if state.Status != ProviderAuthStatusNotConnected {
		t.Fatalf("Status = %q, want %q", state.Status, ProviderAuthStatusNotConnected)
	}
	if state.AccountID != "acct_test" {
		t.Fatalf("AccountID = %q, want acct_test", state.AccountID)
	}
}

func TestNormalizeProviderAuthStateRecord_SanitizesAndDefaults(t *testing.T) {
	t.Parallel()

	fetchedAt := time.Date(2026, time.March, 27, 4, 5, 0, 0, time.UTC)
	state := NormalizeProviderAuthStateRecord(
		"provider-gpt",
		ProviderCredentialTypeChatGPT,
		&ProviderAuthState{
			Status:               ProviderAuthStatus("invalid"),
			StatusReason:         "  requires_login  ",
			LastError:            "  invalid_grant  ",
			Email:                "  user@example.com  ",
			AccountID:            "  acct_test  ",
			PlanType:             "",
			RefreshFailCount:     -3,
			UsageSnapshot:        &ProviderUsageSnapshot{FetchedAt: &fetchedAt, PlanType: "plus"},
			LastTransitionAt:     &fetchedAt,
			LastRefreshAt:        &fetchedAt,
			LastRefreshFailureAt: &fetchedAt,
		},
	)

	if state.ProviderID != "provider-gpt" {
		t.Fatalf("ProviderID = %q, want provider-gpt", state.ProviderID)
	}
	if state.Status != ProviderAuthStatusNotConnected {
		t.Fatalf("Status = %q, want %q", state.Status, ProviderAuthStatusNotConnected)
	}
	if state.StatusReason != "requires_login" {
		t.Fatalf("StatusReason = %q, want requires_login", state.StatusReason)
	}
	if state.LastError != "invalid_grant" {
		t.Fatalf("LastError = %q, want invalid_grant", state.LastError)
	}
	if state.Email != "user@example.com" {
		t.Fatalf("Email = %q, want user@example.com", state.Email)
	}
	if state.AccountID != "acct_test" {
		t.Fatalf("AccountID = %q, want acct_test", state.AccountID)
	}
	if state.PlanType != "plus" {
		t.Fatalf("PlanType = %q, want plus", state.PlanType)
	}
	if state.RefreshFailCount != 0 {
		t.Fatalf("RefreshFailCount = %d, want 0", state.RefreshFailCount)
	}
}
