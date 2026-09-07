package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/clientaccess"
	errorrulesqlite "github.com/doraemonkeys/switch-a/internal/errorrule/sqlite"
)

func importClientKeySnapshot(mode clientaccess.Mode, id string) clientaccess.Snapshot {
	now := time.Date(2026, 9, 6, 1, 0, 0, 0, time.UTC)
	return clientaccess.Snapshot{Mode: mode, Keys: []clientaccess.Key{{
		ID: id, Name: id, Key: "secret-" + id, CreatedAt: now, UpdatedAt: now,
	}}}
}

func TestClientAPIKeysConfigImportRollbackAndCache(t *testing.T) {
	ctx := context.Background()
	target := setupTestStore(t)
	initial := importClientKeySnapshot(clientaccess.ModeRestricted, "local")
	if err := target.ClientAPIKeyRepository().Replace(ctx, initial); err != nil {
		t.Fatal(err)
	}
	if err := target.SetConfig(ctx, "probe-key", "before"); err != nil {
		t.Fatal(err)
	}
	cached := NewCachedStore(CachedStoreConfig{Store: target})
	if _, err := cached.GetConfig(ctx, "probe-key"); err != nil {
		t.Fatal(err)
	}
	replacement := importClientKeySnapshot(clientaccess.ModePermissive, "imported")
	bundle := &ConfigImportBundle{
		ClientAPIKeys:     &replacement,
		RoutingPolicyMode: ConfigImportRoutingPolicyModePreserve,
		Settings:          map[string]string{"probe-key": "after"},
		RuleImport: errorrulesqlite.ImportRequest{
			Mode:  errorrulesqlite.ImportModeFull,
			Rules: []errorrulesqlite.ImportedRule{configImportTestRule("11111111-1111-4111-8111-111111111111", "missing-provider")},
		},
	}
	if err := cached.ApplyConfigImport(ctx, bundle); err == nil {
		t.Fatal("expected late rule-reference failure")
	}
	actual, err := cached.ClientAPIKeySnapshot(ctx)
	if err != nil || !reflect.DeepEqual(actual, initial) {
		t.Fatalf("policy/keys after rollback = %+v, %v", actual, err)
	}
	for _, reader := range []interface {
		GetConfig(context.Context, string) (string, error)
	}{target, cached} {
		if value, err := reader.GetConfig(ctx, "probe-key"); err != nil || value != "before" {
			t.Fatalf("setting after rollback = %q, %v", value, err)
		}
	}

	bundle.RuleImport = errorrulesqlite.ImportRequest{Mode: errorrulesqlite.ImportModePreserve}
	if err := cached.PreviewConfigImport(ctx, bundle); err != nil {
		t.Fatal(err)
	}
	actual, err = cached.ClientAPIKeySnapshot(ctx)
	if err != nil || !reflect.DeepEqual(actual, initial) {
		t.Fatalf("preview persisted policy/keys = %+v, %v", actual, err)
	}
	if err := cached.ApplyConfigImport(ctx, bundle); err != nil {
		t.Fatal(err)
	}
	actual, err = cached.ClientAPIKeySnapshot(ctx)
	if err != nil || !reflect.DeepEqual(actual, replacement) {
		t.Fatalf("committed policy/keys = %+v, %v", actual, err)
	}
	if value, err := cached.GetConfig(ctx, "probe-key"); err != nil || value != "after" {
		t.Fatalf("cache after commit = %q, %v", value, err)
	}
}

func TestClientAPIKeysConfigImportAbsentEmptyAndInvalid(t *testing.T) {
	target := setupTestStore(t)
	ctx := context.Background()
	initial := importClientKeySnapshot(clientaccess.ModeRestricted, "local")
	if err := target.ClientAPIKeyRepository().Replace(ctx, initial); err != nil {
		t.Fatal(err)
	}
	bundle := &ConfigImportBundle{RoutingPolicyMode: ConfigImportRoutingPolicyModePreserve}
	if err := target.ApplyConfigImport(ctx, bundle); err != nil {
		t.Fatal(err)
	}
	actual, err := target.ClientAPIKeySnapshot(ctx)
	if err != nil || !reflect.DeepEqual(actual, initial) {
		t.Fatalf("omitted aggregate = %+v, %v", actual, err)
	}

	invalid := importClientKeySnapshot(clientaccess.ModeRestricted, "invalid")
	invalid.Keys = append(invalid.Keys, invalid.Keys[0])
	bundle.ClientAPIKeys = &invalid
	if err := target.ApplyConfigImport(ctx, bundle); !errors.Is(err, clientaccess.ErrValidation) {
		t.Fatalf("invalid aggregate error = %v", err)
	}
	actual, err = target.ClientAPIKeySnapshot(ctx)
	if err != nil || !reflect.DeepEqual(actual, initial) {
		t.Fatalf("invalid aggregate changed state = %+v, %v", actual, err)
	}

	empty := clientaccess.Snapshot{Mode: clientaccess.ModeRestricted, Keys: []clientaccess.Key{}}
	bundle.ClientAPIKeys = &empty
	if err := target.ApplyConfigImport(ctx, bundle); err != nil {
		t.Fatal(err)
	}
	actual, err = target.ClientAPIKeySnapshot(ctx)
	if err != nil || !reflect.DeepEqual(actual, empty) {
		t.Fatalf("empty aggregate = %+v, %v", actual, err)
	}
}

func TestClientAPIKeysCachedSnapshotUnsupported(t *testing.T) {
	cached := NewCachedStore(CachedStoreConfig{Store: &configOnlyStore{}})
	if _, err := cached.ClientAPIKeySnapshot(context.Background()); err == nil {
		t.Fatal("expected unavailable snapshot error")
	}
}
