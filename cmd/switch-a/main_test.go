package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/config"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/store"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestLogRequestLogTimestampMigrationEmitsBoundedQuarantineDiagnostics(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	report := store.RequestLogTimestampMigrationReport{
		BackfilledCount: 500,
		InvalidCount:    20,
		InvalidIDs:      []uint{4, 5, 6},
	}

	logRequestLogTimestampMigration(zap.New(core), report)

	entries := observed.All()
	if len(entries) != 1 || entries[0].Level != zap.WarnLevel {
		t.Fatalf("entries = %+v, want one warning", entries)
	}
	fields := entries[0].ContextMap()
	if fields["migration_id"] != requestLogTimestampMigrationID ||
		fields["backfilled_count"] != int64(500) ||
		fields["invalid_count"] != int64(20) ||
		fields["invalid_id_sample_truncated"] != true {
		t.Fatalf("fields = %+v", fields)
	}
}

const (
	cleanupObservationTimeout = time.Second
	testProxyPort             = "8080"
	testAdminPort             = "9090"
)

type captureTestClock struct {
	now       time.Time
	monotonic time.Duration
}

func (c *captureTestClock) WallNow() time.Time {
	return c.now
}

func (c *captureTestClock) MonotonicNow() time.Duration {
	return c.monotonic
}

func TestRequestCaptureManagerConfigMapsRuntimeLimits(t *testing.T) {
	clock := &captureTestClock{now: time.Unix(123, 0)}
	log := zap.NewNop()
	startup := &config.Config{
		DebugCaptureMemoryCeilingBytes:     600 << 20,
		DebugCaptureMaxActiveRecords:       11,
		DebugCaptureMaxActiveTraces:        12,
		DebugCaptureMaxTransitionsPerTrace: 13,
		DebugCaptureMaxPendingExports:      14,
		DebugCaptureMaxConcurrentDownloads: 15,
		DebugCaptureDetailPreviewBytes:     16,
		DebugCaptureDetailEventLimit:       17,
		DebugCaptureDownloadTokenTTL:       18 * time.Second,
		DebugCaptureMaxRecordsPerProvider:  19,
		DebugCaptureChunkBytes:             20,
		DebugCaptureExportLineBytes:        21,
	}

	got := requestCaptureManagerConfig(startup, clock, log)
	want := requestcapture.Config{
		ProcessCeilingBytes:       startup.DebugCaptureMemoryCeilingBytes,
		DefaultSessionQuotaBytes:  requestcapture.DefaultSessionQuotaBytes,
		ChunkBytes:                startup.DebugCaptureChunkBytes,
		DefaultRecordsPerProvider: requestcapture.DefaultRecordsPerProvider,
		MaxRecordsPerProvider:     startup.DebugCaptureMaxRecordsPerProvider,
		MaxActiveTraces:           startup.DebugCaptureMaxActiveTraces,
		MaxActiveRecords:          startup.DebugCaptureMaxActiveRecords,
		MaxTransitionsPerTrace:    startup.DebugCaptureMaxTransitionsPerTrace,
		MaxPendingExports:         startup.DebugCaptureMaxPendingExports,
		MaxActiveDownloads:        startup.DebugCaptureMaxConcurrentDownloads,
		PreviewBytes:              startup.DebugCaptureDetailPreviewBytes,
		DetailEventLimit:          startup.DebugCaptureDetailEventLimit,
		ExportLineBytes:           startup.DebugCaptureExportLineBytes,
		DownloadTokenTTL:          startup.DebugCaptureDownloadTokenTTL,
		Clock:                     clock,
		Logger:                    log,
	}
	if got != want {
		t.Fatalf("requestCaptureManagerConfig() = %+v, want %+v", got, want)
	}
}

func TestNewCaptureManagerUsesConfiguredCeiling(t *testing.T) {
	startup := &config.Config{
		DebugCaptureMemoryCeilingBytes:     600 << 20,
		DebugCaptureMaxActiveRecords:       requestcapture.DefaultMaxActiveRecords,
		DebugCaptureMaxActiveTraces:        requestcapture.DefaultMaxActiveTraces,
		DebugCaptureMaxTransitionsPerTrace: requestcapture.DefaultMaxTransitionsPerTrace,
		DebugCaptureMaxPendingExports:      requestcapture.DefaultMaxPendingExports,
		DebugCaptureMaxConcurrentDownloads: requestcapture.DefaultMaxActiveDownloads,
		DebugCaptureDetailPreviewBytes:     requestcapture.DefaultPreviewBytes,
		DebugCaptureDetailEventLimit:       requestcapture.DefaultDetailEventLimit,
		DebugCaptureDownloadTokenTTL:       requestcapture.DefaultDownloadTokenTTL,
		DebugCaptureMaxRecordsPerProvider:  requestcapture.DefaultMaxRecordsPerProvider,
		DebugCaptureChunkBytes:             requestcapture.DefaultChunkBytes,
		DebugCaptureExportLineBytes:        requestcapture.DefaultExportLineBytes,
	}

	manager, err := newCaptureManager(startup, &captureTestClock{}, zap.NewNop())
	if err != nil {
		t.Fatalf("newCaptureManager() error = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := manager.Close(); closeErr != nil {
			t.Errorf("manager.Close() error = %v", closeErr)
		}
	})

	if got := manager.Status().ProcessMemory.CeilingBytes; got != startup.DebugCaptureMemoryCeilingBytes {
		t.Fatalf("capture ceiling = %d, want %d", got, startup.DebugCaptureMemoryCeilingBytes)
	}
}

