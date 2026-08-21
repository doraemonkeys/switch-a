package store

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"

	"gorm.io/gorm"
)

func TestBackfillProviderState_NilAndAPIKeyProviders(t *testing.T) {
	t.Parallel()

	db := setupProviderStateMigrationDB(t)

	if err := db.Transaction(func(tx *gorm.DB) error {
		return backfillProviderState(tx, nil)
	}); err != nil {
		t.Fatalf("backfillProviderState(nil) error = %v", err)
	}

	apiProvider := &legacyProviderCredentialShadow{
		ID:             "api-provider",
		CredentialType: model.ProviderCredentialTypeAPIKey,
		CredentialData: `{"ignored":"value"}`,
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		return backfillProviderState(tx, apiProvider)
	}); err != nil {
		t.Fatalf("backfillProviderState(api key) error = %v", err)
	}

	var credentialCount int64
	if err := db.Model(&model.ProviderCredential{}).
		Where("provider_id = ?", apiProvider.ID).
		Count(&credentialCount).Error; err != nil {
		t.Fatalf("count provider credentials: %v", err)
	}
	if credentialCount != 0 {
		t.Fatalf("credentialCount = %d, want 0 for api-key provider", credentialCount)
	}

	var authState model.ProviderAuthState
	if err := db.First(&authState, "provider_id = ?", apiProvider.ID).Error; err != nil {
		t.Fatalf("read provider auth state: %v", err)
	}
	if authState.Status != model.ProviderAuthStatusActive {
		t.Fatalf("Status = %q, want %q", authState.Status, model.ProviderAuthStatusActive)
	}
}

func TestMigrateProviderStateTables_NoLegacyCredentialColumn(t *testing.T) {
	t.Parallel()

	db := setupProviderStateMigrationDB(t)

	if err := db.Exec(
		fmt.Sprintf(`ALTER TABLE %s DROP COLUMN %s`, providersTableName, providerCredentialDataColumn),
	).Error; err != nil {
		t.Fatalf("drop legacy credential column: %v", err)
	}

	if err := migrateProviderStateTables(db); err != nil {
		t.Fatalf("migrateProviderStateTables(no legacy column) error = %v", err)
	}

	hasCredentialData, err := tableColumnExists(db, providersTableName, providerCredentialDataColumn)
	if err != nil {
		t.Fatalf("check legacy credential column: %v", err)
	}
	if hasCredentialData {
		t.Fatal("providers.credential_data column should remain absent")
	}
}

func TestMigrateProviderStateTables_BackfillsChatGPTCredentialAndAuthState(t *testing.T) {
	t.Parallel()

	db := setupProviderStateMigrationDB(t)
	now := time.Date(2026, time.March, 27, 4, 0, 0, 0, time.UTC)
	resetAt := now.Add(2 * time.Hour)
	credentialData, err := model.EncodeChatGPTProviderCredential(&model.ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		AccountID:    "acct_test",
		Email:        "user@example.com",
		PlanType:     "pro",
		LastRefresh:  now.Add(-time.Minute),
		ExpiresAt:    now.Add(time.Hour),
		Usage: &model.ProviderUsageSnapshot{
			FetchedAt: &now,
			FiveHour: &model.ProviderUsageWindow{
				UsedPercent:   50,
				WindowSeconds: 5 * 60 * 60,
				ResetAt:       &resetAt,
			},
		},
	})
	if err != nil {
		t.Fatalf("encode credential data: %v", err)
	}

	provider := &model.Provider{
		ID:             "gpt",
		Name:           "GPT Provider",
		AuthMode:       "bearer",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Enabled:        true,
	}
	insertLegacyProvider(t, db, provider, credentialData)

	if err := migrateProviderStateTables(db); err != nil {
		t.Fatalf("migrateProviderStateTables error: %v", err)
	}
	hasCredentialData, err := tableColumnExists(db, providersTableName, providerCredentialDataColumn)
	if err != nil {
		t.Fatalf("check legacy provider credential column after migration: %v", err)
	}
	if hasCredentialData {
		t.Fatal("providers.credential_data column still exists after migration")
	}

	var credential model.ProviderCredential
	if err := db.First(&credential, "provider_id = ?", provider.ID).Error; err != nil {
		t.Fatalf("read provider credential: %v", err)
	}
	assertCanonicalChatGPTSecretData(t, credential.SecretData, "access-token", "refresh-token")
	if credential.BindingAccountID == nil || *credential.BindingAccountID != "acct_test" {
		t.Fatalf("BindingAccountID = %v, want acct_test", credential.BindingAccountID)
	}
	if credential.Version != 1 {
		t.Fatalf("Version = %d, want 1", credential.Version)
	}

	var authState model.ProviderAuthState
	if err := db.First(&authState, "provider_id = ?", provider.ID).Error; err != nil {
		t.Fatalf("read provider auth state: %v", err)
	}
	if authState.Status != model.ProviderAuthStatusActive {
		t.Fatalf("Status = %q, want %q", authState.Status, model.ProviderAuthStatusActive)
	}
	if authState.Email != "user@example.com" {
		t.Fatalf("Email = %q, want user@example.com", authState.Email)
	}
	if authState.AccountID != "acct_test" {
		t.Fatalf("AccountID = %q, want acct_test", authState.AccountID)
	}
	if authState.PlanType != "pro" {
		t.Fatalf("PlanType = %q, want pro", authState.PlanType)
	}
	if authState.UsageSnapshot == nil || authState.UsageSnapshot.FiveHour == nil {
		t.Fatalf("UsageSnapshot = %#v, want migrated snapshot", authState.UsageSnapshot)
	}
}

