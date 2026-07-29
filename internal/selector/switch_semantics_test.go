package selector

import (
	"context"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestProviderSelectionEligibility_ReplacementSkipsFailoverIsolation(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	store.authStates["candidate"] = &model.ProviderAuthState{
		ProviderID: "candidate",
		Status:     model.ProviderAuthStatusActive,
	}

	eligibility, err := NewProviderSelectionEligibility(context.Background(), store, newMockHealthChecker(), &model.SelectRequest{
		APIType:    "codex",
		SwitchMode: model.SwitchModeReplacement,
		ProviderSwitchHistory: &model.ProviderSwitchHistory{
			OriginProviderID:    "origin",
			AttemptChain:        []string{"origin"},
			ProviderSwitchCount: 1,
		},
		ProviderContinuityContext: &model.ProviderContinuityContext{
			VisibleOriginProviderID: "origin",
			VisibleOriginVendor:     "vendor-a",
			ContaminatedVendors:     []string{"vendor-a"},
			StrictestScope:          model.ScopeVendor,
		},
		MaxProviderSwitches: 2,
	})
	if err != nil {
		t.Fatalf("NewProviderSelectionEligibility() error = %v", err)
	}

	allowed, err := eligibility.AllowsProvider(context.Background(), &model.Provider{
		ID:             "candidate",
		Enabled:        true,
		Vendor:         "vendor-b",
		AcceptFailover: model.ScopeNone,
		CredentialType: model.ProviderCredentialTypeAPIKey,
		Credential:     &model.ProviderCredential{ProviderID: "candidate", SecretData: "secret"},
		APITypes:       []model.ProviderAPIType{{ProviderID: "candidate", APIType: "codex"}},
	})
	if err != nil {
		t.Fatalf("AllowsProvider() error = %v", err)
	}
	if !allowed {
		t.Fatal("replacement mode should not apply failover-only vendor isolation")
	}
}

func TestProviderSelectionEligibility_FailoverAppliesVendorIsolation(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	candidates := []struct {
		name     string
		provider *model.Provider
		want     bool
	}{
		{
			name: "accept any allows same continuity source",
			provider: &model.Provider{
				ID:             "candidate-any",
				Enabled:        true,
				Vendor:         "vendor-a",
				AcceptFailover: model.ScopeAny,
				CredentialType: model.ProviderCredentialTypeAPIKey,
				Credential:     &model.ProviderCredential{ProviderID: "candidate-any", SecretData: "secret"},
				APITypes:       []model.ProviderAPIType{{ProviderID: "candidate-any", APIType: "codex"}},
			},
			want: true,
		},
		{
			name: "accept vendor blocks mismatched vendor",
			provider: &model.Provider{
				ID:             "candidate-vendor-mismatch",
				Enabled:        true,
				Vendor:         "vendor-b",
				AcceptFailover: model.ScopeVendor,
				CredentialType: model.ProviderCredentialTypeAPIKey,
				Credential:     &model.ProviderCredential{ProviderID: "candidate-vendor-mismatch", SecretData: "secret"},
				APITypes:       []model.ProviderAPIType{{ProviderID: "candidate-vendor-mismatch", APIType: "codex"}},
			},
			want: false,
		},
		{
			name: "accept none blocks failover",
			provider: &model.Provider{
				ID:             "candidate-none",
				Enabled:        true,
				Vendor:         "vendor-a",
				AcceptFailover: model.ScopeNone,
				CredentialType: model.ProviderCredentialTypeAPIKey,
				Credential:     &model.ProviderCredential{ProviderID: "candidate-none", SecretData: "secret"},
				APITypes:       []model.ProviderAPIType{{ProviderID: "candidate-none", APIType: "codex"}},
			},
			want: false,
		},
	}
	for _, candidate := range candidates {
		store.authStates[candidate.provider.ID] = &model.ProviderAuthState{
			ProviderID: candidate.provider.ID,
			Status:     model.ProviderAuthStatusActive,
		}
	}

	eligibility, err := NewProviderSelectionEligibility(context.Background(), store, newMockHealthChecker(), &model.SelectRequest{
		APIType:    "codex",
		SwitchMode: model.SwitchModeFailover,
		ProviderSwitchHistory: &model.ProviderSwitchHistory{
			OriginProviderID:    "origin",
			AttemptChain:        []string{"origin"},
			ProviderSwitchCount: 1,
		},
		ProviderContinuityContext: &model.ProviderContinuityContext{
			VisibleOriginProviderID: "origin",
			VisibleOriginVendor:     "vendor-a",
			ContaminatedVendors:     []string{"vendor-a"},
			StrictestScope:          model.ScopeVendor,
		},
		MaxProviderSwitches: 3,
	})
	if err != nil {
		t.Fatalf("NewProviderSelectionEligibility() error = %v", err)
	}

	for _, candidate := range candidates {
		t.Run(candidate.name, func(t *testing.T) {
			allowed, err := eligibility.AllowsProvider(context.Background(), candidate.provider)
			if err != nil {
				t.Fatalf("AllowsProvider() error = %v", err)
			}
			if allowed != candidate.want {
				t.Fatalf("AllowsProvider() = %v, want %v", allowed, candidate.want)
			}
		})
	}
}

func TestProviderSelectionEligibility_ReplacementStillUsesSwitchHistory(t *testing.T) {
	t.Parallel()

	store := newMockStore()
	store.authStates["candidate"] = &model.ProviderAuthState{
		ProviderID: "candidate",
		Status:     model.ProviderAuthStatusActive,
	}
	eligibility, err := NewProviderSelectionEligibility(context.Background(), store, newMockHealthChecker(), &model.SelectRequest{
		APIType:    "codex",
		SwitchMode: model.SwitchModeReplacement,
		ProviderSwitchHistory: &model.ProviderSwitchHistory{
			OriginProviderID:    "origin",
			AttemptChain:        []string{"origin", "candidate"},
			ProviderSwitchCount: 2,
		},
		MaxProviderSwitches: 2,
	})
	if err != nil {
		t.Fatalf("NewProviderSelectionEligibility() error = %v", err)
	}

	allowed, err := eligibility.AllowsProvider(context.Background(), &model.Provider{
		ID:             "candidate",
		Enabled:        true,
		CredentialType: model.ProviderCredentialTypeAPIKey,
		Credential:     &model.ProviderCredential{ProviderID: "candidate", SecretData: "secret"},
		APITypes:       []model.ProviderAPIType{{ProviderID: "candidate", APIType: "codex"}},
	})
	if err != nil {
		t.Fatalf("AllowsProvider() error = %v", err)
	}
	if allowed {
		t.Fatal("replacement mode should still enforce cycle/max-provider-switch guards")
	}
}

func TestBuildSelectionMetadataCarriesContinuityProvenance(t *testing.T) {
	t.Parallel()

	observedAt := time.Date(2026, 4, 5, 10, 30, 0, 0, time.UTC)
	selectedAt := observedAt.Add(2 * time.Second)
	metadata := BuildSelectionMetadataAt(&model.SelectRequest{
		SwitchMode: model.SwitchModeInitial,
		VisibleContinuitySeedCandidate: &model.VisibleContinuitySeedCandidate{
			SeedID:           "seed-1",
			OriginProviderID: "provider-origin",
			ObservedAt:       observedAt,
		},
	}, SelectionSourceStickyContinuity, selectedAt)

	if metadata.Source != SelectionSourceStickyContinuity {
		t.Fatalf("Source = %q, want %q", metadata.Source, SelectionSourceStickyContinuity)
	}
	if metadata.SwitchMode != model.SwitchModeInitial {
		t.Fatalf("SwitchMode = %q, want %q", metadata.SwitchMode, model.SwitchModeInitial)
	}
	if !metadata.ContinuitySeeded {
		t.Fatal("expected continuity-seeded metadata")
	}
	if metadata.ContinuityOriginProviderID != "provider-origin" {
		t.Fatalf("ContinuityOriginProviderID = %q, want %q", metadata.ContinuityOriginProviderID, "provider-origin")
	}
	if !metadata.ContinuitySeedObservedAt.Equal(observedAt) {
		t.Fatalf("ContinuitySeedObservedAt = %v, want %v", metadata.ContinuitySeedObservedAt, observedAt)
	}
	if metadata.ContinuitySeedAgeAtSelectionMs == nil {
		t.Fatal("expected frozen continuity seed age at selection time")
	}
	if got := *metadata.ContinuitySeedAgeAtSelectionMs; got != selectedAt.Sub(observedAt).Milliseconds() {
		t.Fatalf("ContinuitySeedAgeAtSelectionMs = %d, want %d", got, selectedAt.Sub(observedAt).Milliseconds())
	}
	if age := metadata.ContinuitySeedAge(selectedAt); age != 2*time.Second {
		t.Fatalf("ContinuitySeedAge() = %s, want %s", age, 2*time.Second)
	}
}
