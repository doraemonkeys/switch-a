package sqlite

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
)

func TestSchemaMigrationValidationAndFutureVersion(t *testing.T) {
	if err := Migrate(context.Background(), nil); err == nil {
		t.Fatal("Migrate accepted nil database")
	}
	if err := ValidateSchema(context.Background(), nil); err == nil {
		t.Fatal("ValidateSchema accepted nil database")
	}
	db, closeDB := openTestDB(t)
	defer closeDB()
	if err := ValidateSchema(context.Background(), db); err == nil {
		t.Fatal("ValidateSchema accepted missing tables")
	}
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
	if err := ValidateSchema(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if CurrentSchemaVersion != 2 {
		t.Fatalf("schema version = %d", CurrentSchemaVersion)
	}

	if err := db.Exec("UPDATE "+schemaMetaTable+" SET version = ? WHERE id = ?", 3, schemaRowID).Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(context.Background(), db); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("future migration error = %v", err)
	}
	if err := ValidateSchema(context.Background(), db); err == nil {
		t.Fatal("ValidateSchema accepted future version")
	}

	if err := db.Exec("PRAGMA ignore_check_constraints=ON").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE "+schemaMetaTable+" SET version = 1 WHERE id = ?", schemaRowID).Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(context.Background(), db); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("old migration error = %v", err)
	}

	if err := db.Exec("DELETE FROM " + schemaMetaTable).Error; err != nil {
		t.Fatal(err)
	}
	if err := ValidateSchema(context.Background(), db); err == nil || !strings.Contains(err.Error(), "version row") {
		t.Fatalf("missing version error = %v", err)
	}
	if err := Migrate(context.Background(), db); err == nil || !strings.Contains(err.Error(), "version row") {
		t.Fatalf("migration repaired missing version row: %v", err)
	}
	var versionRows int
	if err := db.Raw("SELECT COUNT(*) FROM " + schemaMetaTable).Scan(&versionRows).Error; err != nil || versionRows != 0 {
		t.Fatalf("damaged schema was mutated: rows=%d error=%v", versionRows, err)
	}

	partial, closePartial := openTestDB(t)
	defer closePartial()
	if err := Migrate(context.Background(), partial); err != nil {
		t.Fatal(err)
	}
	if err := partial.Exec("DROP TABLE " + bindingsTable).Error; err != nil {
		t.Fatal(err)
	}
	if err := ValidateSchema(context.Background(), partial); err == nil || !strings.Contains(err.Error(), "partial") {
		t.Fatalf("missing bindings error = %v", err)
	}
	if err := Migrate(context.Background(), partial); err == nil || !strings.Contains(err.Error(), "partial") {
		t.Fatalf("migration repaired missing binding table: %v", err)
	}
}

func TestCodecRoundTripsAccountKeyedAndTombstone(t *testing.T) {
	for name, binding := range map[string]codexcontinuity.Binding{
		"account":   testBinding(t, accountScope(t, "account", "codex"), codexcontinuity.LifecyclePending),
		"no vendor": testBinding(t, blankVendorScope(t), codexcontinuity.LifecyclePending),
		"keyed":     testBinding(t, keyedScope(t, 80, "h3"), codexcontinuity.LifecycleCommitted),
		"tombstone": testBinding(t, accountScope(t, "account", "codex"), codexcontinuity.LifecycleTombstone),
	} {
		t.Run(name, func(t *testing.T) {
			row, err := encodeBinding(binding)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := decodeBinding(row)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded, binding) {
				t.Fatalf("round trip\n got: %#v\nwant: %#v", decoded, binding)
			}
		})
	}
}

func TestCodecRejectsCorruptRows(t *testing.T) {
	binding := testBinding(t, accountScope(t, "account", "codex"), codexcontinuity.LifecyclePending)
	valid, err := encodeBinding(binding)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*bindingRow)
	}{
		{name: "kind", mutate: func(row *bindingRow) { row.Kind = "future" }},
		{name: "opaque length", mutate: func(row *bindingRow) { row.OpaqueDigest = nil }},
		{name: "opaque version", mutate: func(row *bindingRow) { row.OpaqueKeyVersion = "!" }},
		{name: "client length", mutate: func(row *bindingRow) { row.ClientDigest = nil }},
		{name: "client version", mutate: func(row *bindingRow) { row.ClientKeyVersion = "!" }},
		{name: "origin", mutate: func(row *bindingRow) { row.ProtocolOrigin = "bad" }},
		{name: "account missing", mutate: func(row *bindingRow) { row.ProtocolSubjectAccount = nil }},
		{name: "keyed version missing", mutate: func(row *bindingRow) {
			row.ProtocolSubjectKind = string(codexidentity.CredentialSubjectKeyedDigest)
			row.ProtocolSubjectAccount = nil
			row.ProtocolSubjectKeyVersion = nil
		}},
		{name: "keyed digest length", mutate: func(row *bindingRow) {
			version := "h1"
			row.ProtocolSubjectKind = string(codexidentity.CredentialSubjectKeyedDigest)
			row.ProtocolSubjectAccount = nil
			row.ProtocolSubjectKeyVersion = &version
			row.ProtocolSubjectDigest = nil
		}},
		{name: "subject kind", mutate: func(row *bindingRow) { row.ProtocolSubjectKind = "future" }},
		{name: "api type", mutate: func(row *bindingRow) { row.ProtocolAPIType = "" }},
		{name: "lifecycle", mutate: func(row *bindingRow) { row.Lifecycle = "future" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			row := cloneRow(valid)
			test.mutate(&row)
			if _, err := decodeBinding(row); err == nil {
				t.Fatal("decodeBinding succeeded")
			}
		})
	}
}