func TestMigrateProviderStateTables_BackfillsNotConnectedForIncompleteChatGPTLogin(t *testing.T) {
	t.Parallel()

	db := setupProviderStateMigrationDB(t)
	credentialData, err := model.EncodeChatGPTProviderCredential(&model.ChatGPTProviderCredential{
		AccountID: "acct_test",
		Email:     "user@example.com",
	})
	if err != nil {
		t.Fatalf("encode credential data: %v", err)
	}

	provider := &model.Provider{
		ID:             "gpt",
		Name:           "GPT Provider",
		AuthMode:       "bearer",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Enabled:        true,
	}
	insertLegacyProvider(t, db, provider, credentialData)

	if err := migrateProviderStateTables(db); err != nil {
		t.Fatalf("migrateProviderStateTables error: %v", err)
	}

	var authState model.ProviderAuthState
	if err := db.First(&authState, "provider_id = ?", provider.ID).Error; err != nil {
		t.Fatalf("read provider auth state: %v", err)
	}
	if authState.Status != model.ProviderAuthStatusNotConnected {
		t.Fatalf("Status = %q, want %q", authState.Status, model.ProviderAuthStatusNotConnected)
	}
	if authState.AccountID != "acct_test" {
		t.Fatalf("AccountID = %q, want acct_test", authState.AccountID)
	}
}

func TestMigrateProviderStateTables_DoesNotOverwriteExistingRows(t *testing.T) {
	t.Parallel()

	db := setupProviderStateMigrationDB(t)
	legacyCredentialData, err := model.EncodeChatGPTProviderCredential(&model.ChatGPTProviderCredential{
		AccessToken:  "legacy-access",
		RefreshToken: "legacy-refresh",
		AccountID:    "acct_test",
	})
	if err != nil {
		t.Fatalf("encode legacy credential data: %v", err)
	}

	provider := &model.Provider{
		ID:             "gpt",
		Name:           "GPT Provider",
		AuthMode:       "bearer",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Enabled:        true,
	}
	insertLegacyProvider(t, db, provider, legacyCredentialData)

	existingAccountID := "acct_other"
	existingCredential := &model.ProviderCredential{
		ProviderID:       provider.ID,
		SecretData:       "new-secret",
		BindingAccountID: &existingAccountID,
		Version:          7,
	}
	if err := db.Create(existingCredential).Error; err != nil {
		t.Fatalf("create provider credential: %v", err)
	}

	transitionAt := time.Date(2026, time.March, 27, 4, 10, 0, 0, time.UTC)
	existingAuthState := &model.ProviderAuthState{
		ProviderID:       provider.ID,
		Status:           model.ProviderAuthStatusReauthRequired,
		StatusReason:     "invalid_grant",
		LastError:        "refresh_token_reused",
		LastTransitionAt: &transitionAt,
	}
	if err := db.Create(existingAuthState).Error; err != nil {
		t.Fatalf("create provider auth state: %v", err)
	}

	if err := migrateProviderStateTables(db); err != nil {
		t.Fatalf("migrateProviderStateTables error: %v", err)
	}

	var credential model.ProviderCredential
	if err := db.First(&credential, "provider_id = ?", provider.ID).Error; err != nil {
		t.Fatalf("read provider credential: %v", err)
	}
	if credential.SecretData != "new-secret" {
		t.Fatalf("SecretData = %q, want existing row to remain", credential.SecretData)
	}
	if credential.Version != 7 {
		t.Fatalf("Version = %d, want 7", credential.Version)
	}

	var authState model.ProviderAuthState
	if err := db.First(&authState, "provider_id = ?", provider.ID).Error; err != nil {
		t.Fatalf("read provider auth state: %v", err)
	}
	if authState.Status != model.ProviderAuthStatusReauthRequired {
		t.Fatalf("Status = %q, want %q", authState.Status, model.ProviderAuthStatusReauthRequired)
	}
	if authState.LastError != "refresh_token_reused" {
		t.Fatalf("LastError = %q, want refresh_token_reused", authState.LastError)
	}
}

