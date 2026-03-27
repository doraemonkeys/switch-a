package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"switch-a/internal/model"
)

func TestProviderCRUD(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Create
	groupID := "g1"
	provider := &model.Provider{
		ID:       "p1",
		Name:     "Test Provider",
		APIKey:   "secret-key",
		AuthMode: "bearer",
		GroupID:  &groupID,
		Enabled:  true,
		APITypes: []model.ProviderAPIType{
			{
				ProviderID: "p1",
				APIType:    "claude",
				BaseURL:    "https://api.example.com",
				APIKey:     "claude-key",
			},
		},
	}

	if err := store.CreateProvider(ctx, provider); err != nil {
		t.Fatalf("CreateProvider failed: %v", err)
	}

	// Read
	got, err := store.GetProvider(ctx, "p1")
	if err != nil {
		t.Fatalf("GetProvider failed: %v", err)
	}
	if got == nil {
		t.Fatal("expected provider, got nil")
	}
	if got.Name != "Test Provider" {
		t.Errorf("Name = %q, want %q", got.Name, "Test Provider")
	}
	if len(got.APITypes) != 1 {
		t.Errorf("APITypes len = %d, want 1", len(got.APITypes))
	}
	if got.APITypes[0].BaseURL != "https://api.example.com" {
		t.Errorf("APITypes[0].BaseURL = %q, want %q", got.APITypes[0].BaseURL, "https://api.example.com")
	}
	if got.APITypes[0].APIKey != "claude-key" {
		t.Errorf("APITypes[0].APIKey = %q, want %q", got.APITypes[0].APIKey, "claude-key")
	}

	// List
	providers, err := store.ListProviders(ctx)
	if err != nil {
		t.Fatalf("ListProviders failed: %v", err)
	}
	if len(providers) != 1 {
		t.Errorf("ListProviders len = %d, want 1", len(providers))
	}

	// Update
	provider.Name = "Updated Provider"
	if err := store.UpdateProvider(ctx, provider); err != nil {
		t.Fatalf("UpdateProvider failed: %v", err)
	}

	got, err = store.GetProvider(ctx, "p1")
	if err != nil {
		t.Fatalf("GetProvider after update failed: %v", err)
	}
	if got.Name != "Updated Provider" {
		t.Errorf("Name after update = %q, want %q", got.Name, "Updated Provider")
	}

	// Delete
	if err := store.DeleteProvider(ctx, "p1"); err != nil {
		t.Fatalf("DeleteProvider failed: %v", err)
	}

	got, err = store.GetProvider(ctx, "p1")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetProvider after delete: expected ErrNotFound, got %v", err)
	}
	if got != nil {
		t.Error("expected nil after delete")
	}
}

func TestListProvidersByAPIType(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Create providers with different API types
	p1 := &model.Provider{
		ID:       "p1",
		Name:     "Claude Provider",
		APIKey:   "key1",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude", BaseURL: "https://api.claude.com"}},
	}
	p2 := &model.Provider{
		ID:       "p2",
		Name:     "Codex Provider",
		APIKey:   "key2",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "p2", APIType: "codex", BaseURL: "https://api.codex.com"}},
	}
	p3 := &model.Provider{
		ID:       "p3",
		Name:     "Disabled Claude",
		APIKey:   "key3",
		Enabled:  false,
		APITypes: []model.ProviderAPIType{{ProviderID: "p3", APIType: "claude", BaseURL: "https://api.disabled.com"}},
	}

	for _, p := range []*model.Provider{p1, p2, p3} {
		if err := store.CreateProvider(ctx, p); err != nil {
			t.Fatalf("CreateProvider failed: %v", err)
		}
	}

	// Query claude providers
	providers, err := store.ListProvidersByAPIType(ctx, "claude")
	if err != nil {
		t.Fatalf("ListProvidersByAPIType failed: %v", err)
	}
	if len(providers) != 1 {
		t.Errorf("ListProvidersByAPIType(claude) len = %d, want 1", len(providers))
	}
	if len(providers) > 0 && providers[0].ID != "p1" {
		t.Errorf("Expected p1, got %s", providers[0].ID)
	}
}