type stubLogStore struct {
	mu              sync.Mutex
	getConfigValue  string
	getConfigErr    error
	cleanOldLogsErr error
	cleanCalls      int
	lastRetention   int
	cleanCalled     chan struct{}
}

type recordingCloser struct {
	name   string
	events *[]string
	err    error
}

func (c *recordingCloser) Close() error {
	*c.events = append(*c.events, c.name)
	return c.err
}

func TestCloseApplicationStoresClosesAnalyticsBeforeWriterAndJoinsErrors(t *testing.T) {
	analyticsErr := errors.New("analytics close failed")
	writerErr := errors.New("writer close failed")
	events := make([]string, 0, 2)
	analytics := &recordingCloser{name: "analytics", events: &events, err: analyticsErr}
	writer := &recordingCloser{name: "writer", events: &events, err: writerErr}

	err := closeApplicationStores(analytics, writer)

	if got, want := strings.Join(events, ","), "analytics,writer"; got != want {
		t.Fatalf("close order = %q, want %q", got, want)
	}
	if !errors.Is(err, analyticsErr) || !errors.Is(err, writerErr) {
		t.Fatalf("close error = %v, want both resource errors", err)
	}
}

func (s *stubLogStore) CleanOldLogs(_ context.Context, beforeDays int) error {
	s.mu.Lock()
	s.cleanCalls++
	s.lastRetention = beforeDays
	cleanCalled := s.cleanCalled
	err := s.cleanOldLogsErr
	s.mu.Unlock()

	if cleanCalled != nil {
		select {
		case cleanCalled <- struct{}{}:
		default:
		}
	}

	return err
}

func (s *stubLogStore) GetConfig(_ context.Context, _ string) (string, error) {
	return s.getConfigValue, s.getConfigErr
}

func (s *stubLogStore) snapshot() (cleanCalls int, lastRetention int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cleanCalls, s.lastRetention
}

func TestCleanOldLogsUsesConfiguredRetention(t *testing.T) {
	store := &stubLogStore{getConfigValue: "14"}

	cleanOldLogs(store, zap.NewNop())

	cleanCalls, lastRetention := store.snapshot()
	if cleanCalls != 1 {
		t.Fatalf("expected one cleanup call, got %d", cleanCalls)
	}
	if lastRetention != 14 {
		t.Fatalf("expected configured retention to be used, got %d", lastRetention)
	}
}

func TestCleanOldLogsFallsBackToDefaultRetention(t *testing.T) {
	store := &stubLogStore{
		getConfigValue:  "invalid",
		cleanOldLogsErr: errors.New("cleanup failed"),
	}

	cleanOldLogs(store, zap.NewNop())

	cleanCalls, lastRetention := store.snapshot()
	if cleanCalls != 1 {
		t.Fatalf("expected one cleanup call, got %d", cleanCalls)
	}
	if lastRetention != DefaultLogRetentionDays {
		t.Fatalf("expected default retention %d, got %d", DefaultLogRetentionDays, lastRetention)
	}
}

func TestStartLogCleanupLoopRunsInitialCleanupAndStops(t *testing.T) {
	store := &stubLogStore{cleanCalled: make(chan struct{}, 1)}

	stop := startLogCleanupLoop(store, zap.NewNop())

	select {
	case <-store.cleanCalled:
	case <-time.After(cleanupObservationTimeout):
		t.Fatal("timed out waiting for initial cleanup")
	}

	stop()

	cleanCalls, _ := store.snapshot()
	if cleanCalls != 1 {
		t.Fatalf("expected only the initial cleanup before stop, got %d calls", cleanCalls)
	}
}

func TestWaitForShutdownJoinsQueuedServerErrors(t *testing.T) {
	firstErr := errors.New("proxy failed")
	secondErr := errors.New("admin failed")
	errCh := make(chan error, 2)
	errCh <- firstErr
	errCh <- secondErr

	err := waitForShutdown(errCh, zap.NewNop())
	if !errors.Is(err, firstErr) {
		t.Fatalf("expected joined error to contain first error")
	}
	if !errors.Is(err, secondErr) {
		t.Fatalf("expected joined error to contain second error")
	}
}

func TestPrintServerURLsWritesExpectedEndpoints(t *testing.T) {
	output := captureStdout(t, func() {
		printServerURLs(testProxyPort, testAdminPort)
	})

	if !strings.Contains(output, "http://localhost:"+testProxyPort) {
		t.Fatalf("expected proxy URL in output, got %q", output)
	}
	if !strings.Contains(output, "http://localhost:"+testAdminPort+"/admin") {
		t.Fatalf("expected admin URL in output, got %q", output)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	originalStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	t.Cleanup(func() {
		os.Stdout = originalStdout
		_ = reader.Close()
	})

	os.Stdout = writer
	fn()

	if err := writer.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}

	var output bytes.Buffer
	if _, err := io.Copy(&output, reader); err != nil {
		t.Fatalf("read stdout: %v", err)
	}

	return output.String()
}
