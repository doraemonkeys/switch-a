package requestcapture

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"math"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDownloadTokenGenerationIsHighEntropyBase64URLAndHashOnly(t *testing.T) {
	entropy := make([]byte, downloadTokenEntropyBytes)
	for index := range entropy {
		entropy[index] = byte(index)
	}
	rawToken, storedHash, err := newDownloadToken(bytes.NewReader(entropy))
	if err != nil {
		t.Fatalf("newDownloadToken() error = %v", err)
	}
	wantToken := base64.RawURLEncoding.EncodeToString(entropy)
	if rawToken != wantToken {
		t.Fatalf("token = %q, want %q", rawToken, wantToken)
	}
	if strings.ContainsAny(rawToken, "+/=") {
		t.Fatalf("token is not unpadded base64url: %q", rawToken)
	}
	wantHash := sha256.Sum256([]byte(rawToken))
	if storedHash != wantHash {
		t.Fatalf("stored hash = %x, want %x", storedHash, wantHash)
	}
	if !downloadTokenMatches(storedHash, rawToken) {
		t.Fatal("generated token does not match its hash")
	}
	for _, invalid := range []string{"", rawToken + "x", rawToken[:len(rawToken)-1], strings.Repeat("A", len(rawToken))} {
		if downloadTokenMatches(storedHash, invalid) {
			t.Fatalf("invalid token %q matched", invalid)
		}
	}

	manager := newTestManager(t, func(cfg *Config) {
		cfg.Entropy = bytes.NewReader(bytes.Repeat([]byte{0xa5}, downloadTokenEntropyBytes))
	})
	session := startTestSession(t, manager, 2, 1<<20, "selected")
	before := manager.Status().ProcessMemory.ChargedBytes
	ticket, err := manager.CreateExport(context.Background(), session.SessionID, ExportRequest{Scope: ExportScopeAll})
	if err != nil {
		t.Fatalf("CreateExport() error = %v", err)
	}
	manager.exportMu.Lock()
	retained := manager.lookupExportLocked(ticket.ExportID)
	manager.exportMu.Unlock()
	if retained == nil {
		t.Fatal("export state was not retained")
	}
	if retained.tokenHash != hashDownloadToken(ticket.DownloadToken) {
		t.Fatal("export state did not retain the token hash")
	}
	if manager.Status().ProcessMemory.ChargedBytes <= before {
		t.Fatal("snapshot lease and token metadata were not charged")
	}
}

func TestDownloadTokenEntropyFailureDoesNotPublishExport(t *testing.T) {
	manager := newTestManager(t, func(cfg *Config) {
		cfg.Entropy = io.LimitReader(bytes.NewReader([]byte{1, 2, 3}), 3)
	})
	session := startTestSession(t, manager, 2, 1<<20, "selected")
	before := manager.Status().ProcessMemory.ChargedBytes
	if _, err := manager.CreateExport(context.Background(), session.SessionID, ExportRequest{Scope: ExportScopeAll}); err == nil {
		t.Fatal("CreateExport() succeeded with insufficient entropy")
	}
	status := manager.Status()
	if status.PendingExportCount != 0 || status.ProcessMemory.ChargedBytes != before {
		t.Fatalf("failed token generation changed status: %#v", status)
	}
}

func TestDownloadTokenInvalidExpiredAndReplay(t *testing.T) {
	t.Run("invalid token leaves capability usable", func(t *testing.T) {
		manager, session, ticket := newTokenTestExport(t, nil)
		if _, err := manager.AcceptDownload(ticket.ExportID, strings.Repeat("x", len(ticket.DownloadToken))); !errors.Is(err, ErrDownloadUnavailable) {
			t.Fatalf("invalid AcceptDownload() error = %v", err)
		}
		download, err := manager.AcceptDownload(ticket.ExportID, ticket.DownloadToken)
		if err != nil {
			t.Fatalf("valid AcceptDownload() error = %v", err)
		}
		if err := download.WriteTo(context.Background(), io.Discard); err != nil {
			t.Fatalf("WriteTo() error = %v", err)
		}
		if status := manager.Status(); status.PendingExportCount != 0 || status.ActiveDownloadCount != 0 {
			t.Fatalf("status after download = %#v", status)
		}
		_ = session
	})

	t.Run("expired token releases snapshot pins", func(t *testing.T) {
		manager, _, ticket := newTokenTestExport(t, nil)
		if manager.Status().ProcessMemory.PinnedBytes == 0 {
			t.Fatal("pending export has no pinned payload")
		}
		clock := manager.cfg.clock.(*testClock)
		clock.advance(manager.cfg.downloadTokenTTL)
		if _, err := manager.AcceptDownload(ticket.ExportID, ticket.DownloadToken); !errors.Is(err, ErrDownloadUnavailable) {
			t.Fatalf("expired AcceptDownload() error = %v", err)
		}
		status := manager.Status()
		if status.PendingExportCount != 0 || status.ProcessMemory.PinnedBytes != 0 {
			t.Fatalf("expired lease status = %#v", status)
		}
	})

	t.Run("accepted token cannot be replayed", func(t *testing.T) {
		manager, _, ticket := newTokenTestExport(t, nil)
		download, err := manager.AcceptDownload(ticket.ExportID, ticket.DownloadToken)
		if err != nil {
			t.Fatalf("AcceptDownload() error = %v", err)
		}
		if _, err := manager.AcceptDownload(ticket.ExportID, ticket.DownloadToken); !errors.Is(err, ErrDownloadUnavailable) {
			t.Fatalf("replay error = %v", err)
		}
		if err := download.WriteTo(context.Background(), io.Discard); err != nil {
			t.Fatalf("WriteTo() error = %v", err)
		}
		if err := download.WriteTo(context.Background(), io.Discard); !errors.Is(err, ErrDownloadUnavailable) {
			t.Fatalf("second WriteTo() error = %v", err)
		}
	})
}

