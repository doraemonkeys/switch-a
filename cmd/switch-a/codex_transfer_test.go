package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/doraemonkeys/switch-a/internal"
	codexidentity "github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/store"
	"go.uber.org/zap"
)

func TestBootstrapRestoresPortableHMACBeforeInventoryValidation(t *testing.T) {
	ctx := context.Background()
	source, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "source.db"), internal.RealClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	targetPath := filepath.Join(t.TempDir(), "target.db")
	target, err := store.NewSQLiteStore(targetPath, internal.RealClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sourceFiles, targetFiles := &applicationMemoryKeyringStore{}, &applicationMemoryKeyringStore{}
	sourceSecurity, err := bootstrapApplicationCodexSecurity(ctx, "source", filepath.Join(t.TempDir(), "source-keys.json"), source, sourceFiles, applicationRandom(11), zap.NewNop(), nil)
	if err != nil {
		t.Fatal(err)
	}
	digester, err := codexidentity.NewDigester(sourceSecurity.keyring)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := source.ClientIdentityResolver(digester)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := resolver.Resolve(ctx, []byte("portable-client"))
	if err != nil {
		t.Fatal(err)
	}
	state, err := source.ExportCodexState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	targetKeyPath := filepath.Join(t.TempDir(), "target-keys.json")
	if _, err := bootstrapApplicationCodexSecurity(ctx, "target", targetKeyPath, target, targetFiles, applicationRandom(71), zap.NewNop(), nil); err != nil {
		t.Fatal(err)
	}
	if err := target.ApplyConfigImport(ctx, &store.ConfigImportBundle{CodexState: state, RoutingPolicyMode: store.ConfigImportRoutingPolicyModePreserve}); err != nil {
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := store.NewSQLiteStore(targetPath, internal.RealClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	restored, err := bootstrapApplicationCodexSecurity(ctx, "restart", targetKeyPath, restarted, targetFiles, applicationRandom(81), zap.NewNop(), nil)
	if err != nil {
		t.Fatal(err)
	}
	restartedDigester, err := codexidentity.NewDigester(restored.keyring)
	if err != nil {
		t.Fatal(err)
	}
	restartedResolver, err := restarted.ClientIdentityResolver(restartedDigester)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := restartedResolver.Resolve(ctx, []byte("portable-client"))
	if err != nil {
		t.Fatal(err)
	}
	if actual.ID != identity.ID || !actual.Primary.Equal(identity.Primary) {
		t.Fatal("restored startup lost client continuity")
	}
}
