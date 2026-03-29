package store

import (
	"context"
	"errors"
	"testing"

	"switch-a/internal"
	"switch-a/internal/model"

	"gorm.io/gorm"
)

type providerStatePassthroughStore struct {
	internal.Store
	initCalls                    int
	getProviderAuthStateCalls    int
	updateProviderAuthStateCalls int
	updateCredentialStateCalls   int
	listRoutingPoliciesCalls     int
	gotProviderID                string
	gotAPIType                   string
	gotCredential                *model.ProviderCredential
	gotAuthState                 *model.ProviderAuthState
	authStateResult              *model.ProviderAuthState
	routingPoliciesResult        []model.RoutingPolicy
}

type initErrorStore struct {
	internal.Store
	err error
}

func (s *providerStatePassthroughStore) InitDefaultConfig(context.Context) error {
	s.initCalls++
	return nil
}

func (s initErrorStore) InitDefaultConfig(context.Context) error {
	return s.err
}

func (s *providerStatePassthroughStore) GetProviderAuthState(_ context.Context, providerID string) (*model.ProviderAuthState, error) {
	s.getProviderAuthStateCalls++
	s.gotProviderID = providerID
	return s.authStateResult, nil
}

func (s *providerStatePassthroughStore) UpdateProviderAuthState(
	_ context.Context,
	providerID string,
	authState *model.ProviderAuthState,
) error {
	s.updateProviderAuthStateCalls++
	s.gotProviderID = providerID
	s.gotAuthState = authState
	return nil
}

func (s *providerStatePassthroughStore) UpdateProviderCredentialState(
	_ context.Context,
	providerID string,
	credential *model.ProviderCredential,
	authState *model.ProviderAuthState,
) error {
	s.updateCredentialStateCalls++
	s.gotProviderID = providerID
	s.gotCredential = credential
	s.gotAuthState = authState
	return nil
}

func (s *providerStatePassthroughStore) ListRoutingPoliciesByAPIType(
	_ context.Context,
	apiType string,
) ([]model.RoutingPolicy, error) {
	s.listRoutingPoliciesCalls++
	s.gotAPIType = apiType
	return s.routingPoliciesResult, nil
}

func TestCachedStore_PassthroughProviderStateMethods(t *testing.T) {
	t.Parallel()

	stub := &providerStatePassthroughStore{
		authStateResult: &model.ProviderAuthState{Status: model.ProviderAuthStatusActive},
		routingPoliciesResult: []model.RoutingPolicy{
			{ID: 1, APIType: "codex"},
		},
	}
	cached := NewCachedStore(CachedStoreConfig{Store: stub})
	ctx := context.Background()

	if err := cached.InitDefaultConfig(ctx); err != nil {
		t.Fatalf("InitDefaultConfig returned error: %v", err)
	}
	if stub.initCalls != 1 {
		t.Fatalf("InitDefaultConfig calls = %d, want 1", stub.initCalls)
	}

	authState, err := cached.GetProviderAuthState(ctx, "provider-1")
	if err != nil {
		t.Fatalf("GetProviderAuthState returned error: %v", err)
	}
	if authState != stub.authStateResult {
		t.Fatalf("GetProviderAuthState result = %p, want %p", authState, stub.authStateResult)
	}

	updatedAuthState := &model.ProviderAuthState{Status: model.ProviderAuthStatusReauthRequired}
	if err := cached.UpdateProviderAuthState(ctx, "provider-1", updatedAuthState); err != nil {
		t.Fatalf("UpdateProviderAuthState returned error: %v", err)
	}

	credential := &model.ProviderCredential{SecretData: "{}", Version: 2}
	if err := cached.UpdateProviderCredentialState(ctx, "provider-1", credential, updatedAuthState); err != nil {
		t.Fatalf("UpdateProviderCredentialState returned error: %v", err)
	}

	policies, err := cached.ListRoutingPoliciesByAPIType(ctx, "codex")
	if err != nil {
		t.Fatalf("ListRoutingPoliciesByAPIType returned error: %v", err)
	}
	if len(policies) != 1 || policies[0].APIType != "codex" {
		t.Fatalf("ListRoutingPoliciesByAPIType = %+v, want codex policy", policies)
	}

	if stub.getProviderAuthStateCalls != 1 {
		t.Fatalf("GetProviderAuthState calls = %d, want 1", stub.getProviderAuthStateCalls)
	}
	if stub.updateProviderAuthStateCalls != 1 {
		t.Fatalf("UpdateProviderAuthState calls = %d, want 1", stub.updateProviderAuthStateCalls)
	}
	if stub.updateCredentialStateCalls != 1 {
		t.Fatalf("UpdateProviderCredentialState calls = %d, want 1", stub.updateCredentialStateCalls)
	}
	if stub.listRoutingPoliciesCalls != 1 {
		t.Fatalf("ListRoutingPoliciesByAPIType calls = %d, want 1", stub.listRoutingPoliciesCalls)
	}
	if stub.gotProviderID != "provider-1" {
		t.Fatalf("provider id = %q, want provider-1", stub.gotProviderID)
	}
	if stub.gotAPIType != "codex" {
		t.Fatalf("api type = %q, want codex", stub.gotAPIType)
	}
	if stub.gotCredential != credential {
		t.Fatalf("credential pointer = %p, want %p", stub.gotCredential, credential)
	}
	if stub.gotAuthState != updatedAuthState {
		t.Fatalf("auth state pointer = %p, want %p", stub.gotAuthState, updatedAuthState)
	}
}