func TestDownloadTokenHasExactlyOneConcurrentConsumer(t *testing.T) {
	manager, _, ticket := newTokenTestExport(t, nil)
	const contenders = 16
	type result struct {
		download Download
		err      error
	}
	results := make(chan result, contenders)
	var wait sync.WaitGroup
	wait.Add(contenders)
	for range contenders {
		go func() {
			defer wait.Done()
			download, err := manager.AcceptDownload(ticket.ExportID, ticket.DownloadToken)
			results <- result{download: download, err: err}
		}()
	}
	wait.Wait()
	close(results)

	successes := 0
	var winner Download
	for result := range results {
		if result.err == nil {
			successes++
			winner = result.download
			continue
		}
		if !errors.Is(result.err, ErrDownloadUnavailable) {
			t.Fatalf("loser error = %v", result.err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful consumers = %d, want 1", successes)
	}
	if err := winner.WriteTo(context.Background(), io.Discard); err != nil {
		t.Fatalf("winner WriteTo() error = %v", err)
	}
}

func TestDownloadSaturationDoesNotConsumeToken(t *testing.T) {
	manager := newTestManager(t, func(cfg *Config) {
		cfg.MaxActiveDownloads = 1
		cfg.Entropy = bytes.NewReader(bytes.Repeat([]byte{0x6b}, downloadTokenEntropyBytes*4))
	})
	session := startTestSession(t, manager, 2, 1<<20, "selected")
	gateway, recorder := beginTestHTTP(manager, "gateway", "selected", []byte("request"))
	completeHTTP(recorder, []byte("response"))
	gateway.Finish(GatewayOutcome{})

	first, err := manager.CreateExport(context.Background(), session.SessionID, ExportRequest{Scope: ExportScopeAll})
	if err != nil {
		t.Fatalf("first CreateExport() error = %v", err)
	}
	second, err := manager.CreateExport(context.Background(), session.SessionID, ExportRequest{Scope: ExportScopeAll})
	if err != nil {
		t.Fatalf("second CreateExport() error = %v", err)
	}
	active, err := manager.AcceptDownload(first.ExportID, first.DownloadToken)
	if err != nil {
		t.Fatalf("first AcceptDownload() error = %v", err)
	}
	if _, err := manager.AcceptDownload(second.ExportID, second.DownloadToken); !errors.Is(err, ErrDownloadUnavailable) {
		t.Fatalf("saturated AcceptDownload() error = %v, want ErrDownloadUnavailable", err)
	}
	if err := active.WriteTo(context.Background(), io.Discard); err != nil {
		t.Fatalf("first WriteTo() error = %v", err)
	}
	retried, err := manager.AcceptDownload(second.ExportID, second.DownloadToken)
	if err != nil {
		t.Fatalf("retried AcceptDownload() error = %v", err)
	}
	if err := retried.WriteTo(context.Background(), io.Discard); err != nil {
		t.Fatalf("second WriteTo() error = %v", err)
	}
}

func TestDownloadTokenLifetimeUsesMonotonicTime(t *testing.T) {
	t.Run("wall rollback cannot extend lifetime", func(t *testing.T) {
		manager, _, ticket := newTokenTestExport(t, nil)
		clock := manager.cfg.clock.(*testClock)
		clock.advanceWall(-time.Hour)
		clock.advanceMonotonic(manager.cfg.downloadTokenTTL)

		if _, err := manager.AcceptDownload(ticket.ExportID, ticket.DownloadToken); !errors.Is(err, ErrDownloadUnavailable) {
			t.Fatalf("AcceptDownload() after elapsed TTL error = %v", err)
		}
		status := manager.Status()
		if status.PendingExportCount != 0 || status.ProcessMemory.PinnedBytes != 0 {
			t.Fatalf("expired export retained accounting after wall rollback: %#v", status)
		}
	})

	t.Run("wall forward cannot shorten lifetime", func(t *testing.T) {
		manager, _, ticket := newTokenTestExport(t, nil)
		clock := manager.cfg.clock.(*testClock)
		clock.advanceWall(24 * time.Hour)

		download, err := manager.AcceptDownload(ticket.ExportID, ticket.DownloadToken)
		if err != nil {
			t.Fatalf("AcceptDownload() before elapsed TTL error = %v", err)
		}
		if err := download.Close(); err != nil {
			t.Fatalf("Download.Close() error = %v", err)
		}
	})

	t.Run("deadline addition saturates", func(t *testing.T) {
		var clock *testClock
		manager, _, ticket := newTokenTestExport(t, func(cfg *Config) {
			clock = cfg.Clock.(*testClock)
			clock.monotonic = time.Duration(math.MaxInt64) - time.Second
			cfg.DownloadTokenTTL = 2 * time.Second
		})

		manager.exportMu.Lock()
		state := manager.lookupExportLocked(ticket.ExportID)
		manager.exportMu.Unlock()
		if state == nil {
			t.Fatal("export state was not retained")
		}
		deadline := state.expiresDeadline
		if deadline != time.Duration(math.MaxInt64) {
			t.Fatalf("monotonic deadline = %v, want saturation at %v", deadline, time.Duration(math.MaxInt64))
		}

		clock.advanceMonotonic(2 * time.Second)
		if _, err := manager.AcceptDownload(ticket.ExportID, ticket.DownloadToken); !errors.Is(err, ErrDownloadUnavailable) {
			t.Fatalf("AcceptDownload() at saturated deadline error = %v", err)
		}
	})
}

func TestPendingExportCapIsReusableAfterClaim(t *testing.T) {
	manager := newTestManager(t, func(cfg *Config) {
		cfg.MaxPendingExports = 1
		cfg.Entropy = bytes.NewReader(bytes.Repeat([]byte{0x7c}, downloadTokenEntropyBytes*4))
	})
	session := startTestSession(t, manager, 2, 1<<20, "selected")
	first, err := manager.CreateExport(context.Background(), session.SessionID, ExportRequest{Scope: ExportScopeAll})
	if err != nil {
		t.Fatalf("first CreateExport() error = %v", err)
	}
	if _, err := manager.CreateExport(context.Background(), session.SessionID, ExportRequest{Scope: ExportScopeAll}); !errors.Is(err, ErrExportLimitReached) {
		t.Fatalf("second CreateExport() error = %v", err)
	}
	download, err := manager.AcceptDownload(first.ExportID, first.DownloadToken)
	if err != nil {
		t.Fatalf("AcceptDownload() error = %v", err)
	}
	if _, err := manager.CreateExport(context.Background(), session.SessionID, ExportRequest{Scope: ExportScopeAll}); err != nil {
		t.Fatalf("CreateExport() while first streams error = %v", err)
	}
	if err := download.WriteTo(context.Background(), io.Discard); err != nil {
		t.Fatalf("WriteTo() error = %v", err)
	}
}

func newTokenTestExport(
	t *testing.T,
	mutate func(*Config),
) (*Manager, SessionInfo, ExportTicket) {
	t.Helper()
	manager := newTestManager(t, func(cfg *Config) {
		cfg.Entropy = bytes.NewReader(bytes.Repeat([]byte{0x5a}, downloadTokenEntropyBytes*8))
		if mutate != nil {
			mutate(cfg)
		}
	})
	session := startTestSession(t, manager, 2, 1<<20, "selected")
	gateway, recorder := beginTestHTTP(manager, "gateway", "selected", []byte{0, 1, 2, 3})
	completeHTTP(recorder, []byte{4, 5, 6, 7})
	gateway.Finish(GatewayOutcome{})
	ticket, err := manager.CreateExport(context.Background(), session.SessionID, ExportRequest{
		Scope:     ExportScopeRecords,
		RecordIDs: []string{recorder.ID()},
	})
	if err != nil {
		t.Fatalf("CreateExport() error = %v", err)
	}
	return manager, session, ticket
}