func TestMigrateProviderStateTables_BackfillsLegacyProviders(t *testing.T) {
	t.Parallel()

	db := setupProviderStateMigrationDB(t)

	now := time.Date(2026, time.March, 27, 12, 0, 0, 0, time.UTC)
	credentialData, err := model.EncodeChatGPTProviderCredential(&model.ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		AccountID:    "acct_test",
		Email:        "user@example.com",
		PlanType:     "team",
		LastRefresh:  now.Add(-time.Minute),
		ExpiresAt:    now.Add(time.Hour),
		Usage: &model.ProviderUsageSnapshot{
			PlanType: "team",
		},
	})
	if err != nil {
		t.Fatalf("EncodeChatGPTProviderCredential() error: %v", err)
	}

	providers := []model.Provider{
		{ID: "api", Name: "API Provider", APIKey: "key", CredentialType: model.ProviderCredentialTypeAPIKey, Enabled: true},
		{ID: "gpt-active", Name: "GPT Active", CredentialType: model.ProviderCredentialTypeChatGPT, Enabled: true},
		{ID: "gpt-pending", Name: "GPT Pending", CredentialType: model.ProviderCredentialTypeChatGPT, Enabled: true},
	}
	for i := range providers {
		shadow := ""
		if providers[i].ID == "gpt-active" {
			shadow = credentialData
		}
		insertLegacyProvider(t, db, &providers[i], shadow)
	}

	if err := migrateProviderStateTables(db); err != nil {
		t.Fatalf("migrateProviderStateTables() error: %v", err)
	}

	var credential model.ProviderCredential
	if err := db.First(&credential, "provider_id = ?", "gpt-active").Error; err != nil {
		t.Fatalf("read gpt-active credential: %v", err)
	}
	assertCanonicalChatGPTSecretData(t, credential.SecretData, "access-token", "refresh-token")
	if credential.BindingAccountID == nil || *credential.BindingAccountID != "acct_test" {
		t.Fatalf("BindingAccountID = %v, want acct_test", credential.BindingAccountID)
	}
	if credential.Version != 1 {
		t.Fatalf("Version = %d, want 1", credential.Version)
	}

	var apiAuthState model.ProviderAuthState
	if err := db.First(&apiAuthState, "provider_id = ?", "api").Error; err != nil {
		t.Fatalf("read api auth state: %v", err)
	}
	if apiAuthState.Status != model.ProviderAuthStatusActive {
		t.Fatalf("api auth status = %q, want %q", apiAuthState.Status, model.ProviderAuthStatusActive)
	}

	var activeAuthState model.ProviderAuthState
	if err := db.First(&activeAuthState, "provider_id = ?", "gpt-active").Error; err != nil {
		t.Fatalf("read gpt-active auth state: %v", err)
	}
	if activeAuthState.Status != model.ProviderAuthStatusActive {
		t.Fatalf("gpt-active auth status = %q, want %q", activeAuthState.Status, model.ProviderAuthStatusActive)
	}
	if activeAuthState.AccountID != "acct_test" {
		t.Fatalf("AccountID = %q, want acct_test", activeAuthState.AccountID)
	}
	if activeAuthState.Email != "user@example.com" {
		t.Fatalf("Email = %q, want user@example.com", activeAuthState.Email)
	}
	if activeAuthState.UsageSnapshot == nil || activeAuthState.UsageSnapshot.PlanType != "team" {
		t.Fatalf("UsageSnapshot = %+v, want team snapshot", activeAuthState.UsageSnapshot)
	}

	var pendingAuthState model.ProviderAuthState
	if err := db.First(&pendingAuthState, "provider_id = ?", "gpt-pending").Error; err != nil {
		t.Fatalf("read gpt-pending auth state: %v", err)
	}
	if pendingAuthState.Status != model.ProviderAuthStatusNotConnected {
		t.Fatalf("gpt-pending auth status = %q, want %q", pendingAuthState.Status, model.ProviderAuthStatusNotConnected)
	}
}

