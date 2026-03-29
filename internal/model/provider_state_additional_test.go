package model

import (
	"testing"
	"time"
)

func TestProviderStateHelpers_HandleNilAndEmptyInputs(t *testing.T) {
	var credentialRecord *ProviderCredential
	if clone := credentialRecord.Clone(); clone != nil {
		t.Fatalf("nil ProviderCredential clone = %#v, want nil", clone)
	}

	var authState *ProviderAuthState
	if clone := authState.Clone(); clone != nil {
		t.Fatalf("nil ProviderAuthState clone = %#v, want nil", clone)
	}

	decoded, err := DecodeChatGPTProviderCredential("")
	if err != nil {
		t.Fatalf("DecodeChatGPTProviderCredential(empty) error = %v, want nil", err)
	}
	if decoded != nil {
		t.Fatalf("DecodeChatGPTProviderCredential(empty) = %#v, want nil", decoded)
	}

	if _, err := DecodeChatGPTProviderCredential("{"); err == nil {
		t.Fatal("DecodeChatGPTProviderCredential(invalid json) succeeded, want error")
	}

	encoded, err := EncodeChatGPTProviderCredential(nil)
	if err != nil {
		t.Fatalf("EncodeChatGPTProviderCredential(nil) error = %v, want nil", err)
	}
	if encoded != "" {
		t.Fatalf("EncodeChatGPTProviderCredential(nil) = %q, want empty string", encoded)
	}
}

func TestNormalizeProviderCredentialRecord_DefaultsVersionAndBinding(t *testing.T) {
	if normalized := NormalizeProviderCredentialRecord("provider-gpt", nil); normalized != nil {
		t.Fatalf("NormalizeProviderCredentialRecord(nil) = %#v, want nil", normalized)
	}

	blankBinding := "   "
	normalized := NormalizeProviderCredentialRecord("provider-gpt", &ProviderCredential{
		BindingAccountID: &blankBinding,
		Version:          0,
	})
	if normalized == nil {
		t.Fatal("NormalizeProviderCredentialRecord(...) = nil, want record")
	}
	if normalized.ProviderID != "provider-gpt" {
		t.Fatalf("ProviderID = %q, want provider-gpt", normalized.ProviderID)
	}
	if normalized.BindingAccountID != nil {
		t.Fatalf("BindingAccountID = %v, want nil after trimming blank input", normalized.BindingAccountID)
	}
	if normalized.Version != 1 {
		t.Fatalf("Version = %d, want 1", normalized.Version)
	}
}

func TestProviderCredentialFromLegacy_RejectsUnsupportedOrEmptyData(t *testing.T) {
	if record := ProviderCredentialFromLegacy("provider-api", ProviderCredentialTypeAPIKey, `{"access_token":"ignored"}`); record != nil {
		t.Fatalf("ProviderCredentialFromLegacy(api key) = %#v, want nil", record)
	}

	if record := ProviderCredentialFromLegacy("provider-gpt", ProviderCredentialTypeChatGPT, "   "); record != nil {
		t.Fatalf("ProviderCredentialFromLegacy(empty secret) = %#v, want nil", record)
	}
}

func TestProviderAuthStateFromCredential_FallbacksAndBindingAccount(t *testing.T) {
	t.Run("api key providers remain active without auth state", func(t *testing.T) {
		state := ProviderAuthStateFromCredential("provider-api", ProviderCredentialTypeAPIKey, &ProviderCredential{
			SecretData: `{"ignored":true}`,
		})
		if state == nil {
			t.Fatal("ProviderAuthStateFromCredential(api key) = nil, want state")
		}
		if state.Status != ProviderAuthStatusActive {
			t.Fatalf("Status = %q, want %q", state.Status, ProviderAuthStatusActive)
		}
	})

	t.Run("invalid chatgpt credential falls back to not connected", func(t *testing.T) {
		state := ProviderAuthStateFromCredential("provider-gpt", ProviderCredentialTypeChatGPT, &ProviderCredential{
			SecretData: "{",
		})
		if state == nil {
			t.Fatal("ProviderAuthStateFromCredential(invalid) = nil, want state")
		}
		if state.Status != ProviderAuthStatusNotConnected {
			t.Fatalf("Status = %q, want %q", state.Status, ProviderAuthStatusNotConnected)
		}
	})

	t.Run("binding account completes ready credential snapshot", func(t *testing.T) {
		now := time.Date(2026, time.March, 29, 12, 0, 0, 0, time.UTC)
		raw, err := EncodeChatGPTProviderCredential(&ChatGPTProviderCredential{
			AccessToken:  "access-token",
			RefreshToken: "refresh-token",
			Email:        "user@example.com",
			LastRefresh:  now,
			ExpiresAt:    now.Add(time.Hour),
		})
		if err != nil {
			t.Fatalf("EncodeChatGPTProviderCredential error: %v", err)
		}

		bindingAccountID := " acct-bind "
		state := ProviderAuthStateFromCredential("provider-gpt", ProviderCredentialTypeChatGPT, &ProviderCredential{
			SecretData:       raw,
			BindingAccountID: &bindingAccountID,
		})
		if state == nil {
			t.Fatal("ProviderAuthStateFromCredential(binding fallback) = nil, want state")
		}
		if state.Status != ProviderAuthStatusActive {
			t.Fatalf("Status = %q, want %q", state.Status, ProviderAuthStatusActive)
		}
		if state.AccountID != "acct-bind" {
			t.Fatalf("AccountID = %q, want acct-bind", state.AccountID)
		}
		if state.Email != "user@example.com" {
			t.Fatalf("Email = %q, want user@example.com", state.Email)
		}
		if state.ExpiresAt == nil || !state.ExpiresAt.Equal(now.Add(time.Hour)) {
			t.Fatalf("ExpiresAt = %#v, want %s", state.ExpiresAt, now.Add(time.Hour))
		}
		if state.LastRefreshAt == nil || !state.LastRefreshAt.Equal(now) {
			t.Fatalf("LastRefreshAt = %#v, want %s", state.LastRefreshAt, now)
		}
	})
}

func TestNormalizeProviderAuthStateRecord_NilDefaults(t *testing.T) {
	state := NormalizeProviderAuthStateRecord("provider-gpt", ProviderCredentialTypeChatGPT, nil)
	if state == nil {
		t.Fatal("NormalizeProviderAuthStateRecord(nil) = nil, want state")
	}
	if state.ProviderID != "provider-gpt" {
		t.Fatalf("ProviderID = %q, want provider-gpt", state.ProviderID)
	}
	if state.Status != ProviderAuthStatusNotConnected {
		t.Fatalf("Status = %q, want %q", state.Status, ProviderAuthStatusNotConnected)
	}
}
