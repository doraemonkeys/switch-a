package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestApplyProviderImport_CreatesAndRefreshesAtomically(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	groupID := "imported-group"
	if err := store.CreateGroup(ctx, &model.Group{
		ID:       groupID,
		Name:     "Imported Accounts",
		Strategy: "priority",
		Enabled:  true,
	}); err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}

	existingGroupID := groupID
	existing := importTestProvider(t, "existing-provider", "acct-existing", &existingGroupID)
	existing.Name = "Keep This Name"
	existing.Priority = 7
	existing.Concurrency = 3
	existing.Vendor = "keep-vendor"
	if err := store.CreateProvider(ctx, &existing); err != nil {
		t.Fatalf("CreateProvider(existing) error = %v", err)
	}
	current, err := store.GetProvider(ctx, existing.ID)
	if err != nil {
		t.Fatalf("GetProvider(existing) error = %v", err)
	}

	refreshedCredential := current.Credential.Clone()
	refreshedCredential.SecretData = mustMarshalProviderImportSecret(
		t,
		"access-token-refreshed",
		"refresh-token-refreshed",
	)

	created := importTestProvider(t, "new-provider", "acct-new", &groupID)
	err = store.ApplyProviderImport(ctx, &ProviderImportBundle{
		Creates: []ProviderImportCreate{{
			CandidateID: "candidate-new",
			Provider:    created,
		}},
		CredentialUpdates: []ProviderImportCredentialUpdate{{
			CandidateID:                 "candidate-existing",
			ProviderID:                  existing.ID,
			ExpectedCredentialVersion:   current.Credential.Version,
			ExpectedCredentialCreatedAt: current.Credential.CreatedAt,
			Credential:                  *refreshedCredential,
			AuthState: model.ProviderAuthState{
				Status:    model.ProviderAuthStatusActive,
				AccountID: " acct-existing ",
				Email:     " refreshed@example.com ",
			},
		}},
	})
	if err != nil {
		t.Fatalf("ApplyProviderImport() error = %v", err)
	}

	imported, err := store.GetProvider(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetProvider(created) error = %v", err)
	}
	if imported.Credential == nil || imported.Credential.BindingAccountID == nil ||
		*imported.Credential.BindingAccountID != "acct-new" {
		t.Fatalf("created credential = %#v, want normalized acct-new binding", imported.Credential)
	}
	if imported.AuthState == nil || imported.AuthState.Status != model.ProviderAuthStatusActive ||
		imported.AuthState.AccountID != "acct-new" {
		t.Fatalf("created auth state = %#v, want active acct-new state", imported.AuthState)
	}
	if imported.GroupID == nil || *imported.GroupID != groupID {
		t.Fatalf("created GroupID = %#v, want %q", imported.GroupID, groupID)
	}
	if len(imported.APITypes) != 1 || imported.APITypes[0].ProviderID != created.ID {
		t.Fatalf("created APITypes = %#v, want normalized provider id", imported.APITypes)
	}

	updated, err := store.GetProvider(ctx, existing.ID)
	if err != nil {
		t.Fatalf("GetProvider(updated) error = %v", err)
	}
	if updated.Name != existing.Name || updated.Priority != existing.Priority ||
		updated.Concurrency != existing.Concurrency || updated.Vendor != existing.Vendor ||
		updated.GroupID == nil || *updated.GroupID != groupID || len(updated.APITypes) != len(existing.APITypes) {
		t.Fatalf("updated provider config changed: %#v", updated)
	}
	if updated.Credential == nil || updated.Credential.Version != current.Credential.Version+1 ||
		updated.Credential.SecretData != refreshedCredential.SecretData ||
		!updated.Credential.CreatedAt.Equal(current.Credential.CreatedAt) {
		t.Fatalf("updated credential = %#v, want refreshed version %d", updated.Credential, current.Credential.Version+1)
	}
	if updated.AuthState == nil || updated.AuthState.Email != "refreshed@example.com" ||
		updated.AuthState.AccountID != "acct-existing" {
		t.Fatalf("updated auth state = %#v, want normalized refreshed summary", updated.AuthState)
	}
}