func TestMigrateProviderStateTables_PreservesExistingRows(t *testing.T) {
	t.Parallel()

	db := setupProviderStateMigrationDB(t)

	credentialData, err := model.EncodeChatGPTProviderCredential(&model.ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		AccountID:    "acct_test",
	})
	if err != nil {
		t.Fatalf("EncodeChatGPTProviderCredential() error: %v", err)
	}

	provider := model.Provider{
		ID:             "gpt",
		Name:           "GPT Provider",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Enabled:        true,
	}
	insertLegacyProvider(t, db, &provider, credentialData)
	if err := db.Create(&model.ProviderCredential{
		ProviderID:       "gpt",
		SecretData:       "preserved-secret",
		BindingAccountID: strPtr("acct_preserved"),
		Version:          7,
	}).Error; err != nil {
		t.Fatalf("seed provider credential: %v", err)
	}
	if err := db.Create(&model.ProviderAuthState{
		ProviderID:   "gpt",
		Status:       model.ProviderAuthStatusReauthRequired,
		StatusReason: "refresh_token_reused",
		LastError:    "terminal oauth error",
	}).Error; err != nil {
		t.Fatalf("seed provider auth state: %v", err)
	}

	if err := migrateProviderStateTables(db); err != nil {
		t.Fatalf("migrateProviderStateTables() error: %v", err)
	}

	var credential model.ProviderCredential
	if err := db.First(&credential, "provider_id = ?", "gpt").Error; err != nil {
		t.Fatalf("read provider credential: %v", err)
	}
	if credential.SecretData != "preserved-secret" {
		t.Fatalf("SecretData = %q, want preserved-secret", credential.SecretData)
	}
	if credential.Version != 7 {
		t.Fatalf("Version = %d, want 7", credential.Version)
	}

	var authState model.ProviderAuthState
	if err := db.First(&authState, "provider_id = ?", "gpt").Error; err != nil {
		t.Fatalf("read provider auth state: %v", err)
	}
	if authState.Status != model.ProviderAuthStatusReauthRequired {
		t.Fatalf("Status = %q, want %q", authState.Status, model.ProviderAuthStatusReauthRequired)
	}
	if authState.StatusReason != "refresh_token_reused" {
		t.Fatalf("StatusReason = %q, want refresh_token_reused", authState.StatusReason)
	}
}

func TestMigrateProviderStateTables_ResolvesDuplicateLegacyBindingsDeterministically(t *testing.T) {
	t.Parallel()

	db := setupProviderStateMigrationDB(t)
	accountID := "acct_shared"
	zCredentialData := encodeLegacyChatGPTCredential(t, "access-z", "refresh-z", accountID, "z@example.com")
	aCredentialData := encodeLegacyChatGPTCredential(t, "access-a", "refresh-a", accountID, "a@example.com")

	// Insert in reverse order to prove the migration's id ordering, rather than
	// SQLite insertion order, determines the durable account owner.
	providerZ := &model.Provider{ID: "provider-z", Name: "Provider Z", CredentialType: model.ProviderCredentialTypeChatGPT, Enabled: true}
	providerA := &model.Provider{ID: "provider-a", Name: "Provider A", CredentialType: model.ProviderCredentialTypeChatGPT, Enabled: true}
	insertLegacyProvider(t, db, providerZ, zCredentialData)
	insertLegacyProvider(t, db, providerA, aCredentialData)
	if err := db.Create(&model.ProviderAuthState{
		ProviderID: providerZ.ID,
		Status:     model.ProviderAuthStatusActive,
		AccountID:  accountID,
		Email:      "stored-z@example.com",
	}).Error; err != nil {
		t.Fatalf("seed provider-z auth state: %v", err)
	}

	if err := migrateProviderStateTables(db); err != nil {
		t.Fatalf("migrateProviderStateTables() error: %v", err)
	}

	assertMigratedCredential(t, db, providerA.ID, "access-a", "refresh-a", &accountID)
	assertMigratedCredential(t, db, providerZ.ID, "access-z", "refresh-z", nil)
	assertMigratedAuthState(t, db, providerA.ID, model.ProviderAuthStatusActive, "", "", accountID, "a@example.com")
	assertMigratedAuthState(t, db, providerZ.ID, model.ProviderAuthStatusReauthRequired, providerAuthReasonLegacyDuplicateBinding, providerAuthErrorLegacyDuplicateBinding, accountID, "stored-z@example.com")
	assertBindingCounts(t, db, accountID, 2, 1)

	if err := migrateProviderStateTables(db); err != nil {
		t.Fatalf("second migrateProviderStateTables() error: %v", err)
	}
	assertMigratedCredential(t, db, providerA.ID, "access-a", "refresh-a", &accountID)
	assertMigratedCredential(t, db, providerZ.ID, "access-z", "refresh-z", nil)
	assertMigratedAuthState(t, db, providerZ.ID, model.ProviderAuthStatusReauthRequired, providerAuthReasonLegacyDuplicateBinding, providerAuthErrorLegacyDuplicateBinding, accountID, "stored-z@example.com")
	assertBindingCounts(t, db, accountID, 2, 1)
}

