package store

import (
	"context"
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
		BaseURL:  "https://api.example.com",
		APIKey:   "secret-key",
		AuthMode: "bearer",
		GroupID:  &groupID,
		Enabled:  true,
		APITypes: []model.ProviderAPIType{
			{ProviderID: "p1", APIType: "claude"},
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
		BaseURL:  "https://api.claude.com",
		APIKey:   "key1",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude"}},
	}
	p2 := &model.Provider{
		ID:       "p2",
		Name:     "Codex Provider",
		BaseURL:  "https://api.codex.com",
		APIKey:   "key2",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "p2", APIType: "codex"}},
	}
	p3 := &model.Provider{
		ID:       "p3",
		Name:     "Disabled Claude",
		BaseURL:  "https://api.disabled.com",
		APIKey:   "key3",
		Enabled:  false,
		APITypes: []model.ProviderAPIType{{ProviderID: "p3", APIType: "claude"}},
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
		BaseURL:  "https://test.com",
		APIKey:   "key",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude"}},
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
		ID:       "p1",
		Name:     "Original",
		BaseURL:  "https://test.com",
		APIKey:   "key",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude"}},
	}
	if err := store.CreateProvider(ctx, p); err != nil {
		t.Fatalf("CreateProvider failed: %v", err)
	}

	// Update provider
	p.Name = "Updated"
	p.APITypes = []model.ProviderAPIType{{ProviderID: "p1", APIType: "codex"}}
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
}

func TestCreateProvider_NewFieldsPersisted(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	provider := &model.Provider{
		ID:      "backoff-test",
		Name:    "Backoff Test",
		BaseURL: "https://api.example.com",
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
		APITypes:       []model.ProviderAPIType{{ProviderID: "backoff-test", APIType: "claude"}},
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
		BaseURL:  "https://api.example.com",
		APIKey:   "key",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "backoff-update", APIType: "claude"}},
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