func TestApplyProviderImport_DuplicatePlanKeysReturnTypedConflicts(t *testing.T) {
	tests := []struct {
		name     string
		creates  func(t *testing.T) []ProviderImportCreate
		wantKind ProviderImportConflictKind
	}{
		{
			name: "provider id",
			creates: func(t *testing.T) []ProviderImportCreate {
				return []ProviderImportCreate{
					{CandidateID: "candidate-one", Provider: importTestProvider(t, "same-provider", "acct-one", nil)},
					{CandidateID: "candidate-two", Provider: importTestProvider(t, "same-provider", "acct-two", nil)},
				}
			},
			wantKind: ProviderImportConflictDuplicateProviderID,
		},
		{
			name: "account binding",
			creates: func(t *testing.T) []ProviderImportCreate {
				return []ProviderImportCreate{
					{CandidateID: "candidate-one", Provider: importTestProvider(t, "provider-one", "same-account", nil)},
					{CandidateID: "candidate-two", Provider: importTestProvider(t, "provider-two", "same-account", nil)},
				}
			},
			wantKind: ProviderImportConflictDuplicateAccountBinding,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := setupTestStore(t)
			creates := tc.creates(t)
			err := store.ApplyProviderImport(context.Background(), &ProviderImportBundle{Creates: creates})
			conflict := requireProviderImportConflict(t, err)
			matching := 0
			for i := range conflict.Conflicts {
				if conflict.Conflicts[i].Kind == tc.wantKind {
					matching++
					if conflict.Conflicts[i].ConflictingCandidateID == "" {
						t.Fatalf("conflict %#v lacks conflicting candidate id", conflict.Conflicts[i])
					}
				}
			}
			if matching != 2 {
				t.Fatalf("matching conflicts = %d, want 2; all = %#v", matching, conflict.Conflicts)
			}
			providers, listErr := store.ListProviders(context.Background())
			if listErr != nil {
				t.Fatalf("ListProviders() error = %v", listErr)
			}
			if len(providers) != 0 {
				t.Fatalf("providers = %#v, want no partial writes", providers)
			}
		})
	}
}

func TestApplyProviderImport_AggregatesDurableStateConflicts(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	existing := importTestProvider(t, "existing-provider", "acct-existing", nil)
	if err := store.CreateProvider(ctx, &existing); err != nil {
		t.Fatalf("CreateProvider(existing) error = %v", err)
	}
	stored, err := store.GetProvider(ctx, existing.ID)
	if err != nil {
		t.Fatalf("GetProvider(existing) error = %v", err)
	}

	createExistingID := importTestProvider(t, existing.ID, "acct-new", nil)
	createMissingGroup := importTestProvider(t, "provider-missing-group", "acct-existing", stringPointer("missing-group"))
	wrongAccountCredential := providerImportTestCredential(
		t,
		existing.ID,
		"acct-other",
		"access-other",
		"refresh-other",
	)
	missingProviderCredential := providerImportTestCredential(
		t,
		"missing-provider",
		"acct-missing",
		"access-missing",
		"refresh-missing",
	)
	err = store.ApplyProviderImport(ctx, &ProviderImportBundle{
		Creates: []ProviderImportCreate{
			{CandidateID: "candidate-existing-id", Provider: createExistingID},
			{CandidateID: "candidate-missing-group", Provider: createMissingGroup},
		},
		CredentialUpdates: []ProviderImportCredentialUpdate{
			{
				CandidateID:                 "candidate-stale-mismatch",
				ProviderID:                  existing.ID,
				ExpectedCredentialVersion:   stored.Credential.Version + 10,
				ExpectedCredentialCreatedAt: stored.Credential.CreatedAt,
				Credential:                  *wrongAccountCredential,
				AuthState:                   model.ProviderAuthState{Status: model.ProviderAuthStatusActive, AccountID: "acct-other"},
			},
			{
				CandidateID:                 "candidate-missing-provider",
				ProviderID:                  "missing-provider",
				ExpectedCredentialVersion:   1,
				ExpectedCredentialCreatedAt: stored.Credential.CreatedAt,
				Credential:                  *missingProviderCredential,
				AuthState:                   model.ProviderAuthState{Status: model.ProviderAuthStatusActive, AccountID: "acct-missing"},
			},
		},
	})
	conflict := requireProviderImportConflict(t, err)
	wantKinds := map[ProviderImportConflictKind]bool{
		ProviderImportConflictProviderAlreadyExists:     false,
		ProviderImportConflictGroupNotFound:             false,
		ProviderImportConflictAccountAlreadyBound:       false,
		ProviderImportConflictCredentialVersionMismatch: false,
		ProviderImportConflictAccountBindingMismatch:    false,
		ProviderImportConflictProviderNotFound:          false,
	}
	for i := range conflict.Conflicts {
		if _, wanted := wantKinds[conflict.Conflicts[i].Kind]; wanted {
			wantKinds[conflict.Conflicts[i].Kind] = true
		}
	}
	for kind, found := range wantKinds {
		if !found {
			t.Errorf("missing conflict kind %q in %#v", kind, conflict.Conflicts)
		}
	}
	if _, getErr := store.GetProvider(ctx, createMissingGroup.ID); !errors.Is(getErr, ErrNotFound) {
		t.Fatalf("GetProvider(partial create) error = %v, want ErrNotFound", getErr)
	}
	after, getErr := store.GetProvider(ctx, existing.ID)
	if getErr != nil {
		t.Fatalf("GetProvider(existing after conflict) error = %v", getErr)
	}
	if after.Credential.Version != stored.Credential.Version || after.Credential.SecretData != stored.Credential.SecretData {
		t.Fatal("existing credential changed despite preflight conflicts")
	}
}