func TestNewCachedStore_PanicsWithoutStore(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("NewCachedStore panic = nil, want panic when Store is missing")
		}
	}()

	_ = NewCachedStore(CachedStoreConfig{})
}

func TestCachedStore_ProviderStateMethodsReturnNilWhenUnsupported(t *testing.T) {
	t.Parallel()

	cached := NewCachedStore(CachedStoreConfig{
		Store: &configOnlyStore{configs: map[string]string{}},
	})
	ctx := context.Background()

	authState, err := cached.GetProviderAuthState(ctx, "provider-1")
	if err != nil {
		t.Fatalf("GetProviderAuthState returned error: %v", err)
	}
	if authState != nil {
		t.Fatalf("GetProviderAuthState = %+v, want nil", authState)
	}

	if err := cached.UpdateProviderAuthState(ctx, "provider-1", &model.ProviderAuthState{
		Status: model.ProviderAuthStatusActive,
	}); err != nil {
		t.Fatalf("UpdateProviderAuthState returned error: %v", err)
	}

	if err := cached.UpdateProviderCredentialState(ctx, "provider-1", &model.ProviderCredential{
		SecretData: "{}",
		Version:    1,
	}, &model.ProviderAuthState{
		Status: model.ProviderAuthStatusActive,
	}); err != nil {
		t.Fatalf("UpdateProviderCredentialState returned error: %v", err)
	}

	policies, err := cached.ListRoutingPoliciesByAPIType(ctx, "codex")
	if err != nil {
		t.Fatalf("ListRoutingPoliciesByAPIType returned error: %v", err)
	}
	if policies != nil {
		t.Fatalf("ListRoutingPoliciesByAPIType = %+v, want nil", policies)
	}
}

func TestCachedStore_InitDefaultConfig_ReturnsUnderlyingError(t *testing.T) {
	t.Parallel()

	expected := errors.New("init failed")
	cached := NewCachedStore(CachedStoreConfig{
		Store: initErrorStore{err: expected},
	})

	err := cached.InitDefaultConfig(context.Background())
	if !errors.Is(err, expected) {
		t.Fatalf("InitDefaultConfig error = %v, want %v", err, expected)
	}
}

