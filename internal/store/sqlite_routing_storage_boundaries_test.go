package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestProviderCredentialBindingsRejectAmbiguousRouteSnapshots(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		provider *model.Provider
		want     string
	}{
		{
			name:     "nil provider",
			provider: nil,
			want:     "provider ID is required",
		},
		{
			name:     "blank provider ID",
			provider: &model.Provider{ID: "  "},
			want:     "provider ID is required",
		},
		{
			name: "unsupported API type",
			provider: &model.Provider{
				ID:       "provider",
				APITypes: []model.ProviderAPIType{{APIType: "codex"}},
				CredentialSessions: []credentialsession.RouteSnapshot{{
					APIType: "claude", Credential: credentialsession.Snapshot{SessionID: "session"},
				}},
			},
			want: "unsupported API type",
		},
		{
			name: "duplicate API type",
			provider: &model.Provider{
				ID:       "provider",
				APITypes: []model.ProviderAPIType{{APIType: "codex"}},
				CredentialSessions: []credentialsession.RouteSnapshot{
					{APIType: "codex", Credential: credentialsession.Snapshot{SessionID: "session-a"}},
					{APIType: "codex", Credential: credentialsession.Snapshot{SessionID: "session-b"}},
				},
			},
			want: "duplicate credential session reference",
		},
		{
			name: "missing API type binding",
			provider: &model.Provider{
				ID:       "provider",
				APITypes: []model.ProviderAPIType{{APIType: "codex"}},
			},
			want: "requires a credential session reference",
		},
	}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			_, err := credentialBindingsForProvider(testCase.provider)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("credentialBindingsForProvider() error = %v, want containing %q", err, testCase.want)
			}
		})
	}
}

func TestSQLiteProviderQueriesFailClosedOnCorruptCredentialProjection(t *testing.T) {
	store := newCredentialSessionStore(t)
	ctx := context.Background()
	session := mustCreateStaticSession(t, store, "corrupt-subject", "openai", "secret")
	provider := providerWithSessionRefs("provider-corrupt", "openai", map[string]string{"codex": session.ID})
	if err := store.CreateProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Model(&credentialsession.Session{}).
		Where("id = ?", session.ID).
		UpdateColumn("subject_kind", "corrupt-subject-kind").Error; err != nil {
		t.Fatal(err)
	}

	if providers, err := store.ListProviders(ctx); err == nil || providers != nil {
		t.Fatalf("ListProviders() = (%#v, %v), want hydration failure", providers, err)
	}
	if providers, err := store.ListProvidersByAPIType(ctx, "codex"); err == nil || providers != nil {
		t.Fatalf("ListProvidersByAPIType() = (%#v, %v), want hydration failure", providers, err)
	}
	if provider, err := store.GetProvider(ctx, provider.ID); err == nil || provider != nil {
		t.Fatalf("GetProvider() = (%#v, %v), want hydration failure", provider, err)
	}
}

