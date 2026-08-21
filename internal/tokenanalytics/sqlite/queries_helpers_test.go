package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/tokenanalytics"
)

func TestExactAggregateAndTimeScanners(t *testing.T) {
	if value, err := parseExactInt64("-42"); err != nil || value != -42 {
		t.Fatalf("parseExactInt64(-42) = %d, %v", value, err)
	}
	for _, value := range []string{"+1", "1.0", "9e3", "", "9223372036854775808"} {
		if _, err := parseExactInt64(value); err == nil {
			t.Errorf("parseExactInt64(%q) error = nil", value)
		}
	}

	now := time.Date(2026, time.August, 20, 1, 2, 3, 456, time.FixedZone("test", 8*60*60))
	tests := []struct {
		name   string
		source any
		valid  bool
		want   time.Time
	}{
		{name: "nil", source: nil},
		{name: "time", source: now, valid: true, want: now},
		{name: "RFC3339 string", source: now.Format(time.RFC3339Nano), valid: true, want: now},
		{name: "SQLite bytes", source: []byte("2026-08-20 01:02:03.000000456+08:00"), valid: true, want: now},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var scanned nullableTime
			if err := scanned.Scan(test.source); err != nil {
				t.Fatalf("Scan() error = %v", err)
			}
			if scanned.Valid != test.valid || (test.valid && !scanned.Time.Equal(test.want)) {
				t.Fatalf("Scan() = %+v, want valid=%v time=%v", scanned, test.valid, test.want)
			}
		})
	}
	for _, source := range []any{123, "not-a-time"} {
		var scanned nullableTime
		if err := scanned.Scan(source); err == nil {
			t.Errorf("Scan(%v) error = nil", source)
		}
	}

	sentinel := errors.New("sentinel")
	if err := combineCloseErrors(nil, sql.ErrTxDone); err != nil {
		t.Fatalf("combineCloseErrors(ignored) = %v", err)
	}
	if err := combineCloseErrors(sentinel, sql.ErrTxDone); !errors.Is(err, sentinel) {
		t.Fatalf("combineCloseErrors() = %v", err)
	}
}

func TestRepositoryLifecycleErrorPaths(t *testing.T) {
	var nilRepository *Repository
	if err := nilRepository.Close(); err != nil {
		t.Fatalf("nil Repository.Close() error = %v", err)
	}
	missingPath := filepath.Join(t.TempDir(), "missing", "analytics.db")
	if _, err := Open(missingPath); err == nil {
		t.Fatal("Open(missing read-only database) error = nil")
	}

	database := newTestDatabase(t)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := database.repository.OpenSnapshot(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenSnapshot(canceled) error = %v", err)
	}
	if err := database.repository.Close(); err != nil {
		t.Fatalf("Repository.Close() error = %v", err)
	}
	if _, err := database.repository.OpenSnapshot(context.Background()); err == nil {
		t.Fatal("OpenSnapshot(closed repository) error = nil")
	}
}

func TestReadMethodsRejectClosedSnapshot(t *testing.T) {
	database := newTestDatabase(t)
	snapshot, err := database.repository.OpenSnapshot(context.Background())
	if err != nil {
		t.Fatalf("OpenSnapshot() error = %v", err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	query := testQuery(time.Now().Add(-time.Hour), time.Now(), time.Hour)
	if _, err := snapshot.ReadBuckets(context.Background(), query); !errors.Is(err, errSnapshotClosed) {
		t.Errorf("ReadBuckets(closed) error = %v", err)
	}
	if _, err := snapshot.ReadProviderRanks(context.Background(), query, 1); !errors.Is(err, errSnapshotClosed) {
		t.Errorf("ReadProviderRanks(closed) error = %v", err)
	}
	if _, err := snapshot.ReadModelRanks(context.Background(), query, 1); !errors.Is(err, errSnapshotClosed) {
		t.Errorf("ReadModelRanks(closed) error = %v", err)
	}
}

func TestAggregateScanFailuresAreReturned(t *testing.T) {
	database := newTestDatabase(t)
	start := time.Date(2026, time.August, 20, 0, 0, 0, 0, time.UTC)
	writeDB, err := sql.Open(sqliteDriverName, database.path)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer writeDB.Close()
	_, err = writeDB.Exec(`
		INSERT INTO request_logs (
			request_id, provider_id, api_type, model, created_at,
			prompt_tokens, completion_tokens, total_tokens
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"real-tokens", "real-provider", "codex", "real-model", start.Add(10*time.Minute), 1.5, 0, 1.5,
	)
	if err != nil {
		t.Fatalf("insert REAL token row error = %v", err)
	}

	snapshot, err := database.repository.OpenSnapshot(context.Background())
	if err != nil {
		t.Fatalf("OpenSnapshot() error = %v", err)
	}
	defer snapshot.Close()
	query := testQuery(start, start.Add(time.Hour), time.Hour)
	if _, err := snapshot.ReadProviderRanks(context.Background(), query, tokenanalytics.TopRankLimit); err == nil || !strings.Contains(err.Error(), "non-integer") {
		t.Errorf("ReadProviderRanks(REAL) error = %v", err)
	}
	if _, err := snapshot.ReadModelRanks(context.Background(), query, tokenanalytics.TopRankLimit); err == nil || !strings.Contains(err.Error(), "non-integer") {
		t.Errorf("ReadModelRanks(REAL) error = %v", err)
	}
}

func TestBreakdownScannerMapsEveryField(t *testing.T) {
	values := breakdownText{
		total: "10", input: "6", output: "4", fresh: "1", cacheRead: "2", cacheCreation: "3",
		unclassifiedInput: "0", standardOutput: "1", reasoning: "2", unclassifiedOutput: "1",
	}
	got, err := values.breakdown()
	if err != nil {
		t.Fatalf("breakdown() error = %v", err)
	}
	want := tokenanalytics.Breakdown{
		TotalTokens: 10, InputTokens: 6, OutputTokens: 4, FreshInputTokens: 1,
		CacheReadInputTokens: 2, CacheCreationInputTokens: 3, StandardOutputTokens: 1,
		ReasoningTokens: 2, UnclassifiedOutputTokens: 1,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("breakdown() = %+v, want %+v", got, want)
	}
	values.total = "Inf"
	if _, err := values.breakdown(); err == nil {
		t.Fatal("breakdown(non-integer) error = nil")
	}
}