func TestListProviders(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Empty list initially
	providers, err := store.ListProviders(ctx)
	if err != nil {
		t.Fatalf("ListProviders failed: %v", err)
	}
	if len(providers) != 0 {
		t.Errorf("expected 0 providers, got %d", len(providers))
	}

	// Add a provider
	p := &model.Provider{
		ID:       "p1",
		Name:     "Test",
		APIKey:   "key",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude", BaseURL: "https://test.com"}},
	}
	if err := store.CreateProvider(ctx, p); err != nil {
		t.Fatalf("CreateProvider failed: %v", err)
	}

	providers, err = store.ListProviders(ctx)
	if err != nil {
		t.Fatalf("ListProviders failed: %v", err)
	}
	if len(providers) != 1 {
		t.Errorf("expected 1 provider, got %d", len(providers))
	}
}

func TestUpdateProvider(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Create provider
	p := &model.Provider{
		ID:      "p1",
		Name:    "Original",
		APIKey:  "key",
		Enabled: true,
		APITypes: []model.ProviderAPIType{{
			ProviderID: "p1",
			APIType:    "claude",
			BaseURL:    "https://test.com",
			APIKey:     "claude-key",
		}},
	}
	if err := store.CreateProvider(ctx, p); err != nil {
		t.Fatalf("CreateProvider failed: %v", err)
	}

	// Update provider
	p.Name = "Updated"
	p.APITypes = []model.ProviderAPIType{{
		ProviderID: "p1",
		APIType:    "codex",
		BaseURL:    "https://test.com",
		APIKey:     "codex-key",
	}}
	if err := store.UpdateProvider(ctx, p); err != nil {
		t.Fatalf("UpdateProvider failed: %v", err)
	}

	// Verify update
	got, err := store.GetProvider(ctx, "p1")
	if err != nil {
		t.Fatalf("GetProvider failed: %v", err)
	}
	if got.Name != "Updated" {
		t.Errorf("Name = %q, want %q", got.Name, "Updated")
	}
	if len(got.APITypes) != 1 || got.APITypes[0].APIType != "codex" {
		t.Errorf("APITypes not updated correctly")
	}
	if got.APITypes[0].APIKey != "codex-key" {
		t.Errorf("APITypes[0].APIKey = %q, want %q", got.APITypes[0].APIKey, "codex-key")
	}
}

func TestCreateProvider_NewFieldsPersisted(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	provider := &model.Provider{
		ID:      "backoff-test",
		Name:    "Backoff Test",
		APIKey:  "key",
		Enabled: true,
		Backoff: model.BackoffPolicy{
			InitialDelay: model.Duration(500 * time.Millisecond),
			MaxDelay:     model.Duration(5 * time.Second),
			Multiplier:   2.0,
			Jitter:       true,
		},
		Vendor:         "anthropic",
		FailoverScope:  model.ScopeVendor,
		AcceptFailover: model.ScopeVendor,
		MaxRetries:     3,
		APITypes:       []model.ProviderAPIType{{ProviderID: "backoff-test", APIType: "claude", BaseURL: "https://api.example.com"}},
	}

	if err := store.CreateProvider(ctx, provider); err != nil {
		t.Fatalf("CreateProvider failed: %v", err)
	}

	got, err := store.GetProvider(ctx, "backoff-test")
	if err != nil {
		t.Fatalf("GetProvider failed: %v", err)
	}

	// Backoff fields must survive create→read roundtrip
	if got.Backoff.InitialDelay != model.Duration(500*time.Millisecond) {
		t.Errorf("InitialDelay = %v, want 500ms", time.Duration(got.Backoff.InitialDelay))
	}
	if got.Backoff.MaxDelay != model.Duration(5*time.Second) {
		t.Errorf("MaxDelay = %v, want 5s", time.Duration(got.Backoff.MaxDelay))
	}
	if got.Backoff.Multiplier != 2.0 {
		t.Errorf("Multiplier = %v, want 2.0", got.Backoff.Multiplier)
	}
	if !got.Backoff.Jitter {
		t.Error("Jitter = false, want true")
	}
	if got.Vendor != "anthropic" {
		t.Errorf("Vendor = %q, want %q", got.Vendor, "anthropic")
	}
	if got.FailoverScope != model.ScopeVendor {
		t.Errorf("FailoverScope = %q, want %q", got.FailoverScope, model.ScopeVendor)
	}
	if got.AcceptFailover != model.ScopeVendor {
		t.Errorf("AcceptFailover = %q, want %q", got.AcceptFailover, model.ScopeVendor)
	}
	if got.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", got.MaxRetries)
	}
}

