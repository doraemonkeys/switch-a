package healthstate

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type configSource struct{}

func (configSource) GetConfig(context.Context, string) (string, error) { return "3", nil }

func testStore(t *testing.T) (*Store, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "health.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.AutoMigrate(&model.HealthState{}); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	return New(db, configSource{}), db
}

func TestRouteHealthIsolationAndRecovery(t *testing.T) {
	root, _ := testStore(t)
	ctx := context.Background()
	ws := root.HealthScope("codex", "websocket")
	http := root.HealthScope("codex", "http")
	otherAPI := root.HealthScope("openai", "http")
	now := time.Now().UTC().Truncate(time.Second)
	until := now.Add(time.Minute)
	for _, repo := range []internal.HealthStateStore{root, http, ws, otherAPI} {
		state, err := repo.GetHealthState(ctx, "p")
		if err != nil || !state.Available || state.FailCount != 0 {
			t.Fatalf("initial health: %+v %v", state, err)
		}
	}
	if value, err := ws.GetConfig(ctx, "circuit_failure"); err != nil || value != "3" {
		t.Fatal(value, err)
	}
	for range 3 {
		if _, err := ws.IncrementFailCount(ctx, "p", now, "handshake"); err != nil {
			t.Fatal(err)
		}
	}
	if err := ws.AutoDisableUntil(ctx, "p", until, "auto: handshake"); err != nil {
		t.Fatal(err)
	}
	if err := ws.AutoDisableUntil(ctx, "p", now.Add(time.Second), "auto: shorter"); err != nil {
		t.Fatal(err)
	}
	state, err := ws.IncrementSuccessCount(ctx, "p", now)
	if err != nil || state.Available || state.FailCount != 3 || state.SuccessCount != 1 || !state.DisabledUntil.Equal(until) || state.DisabledReason != "auto: handshake" {
		t.Fatalf("cooldown lost: %+v %v", state, err)
	}
	for _, repo := range []internal.HealthStateStore{root, http, otherAPI} {
		state, err = repo.IncrementSuccessCount(ctx, "p", now)
		if err != nil || !state.Available || state.FailCount != 0 {
			t.Fatalf("WS failure leaked: %+v %v", state, err)
		}
	}
	if recovered, err := ws.AtomicRecoverIfExpired(ctx, "p", until); err != nil || recovered {
		t.Fatal("recovered at boundary", err)
	}
	if recovered, err := ws.AtomicRecoverIfExpired(ctx, "p", until.Add(time.Second)); err != nil || !recovered {
		t.Fatal("failed recovery", err)
	}
	if recovered, err := ws.AtomicRecoverIfExpired(ctx, "p", until.Add(time.Second)); err != nil || recovered {
		t.Fatal("repeated recovery", err)
	}
	state, err = ws.GetHealthState(ctx, "p")
	if err != nil || !state.Available || state.DisabledUntil != nil || state.DisabledReason != "" {
		t.Fatalf("recovered state: %+v %v", state, err)
	}
}

func TestManualRouteHealthAndResetAfterRestart(t *testing.T) {
	root, db := testStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	repo := root.HealthScope("codex", "websocket")
	if err := repo.UpdateHealthState(ctx, &model.HealthState{ProviderID: "p", Available: false, DisabledReason: "manual: maintenance", FailCount: 4}); err != nil {
		t.Fatal(err)
	}
	if err := repo.AutoDisableUntil(ctx, "p", now.Add(time.Minute), "auto: cooldown"); err != nil {
		t.Fatal(err)
	}
	state, err := repo.IncrementSuccessCount(ctx, "p", now)
	if err != nil || state.Available || state.DisabledUntil != nil || state.DisabledReason != "manual: maintenance" {
		t.Fatalf("manual state overwritten: %+v %v", state, err)
	}
	if recovered, err := repo.AtomicRecoverIfExpired(ctx, "p", now.Add(time.Hour)); err != nil || recovered {
		t.Fatal(recovered, err)
	}
	reopened := New(db, configSource{})
	if err := reopened.ResetRouteAvailability(ctx, "p"); err != nil {
		t.Fatal(err)
	}
	state, err = repo.GetHealthState(ctx, "p")
	if err != nil || !state.Available || state.FailCount != 4 || state.SuccessCount != 1 || state.DisabledReason != "" {
		t.Fatalf("reset state: %+v %v", state, err)
	}
	if err := repo.AutoDisableUntil(ctx, "p", now.Add(time.Hour), "auto: quota"); err != nil {
		t.Fatal(err)
	}
	if err := repo.AutoDisableUntil(ctx, "p", now.Add(2*time.Hour), "auto: extended"); err != nil {
		t.Fatal(err)
	}
	state, err = repo.GetHealthState(ctx, "p")
	if err != nil || state.DisabledReason != "auto: extended" {
		t.Fatal(state, err)
	}
}

func TestRepositoryDatabaseErrors(t *testing.T) {
	root, db := testStore(t)
	conn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, repo := range []internal.HealthStateStore{root, root.HealthScope("codex", "websocket")} {
		_, readErr := repo.GetHealthState(ctx, "p")
		_, successErr := repo.IncrementSuccessCount(ctx, "p", time.Now())
		_, failErr := repo.IncrementFailCount(ctx, "p", time.Now(), "failed")
		_, recoverErr := repo.AtomicRecoverIfExpired(ctx, "p", time.Now())
		for _, err := range []error{readErr, successErr, failErr, recoverErr, repo.UpdateHealthState(ctx, &model.HealthState{ProviderID: "p"}), repo.AutoDisableUntil(ctx, "p", time.Now(), "auto: failure"), repo.ResetRouteAvailability(ctx, "p"), Migrate(db)} {
			if err == nil {
				t.Fatal(errors.New("closed database accepted"))
			}
		}
	}
}
