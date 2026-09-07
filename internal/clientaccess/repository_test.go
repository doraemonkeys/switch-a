package clientaccess

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func testRepository(t *testing.T) (*Repository, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "access.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	return NewRepository(db), db
}

func testStoredKey(id, value string) Key {
	at := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	return Key{ID: id, Name: "Device " + id, Key: value, CreatedAt: at, UpdatedAt: at}
}

func TestRepositoryManagementAndRestart(t *testing.T) {
	ctx := context.Background()
	repo, db := testRepository(t)
	snapshot, err := repo.Snapshot(ctx)
	if err != nil || snapshot.Mode != ModePermissive || snapshot.Keys == nil || len(snapshot.Keys) != 0 {
		t.Fatalf("default %#v %v", snapshot, err)
	}
	first, second := testStoredKey("b", "first"), testStoredKey("a", "second")
	for _, key := range []Key{first, second} {
		if err := repo.CreateKey(ctx, key); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.CreateKey(ctx, first); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate ID %v", err)
	}
	duplicate := first
	duplicate.ID = "other"
	if err := repo.CreateKey(ctx, duplicate); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate value %v", err)
	}
	if err := repo.SetMode(ctx, ModeRestricted); err != nil {
		t.Fatal(err)
	}
	updatedAt := first.UpdatedAt.Add(time.Hour)
	renamed, err := repo.RenameKey(ctx, first.ID, "Renamed", updatedAt)
	if err != nil || renamed.Name != "Renamed" || renamed.Key != first.Key || !renamed.CreatedAt.Equal(first.CreatedAt) || !renamed.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("rename %#v %v", renamed, err)
	}
	// A newly constructed repository has no process-local policy to warm up.
	snapshot, err = NewRepository(db).Snapshot(ctx)
	if err != nil || snapshot.Mode != ModeRestricted || len(snapshot.Keys) != 2 || snapshot.Keys[0].ID != "a" || snapshot.Keys[1] != renamed {
		t.Fatalf("restart %#v %v", snapshot, err)
	}
	if err := repo.DeleteKey(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteKey(ctx, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing %v", err)
	}
	if _, err := repo.RenameKey(ctx, first.ID, "missing", updatedAt); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rename missing %v", err)
	}
	if err := repo.SetMode(ctx, ModePermissive); err != nil {
		t.Fatal(err)
	}
	snapshot, err = repo.Snapshot(ctx)
	if err != nil || snapshot.Mode != ModePermissive || len(snapshot.Keys) != 1 {
		t.Fatalf("delete persisted %#v %v", snapshot, err)
	}
}