func TestUpdateProvider_BackoffFieldsPersisted(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Create provider with zero backoff
	provider := &model.Provider{
		ID:       "backoff-update",
		Name:     "Backoff Update Test",
		APIKey:   "key",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "backoff-update", APIType: "claude", BaseURL: "https://api.example.com"}},
	}
	if err := store.CreateProvider(ctx, provider); err != nil {
		t.Fatalf("CreateProvider failed: %v", err)
	}

	// Update with non-zero backoff values (exercises the GORM Save path)
	provider.Backoff = model.BackoffPolicy{
		InitialDelay: model.Duration(200 * time.Millisecond),
		MaxDelay:     model.Duration(10 * time.Second),
		Multiplier:   3.0,
		Jitter:       true,
	}
	if err := store.UpdateProvider(ctx, provider); err != nil {
		t.Fatalf("UpdateProvider failed: %v", err)
	}

	got, err := store.GetProvider(ctx, "backoff-update")
	if err != nil {
		t.Fatalf("GetProvider failed: %v", err)
	}
	if got.Backoff.InitialDelay != model.Duration(200*time.Millisecond) {
		t.Errorf("InitialDelay = %v, want 200ms", time.Duration(got.Backoff.InitialDelay))
	}
	if got.Backoff.MaxDelay != model.Duration(10*time.Second) {
		t.Errorf("MaxDelay = %v, want 10s", time.Duration(got.Backoff.MaxDelay))
	}
	if got.Backoff.Multiplier != 3.0 {
		t.Errorf("Multiplier = %v, want 3.0", got.Backoff.Multiplier)
	}
	if !got.Backoff.Jitter {
		t.Error("Jitter = false, want true")
	}

	// Update back to zero backoff to confirm clearing works
	provider.Backoff = model.BackoffPolicy{}
	if err := store.UpdateProvider(ctx, provider); err != nil {
		t.Fatalf("UpdateProvider (clear backoff) failed: %v", err)
	}

	got, err = store.GetProvider(ctx, "backoff-update")
	if err != nil {
		t.Fatalf("GetProvider after clear failed: %v", err)
	}
	if got.Backoff.InitialDelay != 0 {
		t.Errorf("InitialDelay after clear = %v, want 0", time.Duration(got.Backoff.InitialDelay))
	}
	if got.Backoff.MaxDelay != 0 {
		t.Errorf("MaxDelay after clear = %v, want 0", time.Duration(got.Backoff.MaxDelay))
	}
	if got.Backoff.Multiplier != 0 {
		t.Errorf("Multiplier after clear = %v, want 0", got.Backoff.Multiplier)
	}
	if got.Backoff.Jitter {
		t.Error("Jitter after clear = true, want false")
	}
}