func TestSQLiteStore_UpdateAndGetProviderAuthState(t *testing.T) {
	t.Parallel()

	store := setupTestStore(t)
	ctx := context.Background()

	if err := store.CreateProvider(ctx, &model.Provider{
		ID:             "api-provider",
		Name:           "API Provider",
		APIKey:         "api-key",
		CredentialType: model.ProviderCredentialTypeAPIKey,
		Enabled:        true,
	}); err != nil {
		t.Fatalf("CreateProvider failed: %v", err)
	}

	state := &model.ProviderAuthState{
		Status:           model.ProviderAuthStatusReauthRequired,
		StatusReason:     " invalid_grant ",
		LastError:        " refresh_token_reused ",
		Email:            " user@example.com ",
		AccountID:        " acct-123 ",
		PlanType:         " pro ",
		RefreshFailCount: -5,
	}
	if err := store.UpdateProviderAuthState(ctx, "api-provider", state); err != nil {
		t.Fatalf("UpdateProviderAuthState failed: %v", err)
	}

	got, err := store.GetProviderAuthState(ctx, "api-provider")
	if err != nil {
		t.Fatalf("GetProviderAuthState failed: %v", err)
	}
	if got == nil {
		t.Fatal("GetProviderAuthState = nil, want auth state")
	}
	if got.ProviderID != "api-provider" {
		t.Fatalf("ProviderID = %q, want api-provider", got.ProviderID)
	}
	if got.Status != model.ProviderAuthStatusReauthRequired {
		t.Fatalf("Status = %q, want %q", got.Status, model.ProviderAuthStatusReauthRequired)
	}
	if got.StatusReason != "invalid_grant" {
		t.Fatalf("StatusReason = %q, want invalid_grant", got.StatusReason)
	}
	if got.LastError != "refresh_token_reused" {
		t.Fatalf("LastError = %q, want refresh_token_reused", got.LastError)
	}
	if got.Email != "user@example.com" {
		t.Fatalf("Email = %q, want user@example.com", got.Email)
	}
	if got.AccountID != "acct-123" {
		t.Fatalf("AccountID = %q, want acct-123", got.AccountID)
	}
	if got.PlanType != "pro" {
		t.Fatalf("PlanType = %q, want pro", got.PlanType)
	}
	if got.RefreshFailCount != 0 {
		t.Fatalf("RefreshFailCount = %d, want 0", got.RefreshFailCount)
	}

	missing, err := store.GetProviderAuthState(ctx, "missing-provider")
	if err != nil {
		t.Fatalf("GetProviderAuthState missing returned error: %v", err)
	}
	if missing != nil {
		t.Fatalf("GetProviderAuthState missing = %+v, want nil", missing)
	}

	if err := store.CreateProvider(ctx, &model.Provider{
		ID:             "no-state",
		Name:           "No State",
		APIKey:         "api-key",
		CredentialType: model.ProviderCredentialTypeAPIKey,
		Enabled:        true,
	}); err != nil {
		t.Fatalf("CreateProvider no-state failed: %v", err)
	}
	if err := store.db.Where("provider_id = ?", "no-state").Delete(&model.ProviderAuthState{}).Error; err != nil {
		t.Fatalf("delete auth state for no-state provider: %v", err)
	}
	missingState, err := store.GetProviderAuthState(ctx, "no-state")
	if err != nil {
		t.Fatalf("GetProviderAuthState no-state returned error: %v", err)
	}
	if missingState != nil {
		t.Fatalf("GetProviderAuthState no-state = %+v, want nil", missingState)
	}
}