func TestApplyProviderImport_RollsBackEveryMutationOnWriteFailure(t *testing.T) {
	t.Run("provider insert failure", func(t *testing.T) {
		store := setupTestStore(t)
		ctx := context.Background()
		if err := store.db.Exec(`
			CREATE TRIGGER fail_provider_import_insert
			BEFORE INSERT ON providers
			WHEN NEW.id = 'provider-fail'
			BEGIN
				SELECT RAISE(ABORT, 'forced provider import failure');
			END;
		`).Error; err != nil {
			t.Fatalf("create trigger: %v", err)
		}

		err := store.ApplyProviderImport(ctx, &ProviderImportBundle{Creates: []ProviderImportCreate{
			{CandidateID: "candidate-good", Provider: importTestProvider(t, "provider-good", "acct-good", nil)},
			{CandidateID: "candidate-fail", Provider: importTestProvider(t, "provider-fail", "acct-fail", nil)},
		}})
		if err == nil || !strings.Contains(err.Error(), "forced provider import failure") {
			t.Fatalf("ApplyProviderImport() error = %v, want injected failure", err)
		}
		assertProviderMissing(t, store, "provider-good")
		assertProviderMissing(t, store, "provider-fail")
	})

	t.Run("credential update failure", func(t *testing.T) {
		store := setupTestStore(t)
		ctx := context.Background()
		existing := importTestProvider(t, "existing-provider", "acct-existing", nil)
		if err := store.CreateProvider(ctx, &existing); err != nil {
			t.Fatalf("CreateProvider(existing) error = %v", err)
		}
		before, err := store.GetProvider(ctx, existing.ID)
		if err != nil {
			t.Fatalf("GetProvider(before) error = %v", err)
		}
		if err := store.db.Exec(`
			CREATE TRIGGER fail_provider_import_credential
			BEFORE UPDATE OF secret_data ON provider_credentials
			WHEN OLD.provider_id = 'existing-provider'
			BEGIN
				SELECT RAISE(ABORT, 'forced credential import failure');
			END;
		`).Error; err != nil {
			t.Fatalf("create trigger: %v", err)
		}

		refreshed := before.Credential.Clone()
		refreshed.SecretData = mustMarshalProviderImportSecret(t, "access-next", "refresh-next")
		err = store.ApplyProviderImport(ctx, &ProviderImportBundle{
			Creates: []ProviderImportCreate{{
				CandidateID: "candidate-new",
				Provider:    importTestProvider(t, "provider-new", "acct-new", nil),
			}},
			CredentialUpdates: []ProviderImportCredentialUpdate{{
				CandidateID:                 "candidate-existing",
				ProviderID:                  existing.ID,
				ExpectedCredentialVersion:   before.Credential.Version,
				ExpectedCredentialCreatedAt: before.Credential.CreatedAt,
				Credential:                  *refreshed,
				AuthState:                   model.ProviderAuthState{Status: model.ProviderAuthStatusActive, AccountID: "acct-existing"},
			}},
		})
		if err == nil || !strings.Contains(err.Error(), "forced credential import failure") {
			t.Fatalf("ApplyProviderImport() error = %v, want injected failure", err)
		}
		assertProviderMissing(t, store, "provider-new")
		after, getErr := store.GetProvider(ctx, existing.ID)
		if getErr != nil {
			t.Fatalf("GetProvider(after) error = %v", getErr)
		}
		if after.Credential.Version != before.Credential.Version || after.Credential.SecretData != before.Credential.SecretData {
			t.Fatal("existing credential changed despite transaction rollback")
		}
	})
}

