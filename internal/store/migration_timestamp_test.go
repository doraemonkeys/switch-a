package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/instant"
	"github.com/doraemonkeys/switch-a/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestNewSQLiteStoreObservesTimestampQuarantineBeforeLaterIndexFailure(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "startup_diagnostic.db")
	initial, err := NewSQLiteStore(databasePath, internal.RealClock{}, nil)
	if err != nil {
		t.Fatalf("initialize store: %v", err)
	}
	if err := initial.db.Exec("INSERT INTO request_logs (created_at) VALUES ('poison')").Error; err != nil {
		t.Fatalf("insert poison row: %v", err)
	}
	var poisonID uint
	if err := initial.db.Raw("SELECT MAX(id) FROM request_logs").Scan(&poisonID).Error; err != nil {
		t.Fatalf("read poison row ID: %v", err)
	}
	if err := initial.db.Exec("DROP INDEX " + requestLogAPITypeCreatedAtUnixNanoIndex).Error; err != nil {
		t.Fatalf("drop final analytics index: %v", err)
	}
	if err := initial.db.Exec("CREATE INDEX " + requestLogAPITypeCreatedAtUnixNanoIndex + " ON request_logs (api_type)").Error; err != nil {
		t.Fatalf("create malformed final analytics index: %v", err)
	}
	if err := initial.Close(); err != nil {
		t.Fatalf("close initial store: %v", err)
	}

	var observed RequestLogTimestampMigrationReport
	failedStore, err := NewSQLiteStore(databasePath, internal.RealClock{}, func(report RequestLogTimestampMigrationReport) {
		observed = report
	})
	if failedStore != nil || err == nil || !strings.Contains(err.Error(), "verify request-log analytics index") {
		t.Fatalf("failed store/error = %v/%v", failedStore, err)
	}
	if observed.InvalidCount != 1 || len(observed.InvalidIDs) != 1 || observed.InvalidIDs[0] != poisonID {
		t.Fatalf("observed report = %+v, want poison row %d", observed, poisonID)
	}

	repairDB, err := gorm.Open(sqlite.Open(databasePath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open repair database: %v", err)
	}
	if err := repairDB.Exec("DROP INDEX " + requestLogAPITypeCreatedAtUnixNanoIndex).Error; err != nil {
		t.Fatalf("drop malformed final analytics index: %v", err)
	}
	var quarantined struct {
		CreatedAt         sql.NullTime
		CreatedAtUnixNano sql.NullInt64
	}
	if err := repairDB.Raw(
		"SELECT created_at, created_at_unix_nano FROM request_logs WHERE id = ?",
		poisonID,
	).Scan(&quarantined).Error; err != nil {
		t.Fatalf("read quarantined row: %v", err)
	}
	if quarantined.CreatedAt.Valid || quarantined.CreatedAtUnixNano.Valid {
		t.Fatalf("quarantined row = %+v, want terminal NULL representation", quarantined)
	}
	repairSQLDB, err := repairDB.DB()
	if err != nil {
		t.Fatalf("get repair SQL database: %v", err)
	}
	if err := repairSQLDB.Close(); err != nil {
		t.Fatalf("close repair database: %v", err)
	}

	var restartReport RequestLogTimestampMigrationReport
	restarted, err := NewSQLiteStore(databasePath, internal.RealClock{}, func(report RequestLogTimestampMigrationReport) {
		restartReport = report
	})
	if err != nil {
		t.Fatalf("restart store: %v", err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	if restartReport.InvalidCount != 0 || restartReport.BackfilledCount != 0 {
		t.Fatalf("restart report = %+v, want no repeated poison row", restartReport)
	}
}

func TestInsertLogPersistsInstantKeyWithoutChangingTimestampOffset(t *testing.T) {
	store := setupTestStore(t)
	createdAt := time.Date(2026, time.August, 21, 21, 45, 3, 751123400, time.FixedZone("source", 8*60*60))
	log := &model.RequestLog{RequestID: "timestamp-contract", CreatedAt: createdAt}

	if err := store.InsertLog(context.Background(), log); err != nil {
		t.Fatalf("InsertLog() error = %v", err)
	}
	if !log.CreatedAt.Equal(createdAt) || log.CreatedAt.Location() != createdAt.Location() {
		t.Fatalf("CreatedAt = %v (%v), want original %v (%v)", log.CreatedAt, log.CreatedAt.Location(), createdAt, createdAt.Location())
	}
	if log.CreatedAtUnixNano == nil || *log.CreatedAtUnixNano != createdAt.UnixNano() {
		t.Fatalf("CreatedAtUnixNano = %v, want %d", log.CreatedAtUnixNano, createdAt.UnixNano())
	}

	var persisted struct {
		CreatedAt         time.Time
		CreatedAtUnixNano int64
	}
	if err := store.db.Raw(
		"SELECT created_at, created_at_unix_nano FROM request_logs WHERE request_id = ?",
		log.RequestID,
	).Scan(&persisted).Error; err != nil {
		t.Fatalf("read persisted timestamp: %v", err)
	}
	_, wantOffset := createdAt.Zone()
	_, gotOffset := persisted.CreatedAt.Zone()
	if !persisted.CreatedAt.Equal(createdAt) || gotOffset != wantOffset || persisted.CreatedAtUnixNano != createdAt.UnixNano() {
		t.Fatalf("persisted timestamp = %+v, want offset-preserving instant %v and key %d", persisted, createdAt, createdAt.UnixNano())
	}
}

type fixedStoreClock struct {
	now time.Time
}

func (clock fixedStoreClock) Now() time.Time {
	return clock.now
}

func (clock fixedStoreClock) NewTicker(interval time.Duration) *time.Ticker {
	return time.NewTicker(interval)
}

func TestInsertLogResolvesZeroCreatedAtOnce(t *testing.T) {
	store := setupTestStore(t)
	want := time.Date(2026, time.August, 21, 21, 45, 3, 751123400, time.FixedZone("fake-clock", 8*60*60))
	store.clock = fixedStoreClock{now: want}
	log := &model.RequestLog{RequestID: "zero-created-at"}

	if err := store.InsertLog(context.Background(), log); err != nil {
		t.Fatalf("InsertLog() error = %v", err)
	}
	if !log.CreatedAt.Equal(want) || log.CreatedAtUnixNano == nil || *log.CreatedAtUnixNano != want.UnixNano() {
		t.Fatalf("resolved log timestamps = CreatedAt %v, key %v", log.CreatedAt, log.CreatedAtUnixNano)
	}
	var persisted struct {
		CreatedAt         time.Time
		CreatedAtUnixNano int64
	}
	if err := store.db.Raw(
		"SELECT created_at, created_at_unix_nano FROM request_logs WHERE request_id = ?",
		log.RequestID,
	).Scan(&persisted).Error; err != nil {
		t.Fatalf("read persisted timestamp: %v", err)
	}
	if !persisted.CreatedAt.Equal(want) || persisted.CreatedAtUnixNano != want.UnixNano() {
		t.Fatalf("persisted timestamp = %+v, want one fake-clock instant %v", persisted, want)
	}
}

func TestInsertLogRejectsUnrepresentableCreatedAt(t *testing.T) {
	store := setupTestStore(t)
	log := &model.RequestLog{RequestID: "out-of-range", CreatedAt: time.Time{}}
	store.clock = fixedStoreClock{now: time.Date(1500, time.January, 1, 0, 0, 0, 0, time.UTC)}

	err := store.InsertLog(context.Background(), log)
	if !errors.Is(err, instant.ErrOutOfRange) {
		t.Fatalf("InsertLog() error = %v, want %v", err, instant.ErrOutOfRange)
	}
	var count int64
	if err := store.db.Model(&model.RequestLog{}).Where("request_id = ?", log.RequestID).Count(&count).Error; err != nil {
		t.Fatalf("count rejected log: %v", err)
	}
	if count != 0 {
		t.Fatalf("persisted rejected log count = %d", count)
	}
}
