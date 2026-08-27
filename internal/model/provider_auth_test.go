package model

import (
	"testing"
	"time"
)

func TestChatGPTProviderSecrets(t *testing.T) {
	secret := &ChatGPTProviderSecret{AccessToken: "access", RefreshToken: "refresh", IDToken: "id"}
	payload, err := EncodeChatGPTProviderSecret(secret)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeChatGPTProviderSecret(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.Ready() || decoded.IDToken != "id" {
		t.Fatalf("decoded secret = %#v", decoded)
	}
	if empty, err := DecodeChatGPTProviderSecret("  "); err != nil || empty != nil {
		t.Fatalf("empty secret = %#v, %v", empty, err)
	}
	if _, err := DecodeChatGPTProviderSecret("{"); err == nil {
		t.Fatal("invalid secret succeeded")
	}
	if payload, err := EncodeChatGPTProviderSecret(nil); err != nil || payload != "" {
		t.Fatalf("nil secret = %q, %v", payload, err)
	}
}

func TestChatGPTProviderCredentialReady(t *testing.T) {
	ready := &ChatGPTProviderCredential{AccessToken: "access", RefreshToken: "refresh", AccountID: "account"}
	if !ready.Ready() {
		t.Fatal("complete credential was not ready")
	}
	ready.AccountID = ""
	if ready.Ready() {
		t.Fatal("credential without account was ready")
	}
	var credential *ChatGPTProviderCredential
	if credential.Ready() {
		t.Fatal("nil credential was ready")
	}
}

func TestCloneProviderUsageSnapshotIsDetached(t *testing.T) {
	now := time.Now()
	reset := now.Add(time.Hour)
	original := &ProviderUsageSnapshot{
		FetchedAt: &now,
		PlanType:  "plus",
		FiveHour:  &ProviderUsageWindow{UsedPercent: 25, ResetAt: &reset},
	}
	clone := CloneProviderUsageSnapshot(original)
	clone.PlanType = "team"
	*clone.FetchedAt = now.Add(time.Minute)
	*clone.FiveHour.ResetAt = reset.Add(time.Minute)
	if original.PlanType != "plus" || !original.FetchedAt.Equal(now) || !original.FiveHour.ResetAt.Equal(reset) {
		t.Fatalf("clone mutated original: %#v", original)
	}
	if CloneProviderUsageSnapshot(nil) != nil {
		t.Fatal("nil snapshot clone must be nil")
	}
}