func TestUpdateProvider_DoesNotMutateInput(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Create provider with empty scopes (should default to "any")
	p := &model.Provider{
		ID:       "p1",
		Name:     "Test",
		APIKey:   "key",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude", BaseURL: "https://test.com"}},
		// Leave FailoverScope and AcceptFailover empty
	}
	if err := store.CreateProvider(ctx, p); err != nil {
		t.Fatalf("CreateProvider failed: %v", err)
	}

	// Update and verify the caller's struct is not mutated
	p.Name = "Updated"
	if err := store.UpdateProvider(ctx, p); err != nil {
		t.Fatalf("UpdateProvider failed: %v", err)
	}

	// The caller's struct should retain its original empty scope values
	if p.FailoverScope != "" {
		t.Errorf("FailoverScope was mutated to %q, expected empty string", p.FailoverScope)
	}
	if p.AcceptFailover != "" {
		t.Errorf("AcceptFailover was mutated to %q, expected empty string", p.AcceptFailover)
	}

	// But the database should have the default values
	got, err := store.GetProvider(ctx, "p1")
	if err != nil {
		t.Fatalf("GetProvider failed: %v", err)
	}
	if got.FailoverScope != model.ScopeAny {
		t.Errorf("stored FailoverScope = %q, want %q", got.FailoverScope, model.ScopeAny)
	}
	if got.AcceptFailover != model.ScopeAny {
		t.Errorf("stored AcceptFailover = %q, want %q", got.AcceptFailover, model.ScopeAny)
	}
}

func TestGetProviderNotFound(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	got, err := store.GetProvider(ctx, "nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetProvider: expected ErrNotFound, got %v", err)
	}
	if got != nil {
		t.Error("expected nil for non-existent provider")
	}
}

func TestCreateProvider_PersistsCredentialAndAuthState(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	credentialData := mustMarshalChatGPTCredentialDataWithTokens(
		t,
		"acct_test",
		"access-token-one",
		"refresh-token-one",
	)
	provider := &model.Provider{
		ID:             "gpt",
		Name:           "GPT Provider",
		AuthMode:       "bearer",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Credential:     testProviderCredential("gpt", credentialData),
		Enabled:        true,
	}
	if err := store.CreateProvider(ctx, provider); err != nil {
		t.Fatalf("CreateProvider failed: %v", err)
	}

	stored, err := store.GetProvider(ctx, provider.ID)
	if err != nil {
		t.Fatalf("GetProvider failed: %v", err)
	}
	if stored.Credential == nil {
		t.Fatal("Credential = nil, want hydrated provider credential")
	}
	if stored.Credential.SecretData != credentialData {
		t.Fatalf("Credential.SecretData = %q, want original payload", stored.Credential.SecretData)
	}
	if stored.Credential.BindingAccountID == nil || *stored.Credential.BindingAccountID != "acct_test" {
		t.Fatalf("Credential.BindingAccountID = %v, want acct_test", stored.Credential.BindingAccountID)
	}
	if stored.AuthState == nil {
		t.Fatal("AuthState = nil, want hydrated provider auth state")
	}
	if stored.AuthState.Status != model.ProviderAuthStatusActive {
		t.Fatalf("AuthState.Status = %q, want %q", stored.AuthState.Status, model.ProviderAuthStatusActive)
	}
}

