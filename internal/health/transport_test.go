package health

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/health/state"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/glebarez/sqlite"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestRouteCircuitsKeepHTTPAvailableAndManualControlsGlobal(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "routes.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
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
	if err := healthstate.Migrate(db); err != nil {
		t.Fatal(err)
	}
	repository := healthstate.New(db, newMockStore())
	root := NewManager(Config{Store: repository, Clock: internal.RealClock{}, Logger: zap.NewNop()})
	ws := root.ForRoute("codex", "websocket")
	http := root.ForRoute("codex", "http")
	if root.ForRoute("codex", "") != http || ws.ForRoute("codex", "http") != http {
		t.Fatal("scope cache lost")
	}
	ctx := context.Background()
	for i := range 3 {
		if ws.MarkFailure(ctx, "p", errors.New("handshake failed")) != (i == 2) {
			t.Fatal("incorrect circuit threshold")
		}
	}
	if ws.IsAvailable(ctx, "p") || !http.IsAvailable(ctx, "p") || !root.IsAvailable(ctx, "p") {
		t.Fatal("WS circuit disabled HTTP")
	}
	http.MarkSuccess(ctx, "p")
	if ws.IsAvailable(ctx, "p") {
		t.Fatal("HTTP success cleared WS circuit")
	}
	aggregate, err := repository.GetHealthState(ctx, "p")
	if err != nil || aggregate.FailCount != 3 || aggregate.SuccessCount != 1 {
		t.Fatal(aggregate, err)
	}
	if err := root.ManualDisable(ctx, "p", "maintenance"); err != nil {
		t.Fatal(err)
	}
	if http.IsAvailable(ctx, "p") || ws.IsAvailable(ctx, "p") {
		t.Fatal("manual disable was not global")
	}
	// A fresh manager has no loaded WS scope, but enable must still clear its
	// persisted cooldown from the prior process.
	reopened := NewManager(Config{Store: repository, Clock: internal.RealClock{}, Logger: zap.NewNop()})
	if err := reopened.ManualEnable(ctx, "p"); err != nil {
		t.Fatal(err)
	}
	if !reopened.ForRoute("codex", "websocket").IsAvailable(ctx, "p") || !http.IsAvailable(ctx, "p") {
		t.Fatal("manual enable failed after restart")
	}
	if err := repository.HealthScope("codex", "websocket").AutoDisableUntil(ctx, "p", time.Now().Add(-time.Second), "auto: expired"); err != nil {
		t.Fatal(err)
	}
	if !ws.RecoverIfExpired(ctx, "p") {
		t.Fatal("route cooldown did not recover")
	}
	root.ResetCircuitBreaker("p")
	if ws.MarkFailure(ctx, "p", errors.New("one failure")) {
		t.Fatal("reset retained route failure count")
	}
}