func TestSQLiteStore_UpdateProviderAuthState_AndCredentialState_Errors(t *testing.T) {
	t.Parallel()

	store := setupTestStore(t)
	ctx := context.Background()

	err := store.UpdateProviderAuthState(ctx, "missing-provider", &model.ProviderAuthState{
		Status: model.ProviderAuthStatusActive,
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateProviderAuthState missing = %v, want ErrNotFound", err)
	}

	if err := store.UpdateProviderCredentialState(ctx, "missing-provider", nil, nil); err == nil {
		t.Fatal("UpdateProviderCredentialState(nil credential) error = nil, want error")
	}
}

func TestSQLiteStore_UpdateProviderCredentialState_UsesCASAndPersistsAuthState(t *testing.T) {
	t.Parallel()

	store := setupTestStore(t)
	ctx := context.Background()

	initialCredentialData := mustMarshalChatGPTCredentialDataWithTokens(
		t,
		"acct-state",
		"access-token-one",
		"refresh-token-one",
	)
	if err := store.CreateProvider(ctx, &model.Provider{
		ID:             "chatgpt-provider",
		Name:           "ChatGPT Provider",
		AuthMode:       "bearer",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Credential:     testProviderCredential("chatgpt-provider", initialCredentialData),
		Enabled:        true,
	}); err != nil {
		t.Fatalf("CreateProvider failed: %v", err)
	}

	current, err := store.GetProvider(ctx, "chatgpt-provider")
	if err != nil {
		t.Fatalf("GetProvider current failed: %v", err)
	}
	if current.Credential == nil {
		t.Fatal("current credential = nil, want persisted credential")
	}

	refreshed := current.Credential.Clone()
	refreshed.SecretData = mustMarshalChatGPTCredentialDataWithTokens(
		t,
		"acct-state",
		"access-token-two",
		"refresh-token-two",
	)
	authState := &model.ProviderAuthState{
		Status:       model.ProviderAuthStatusActive,
		StatusReason: " should-trim ",
		LastError:    " transient-error ",
		Email:        " next@example.com ",
	}

	if err := store.UpdateProviderCredentialState(ctx, "chatgpt-provider", refreshed, authState); err != nil {
		t.Fatalf("UpdateProviderCredentialState failed: %v", err)
	}
	if refreshed.Version != current.Credential.Version+1 {
		t.Fatalf("credential version = %d, want %d", refreshed.Version, current.Credential.Version+1)
	}

	stored, err := store.GetProvider(ctx, "chatgpt-provider")
	if err != nil {
		t.Fatalf("GetProvider stored failed: %v", err)
	}
	if stored.Credential == nil {
		t.Fatal("stored credential = nil, want persisted credential")
	}
	if stored.Credential.Version != refreshed.Version {
		t.Fatalf("stored version = %d, want %d", stored.Credential.Version, refreshed.Version)
	}
	if stored.Credential.SecretData != refreshed.SecretData {
		t.Fatal("stored credential secret did not update")
	}
	if stored.AuthState == nil {
		t.Fatal("stored auth state = nil, want persisted auth state")
	}
	if stored.AuthState.Status != model.ProviderAuthStatusActive {
		t.Fatalf("stored auth status = %q, want %q", stored.AuthState.Status, model.ProviderAuthStatusActive)
	}
	if stored.AuthState.StatusReason != "should-trim" {
		t.Fatalf("stored auth reason = %q, want should-trim", stored.AuthState.StatusReason)
	}
	if stored.AuthState.LastError != "transient-error" {
		t.Fatalf("stored auth last error = %q, want transient-error", stored.AuthState.LastError)
	}
	if stored.AuthState.Email != "next@example.com" {
		t.Fatalf("stored auth email = %q, want next@example.com", stored.AuthState.Email)
	}

	stale := current.Credential.Clone()
	stale.SecretData = mustMarshalChatGPTCredentialDataWithTokens(
		t,
		"acct-state",
		"access-token-stale",
		"refresh-token-stale",
	)
	err = store.UpdateProviderCredentialState(ctx, "chatgpt-provider", stale, &model.ProviderAuthState{
		Status: model.ProviderAuthStatusActive,
	})
	if !errors.Is(err, ErrCredentialVersionConflict) {
		t.Fatalf("UpdateProviderCredentialState stale = %v, want ErrCredentialVersionConflict", err)
	}

	var conflict *CredentialVersionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("UpdateProviderCredentialState stale error = %v, want CredentialVersionConflictError", err)
	}
	if conflict.ProviderID != "chatgpt-provider" {
		t.Fatalf("conflict.ProviderID = %q, want chatgpt-provider", conflict.ProviderID)
	}
	if conflict.ExpectedVersion != current.Credential.Version {
		t.Fatalf("conflict.ExpectedVersion = %d, want %d", conflict.ExpectedVersion, current.Credential.Version)
	}
	if conflict.CurrentVersion != refreshed.Version {
		t.Fatalf("conflict.CurrentVersion = %d, want %d", conflict.CurrentVersion, refreshed.Version)
	}
}