func TestApplyProviderImport_StaleCredentialVersionRollsBackCreates(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	existing := importTestProvider(t, "existing-provider", "acct-existing", nil)
	if err := store.CreateProvider(ctx, &existing); err != nil {
		t.Fatalf("CreateProvider(existing) error = %v", err)
	}
	previewed, err := store.GetProvider(ctx, existing.ID)
	if err != nil {
		t.Fatalf("GetProvider(previewed) error = %v", err)
	}

	rotated := previewed.Credential.Clone()
	rotated.SecretData = mustMarshalProviderImportSecret(t, "rotated-access", "rotated-refresh")
	if err := store.UpdateProviderCredentialState(ctx, existing.ID, rotated, &model.ProviderAuthState{
		Status:    model.ProviderAuthStatusActive,
		AccountID: "acct-existing",
	}); err != nil {
		t.Fatalf("UpdateProviderCredentialState(rotation) error = %v", err)
	}

	stale := previewed.Credential.Clone()
	stale.SecretData = mustMarshalProviderImportSecret(t, "stale-access", "stale-refresh")
	err = store.ApplyProviderImport(ctx, &ProviderImportBundle{
		Creates: []ProviderImportCreate{{
			CandidateID: "candidate-new",
			Provider:    importTestProvider(t, "provider-new", "acct-new", nil),
		}},
		CredentialUpdates: []ProviderImportCredentialUpdate{{
			CandidateID:                 "candidate-stale",
			ProviderID:                  existing.ID,
			ExpectedCredentialVersion:   previewed.Credential.Version,
			ExpectedCredentialCreatedAt: previewed.Credential.CreatedAt,
			Credential:                  *stale,
			AuthState:                   model.ProviderAuthState{Status: model.ProviderAuthStatusActive, AccountID: "acct-existing"},
		}},
	})
	conflict := requireProviderImportConflict(t, err)
	foundVersionConflict := false
	for i := range conflict.Conflicts {
		item := conflict.Conflicts[i]
		if item.Kind == ProviderImportConflictCredentialVersionMismatch {
			foundVersionConflict = true
			if item.ExpectedCredentialVersion != previewed.Credential.Version ||
				item.CurrentCredentialVersion != rotated.Version {
				t.Fatalf("version conflict = %#v, want expected %d current %d", item, previewed.Credential.Version, rotated.Version)
			}
		}
	}
	if !foundVersionConflict {
		t.Fatalf("conflicts = %#v, want credential version mismatch", conflict.Conflicts)
	}
	assertProviderMissing(t, store, "provider-new")
	after, err := store.GetProvider(ctx, existing.ID)
	if err != nil {
		t.Fatalf("GetProvider(after) error = %v", err)
	}
	if after.Credential.Version != rotated.Version || after.Credential.SecretData != rotated.SecretData {
		t.Fatal("stale import overwrote the rotated credential")
	}
}

