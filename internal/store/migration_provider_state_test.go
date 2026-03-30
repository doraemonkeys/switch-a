package store

import (
	"fmt"
	"testing"
	"time"

	"switch-a/internal/model"

	"gorm.io/gorm"
)

func TestMigrateProviderUsageLimitPolicyStorage_BackfillsDerivedDefaults(t *testing.T) {
	t.Parallel()

	db := setupProviderStateMigrationDB(t)
	providers := []model.Provider{
		{
			ID:               "relay-default",
			Name:             "Relay Default",
			APIKey:           "key",
			CredentialType:   model.ProviderCredentialTypeAPIKey,
			UsageLimitPolicy: model.ProviderUsageLimitPolicySwitchProvider,
			APITypes:         []model.ProviderAPIType{{ProviderID: "relay-default", APIType: "claude", BaseURL: "https://api.example.com"}},
			Enabled:          true,
		},
		{
			ID:               "gpt-default",
			Name:             "GPT Default",
			CredentialType:   model.ProviderCredentialTypeChatGPT,
			UsageLimitPolicy: model.ProviderUsageLimitPolicySuspend,
			APITypes:         []model.ProviderAPIType{{ProviderID: "gpt-default", APIType: "codex", BaseURL: "https://chatgpt.com/backend-api/codex"}},
			Enabled:          true,
		},
		{
			ID:               "gpt-explicit",
			Name:             "GPT Explicit",
			CredentialType:   model.ProviderCredentialTypeChatGPT,
			UsageLimitPolicy: model.ProviderUsageLimitPolicySwitchProvider,
			APITypes:         []model.ProviderAPIType{{ProviderID: "gpt-explicit", APIType: "codex", BaseURL: "https://chatgpt.com/backend-api/codex"}},
			Enabled:          true,
		},
	}
	for i := range providers {
		if err := db.Omit("Credential", "AuthState").Create(&providers[i]).Error; err != nil {
			t.Fatalf("create provider %q: %v", providers[i].ID, err)
		}
	}

	if err := migrateProviderUsageLimitPolicyStorage(db); err != nil {
		t.Fatalf("migrateProviderUsageLimitPolicyStorage: %v", err)
	}

	var relayDefault model.Provider
	if err := db.First(&relayDefault, "id = ?", "relay-default").Error; err != nil {
		t.Fatalf("read relay-default: %v", err)
	}
	if relayDefault.UsageLimitPolicy != "" {
		t.Fatalf("relay-default UsageLimitPolicy = %q, want empty inherit-default value", relayDefault.UsageLimitPolicy)
	}

	var gptDefault model.Provider
	if err := db.First(&gptDefault, "id = ?", "gpt-default").Error; err != nil {
		t.Fatalf("read gpt-default: %v", err)
	}
	if gptDefault.UsageLimitPolicy != "" {
		t.Fatalf("gpt-default UsageLimitPolicy = %q, want empty inherit-default value", gptDefault.UsageLimitPolicy)
	}

	var gptExplicit model.Provider
	if err := db.First(&gptExplicit, "id = ?", "gpt-explicit").Error; err != nil {
		t.Fatalf("read gpt-explicit: %v", err)
	}
	if gptExplicit.UsageLimitPolicy != model.ProviderUsageLimitPolicySwitchProvider {
		t.Fatalf("gpt-explicit UsageLimitPolicy = %q, want %q", gptExplicit.UsageLimitPolicy, model.ProviderUsageLimitPolicySwitchProvider)
	}
}

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
	if credential.SecretData != credentialData {
		t.Fatalf("SecretData = %q, want original payload", credential.SecretData)
	}
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
	if credential.SecretData != credentialData {
		t.Fatalf("SecretData = %q, want original payload", credential.SecretData)
	}
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