func TestSQLiteStore_ListRoutingPoliciesByAPIType_LoadsAssociations(t *testing.T) {
	t.Parallel()

	store := setupTestStore(t)
	ctx := context.Background()

	prefixPolicy := model.RoutingPolicy{
		Enabled:         true,
		APIType:         "codex",
		ModelMatchType:  model.RoutingPolicyModelMatchTypePrefix,
		ModelMatchValue: "gpt-",
	}
	if err := store.db.Create(&prefixPolicy).Error; err != nil {
		t.Fatalf("create prefix policy: %v", err)
	}
	if err := store.db.Create(&model.RoutingPolicyGroup{
		RoutingPolicyID: prefixPolicy.ID,
		GroupID:         "group-a",
	}).Error; err != nil {
		t.Fatalf("create prefix group: %v", err)
	}
	if err := store.db.Create(&model.RoutingPolicyVendor{
		RoutingPolicyID: prefixPolicy.ID,
		Vendor:          "openai",
	}).Error; err != nil {
		t.Fatalf("create prefix vendor: %v", err)
	}

	apiOnlyPolicy := model.RoutingPolicy{
		Enabled: true,
		APIType: "codex",
	}
	if err := store.db.Create(&apiOnlyPolicy).Error; err != nil {
		t.Fatalf("create api-only policy: %v", err)
	}
	if err := store.db.Create(&model.RoutingPolicyVendor{
		RoutingPolicyID: apiOnlyPolicy.ID,
		Vendor:          "anthropic",
	}).Error; err != nil {
		t.Fatalf("create api-only vendor: %v", err)
	}

	otherPolicy := model.RoutingPolicy{
		Enabled: true,
		APIType: "chat_completions",
	}
	if err := store.db.Create(&otherPolicy).Error; err != nil {
		t.Fatalf("create other policy: %v", err)
	}

	policies, err := store.ListRoutingPoliciesByAPIType(ctx, "codex")
	if err != nil {
		t.Fatalf("ListRoutingPoliciesByAPIType failed: %v", err)
	}
	if len(policies) != 2 {
		t.Fatalf("policy count = %d, want 2", len(policies))
	}
	if policies[0].ID != prefixPolicy.ID || policies[1].ID != apiOnlyPolicy.ID {
		t.Fatalf("policy order = [%d %d], want [%d %d]", policies[0].ID, policies[1].ID, prefixPolicy.ID, apiOnlyPolicy.ID)
	}
	if len(policies[0].Groups) != 1 || policies[0].Groups[0].GroupID != "group-a" {
		t.Fatalf("prefix policy groups = %+v, want group-a", policies[0].Groups)
	}
	if len(policies[0].Vendors) != 1 || policies[0].Vendors[0].Vendor != "openai" {
		t.Fatalf("prefix policy vendors = %+v, want openai", policies[0].Vendors)
	}
	if len(policies[1].Vendors) != 1 || policies[1].Vendors[0].Vendor != "anthropic" {
		t.Fatalf("api-only policy vendors = %+v, want anthropic", policies[1].Vendors)
	}
}

func TestStoreConflictErrors(t *testing.T) {
	t.Parallel()

	bindingErr := &CredentialBindingConflictError{
		AccountID:  "acct-1",
		ProviderID: "provider-1",
	}
	if !errors.Is(bindingErr, ErrCredentialBindingConflict) {
		t.Fatal("CredentialBindingConflictError should match ErrCredentialBindingConflict")
	}
	if bindingErr.Error() == "" {
		t.Fatal("CredentialBindingConflictError.Error() = empty, want message")
	}

	versionErr := &CredentialVersionConflictError{
		ProviderID:      "provider-1",
		ExpectedVersion: 1,
		CurrentVersion:  2,
	}
	if !errors.Is(versionErr, ErrCredentialVersionConflict) {
		t.Fatal("CredentialVersionConflictError should match ErrCredentialVersionConflict")
	}
	if versionErr.Error() == "" {
		t.Fatal("CredentialVersionConflictError.Error() = empty, want message")
	}
}