func TestSQLiteRoutingAndProviderOperationsPropagateCanceledContext(t *testing.T) {
	store := newCredentialSessionStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if providers, err := store.ListProviders(ctx); !errors.Is(err, context.Canceled) || providers != nil {
		t.Fatalf("ListProviders(canceled) = (%#v, %v)", providers, err)
	}
	if providers, err := store.ListProvidersByAPIType(ctx, "codex"); !errors.Is(err, context.Canceled) || providers != nil {
		t.Fatalf("ListProvidersByAPIType(canceled) = (%#v, %v)", providers, err)
	}
	if provider, err := store.GetProvider(ctx, "provider"); !errors.Is(err, context.Canceled) || provider != nil {
		t.Fatalf("GetProvider(canceled) = (%#v, %v)", provider, err)
	}
	if err := store.CreateProvider(ctx, &model.Provider{ID: "provider"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("CreateProvider(canceled) error = %v", err)
	}
	if err := store.UpdateProvider(ctx, &model.Provider{ID: "provider"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("UpdateProvider(canceled) error = %v", err)
	}
	if err := store.DeleteProvider(ctx, "provider"); !errors.Is(err, context.Canceled) {
		t.Fatalf("DeleteProvider(canceled) error = %v", err)
	}

	if policies, err := store.ListRoutingPolicies(ctx); !errors.Is(err, context.Canceled) || policies != nil {
		t.Fatalf("ListRoutingPolicies(canceled) = (%#v, %v)", policies, err)
	}
	if policy, err := store.GetRoutingPolicy(ctx, 1); !errors.Is(err, context.Canceled) || policy != nil {
		t.Fatalf("GetRoutingPolicy(canceled) = (%#v, %v)", policy, err)
	}
	if err := store.CreateRoutingPolicy(ctx, &model.RoutingPolicy{APIType: "codex", Enabled: true}); !errors.Is(err, context.Canceled) {
		t.Fatalf("CreateRoutingPolicy(canceled) error = %v", err)
	}
	if err := store.UpdateRoutingPolicy(ctx, &model.RoutingPolicy{ID: 1, APIType: "codex", Enabled: true}); !errors.Is(err, context.Canceled) {
		t.Fatalf("UpdateRoutingPolicy(canceled) error = %v", err)
	}
	if err := store.DeleteRoutingPolicy(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("DeleteRoutingPolicy(canceled) error = %v", err)
	}

	if err := store.ApplyConfigImport(ctx, &ConfigImportBundle{
		RoutingPolicyMode: ConfigImportRoutingPolicyModePreserve,
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ApplyConfigImport(canceled) error = %v", err)
	}
}

func TestConfigImportHelpersPropagateRepositoryFailures(t *testing.T) {
	store := newCredentialSessionStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := applyImportedCredentialSessions(ctx, store, []credentialsession.Session{
		testStaticCredentialSession("session", "openai", "secret"),
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("applyImportedCredentialSessions(canceled) error = %v", err)
	}
	if err := applyImportedGroups(ctx, store, []model.Group{{
		ID: "group", Name: "Group", Strategy: "priority", Enabled: true,
	}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("applyImportedGroups(canceled) error = %v", err)
	}
	if err := applyImportedProviders(ctx, store, []model.Provider{{
		ID: "provider", Name: "Provider", Enabled: true,
	}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("applyImportedProviders(canceled) error = %v", err)
	}
	if err := replaceImportedRoutingPolicies(ctx, store, store.db.WithContext(ctx), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("replaceImportedRoutingPolicies(canceled) error = %v", err)
	}
	if err := upsertImportedRoutingPolicy(
		ctx,
		store,
		store.db.WithContext(ctx),
		model.RoutingPolicy{APIType: "codex", Enabled: true},
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("upsertImportedRoutingPolicy(canceled) error = %v", err)
	}
}

func TestRoutingPolicyNormalizationElidesBlankScopesAndPreservesExplicitID(t *testing.T) {
	store := newCredentialSessionStore(t)
	ctx := context.Background()
	blank := "  "
	if normalized := normalizeRoutingPolicyTargetProviderID(&blank); normalized != nil {
		t.Fatalf("blank target provider normalized to %#v", normalized)
	}
	if groups := normalizeRoutingPolicyGroups([]model.RoutingPolicyGroup{{GroupID: "  "}}); groups != nil {
		t.Fatalf("blank groups normalized to %#v", groups)
	}
	if vendors := normalizeRoutingPolicyVendors([]model.RoutingPolicyVendor{{Vendor: "  "}}); vendors != nil {
		t.Fatalf("blank vendors normalized to %#v", vendors)
	}

	policy := &model.RoutingPolicy{ID: 42, APIType: " codex ", Enabled: true}
	if err := store.CreateRoutingPolicy(ctx, policy); err != nil {
		t.Fatalf("CreateRoutingPolicy(explicit ID) error = %v", err)
	}
	if policy.ID != 42 || policy.APIType != "codex" {
		t.Fatalf("explicit policy identity = %#v", policy)
	}
	if err := store.DeleteRoutingPolicy(ctx, policy.ID); err != nil {
		t.Fatalf("DeleteRoutingPolicy() error = %v", err)
	}
	if err := store.DeleteRoutingPolicy(ctx, policy.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteRoutingPolicy(missing) error = %v", err)
	}
}
