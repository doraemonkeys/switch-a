package migration

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func transportMigrationDB(t *testing.T, hasBaseURL bool) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "legacy.db")+"?_pragma=foreign_keys(1)"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = conn.Close() })
	baseColumn, baseValue := "", ""
	if hasBaseURL {
		baseColumn = ", base_url TEXT NOT NULL"
		baseValue = ",'https://example.test/v1'"
	}
	statements := []string{
		"CREATE TABLE providers (id TEXT PRIMARY KEY)",
		"CREATE TABLE credential_sessions (id TEXT PRIMARY KEY)",
		"CREATE TABLE provider_api_types (provider_id TEXT,api_type TEXT" + baseColumn + ",PRIMARY KEY(provider_id,api_type))",
		"CREATE TABLE route_target_credentials (route_target_id TEXT,api_type TEXT,session_id TEXT,created_at DATETIME,updated_at DATETIME,PRIMARY KEY(route_target_id,api_type),FOREIGN KEY(route_target_id,api_type) REFERENCES provider_api_types(provider_id,api_type) ON DELETE CASCADE,FOREIGN KEY(session_id) REFERENCES credential_sessions(id))",
		"INSERT INTO providers VALUES ('p')",
		"INSERT INTO credential_sessions VALUES ('secret-session')",
		"INSERT INTO provider_api_types VALUES ('p','codex'" + baseValue + "),('p','claude'" + baseValue + ")",
		"INSERT INTO route_target_credentials VALUES ('p','codex','secret-session','2026-01-02','2026-02-03'),('p','claude','secret-session','2026-01-02','2026-02-03')",
	}
	for _, sql := range statements {
		if err := db.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestProviderTransportsPreserveHistoricalCredentialsAndConstraints(t *testing.T) {
	for _, hasBaseURL := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy-root-url", true: "route-url"}[hasBaseURL], func(t *testing.T) {
			db := transportMigrationDB(t, hasBaseURL)
			for range 2 {
				if err := MigrateProviderTransports(db); err != nil {
					t.Fatal(err)
				}
			}
			var routes []struct{ APIType, Transport, BaseURL string }
			if err := db.Table("provider_api_types").Order("api_type,transport").Find(&routes).Error; err != nil {
				t.Fatal(err)
			}
			if len(routes) != 3 || routes[0].Transport != "http" || routes[1].APIType != "codex" || routes[1].Transport != "http" || routes[2].Transport != "websocket" {
				t.Fatalf("historical capabilities: %+v", routes)
			}
			var bindings []struct {
				APIType, Transport, SessionID string
				CreatedAt, UpdatedAt          time.Time
			}
			if err := db.Table("route_target_credentials").Order("api_type,transport").Find(&bindings).Error; err != nil {
				t.Fatal(err)
			}
			if len(bindings) != 3 {
				t.Fatal(bindings)
			}
			for _, b := range bindings {
				if b.SessionID != "secret-session" || b.CreatedAt.Year() != 2026 || b.UpdatedAt.Month() != time.February {
					t.Fatalf("binding changed: %+v", b)
				}
			}
			if hasBaseURL && routes[2].BaseURL != "https://example.test/v1" {
				t.Fatal(routes)
			}
			// An HTTP edit must not alter the WebSocket reference.
			if err := db.Exec("INSERT INTO credential_sessions VALUES ('http-session')").Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Exec("UPDATE route_target_credentials SET session_id='http-session' WHERE api_type='codex' AND transport='http'").Error; err != nil {
				t.Fatal(err)
			}
			var session string
			if err := db.Table("route_target_credentials").Select("session_id").Where("api_type='codex' AND transport='websocket'").Scan(&session).Error; err != nil || session != "secret-session" {
				t.Fatal(session, err)
			}
			if err := db.Exec("DELETE FROM credential_sessions WHERE id='secret-session'").Error; err == nil {
				t.Fatal("bound session deletion accepted")
			}
			if err := db.Exec("DELETE FROM provider_api_types WHERE api_type='codex' AND transport='http'").Error; err != nil {
				t.Fatal(err)
			}
			var count int64
			if err := db.Table("route_target_credentials").Where("api_type='codex'").Count(&count).Error; err != nil || count != 1 {
				t.Fatal(count, err)
			}
		})
	}
}

func TestProviderTransportMigrationRollsBack(t *testing.T) {
	db := transportMigrationDB(t, true)
	if err := db.Exec("CREATE TABLE provider_api_types_transport (occupied TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateProviderTransports(db); err == nil {
		t.Fatal("expected migration conflict")
	}
	if db.Migrator().HasColumn("provider_api_types", "transport") {
		t.Fatal("partial schema published")
	}
	var count int64
	if err := db.Table("route_target_credentials").Count(&count).Error; err != nil || count != 2 {
		t.Fatal(count, err)
	}
}

func TestStickyTransportMigrationPreservesIndependentWinners(t *testing.T) {
	db := transportMigrationDB(t, true)
	if err := MigrateStickyTransports(db); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		"CREATE TABLE sticky_entries (ip TEXT,user TEXT,api_type TEXT,model TEXT,client_scope TEXT,provider_id TEXT,expires_at DATETIME,updated_at DATETIME,PRIMARY KEY(ip,user,api_type,model,client_scope))",
		"INSERT INTO sticky_entries VALUES ('ip','user','codex','model','client','p','2099-01-01','2026-01-01'),('ip','user','claude','model','','q','2099-01-01','2026-01-01')",
	} {
		if err := db.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := MigrateStickyTransports(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE sticky_entries SET provider_id='ws-provider' WHERE transport='websocket'").Error; err != nil {
		t.Fatal(err)
	}
	// Reopening must not invoke the older client-scope migration destructively.
	if err := MigrateStickyClientScope(db); err != nil {
		t.Fatal(err)
	}
	if err := MigrateStickyTransports(db); err != nil {
		t.Fatal(err)
	}
	var rows []struct{ APIType, Transport, ProviderID string }
	if err := db.Table("sticky_entries").Order("api_type,transport").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[1].ProviderID != "p" || rows[2].ProviderID != "ws-provider" {
		t.Fatal(rows)
	}
	if err := db.Exec("DROP TABLE sticky_entries").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE sticky_entries (invalid TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateStickyTransports(db); err == nil {
		t.Fatal("malformed legacy schema accepted")
	}
}