func TestResolveProviderCredentialRecord(t *testing.T) {
	t.Parallel()

	binding := "acct-current"
	current := &model.ProviderCredential{
		ProviderID:       "provider-1",
		SecretData:       `{"access_token":"current","refresh_token":"refresh","account_id":"acct-current"}`,
		BindingAccountID: &binding,
		Version:          4,
	}

	t.Run("non chatgpt returns nil", func(t *testing.T) {
		t.Parallel()
		if got := resolveProviderCredentialRecord(
			"provider-1",
			model.ProviderCredentialTypeAPIKey,
			"",
			&model.ProviderCredential{SecretData: "ignored"},
			current,
		); got != nil {
			t.Fatalf("resolveProviderCredentialRecord() = %+v, want nil", got)
		}
	})

	t.Run("empty explicit secret clears credential", func(t *testing.T) {
		t.Parallel()
		got := resolveProviderCredentialRecord(
			"provider-1",
			model.ProviderCredentialTypeChatGPT,
			"",
			&model.ProviderCredential{SecretData: "   "},
			current,
		)
		if got != nil {
			t.Fatalf("resolveProviderCredentialRecord() = %+v, want nil", got)
		}
	})

	t.Run("matching explicit credential keeps current version", func(t *testing.T) {
		t.Parallel()
		got := resolveProviderCredentialRecord(
			"provider-1",
			model.ProviderCredentialTypeChatGPT,
			"",
			&model.ProviderCredential{
				SecretData:       current.SecretData,
				BindingAccountID: &binding,
			},
			current,
		)
		if got == nil {
			t.Fatal("resolveProviderCredentialRecord() = nil, want credential")
		}
		if got.Version != current.Version {
			t.Fatalf("Version = %d, want %d", got.Version, current.Version)
		}
	})

	t.Run("changed explicit credential bumps version", func(t *testing.T) {
		t.Parallel()
		got := resolveProviderCredentialRecord(
			"provider-1",
			model.ProviderCredentialTypeChatGPT,
			"",
			&model.ProviderCredential{
				SecretData:       `{"access_token":"next","refresh_token":"refresh","account_id":"acct-current"}`,
				BindingAccountID: &binding,
			},
			current,
		)
		if got == nil {
			t.Fatal("resolveProviderCredentialRecord() = nil, want credential")
		}
		if got.Version != current.Version+1 {
			t.Fatalf("Version = %d, want %d", got.Version, current.Version+1)
		}
	})

	t.Run("explicit version is preserved", func(t *testing.T) {
		t.Parallel()
		got := resolveProviderCredentialRecord(
			"provider-1",
			model.ProviderCredentialTypeChatGPT,
			"",
			&model.ProviderCredential{
				SecretData:       `{"access_token":"explicit","refresh_token":"refresh","account_id":"acct-current"}`,
				BindingAccountID: &binding,
				Version:          9,
			},
			current,
		)
		if got == nil {
			t.Fatal("resolveProviderCredentialRecord() = nil, want credential")
		}
		if got.Version != 9 {
			t.Fatalf("Version = %d, want 9", got.Version)
		}
	})

	t.Run("legacy credential reuses current when equal", func(t *testing.T) {
		t.Parallel()
		got := resolveProviderCredentialRecord(
			"provider-1",
			model.ProviderCredentialTypeChatGPT,
			current.SecretData,
			nil,
			current,
		)
		if got == nil {
			t.Fatal("resolveProviderCredentialRecord() = nil, want credential")
		}
		if got.Version != current.Version {
			t.Fatalf("Version = %d, want %d", got.Version, current.Version)
		}
	})
}

func TestResolveProviderAuthStateRecord(t *testing.T) {
	t.Parallel()

	currentCredential := &model.ProviderCredential{
		ProviderID: "provider-1",
		SecretData: mustMarshalChatGPTCredentialDataWithTokens(
			t,
			"acct-1",
			"access-current",
			"refresh-current",
		),
		Version: 1,
	}
	newCredential := &model.ProviderCredential{
		ProviderID: "provider-1",
		SecretData: mustMarshalChatGPTCredentialDataWithTokens(
			t,
			"acct-1",
			"access-next",
			"refresh-next",
		),
		Version: 2,
	}

	t.Run("explicit auth state is normalized", func(t *testing.T) {
		t.Parallel()
		got := resolveProviderAuthStateRecord(
			"provider-1",
			model.ProviderCredentialTypeAPIKey,
			&model.ProviderAuthState{
				Status:           model.ProviderAuthStatusReauthRequired,
				StatusReason:     " invalid_grant ",
				RefreshFailCount: -1,
			},
			nil,
			nil,
			nil,
		)
		if got.Status != model.ProviderAuthStatusReauthRequired {
			t.Fatalf("Status = %q, want %q", got.Status, model.ProviderAuthStatusReauthRequired)
		}
		if got.StatusReason != "invalid_grant" {
			t.Fatalf("StatusReason = %q, want invalid_grant", got.StatusReason)
		}
		if got.RefreshFailCount != 0 {
			t.Fatalf("RefreshFailCount = %d, want 0", got.RefreshFailCount)
		}
	})

	t.Run("chatgpt preserves reauth terminal state across credential refresh", func(t *testing.T) {
		t.Parallel()
		refreshFailure := model.ProviderAuthState{
			Status:           model.ProviderAuthStatusReauthRequired,
			StatusReason:     "invalid_grant",
			LastError:        "refresh_token_reused",
			RefreshFailCount: 3,
		}
		got := resolveProviderAuthStateRecord(
			"provider-1",
			model.ProviderCredentialTypeChatGPT,
			nil,
			&refreshFailure,
			newCredential,
			currentCredential,
		)
		if got.Status != model.ProviderAuthStatusReauthRequired {
			t.Fatalf("Status = %q, want %q", got.Status, model.ProviderAuthStatusReauthRequired)
		}
		if got.StatusReason != "invalid_grant" {
			t.Fatalf("StatusReason = %q, want invalid_grant", got.StatusReason)
		}
		if got.LastError != "refresh_token_reused" {
			t.Fatalf("LastError = %q, want refresh_token_reused", got.LastError)
		}
		if got.RefreshFailCount != 3 {
			t.Fatalf("RefreshFailCount = %d, want 3", got.RefreshFailCount)
		}
	})

	t.Run("matching credentials keep current auth snapshot", func(t *testing.T) {
		t.Parallel()
		current := &model.ProviderAuthState{
			Status:       model.ProviderAuthStatusActive,
			StatusReason: "unchanged",
			LastError:    "still-present",
		}
		got := resolveProviderAuthStateRecord(
			"provider-1",
			model.ProviderCredentialTypeChatGPT,
			nil,
			current,
			currentCredential,
			currentCredential,
		)
		if got.StatusReason != "unchanged" {
			t.Fatalf("StatusReason = %q, want unchanged", got.StatusReason)
		}
		if got.LastError != "still-present" {
			t.Fatalf("LastError = %q, want still-present", got.LastError)
		}
	})
}

