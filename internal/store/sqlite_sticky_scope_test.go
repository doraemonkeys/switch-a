package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestSQLiteStickyScopeMigrationAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sticky.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE sticky_entries (
		ip text, user text, api_type text, model text,
		provider_id text NOT NULL, expires_at datetime NOT NULL, updated_at datetime NOT NULL,
		PRIMARY KEY(ip,user,api_type,model))`).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, apiType := range []string{"chat", "codex"} {
		if err := db.Exec(`INSERT INTO sticky_entries VALUES ('ip','user',?,'model','legacy',?,?)`,
			apiType, now.Add(time.Hour), now).Error; err != nil {
			t.Fatal(err)
		}
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := NewSQLiteStore(path, internal.RealClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	entries, err := s.LoadStickyEntries(ctx, now)
	if err != nil || len(entries) != 1 || entries[0].Key.APIType != "chat" {
		t.Fatalf("legacy Codex affinity must miss while chat survives: %+v, %v", entries, err)
	}
	keyA := model.StickyKey{IP: "ip", User: "user", APIType: "codex", Model: "model", ClientScope: "scope-a"}
	keyB := keyA
	keyB.ClientScope = "scope-b"
	for key, provider := range map[model.StickyKey]string{keyA: "a", keyB: "b"} {
		if err := s.UpsertStickyEntry(ctx, model.StickyEntry{Key: key, ProviderID: provider, ExpiresAt: now.Add(time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpsertStickyEntry(ctx, model.StickyEntry{Key: keyA, ProviderID: "c", ExpiresAt: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	unscoped := keyA
	unscoped.ClientScope = ""
	if err := s.UpsertStickyEntry(ctx, model.StickyEntry{Key: unscoped, ProviderID: "unscoped", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteStore(path, internal.RealClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	entries, err = s.LoadStickyEntries(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[model.StickyKey]string)
	for _, entry := range entries {
		got[entry.Key] = entry.ProviderID
	}
	if len(got) != 3 || got[keyA] != "c" || got[keyB] != "b" {
		t.Fatalf("scoped upsert/restart lost affinity: %+v", got)
	}
	if err := s.DeleteStickyEntry(ctx, keyA); err != nil {
		t.Fatal(err)
	}
	entries, err = s.LoadStickyEntries(ctx, now)
	if err != nil || len(entries) != 2 {
		t.Fatalf("scoped delete: %+v, %v", entries, err)
	}
	for _, entry := range entries {
		if entry.Key == keyA {
			t.Fatal("deleted scope was restored")
		}
	}
	entries, err = s.LoadStickyEntries(ctx, now.Add(time.Minute))
	if err != nil || len(entries) != 1 || entries[0].Key.APIType != "chat" {
		t.Fatalf("restart extended scoped TTL: %+v, %v", entries, err)
	}
}