func TestRepositoryReplaceOuterRollbackAndValidation(t *testing.T) {
	ctx := context.Background()
	repo, db := testRepository(t)
	original := Snapshot{Mode: ModeRestricted, Keys: []Key{testStoredKey("old", "old-key")}}
	replacement := Snapshot{Mode: ModePermissive, Keys: []Key{testStoredKey("new", "new-key")}}
	if err := repo.Replace(ctx, original); err != nil {
		t.Fatal(err)
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := NewRepository(tx).Replace(ctx, replacement); err != nil {
			return err
		}
		current, err := NewRepository(tx).Snapshot(ctx)
		if err != nil || !reflect.DeepEqual(current, replacement) {
			t.Fatalf("transaction snapshot %#v %v", current, err)
		}
		return errStorage
	})
	if !errors.Is(err, errStorage) {
		t.Fatal(err)
	}
	current, err := repo.Snapshot(ctx)
	if err != nil || !reflect.DeepEqual(current, original) {
		t.Fatalf("outer rollback %#v %v", current, err)
	}
	if err := repo.Replace(ctx, Snapshot{Mode: "bad"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("validate before write %v", err)
	}
	for _, call := range []func() error{
		func() error { return repo.CreateKey(ctx, Key{}) },
		func() error { _, err := repo.RenameKey(ctx, "old", " ", time.Now()); return err },
		func() error { return repo.SetMode(ctx, "bad") },
	} {
		if err := call(); !errors.Is(err, ErrValidation) {
			t.Fatalf("validation %v", err)
		}
	}
	if err := repo.Replace(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	if err := repo.Replace(ctx, Snapshot{Mode: ModeRestricted}); err != nil {
		t.Fatal(err)
	}
	current, err = repo.Snapshot(ctx)
	if err != nil || current.Mode != ModeRestricted || len(current.Keys) != 0 {
		t.Fatalf("empty replacement %#v %v", current, err)
	}
}

func TestRepositoryReplaceFailureRollsBackDeletion(t *testing.T) {
	ctx := context.Background()
	repo, db := testRepository(t)
	original := Snapshot{Mode: ModeRestricted, Keys: []Key{testStoredKey("old", "old-key")}}
	if err := repo.Replace(ctx, original); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TRIGGER reject_client_key_insert BEFORE INSERT ON client_api_keys BEGIN SELECT RAISE(ABORT, 'injected insert failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.Replace(ctx, Snapshot{Mode: ModePermissive, Keys: []Key{testStoredKey("new", "new-key")}}); err == nil {
		t.Fatal("expected failed insert")
	}
	current, err := repo.Snapshot(ctx)
	if err != nil || !reflect.DeepEqual(current, original) {
		t.Fatalf("failed replacement changed access %#v %v", current, err)
	}
	if err := db.Exec("DROP TRIGGER reject_client_key_insert").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TRIGGER reject_client_key_delete BEFORE DELETE ON client_api_keys BEGIN SELECT RAISE(ABORT, 'injected delete failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.Replace(ctx, Snapshot{Mode: ModePermissive}); err == nil {
		t.Fatal("expected failed delete")
	}
	if err := repo.DeleteKey(ctx, "old"); err == nil {
		t.Fatal("expected failed delete")
	}
}

func TestRepositoryStorageFailuresAndCorruptPolicy(t *testing.T) {
	ctx := context.Background()
	t.Run("missing policy table", func(t *testing.T) {
		repo, db := testRepository(t)
		if err := db.Migrator().DropTable(&policyRecord{}); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Snapshot(ctx); err == nil {
			t.Fatal("missing policy silently opened access")
		}
		if err := repo.SetMode(ctx, ModeRestricted); err == nil {
			t.Fatal("mode failure ignored")
		}
	})
	t.Run("missing key table", func(t *testing.T) {
		repo, db := testRepository(t)
		if err := db.Migrator().DropTable(&keyRecord{}); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Snapshot(ctx); err == nil {
			t.Fatal("missing keys silently opened access")
		}
		if err := repo.CreateKey(ctx, testStoredKey("a", "value")); err == nil {
			t.Fatal("create failure ignored")
		}
		if _, err := repo.RenameKey(ctx, "a", "name", time.Now()); err == nil {
			t.Fatal("rename failure ignored")
		}
	})
	t.Run("corrupt mode", func(t *testing.T) {
		repo, db := testRepository(t)
		if err := db.Create(&policyRecord{ID: policySingletonID, Mode: "unknown"}).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Snapshot(ctx); !errors.Is(err, ErrValidation) {
			t.Fatalf("corrupt mode %v", err)
		}
	})
	t.Run("migration failure", func(t *testing.T) {
		_, db := testRepository(t)
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if err := Migrate(db.WithContext(canceled)); err == nil {
			t.Fatal("migration should fail for canceled context")
		}
	})
}

func TestRepositoryAdmissionReadsCoherentCommittedSnapshots(t *testing.T) {
	ctx := context.Background()
	repo, _ := testRepository(t)
	permissive := Snapshot{Mode: ModePermissive, Keys: []Key{testStoredKey("open", "open-key")}}
	restricted := Snapshot{Mode: ModeRestricted, Keys: []Key{testStoredKey("closed", "closed-key")}}
	if err := repo.Replace(ctx, permissive); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 40; i++ {
			next := restricted
			if i%2 == 0 {
				next = permissive
			}
			if err := repo.Replace(ctx, next); err != nil {
				failures <- err
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 80; i++ {
			snapshot, err := repo.Snapshot(ctx)
			if err != nil {
				failures <- err
				return
			}
			if !reflect.DeepEqual(snapshot, permissive) && !reflect.DeepEqual(snapshot, restricted) {
				failures <- fmt.Errorf("torn snapshot: %#v", snapshot)
				return
			}
		}
	}()
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if err := repo.Replace(ctx, restricted); err != nil {
		t.Fatal(err)
	}
	service := NewService(ServiceConfig{Store: repo})
	if err := service.Delete(ctx, "closed"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Snapshot(ctx)
	if err != nil || len(snapshot.Keys) != 0 || snapshot.Mode != ModeRestricted {
		t.Fatalf("delete visible immediately %#v %v", snapshot, err)
	}
}
