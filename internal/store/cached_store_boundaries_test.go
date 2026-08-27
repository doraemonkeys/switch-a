package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
)

type initDefaultFailureStore struct {
	internal.Store
	err error
}

func (s *initDefaultFailureStore) InitDefaultConfig(context.Context) error {
	return s.err
}

func TestNewCachedStoreRejectsMissingStoreAndAppliesSafeDefaults(t *testing.T) {
	t.Run("missing store panics", func(t *testing.T) {
		defer func() {
			if recovered := recover(); recovered == nil {
				t.Fatal("NewCachedStore(nil store) did not panic")
			}
		}()
		NewCachedStore(CachedStoreConfig{})
	})

	t.Run("non-positive TTL and nil clock use defaults", func(t *testing.T) {
		base := setupTestStore(t)
		cached := NewCachedStore(CachedStoreConfig{Store: base, CacheTTL: -time.Second})
		if cached.cacheTTL != DefaultConfigCacheTTL {
			t.Fatalf("cache TTL = %s, want %s", cached.cacheTTL, DefaultConfigCacheTTL)
		}
		if cached.clock == nil {
			t.Fatal("default cache clock is nil")
		}
	})
}

func TestCachedStoreInitDefaultConfigInvalidatesOnlyAfterSuccess(t *testing.T) {
	t.Run("success invalidates cached values", func(t *testing.T) {
		cached, underlying, _ := setupCachedStoreTest(t)
		ctx := context.Background()
		if _, err := cached.GetConfig(ctx, "key1"); err != nil {
			t.Fatal(err)
		}
		underlying.configs["key1"] = "changed"
		if err := cached.InitDefaultConfig(ctx); err != nil {
			t.Fatalf("InitDefaultConfig() error = %v", err)
		}
		value, err := cached.GetConfig(ctx, "key1")
		if err != nil || value != "changed" {
			t.Fatalf("GetConfig() after initialization = (%q, %v)", value, err)
		}
	})

	t.Run("failure preserves cached values", func(t *testing.T) {
		base := setupTestStore(t)
		wantErr := errors.New("initialize defaults failed")
		wrapped := &initDefaultFailureStore{Store: base, err: wantErr}
		cached := NewCachedStore(CachedStoreConfig{Store: wrapped})
		cached.cache["key"] = configEntry{
			value:     "cached",
			expiresAt: time.Now().Add(time.Hour),
		}
		if err := cached.InitDefaultConfig(context.Background()); !errors.Is(err, wantErr) {
			t.Fatalf("InitDefaultConfig() error = %v, want %v", err, wantErr)
		}
		if _, ok := cached.cache["key"]; !ok {
			t.Fatal("failed initialization invalidated a valid cache entry")
		}
	})
}

func TestCachedStoreInternalErrorRuleRepositoryCapability(t *testing.T) {
	base := setupTestStore(t)
	supported := NewCachedStore(CachedStoreConfig{Store: base})
	if got := supported.InternalErrorRuleRepository(); got != base.InternalErrorRuleRepository() {
		t.Fatalf("supported repository = %p, want %p", got, base.InternalErrorRuleRepository())
	}

	unsupported := NewCachedStore(CachedStoreConfig{
		Store: newConfigOnlyStoreWithoutImport(base),
	})
	if got := unsupported.InternalErrorRuleRepository(); got != nil {
		t.Fatalf("unsupported repository = %p, want nil", got)
	}
}