func TestMigrateProviderStateTables_PreservesPreexistingBindingOwner(t *testing.T) {
	t.Parallel()

	db := setupProviderStateMigrationDB(t)
	accountID := "acct_shared"
	legacyData := encodeLegacyChatGPTCredential(t, "access-legacy", "refresh-legacy", accountID, "legacy@example.com")
	ownerSecret, err := model.EncodeChatGPTProviderSecret(&model.ChatGPTProviderSecret{
		AccessToken:  "access-owner",
		RefreshToken: "refresh-owner",
	})
	if err != nil {
		t.Fatalf("encode existing provider secret: %v", err)
	}

	// The legacy row sorts first; the already-split row must still retain the
	// account because it is the newer, authoritative representation.
	legacyProvider := &model.Provider{ID: "provider-a", Name: "Legacy Provider", CredentialType: model.ProviderCredentialTypeChatGPT, Enabled: true}
	ownerProvider := &model.Provider{ID: "provider-z", Name: "Existing Owner", CredentialType: model.ProviderCredentialTypeChatGPT, Enabled: true}
	insertLegacyProvider(t, db, legacyProvider, legacyData)
	insertLegacyProvider(t, db, ownerProvider, "")
	if err := db.Create(&model.ProviderCredential{
		ProviderID:       ownerProvider.ID,
		SecretData:       ownerSecret,
		BindingAccountID: &accountID,
		Version:          7,
	}).Error; err != nil {
		t.Fatalf("seed existing owner credential: %v", err)
	}
	if err := db.Create(&model.ProviderAuthState{
		ProviderID: ownerProvider.ID,
		Status:     model.ProviderAuthStatusActive,
		AccountID:  accountID,
		Email:      "owner@example.com",
	}).Error; err != nil {
		t.Fatalf("seed existing owner auth state: %v", err)
	}
	if err := db.Create(&model.ProviderAuthState{
		ProviderID: legacyProvider.ID,
		Status:     model.ProviderAuthStatusActive,
	}).Error; err != nil {
		t.Fatalf("seed legacy duplicate auth state: %v", err)
	}

	if err := migrateProviderStateTables(db); err != nil {
		t.Fatalf("migrateProviderStateTables() error: %v", err)
	}

	assertMigratedCredential(t, db, ownerProvider.ID, "access-owner", "refresh-owner", &accountID)
	var ownerCredential model.ProviderCredential
	if err := db.First(&ownerCredential, "provider_id = ?", ownerProvider.ID).Error; err != nil {
		t.Fatalf("read owner credential version: %v", err)
	}
	if ownerCredential.Version != 7 {
		t.Fatalf("owner credential Version = %d, want 7", ownerCredential.Version)
	}
	assertMigratedAuthState(t, db, ownerProvider.ID, model.ProviderAuthStatusActive, "", "", accountID, "owner@example.com")
	assertMigratedCredential(t, db, legacyProvider.ID, "access-legacy", "refresh-legacy", nil)
	assertMigratedAuthState(t, db, legacyProvider.ID, model.ProviderAuthStatusReauthRequired, providerAuthReasonLegacyDuplicateBinding, providerAuthErrorLegacyDuplicateBinding, accountID, "legacy@example.com")
	assertBindingCounts(t, db, accountID, 2, 1)
}

