package migration

import (
	"reflect"
	"testing"
)

func TestMigrateStickyClientScopePreservesIdentifiedBindings(t *testing.T) {
	for _, hasScope := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "column-with-old-primary-key"}[hasScope], func(t *testing.T) {
			db := openMigrationSQLiteDB(t, "sticky.db")
			extraColumn := ""
			if hasScope {
				extraColumn = ", client_scope text"
			}
			if err := db.Exec(`CREATE TABLE sticky_entries (ip text, user text, api_type text, model text,
				provider_id text NOT NULL, expires_at datetime NOT NULL, updated_at datetime NOT NULL` + extraColumn + `,
				PRIMARY KEY (ip, user, api_type, model))`).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Exec(`INSERT INTO sticky_entries (ip,user,api_type,model,provider_id,expires_at,updated_at)
				VALUES ('ip','user','chat','model','chat-provider','2099-01-01','2026-01-01'),
				('ip','user','codex','model','old-codex-provider','2099-01-01','2026-01-01')`).Error; err != nil {
				t.Fatal(err)
			}
			if hasScope {
				if err := db.Exec(`INSERT INTO sticky_entries VALUES
					('other-ip','user','codex','model','scoped-provider','2099-01-01','2026-01-01','scope-a')`).Error; err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err := MigrateStickyClientScope(db); err != nil {
					t.Fatal(err)
				}
			}
			var primaryKey []string
			if err := db.Raw("SELECT name FROM pragma_table_info('sticky_entries') WHERE pk > 0 ORDER BY pk").Scan(&primaryKey).Error; err != nil {
				t.Fatal(err)
			}
			want := []string{"ip", "user", "api_type", "model", "client_scope"}
			if !reflect.DeepEqual(primaryKey, want) {
				t.Fatalf("primary key %v, want %v", primaryKey, want)
			}
			var rows []struct{ APIType, ProviderID, ClientScope string }
			if err := db.Table("sticky_entries").Order("api_type").Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			wantCount := 1
			if hasScope {
				wantCount++
			}
			if len(rows) != wantCount || rows[0].ProviderID != "chat-provider" || rows[0].ClientScope != "" {
				t.Fatalf("non-Codex affinity was not preserved: %+v", rows)
			}
			if hasScope && (rows[1].ProviderID != "scoped-provider" || rows[1].ClientScope != "scope-a") {
				t.Fatalf("scoped affinity was not preserved: %+v", rows)
			}
		})
	}
}

func TestMigrateStickyClientScopeFreshDatabaseAndErrors(t *testing.T) {
	db := openMigrationSQLiteDB(t, "sticky.db")
	if err := MigrateStickyClientScope(db); err != nil {
		t.Fatal(err)
	}
	// Missing legacy columns force copy failure after CREATE, proving the old
	// table remains intact and no partial replacement survives rollback.
	if err := db.Exec("CREATE TABLE sticky_entries (ip text PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateStickyClientScope(db); err == nil {
		t.Fatal("expected invalid legacy schema failure")
	}
	if !db.Migrator().HasTable("sticky_entries") || db.Migrator().HasTable("sticky_entries_scoped") {
		t.Fatal("failed rebuild did not roll back atomically")
	}
	if err := db.Exec("CREATE TABLE sticky_entries_scoped (ip text)").Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateStickyClientScope(db); err == nil {
		t.Fatal("expected replacement-table collision failure")
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	if err := MigrateStickyClientScope(db); err == nil {
		t.Fatal("expected closed database failure")
	}
}