func TestApplyProviderImport_RejectsRecreatedCredentialWithSameVersion(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	existing := importTestProvider(t, "existing-provider", "acct-existing", nil)
	if err := store.CreateProvider(ctx, &existing); err != nil {
		t.Fatalf("CreateProvider(existing) error = %v", err)
	}
	previewed, err := store.GetProvider(ctx, existing.ID)
	if err != nil {
		t.Fatalf("GetProvider(previewed) error = %v", err)
	}
	if err := store.DeleteProvider(ctx, existing.ID); err != nil {
		t.Fatalf("DeleteProvider() error = %v", err)
	}

	recreated := importTestProvider(t, existing.ID, "acct-existing", nil)
	recreated.Credential.CreatedAt = previewed.Credential.CreatedAt.Add(time.Hour)
	if err := store.CreateProvider(ctx, &recreated); err != nil {
		t.Fatalf("CreateProvider(recreated) error = %v", err)
	}
	current, err := store.GetProvider(ctx, existing.ID)
	if err != nil {
		t.Fatalf("GetProvider(recreated) error = %v", err)
	}
	if current.Credential.Version != previewed.Credential.Version {
		t.Fatalf(
			"recreated version = %d, want previewed version %d to exercise ABA",
			current.Credential.Version,
			previewed.Credential.Version,
		)
	}
	if current.Credential.CreatedAt.Equal(previewed.Credential.CreatedAt) {
		t.Fatal("recreated credential has the previewed creation identity")
	}

	stale := previewed.Credential.Clone()
	stale.SecretData = mustMarshalProviderImportSecret(t, "stale-access", "stale-refresh")
	err = store.ApplyProviderImport(ctx, &ProviderImportBundle{
		CredentialUpdates: []ProviderImportCredentialUpdate{{
			CandidateID:                 "candidate-stale-generation",
			ProviderID:                  existing.ID,
			ExpectedCredentialVersion:   previewed.Credential.Version,
			ExpectedCredentialCreatedAt: previewed.Credential.CreatedAt,
			Credential:                  *stale,
			AuthState: model.ProviderAuthState{
				Status:    model.ProviderAuthStatusActive,
				AccountID: "acct-existing",
			},
		}},
	})
	conflict := requireProviderImportConflict(t, err)
	foundGenerationConflict := false
	for i := range conflict.Conflicts {
		if conflict.Conflicts[i].Kind == ProviderImportConflictCredentialVersionMismatch {
			foundGenerationConflict = true
		}
	}
	if !foundGenerationConflict {
		t.Fatalf("conflicts = %#v, want credential generation mismatch", conflict.Conflicts)
	}
	after, err := store.GetProvider(ctx, existing.ID)
	if err != nil {
		t.Fatalf("GetProvider(after conflict) error = %v", err)
	}
	if after.Credential.SecretData != current.Credential.SecretData ||
		!after.Credential.CreatedAt.Equal(current.Credential.CreatedAt) {
		t.Fatal("stale import overwrote the recreated credential generation")
	}
}

