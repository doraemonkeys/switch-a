package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/tokenanalytics"
)

func TestSnapshotPinsAllAggregateReadsAndRejectsMutation(t *testing.T) {
	database := newTestDatabase(t)
	start := time.Date(2026, time.August, 20, 0, 0, 0, 0, time.UTC)
	query := testQuery(start, start.Add(3*time.Hour), time.Hour)
	database.insertLog(t, model.RequestLog{
		ProviderID: "before", APIType: "codex", Model: "before", CreatedAt: start.Add(10 * time.Minute),
		PromptTokens: int64Pointer(10), CompletionTokens: int64Pointer(5),
	})

	snapshotInterface, err := database.repository.OpenSnapshot(context.Background())
	if err != nil {
		t.Fatalf("OpenSnapshot() error = %v", err)
	}
	snapshot := snapshotInterface.(*Snapshot)
	summary, err := snapshot.ReadSummary(context.Background(), query)
	if err != nil {
		t.Fatalf("ReadSummary() error = %v", err)
	}
	if summary.ComparableRequests != 1 || summary.TotalTokens != 15 {
		t.Fatalf("initial summary = %+v", summary)
	}

	database.insertLog(t, model.RequestLog{
		ProviderID: "after", APIType: "codex", Model: "after", CreatedAt: start.Add(time.Hour + 10*time.Minute),
		PromptTokens: int64Pointer(100), CompletionTokens: int64Pointer(50),
	})

	buckets, err := snapshot.ReadBuckets(context.Background(), query)
	if err != nil {
		t.Fatalf("ReadBuckets() error = %v", err)
	}
	providers, err := snapshot.ReadProviderRanks(context.Background(), query, tokenanalytics.TopRankLimit)
	if err != nil {
		t.Fatalf("ReadProviderRanks() error = %v", err)
	}
	models, err := snapshot.ReadModelRanks(context.Background(), query, tokenanalytics.TopRankLimit)
	if err != nil {
		t.Fatalf("ReadModelRanks() error = %v", err)
	}
	if len(buckets) != 1 || buckets[0].TotalTokens != 15 || len(providers) != 1 || providers[0].ProviderID != "before" || len(models) != 1 || models[0].Model != "before" {
		t.Fatalf("later reads escaped pinned snapshot: buckets=%+v providers=%+v models=%+v", buckets, providers, models)
	}
	if _, err := snapshot.tx.ExecContext(context.Background(), "INSERT INTO request_logs (request_id, created_at) VALUES ('forbidden', ?)", start); err == nil {
		t.Fatal("analytics mutation error = nil, want query-only failure")
	}
	if err := snapshot.Close(); err != nil {
		t.Fatalf("Snapshot.Close() error = %v", err)
	}

	later := readSummary(t, database.repository, query)
	if later.ComparableRequests != 2 || later.TotalTokens != 165 {
		t.Fatalf("later snapshot summary = %+v", later)
	}
}

func TestSnapshotCancellationAndConnectionRelease(t *testing.T) {
	database := newTestDatabase(t)
	query := testQuery(time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(time.Hour), time.Hour)
	snapshot, err := database.repository.OpenSnapshot(context.Background())
	if err != nil {
		t.Fatalf("OpenSnapshot() error = %v", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := snapshot.ReadSummary(canceled, query); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadSummary(canceled) error = %v, want context.Canceled", err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatalf("Snapshot.Close() error = %v", err)
	}

	next, err := database.repository.OpenSnapshot(context.Background())
	if err != nil {
		t.Fatalf("OpenSnapshot(after cancellation) error = %v", err)
	}
	if err := next.Close(); err != nil {
		t.Fatalf("next Snapshot.Close() error = %v", err)
	}
}

func TestRepositorySerializesReadSnapshotsOnIndependentPool(t *testing.T) {
	database := newTestDatabase(t)
	first, err := database.repository.OpenSnapshot(context.Background())
	if err != nil {
		t.Fatalf("first OpenSnapshot() error = %v", err)
	}

	waitContext, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := database.repository.OpenSnapshot(waitContext); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second OpenSnapshot() error = %v, want deadline exceeded", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	second, err := database.repository.OpenSnapshot(context.Background())
	if err != nil {
		t.Fatalf("second OpenSnapshot(after release) error = %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestConcurrentWriterCleanupAndCheckpointStayWithinWriteBudget(t *testing.T) {
	database := newTestDatabase(t)
	start := time.Now().UTC().Add(-48 * time.Hour)
	query := testQuery(start, time.Now().UTC().Add(time.Hour), time.Hour)
	for index := range 20 {
		database.insertLog(t, model.RequestLog{
			ProviderID: "seed", APIType: "codex", Model: "seed", CreatedAt: start.Add(time.Duration(index) * time.Minute),
			PromptTokens: int64Pointer(1), CompletionTokens: int64Pointer(1),
		})
	}

	snapshot, err := database.repository.OpenSnapshot(context.Background())
	if err != nil {
		t.Fatalf("OpenSnapshot() error = %v", err)
	}
	if _, err := snapshot.ReadSummary(context.Background(), query); err != nil {
		t.Fatalf("ReadSummary() error = %v", err)
	}
	defer snapshot.Close()

	checkpointDB, err := sql.Open(sqliteDriverName, database.path)
	if err != nil {
		t.Fatalf("sql.Open(checkpoint) error = %v", err)
	}
	checkpointDB.SetMaxOpenConns(1)
	defer checkpointDB.Close()

	const writerCount = 40
	errCh := make(chan error, writerCount+2)
	var group sync.WaitGroup
	group.Add(3)
	go func() {
		defer group.Done()
		for index := range writerCount {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			started := time.Now()
			err := database.writer.InsertLog(ctx, &model.RequestLog{
				RequestID: "concurrent-" + fmt.Sprint(index), ProviderID: "writer", APIType: "codex", Model: "writer",
				CreatedAt: time.Now().UTC(), PromptTokens: int64Pointer(1), CompletionTokens: int64Pointer(1),
			})
			cancel()
			if err != nil {
				errCh <- fmt.Errorf("InsertLog(%d): %w", index, err)
				return
			}
			if elapsed := time.Since(started); elapsed >= 2*time.Second {
				errCh <- fmt.Errorf("InsertLog(%d) elapsed %s", index, elapsed)
				return
			}
		}
	}()
	go func() {
		defer group.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := database.writer.CleanOldLogs(ctx, 0); err != nil {
			errCh <- fmt.Errorf("CleanOldLogs: %w", err)
		}
	}()
	go func() {
		defer group.Done()
		for range 5 {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			var busy, logFrames, checkpointedFrames int
			err := checkpointDB.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &logFrames, &checkpointedFrames)
			cancel()
			if err != nil {
				errCh <- fmt.Errorf("wal_checkpoint: %w", err)
				return
			}
		}
	}()
	group.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}