func TestUpdateProviderCredential_PreservesReauthRequiredState(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	initialCredentialData := mustMarshalChatGPTCredentialDataWithTokens(
		t,
		"acct_test",
		"access-token-one",
		"refresh-token-one",
	)
	if err := store.CreateProvider(ctx, &model.Provider{
		ID:             "gpt",
		Name:           "GPT Provider",
		AuthMode:       "bearer",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Credential:     testProviderCredential("gpt", initialCredentialData),
		Enabled:        true,
	}); err != nil {
		t.Fatalf("CreateProvider failed: %v", err)
	}

	if err := store.db.Save(&model.ProviderAuthState{
		ProviderID:   "gpt",
		Status:       model.ProviderAuthStatusReauthRequired,
		StatusReason: "invalid_grant",
		LastError:    "refresh_token_reused",
	}).Error; err != nil {
		t.Fatalf("seed reauth state: %v", err)
	}

	refreshedCredentialData := mustMarshalChatGPTCredentialDataWithTokens(
		t,
		"acct_test",
		"access-token-two",
		"refresh-token-two",
	)
	if err := store.UpdateProviderCredential(
		ctx,
		"gpt",
		model.ProviderCredentialTypeChatGPT,
		refreshedCredentialData,
	); err != nil {
		t.Fatalf("UpdateProviderCredential failed: %v", err)
	}

	stored, err := store.GetProvider(ctx, "gpt")
	if err != nil {
		t.Fatalf("GetProvider failed: %v", err)
	}
	if stored.Credential == nil {
		t.Fatal("Credential = nil, want hydrated provider credential")
	}
	if stored.Credential.SecretData != refreshedCredentialData {
		t.Fatalf("Credential.SecretData = %q, want refreshed payload", stored.Credential.SecretData)
	}
	if stored.Credential.Version != 2 {
		t.Fatalf("Credential.Version = %d, want 2", stored.Credential.Version)
	}
	if stored.AuthState == nil {
		t.Fatal("AuthState = nil, want persisted auth state")
	}
	if stored.AuthState.Status != model.ProviderAuthStatusReauthRequired {
		t.Fatalf("AuthState.Status = %q, want %q", stored.AuthState.Status, model.ProviderAuthStatusReauthRequired)
	}
	if stored.AuthState.StatusReason != "invalid_grant" {
		t.Fatalf("AuthState.StatusReason = %q, want invalid_grant", stored.AuthState.StatusReason)
	}
}

func TestDeleteProvider_RemovesCredentialAndAuthState(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	if err := store.CreateProvider(ctx, &model.Provider{
		ID:             "gpt",
		Name:           "GPT Provider",
		AuthMode:       "bearer",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Credential:     testProviderCredential("gpt", mustMarshalChatGPTCredentialData(t, "acct_test")),
		Enabled:        true,
	}); err != nil {
		t.Fatalf("CreateProvider failed: %v", err)
	}

	if err := store.DeleteProvider(ctx, "gpt"); err != nil {
		t.Fatalf("DeleteProvider failed: %v", err)
	}

	var credentialCount int64
	if err := store.db.Model(&model.ProviderCredential{}).
		Where("provider_id = ?", "gpt").
		Count(&credentialCount).Error; err != nil {
		t.Fatalf("count provider credentials: %v", err)
	}
	if credentialCount != 0 {
		t.Fatalf("provider credential count = %d, want 0", credentialCount)
	}

	var authStateCount int64
	if err := store.db.Model(&model.ProviderAuthState{}).
		Where("provider_id = ?", "gpt").
		Count(&authStateCount).Error; err != nil {
		t.Fatalf("count provider auth states: %v", err)
	}
	if authStateCount != 0 {
		t.Fatalf("provider auth state count = %d, want 0", authStateCount)
	}
}

func TestCreateProvider_RejectsDuplicateChatGPTCredentialBinding(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	if err := store.CreateProvider(ctx, &model.Provider{
		ID:             "p1",
		Name:           "GPT One",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Credential:     testProviderCredential("p1", mustMarshalChatGPTCredentialData(t, "acct-shared")),
		Enabled:        true,
	}); err != nil {
		t.Fatalf("CreateProvider p1 failed: %v", err)
	}

	err := store.CreateProvider(ctx, &model.Provider{
		ID:             "p2",
		Name:           "GPT Two",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Credential:     testProviderCredential("p2", mustMarshalChatGPTCredentialData(t, "acct-shared")),
		Enabled:        true,
	})
	if !errors.Is(err, ErrCredentialBindingConflict) {
		t.Fatalf("CreateProvider duplicate = %v, want ErrCredentialBindingConflict", err)
	}

	var conflict *CredentialBindingConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("CreateProvider duplicate error = %v, want CredentialBindingConflictError", err)
	}
	if conflict.ProviderID != "p1" {
		t.Fatalf("conflict.ProviderID = %q, want p1", conflict.ProviderID)
	}
	if conflict.AccountID != "acct-shared" {
		t.Fatalf("conflict.AccountID = %q, want acct-shared", conflict.AccountID)
	}

	providers, listErr := store.ListProviders(ctx)
	if listErr != nil {
		t.Fatalf("ListProviders failed: %v", listErr)
	}
	if len(providers) != 1 {
		t.Fatalf("provider count = %d, want 1", len(providers))
	}
}