func TestApplyProviderImport_PreservesNewerDurableUsageSnapshot(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	existing := importTestProvider(t, "existing-provider", "acct-existing", nil)
	if err := store.CreateProvider(ctx, &existing); err != nil {
		t.Fatalf("CreateProvider(existing) error = %v", err)
	}
	previewed, err := store.GetProvider(ctx, existing.ID)
	if err != nil {
		t.Fatalf("GetProvider(previewed) error = %v", err)
	}

	previewFetchedAt := time.Date(2026, time.July, 30, 10, 0, 0, 0, time.UTC)
	durableFetchedAt := previewFetchedAt.Add(time.Hour)
	if err := store.UpdateProviderAuthState(ctx, existing.ID, &model.ProviderAuthState{
		Status:    model.ProviderAuthStatusActive,
		AccountID: "acct-existing",
		Email:     "durable@example.com",
		UsageSnapshot: &model.ProviderUsageSnapshot{
			FetchedAt: &durableFetchedAt,
			PlanType:  "pro",
			FiveHour: &model.ProviderUsageWindow{
				UsedPercent:   61,
				WindowSeconds: 18_000,
			},
		},
	}); err != nil {
		t.Fatalf("UpdateProviderAuthState(newer usage) error = %v", err)
	}

	refreshed := previewed.Credential.Clone()
	refreshed.SecretData = mustMarshalProviderImportSecret(t, "imported-access", "imported-refresh")
	err = store.ApplyProviderImport(ctx, &ProviderImportBundle{
		CredentialUpdates: []ProviderImportCredentialUpdate{{
			CandidateID:                 "candidate-existing",
			ProviderID:                  existing.ID,
			ExpectedCredentialVersion:   previewed.Credential.Version,
			ExpectedCredentialCreatedAt: previewed.Credential.CreatedAt,
			Credential:                  *refreshed,
			AuthState: model.ProviderAuthState{
				Status:    model.ProviderAuthStatusActive,
				AccountID: "acct-existing",
				Email:     "imported@example.com",
				UsageSnapshot: &model.ProviderUsageSnapshot{
					FetchedAt: &previewFetchedAt,
					PlanType:  "free",
					FiveHour: &model.ProviderUsageWindow{
						UsedPercent:   12,
						WindowSeconds: 18_000,
					},
				},
			},
		}},
	})
	if err != nil {
		t.Fatalf("ApplyProviderImport() error = %v", err)
	}
	after, err := store.GetProvider(ctx, existing.ID)
	if err != nil {
		t.Fatalf("GetProvider(after import) error = %v", err)
	}
	if after.AuthState == nil || after.AuthState.UsageSnapshot == nil ||
		after.AuthState.UsageSnapshot.FetchedAt == nil {
		t.Fatalf("auth state after import = %#v, want durable usage snapshot", after.AuthState)
	}
	if !after.AuthState.UsageSnapshot.FetchedAt.Equal(durableFetchedAt) ||
		after.AuthState.UsageSnapshot.FiveHour == nil ||
		after.AuthState.UsageSnapshot.FiveHour.UsedPercent != 61 {
		t.Fatalf("usage snapshot after import = %#v, want newer durable snapshot", after.AuthState.UsageSnapshot)
	}
	if after.AuthState.Email != "imported@example.com" {
		t.Fatalf("email after import = %q, want non-usage import fields to update", after.AuthState.Email)
	}
}

