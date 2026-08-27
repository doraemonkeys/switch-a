package sqlite

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/analyticswindow"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/store"
	"github.com/doraemonkeys/switch-a/internal/tokenanalytics"
)

type testDatabase struct {
	path       string
	writer     *store.SQLiteStore
	repository *Repository
	nextID     int
}

func newTestDatabase(t *testing.T) *testDatabase {
	t.Helper()
	databasePath := filepath.Join(t.TempDir(), "token-analytics.db")
	writer, err := store.NewSQLiteStore(databasePath, internal.RealClock{}, nil)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	repository, err := Open(databasePath)
	if err != nil {
		_ = writer.Close()
		t.Fatalf("Open() error = %v", err)
	}
	database := &testDatabase{path: databasePath, writer: writer, repository: repository}
	t.Cleanup(func() {
		if err := repository.Close(); err != nil {
			t.Errorf("Repository.Close() error = %v", err)
		}
		if err := writer.Close(); err != nil {
			t.Errorf("SQLiteStore.Close() error = %v", err)
		}
	})
	return database
}

func (database *testDatabase) insertLog(t *testing.T, log model.RequestLog) {
	t.Helper()
	database.nextID++
	if log.RequestID == "" {
		log.RequestID = "analytics-" + strconv.Itoa(database.nextID)
	}
	if err := database.writer.InsertLog(context.Background(), &log); err != nil {
		t.Fatalf("InsertLog(%q) error = %v", log.RequestID, err)
	}
}

func (database *testDatabase) createProvider(t *testing.T, id, name string) {
	t.Helper()
	provider := model.Provider{ID: id, Name: name, Enabled: true}
	if err := database.writer.CreateProvider(context.Background(), &provider); err != nil {
		t.Fatalf("CreateProvider(%q) error = %v", id, err)
	}
}

func testQuery(start, end time.Time, granularity time.Duration) tokenanalytics.Query {
	return tokenanalytics.Query{Window: analyticswindow.Window{
		Period:          analyticswindow.Period24Hours,
		GranularityName: analyticswindow.Granularity1Hour,
		Granularity:     granularity,
		Start:           start.UTC(),
		End:             end.UTC(),
	}}
}

func int64Pointer(value int64) *int64 {
	return &value
}

func readSummary(t *testing.T, repository *Repository, query tokenanalytics.Query) tokenanalytics.SummaryRecord {
	t.Helper()
	snapshot, err := repository.OpenSnapshot(context.Background())
	if err != nil {
		t.Fatalf("OpenSnapshot() error = %v", err)
	}
	defer func() {
		if err := snapshot.Close(); err != nil {
			t.Errorf("Snapshot.Close() error = %v", err)
		}
	}()
	record, err := snapshot.ReadSummary(context.Background(), query)
	if err != nil {
		t.Fatalf("ReadSummary() error = %v", err)
	}
	return record
}