func TestUpdateProvider_RejectsDuplicateChatGPTCredentialBinding(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	if err := store.CreateProvider(ctx, &model.Provider{
		ID:             "p1",
		Name:           "GPT One",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Credential:     testProviderCredential("p1", mustMarshalChatGPTCredentialData(t, "acct-one")),
		Enabled:        true,
	}); err != nil {
		t.Fatalf("CreateProvider p1 failed: %v", err)
	}
	if err := store.CreateProvider(ctx, &model.Provider{
		ID:             "p2",
		Name:           "GPT Two",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Credential:     testProviderCredential("p2", mustMarshalChatGPTCredentialData(t, "acct-two")),
		Enabled:        true,
	}); err != nil {
		t.Fatalf("CreateProvider p2 failed: %v", err)
	}

	err := store.UpdateProvider(ctx, &model.Provider{
		ID:             "p2",
		Name:           "GPT Two Updated",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Credential:     testProviderCredential("p2", mustMarshalChatGPTCredentialData(t, "acct-one")),
		Enabled:        true,
	})
	if !errors.Is(err, ErrCredentialBindingConflict) {
		t.Fatalf("UpdateProvider duplicate = %v, want ErrCredentialBindingConflict", err)
	}

	got, getErr := store.GetProvider(ctx, "p2")
	if getErr != nil {
		t.Fatalf("GetProvider p2 failed: %v", getErr)
	}
	if got.Credential == nil || got.Credential.BindingAccountID == nil || *got.Credential.BindingAccountID != "acct-two" {
		t.Fatalf("Credential.BindingAccountID = %v, want acct-two", got.Credential)
	}
}

func TestUpdateProviderCredential_RejectsDuplicateChatGPTCredentialBinding(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	if err := store.CreateProvider(ctx, &model.Provider{
		ID:             "p1",
		Name:           "GPT One",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Credential:     testProviderCredential("p1", mustMarshalChatGPTCredentialData(t, "acct-one")),
		Enabled:        true,
	}); err != nil {
		t.Fatalf("CreateProvider p1 failed: %v", err)
	}
	if err := store.CreateProvider(ctx, &model.Provider{
		ID:             "p2",
		Name:           "GPT Two",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Credential:     testProviderCredential("p2", mustMarshalChatGPTCredentialData(t, "acct-two")),
		Enabled:        true,
	}); err != nil {
		t.Fatalf("CreateProvider p2 failed: %v", err)
	}

	err := store.UpdateProviderCredential(
		ctx,
		"p2",
		model.ProviderCredentialTypeChatGPT,
		mustMarshalChatGPTCredentialData(t, "acct-one"),
	)
	if !errors.Is(err, ErrCredentialBindingConflict) {
		t.Fatalf("UpdateProviderCredential duplicate = %v, want ErrCredentialBindingConflict", err)
	}

	got, getErr := store.GetProvider(ctx, "p2")
	if getErr != nil {
		t.Fatalf("GetProvider p2 failed: %v", getErr)
	}
	if got.Credential == nil || got.Credential.BindingAccountID == nil || *got.Credential.BindingAccountID != "acct-two" {
		t.Fatalf("Credential.BindingAccountID = %v, want acct-two", got.Credential)
	}
}