func TestApplyProviderImport_RejectsInvalidBundleShapes(t *testing.T) {
	validCreate := func(t *testing.T) ProviderImportCreate {
		return ProviderImportCreate{CandidateID: "candidate", Provider: importTestProvider(t, "provider", "account", nil)}
	}
	validUpdate := func(t *testing.T) ProviderImportCredentialUpdate {
		credential := providerImportTestCredential(t, "provider", "account", "access-token", "refresh-token")
		return ProviderImportCredentialUpdate{
			CandidateID:                 "candidate",
			ProviderID:                  "provider",
			ExpectedCredentialVersion:   1,
			ExpectedCredentialCreatedAt: time.Unix(1, 0),
			Credential:                  *credential,
			AuthState:                   model.ProviderAuthState{Status: model.ProviderAuthStatusActive, AccountID: "account"},
		}
	}

	tests := []struct {
		name   string
		bundle func(t *testing.T) *ProviderImportBundle
	}{
		{name: "blank create candidate", bundle: func(t *testing.T) *ProviderImportBundle {
			entry := validCreate(t)
			entry.CandidateID = " "
			return &ProviderImportBundle{Creates: []ProviderImportCreate{entry}}
		}},
		{name: "blank create provider", bundle: func(t *testing.T) *ProviderImportBundle {
			entry := validCreate(t)
			entry.Provider.ID = " "
			return &ProviderImportBundle{Creates: []ProviderImportCreate{entry}}
		}},
		{name: "non chatgpt create", bundle: func(t *testing.T) *ProviderImportBundle {
			entry := validCreate(t)
			entry.Provider.CredentialType = model.ProviderCredentialTypeAPIKey
			return &ProviderImportBundle{Creates: []ProviderImportCreate{entry}}
		}},
		{name: "missing create credential", bundle: func(t *testing.T) *ProviderImportBundle {
			entry := validCreate(t)
			entry.Provider.Credential = nil
			return &ProviderImportBundle{Creates: []ProviderImportCreate{entry}}
		}},
		{name: "malformed secret", bundle: func(t *testing.T) *ProviderImportBundle {
			entry := validCreate(t)
			entry.Provider.Credential.SecretData = "not-json"
			return &ProviderImportBundle{Creates: []ProviderImportCreate{entry}}
		}},
		{name: "incomplete secret", bundle: func(t *testing.T) *ProviderImportBundle {
			entry := validCreate(t)
			entry.Provider.Credential.SecretData = `{}`
			return &ProviderImportBundle{Creates: []ProviderImportCreate{entry}}
		}},
		{name: "missing access token", bundle: func(t *testing.T) *ProviderImportBundle {
			entry := validCreate(t)
			entry.Provider.Credential.SecretData = mustMarshalProviderImportSecret(t, "", "refresh-token")
			return &ProviderImportBundle{Creates: []ProviderImportCreate{entry}}
		}},
		{name: "missing refresh token", bundle: func(t *testing.T) *ProviderImportBundle {
			entry := validCreate(t)
			entry.Provider.Credential.SecretData = mustMarshalProviderImportSecret(t, "access-token", "")
			return &ProviderImportBundle{Creates: []ProviderImportCreate{entry}}
		}},
		{name: "missing credential binding", bundle: func(t *testing.T) *ProviderImportBundle {
			entry := validCreate(t)
			entry.Provider.Credential.BindingAccountID = nil
			return &ProviderImportBundle{Creates: []ProviderImportCreate{entry}}
		}},
		{name: "credential binding mismatch", bundle: func(t *testing.T) *ProviderImportBundle {
			entry := validCreate(t)
			entry.Provider.Credential.BindingAccountID = stringPointer("different-account")
			return &ProviderImportBundle{Creates: []ProviderImportCreate{entry}}
		}},
		{name: "auth account mismatch", bundle: func(t *testing.T) *ProviderImportBundle {
			entry := validCreate(t)
			entry.Provider.AuthState = &model.ProviderAuthState{Status: model.ProviderAuthStatusActive, AccountID: "different-account"}
			return &ProviderImportBundle{Creates: []ProviderImportCreate{entry}}
		}},
		{name: "blank update candidate", bundle: func(t *testing.T) *ProviderImportBundle {
			entry := validUpdate(t)
			entry.CandidateID = ""
			return &ProviderImportBundle{CredentialUpdates: []ProviderImportCredentialUpdate{entry}}
		}},
		{name: "blank update provider", bundle: func(t *testing.T) *ProviderImportBundle {
			entry := validUpdate(t)
			entry.ProviderID = ""
			return &ProviderImportBundle{CredentialUpdates: []ProviderImportCredentialUpdate{entry}}
		}},
		{name: "negative expected version", bundle: func(t *testing.T) *ProviderImportBundle {
			entry := validUpdate(t)
			entry.ExpectedCredentialVersion = -1
			return &ProviderImportBundle{CredentialUpdates: []ProviderImportCredentialUpdate{entry}}
		}},
		{name: "missing expected credential creation time", bundle: func(t *testing.T) *ProviderImportBundle {
			entry := validUpdate(t)
			entry.ExpectedCredentialCreatedAt = time.Time{}
			return &ProviderImportBundle{CredentialUpdates: []ProviderImportCredentialUpdate{entry}}
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := setupTestStore(t)
			if err := store.ApplyProviderImport(context.Background(), tc.bundle(t)); err == nil {
				t.Fatal("ApplyProviderImport() error = nil, want invalid bundle error")
			}
		})
	}

	store := setupTestStore(t)
	if err := store.ApplyProviderImport(context.Background(), nil); err != nil {
		t.Fatalf("ApplyProviderImport(nil) error = %v", err)
	}
	if err := store.ApplyProviderImport(context.Background(), &ProviderImportBundle{}); err != nil {
		t.Fatalf("ApplyProviderImport(empty) error = %v", err)
	}
}

type providerImportPassthroughStore struct {
	internal.Store
	bundle *ProviderImportBundle
	err    error
}

func (s *providerImportPassthroughStore) ApplyProviderImport(_ context.Context, bundle *ProviderImportBundle) error {
	s.bundle = bundle
	return s.err
}