func blankVendorScope(t *testing.T) codexidentity.ProtocolScope {
	t.Helper()
	origin, err := codexidentity.ParseOrigin("https://api.example.com")
	if err != nil {
		t.Fatal(err)
	}
	subject, err := codexidentity.NewAccountCredentialSubject("account-without-vendor")
	if err != nil {
		t.Fatal(err)
	}
	authority, err := codexidentity.NewUpstreamAuthority("", origin, subject)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := codexidentity.NewProtocolScope(authority, "codex")
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func TestReconcileRejectsMalformedTombstoneAndMissingUpdates(t *testing.T) {
	db, closeDB := openMigratedDB(t)
	defer closeDB()
	limits := codexcontinuity.Limits{
		PendingTTL: time.Minute, CommittedTTL: time.Minute, TombstoneTTL: time.Minute, MaxBindings: 1,
	}
	binding := testBinding(t, accountScope(t, "account", "codex"), codexcontinuity.LifecycleTombstone)
	row, err := encodeBinding(binding)
	if err != nil {
		t.Fatal(err)
	}
	row.TombstoneUntilNS = nil
	if _, _, err := reconcileLifecycle(db, row, repositoryNow, limits); err == nil {
		t.Fatal("reconcile accepted tombstone without expiry")
	}
	if _, err := reconcileForCleanup(db, row, repositoryNow, limits); err == nil {
		t.Fatal("cleanup accepted tombstone without expiry")
	}
	missing := testBinding(t, accountScope(t, "account", "codex"), codexcontinuity.LifecycleCommitted)
	if err := updateLifecycle(db, missing); err == nil {
		t.Fatal("updateLifecycle accepted missing row")
	}
}

func TestSchemaAndHelperDatabaseFailures(t *testing.T) {
	db, closeDB := openMigratedDB(t)
	if err := db.Exec("DROP TABLE " + schemaMetaTable).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := readSchemaVersion(db); err == nil {
		t.Fatal("readSchemaVersion succeeded without metadata table")
	}
	if err := createIndexes(db); err != nil {
		t.Fatalf("idempotent createIndexes: %v", err)
	}
	closeDB()
	if _, err := tableExists(db, bindingsTable); err == nil {
		t.Fatal("tableExists succeeded on closed database")
	}
	if err := ValidateSchema(context.Background(), db); err == nil {
		t.Fatal("ValidateSchema succeeded on closed database")
	}
	if err := Migrate(context.Background(), db); err == nil {
		t.Fatal("Migrate succeeded on closed database")
	}
}

func TestEncodeRejectsUninitializedCredentialSubject(t *testing.T) {
	binding := codexcontinuity.Binding{
		Kind:             codexcontinuity.KindTurnState,
		Digest:           testDigest(t, codexcontinuity.KindTurnState, 110, "h1"),
		Lifecycle:        codexcontinuity.LifecyclePending,
		ClaimOperationID: "invalid-owner",
		CreatedAt:        repositoryNow,
		UpdatedAt:        repositoryNow,
		ExpiresAt:        repositoryNow.Add(time.Hour),
	}
	if _, err := encodeBinding(binding); err == nil {
		t.Fatal("encodeBinding accepted an uninitialized credential subject")
	}
}

func testBinding(
	t *testing.T,
	scope codexidentity.ProtocolScope,
	lifecycle codexcontinuity.Lifecycle,
) codexcontinuity.Binding {
	t.Helper()
	created := repositoryNow
	committed := repositoryNow.Add(time.Second)
	tombstone := repositoryNow.Add(2 * time.Hour)
	binding := codexcontinuity.Binding{
		Kind:             codexcontinuity.KindResponseReference,
		Digest:           testDigest(t, codexcontinuity.KindResponseReference, 100, "h1"),
		Owner:            testOwner(t, 101, "h2", scope, "route"),
		Lifecycle:        lifecycle,
		ClaimOperationID: "operation",
		CreatedAt:        created,
		UpdatedAt:        created,
		ExpiresAt:        repositoryNow.Add(time.Hour),
	}
	switch lifecycle {
	case codexcontinuity.LifecycleCommitted:
		binding.CommittedAt = &committed
	case codexcontinuity.LifecycleTombstone:
		binding.TombstoneUntil = &tombstone
	}
	return binding
}

func cloneRow(source bindingRow) bindingRow {
	clone := source
	clone.OpaqueDigest = append([]byte(nil), source.OpaqueDigest...)
	clone.ClientDigest = append([]byte(nil), source.ClientDigest...)
	clone.ProtocolSubjectDigest = append([]byte(nil), source.ProtocolSubjectDigest...)
	return clone
}