func TestCanonicalizeLegacyChatGPTSecret_PreservesUnknownPayloads(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"{", `{"future_secret":"recover-me"}`} {
		got, err := canonicalizeLegacyChatGPTSecret(raw)
		if err != nil {
			t.Fatalf("canonicalizeLegacyChatGPTSecret(%q) error: %v", raw, err)
		}
		if got != raw {
			t.Fatalf("canonicalizeLegacyChatGPTSecret(%q) = %q, want original payload", raw, got)
		}
	}
}

func encodeLegacyChatGPTCredential(t *testing.T, accessToken, refreshToken, accountID, email string) string {
	t.Helper()
	raw, err := model.EncodeChatGPTProviderCredential(&model.ChatGPTProviderCredential{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		AccountID:    accountID,
		Email:        email,
	})
	if err != nil {
		t.Fatalf("encode legacy ChatGPT credential: %v", err)
	}
	return raw
}

func assertMigratedCredential(t *testing.T, db *gorm.DB, providerID, wantAccess, wantRefresh string, wantBinding *string) {
	t.Helper()
	var credential model.ProviderCredential
	if err := db.First(&credential, "provider_id = ?", providerID).Error; err != nil {
		t.Fatalf("read credential %q: %v", providerID, err)
	}
	assertCanonicalChatGPTSecretData(t, credential.SecretData, wantAccess, wantRefresh)
	if wantBinding == nil {
		if credential.BindingAccountID != nil {
			t.Fatalf("credential %q BindingAccountID = %v, want nil", providerID, credential.BindingAccountID)
		}
		return
	}
	if credential.BindingAccountID == nil || *credential.BindingAccountID != *wantBinding {
		t.Fatalf("credential %q BindingAccountID = %v, want %q", providerID, credential.BindingAccountID, *wantBinding)
	}
}

func assertCanonicalChatGPTSecretData(t *testing.T, raw, wantAccess, wantRefresh string) {
	t.Helper()
	secret, err := model.DecodeChatGPTProviderSecret(raw)
	if err != nil || secret == nil {
		t.Fatalf("decode canonical credential: secret=%#v err=%v", secret, err)
	}
	if secret.AccessToken != wantAccess || secret.RefreshToken != wantRefresh {
		t.Fatalf("canonical secret tokens = (%q, %q), want (%q, %q)", secret.AccessToken, secret.RefreshToken, wantAccess, wantRefresh)
	}
	for _, summaryField := range []string{"account_id", "email", "plan_type", "usage", "last_refresh", "expires_at"} {
		if strings.Contains(raw, `"`+summaryField+`"`) {
			t.Fatalf("canonical secret contains summary field %q", summaryField)
		}
	}
}

func assertMigratedAuthState(t *testing.T, db *gorm.DB, providerID string, wantStatus model.ProviderAuthStatus, wantReason, wantError, wantAccountID, wantEmail string) {
	t.Helper()
	var authState model.ProviderAuthState
	if err := db.First(&authState, "provider_id = ?", providerID).Error; err != nil {
		t.Fatalf("read auth state %q: %v", providerID, err)
	}
	if authState.Status != wantStatus || authState.StatusReason != wantReason || authState.LastError != wantError {
		t.Fatalf("auth state %q lifecycle = (%q, %q, %q), want (%q, %q, %q)", providerID, authState.Status, authState.StatusReason, authState.LastError, wantStatus, wantReason, wantError)
	}
	if authState.AccountID != wantAccountID || authState.Email != wantEmail {
		t.Fatalf("auth state %q identity = (%q, %q), want (%q, %q)", providerID, authState.AccountID, authState.Email, wantAccountID, wantEmail)
	}
}

func assertBindingCounts(t *testing.T, db *gorm.DB, accountID string, wantTotal, wantBound int64) {
	t.Helper()
	var total, bound int64
	if err := db.Model(&model.ProviderCredential{}).Count(&total).Error; err != nil {
		t.Fatalf("count credentials: %v", err)
	}
	if err := db.Model(&model.ProviderCredential{}).Where("binding_account_id = ?", accountID).Count(&bound).Error; err != nil {
		t.Fatalf("count bound credentials: %v", err)
	}
	if total != wantTotal || bound != wantBound {
		t.Fatalf("credential counts = (total=%d, bound=%d), want (total=%d, bound=%d)", total, bound, wantTotal, wantBound)
	}
}
