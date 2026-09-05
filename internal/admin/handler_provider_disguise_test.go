package admin

import (
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/model"
	"testing"
)

func TestProviderDisguisePolicyWriteBoundaries(t *testing.T) {
	disabled := false
	policy := clientdisguise.Policy{Enabled: true, MatchPlatform: &disabled, UnknownPlatform: clientdisguise.UnknownAllowCurrent}
	create := CreateProviderRequest{ClientDisguise: policy}
	provider := create.toProvider()
	if !provider.ClientDisguise.Enabled || provider.ClientDisguise.PlatformMatching() {
		t.Fatal(provider.ClientDisguise)
	}
	update := UpdateProviderRequest{}
	update.applyTo(provider)
	if !provider.ClientDisguise.Enabled {
		t.Fatal("omitted policy reset")
	}
	update.ClientDisguise = &clientdisguise.Policy{}
	update.applyTo(provider)
	if provider.ClientDisguise.Enabled || !provider.ClientDisguise.PlatformMatching() {
		t.Fatal("explicit disable lost")
	}
	bad := clientdisguise.Policy{UnknownPlatform: "guess"}
	if (&CreateProviderRequest{ClientDisguise: bad}).validate() == "" || (&UpdateProviderRequest{ClientDisguise: &bad}).validate() == "" {
		t.Fatal("invalid policy accepted")
	}
	handler := NewHandler(Config{})
	payload := handler.providerPayload(&model.Provider{ClientDisguise: policy})
	if !payload.ClientDisguise.Enabled || payload.ClientDisguise.UnknownPlatform != clientdisguise.UnknownAllowCurrent {
		t.Fatal(payload)
	}
}