func TestCreateProvider_ChatGPTWithoutCredentialBackfillsNotConnectedAuthState(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	if err := store.CreateProvider(ctx, &model.Provider{
		ID:             "gpt-pending",
		Name:           "GPT Pending",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Enabled:        true,
	}); err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}

	got, err := store.GetProvider(ctx, "gpt-pending")
	if err != nil {
		t.Fatalf("GetProvider() error = %v", err)
	}
	if got.Credential != nil {
		t.Fatalf("Credential = %+v, want nil for not-connected provider", got.Credential)
	}
	if got.AuthState == nil {
		t.Fatal("AuthState = nil, want not_connected snapshot")
	}
	if got.AuthState.Status != model.ProviderAuthStatusNotConnected {
		t.Fatalf("AuthState.Status = %q, want %q", got.AuthState.Status, model.ProviderAuthStatusNotConnected)
	}
}

func TestUpdateProviderCredential_SyncsCredentialAndAuthState(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	if err := store.CreateProvider(ctx, &model.Provider{
		ID:             "gpt-active",
		Name:           "GPT Active",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Credential:     testProviderCredential("gpt-active", mustMarshalChatGPTCredentialData(t, "acct-one")),
		Enabled:        true,
	}); err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}

	if err := store.UpdateProviderCredential(
		ctx,
		"gpt-active",
		model.ProviderCredentialTypeChatGPT,
		mustMarshalChatGPTCredentialDataWithSummary(t, "acct-one", "user@example.com"),
	); err != nil {
		t.Fatalf("UpdateProviderCredential() error = %v", err)
	}

	got, err := store.GetProvider(ctx, "gpt-active")
	if err != nil {
		t.Fatalf("GetProvider() error = %v", err)
	}
	if got.Credential == nil {
		t.Fatal("Credential = nil, want persisted provider credential")
	}
	if got.Credential.BindingAccountID == nil || *got.Credential.BindingAccountID != "acct-one" {
		t.Fatalf("BindingAccountID = %v, want acct-one", got.Credential.BindingAccountID)
	}
	if got.Credential.Version != 2 {
		t.Fatalf("Version = %d, want 2 after credential refresh", got.Credential.Version)
	}
	if got.AuthState == nil {
		t.Fatal("AuthState = nil, want active snapshot")
	}
	if got.AuthState.Status != model.ProviderAuthStatusActive {
		t.Fatalf("AuthState.Status = %q, want %q", got.AuthState.Status, model.ProviderAuthStatusActive)
	}
	if got.AuthState.Email != "user@example.com" {
		t.Fatalf("AuthState.Email = %q, want user@example.com", got.AuthState.Email)
	}
}

func mustMarshalChatGPTCredentialData(t *testing.T, accountID string) string {
	return mustMarshalChatGPTCredentialDataWithTokens(
		t,
		accountID,
		"access-token",
		"refresh-token",
	)
}

func mustMarshalChatGPTCredentialDataWithTokens(
	t *testing.T,
	accountID string,
	accessToken string,
	refreshToken string,
) string {
	t.Helper()

	payload, err := json.Marshal(model.ChatGPTProviderCredential{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		IDToken:      "id-token",
		AccountID:    accountID,
	})
	if err != nil {
		t.Fatalf("marshal GPT credential payload: %v", err)
	}
	return string(payload)
}

func mustMarshalChatGPTCredentialDataWithSummary(t *testing.T, accountID, email string) string {
	t.Helper()

	payload, err := json.Marshal(model.ChatGPTProviderCredential{
		AccessToken:  "access-token-next",
		RefreshToken: "refresh-token-next",
		IDToken:      "id-token-next",
		AccountID:    accountID,
		Email:        email,
	})
	if err != nil {
		t.Fatalf("marshal GPT credential payload with summary: %v", err)
	}
	return string(payload)
}

func testProviderCredential(providerID, raw string) *model.ProviderCredential {
	return model.ProviderCredentialFromLegacy(providerID, model.ProviderCredentialTypeChatGPT, raw)
}