func TestPersistProviderSupplementalState_CreateAndDelete(t *testing.T) {
	t.Parallel()

	store := setupTestStore(t)
	ctx := context.Background()

	if err := store.CreateProvider(ctx, &model.Provider{
		ID:             "provider-supplemental",
		Name:           "Supplemental",
		AuthMode:       "bearer",
		CredentialType: model.ProviderCredentialTypeChatGPT,
		Credential:     testProviderCredential("provider-supplemental", mustMarshalChatGPTCredentialData(t, "acct-supplemental")),
		Enabled:        true,
	}); err != nil {
		t.Fatalf("CreateProvider failed: %v", err)
	}

	binding := "acct-supplemental"
	err := store.db.Transaction(func(tx *gorm.DB) error {
		return persistProviderSupplementalState(tx, "provider-supplemental", providerSupplementalState{
			credential: &model.ProviderCredential{
				ProviderID:       "provider-supplemental",
				SecretData:       mustMarshalChatGPTCredentialDataWithTokens(t, "acct-supplemental", "access-next", "refresh-next"),
				BindingAccountID: &binding,
				Version:          3,
			},
			authState: &model.ProviderAuthState{
				ProviderID:   "provider-supplemental",
				Status:       model.ProviderAuthStatusActive,
				StatusReason: "synced",
				LastError:    "",
			},
		})
	})
	if err != nil {
		t.Fatalf("persistProviderSupplementalState upsert failed: %v", err)
	}

	stored, err := store.GetProvider(ctx, "provider-supplemental")
	if err != nil {
		t.Fatalf("GetProvider failed: %v", err)
	}
	if stored.Credential == nil || stored.Credential.Version != 3 {
		t.Fatalf("stored credential = %+v, want version 3", stored.Credential)
	}
	if stored.AuthState == nil || stored.AuthState.StatusReason != "synced" {
		t.Fatalf("stored auth state = %+v, want synced reason", stored.AuthState)
	}

	err = store.db.Transaction(func(tx *gorm.DB) error {
		return persistProviderSupplementalState(tx, "provider-supplemental", providerSupplementalState{})
	})
	if err != nil {
		t.Fatalf("persistProviderSupplementalState delete failed: %v", err)
	}

	afterDelete, err := store.GetProvider(ctx, "provider-supplemental")
	if err != nil {
		t.Fatalf("GetProvider after delete failed: %v", err)
	}
	if afterDelete.Credential != nil {
		t.Fatalf("Credential after delete = %+v, want nil", afterDelete.Credential)
	}
	if afterDelete.AuthState == nil {
		t.Fatal("AuthState after delete = nil, want derived default auth state")
	}
}