func TestCachedStore_ApplyProviderImportForwardsOrReportsUnsupported(t *testing.T) {
	wantErr := errors.New("import unavailable")
	stub := &providerImportPassthroughStore{err: wantErr}
	cached := NewCachedStore(CachedStoreConfig{Store: stub})
	bundle := &ProviderImportBundle{}
	if err := cached.ApplyProviderImport(context.Background(), bundle); !errors.Is(err, wantErr) {
		t.Fatalf("ApplyProviderImport() error = %v, want %v", err, wantErr)
	}
	if stub.bundle != bundle {
		t.Fatal("ApplyProviderImport() did not forward the original bundle")
	}

	unsupported := NewCachedStore(CachedStoreConfig{Store: &unsupportedProviderImportStore{}})
	if err := unsupported.ApplyProviderImport(context.Background(), bundle); err == nil ||
		!strings.Contains(err.Error(), "does not support ApplyProviderImport") {
		t.Fatalf("unsupported ApplyProviderImport() error = %v", err)
	}
}

type unsupportedProviderImportStore struct {
	internal.Store
}

func TestProviderImportConflictError(t *testing.T) {
	var nilConflict *ProviderImportConflictError
	if nilConflict.Error() != ErrProviderImportConflict.Error() {
		t.Fatalf("nil conflict Error() = %q", nilConflict.Error())
	}
	empty := &ProviderImportConflictError{}
	if !errors.Is(empty, ErrProviderImportConflict) {
		t.Fatal("ProviderImportConflictError should match ErrProviderImportConflict")
	}
	conflict := &ProviderImportConflictError{Conflicts: []ProviderImportConflict{
		{Kind: ProviderImportConflictGroupNotFound},
		{Kind: ProviderImportConflictCredentialVersionMismatch},
	}}
	if got := conflict.Error(); !strings.Contains(got, "group_not_found") ||
		!strings.Contains(got, "credential_version_mismatch") {
		t.Fatalf("Error() = %q, want both stable conflict kinds", got)
	}
}

func importTestProvider(t *testing.T, providerID, accountID string, groupID *string) model.Provider {
	t.Helper()
	return model.Provider{
		ID:             providerID,
		Name:           "Provider " + providerID,
		AuthMode:       "bearer",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		GroupID:        groupID,
		Weight:         1,
		Priority:       1,
		Concurrency:    10,
		Enabled:        true,
		APITypes: []model.ProviderAPIType{{
			ProviderID: providerID,
			APIType:    "codex",
			BaseURL:    "https://chatgpt.com/backend-api/codex",
		}},
		Credential: providerImportTestCredential(
			t,
			providerID,
			accountID,
			"access-token",
			"refresh-token",
		),
		AuthState: &model.ProviderAuthState{
			Status:    model.ProviderAuthStatusActive,
			AccountID: accountID,
		},
	}
}

func providerImportTestCredential(
	t *testing.T,
	providerID string,
	accountID string,
	accessToken string,
	refreshToken string,
) *model.ProviderCredential {
	t.Helper()
	return model.NormalizeProviderCredentialRecord(providerID, &model.ProviderCredential{
		SecretData:       mustMarshalProviderImportSecret(t, accessToken, refreshToken),
		BindingAccountID: stringPointer(accountID),
		Version:          1,
	})
}

func mustMarshalProviderImportSecret(t *testing.T, accessToken, refreshToken string) string {
	t.Helper()
	payload, err := model.EncodeChatGPTProviderSecret(&model.ChatGPTProviderSecret{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		IDToken:      "id-token",
	})
	if err != nil {
		t.Fatalf("EncodeChatGPTProviderSecret() error = %v", err)
	}
	return payload
}

func requireProviderImportConflict(t *testing.T, err error) *ProviderImportConflictError {
	t.Helper()
	if !errors.Is(err, ErrProviderImportConflict) {
		t.Fatalf("error = %v, want ErrProviderImportConflict", err)
	}
	var conflict *ProviderImportConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error = %v, want ProviderImportConflictError", err)
	}
	return conflict
}

func assertProviderMissing(t *testing.T, store *SQLiteStore, providerID string) {
	t.Helper()
	if _, err := store.GetProvider(context.Background(), providerID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetProvider(%q) error = %v, want ErrNotFound", providerID, err)
	}
}
