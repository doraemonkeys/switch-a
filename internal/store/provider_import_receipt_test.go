package store

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
)

func TestApplyProviderImportReceiptCommitsAndReplaysExactPayload(t *testing.T) {
	store := setupTestStore(t)
	now := time.Now()
	payload := []byte("{\"import_id\":\"import-replay\",\"summary\":{\"created\":1}}\n")
	receipt := providerImportReceiptForTest("import-replay", "fingerprint-a", payload, now)
	bundle := &ProviderImportBundle{
		Creates: []ProviderImportCreate{{
			CandidateID: "candidate-a",
			Provider:    importTestProvider(t, "provider-a", "account-a", nil),
		}},
		Receipt: receipt,
	}
	if err := store.ApplyProviderImport(context.Background(), bundle); err != nil {
		t.Fatalf("ApplyProviderImport(first) error = %v", err)
	}

	stored, err := store.GetProviderImportReceipt(context.Background(), receipt.ImportID)
	if err != nil {
		t.Fatalf("GetProviderImportReceipt() error = %v", err)
	}
	assertProviderImportReceipt(t, stored, receipt)
	stored.ResponsePayload[0] = '!'
	storedAgain, err := store.GetProviderImportReceipt(context.Background(), receipt.ImportID)
	if err != nil {
		t.Fatalf("GetProviderImportReceipt(second) error = %v", err)
	}
	if !bytes.Equal(storedAgain.ResponsePayload, payload) {
		t.Fatalf("stored response payload was aliased: %q", storedAgain.ResponsePayload)
	}

	err = store.ApplyProviderImport(context.Background(), bundle)
	var replay *ProviderImportReceiptReplayError
	if !errors.As(err, &replay) || !errors.Is(err, ErrProviderImportReceiptReplay) {
		t.Fatalf("ApplyProviderImport(replay) error = %v, want typed replay", err)
	}
	assertProviderImportReceipt(t, &replay.Receipt, receipt)
}

func TestApplyProviderImportReceiptRejectsFingerprintMismatch(t *testing.T) {
	store := setupTestStore(t)
	now := time.Now()
	original := providerImportReceiptForTest(
		"import-mismatch",
		"fingerprint-a",
		[]byte("{\"result\":\"original\"}\n"),
		now,
	)
	if err := store.ApplyProviderImport(context.Background(), &ProviderImportBundle{Receipt: original}); err != nil {
		t.Fatalf("ApplyProviderImport(original) error = %v", err)
	}

	mismatch := providerImportReceiptForTest(
		original.ImportID,
		"fingerprint-b",
		[]byte("{\"result\":\"different\"}\n"),
		now,
	)
	err := store.ApplyProviderImport(context.Background(), &ProviderImportBundle{Receipt: mismatch})
	var conflict *ProviderImportReceiptConflictError
	if !errors.As(err, &conflict) || !errors.Is(err, ErrProviderImportReceiptConflict) {
		t.Fatalf("ApplyProviderImport(mismatch) error = %v, want typed conflict", err)
	}
	if conflict.ImportID != original.ImportID {
		t.Fatalf("conflict import ID = %q, want %q", conflict.ImportID, original.ImportID)
	}
	stored, getErr := store.GetProviderImportReceipt(context.Background(), original.ImportID)
	if getErr != nil {
		t.Fatalf("GetProviderImportReceipt() error = %v", getErr)
	}
	assertProviderImportReceipt(t, stored, original)
}

func TestProviderImportReceiptRollsBackWithMutationFailure(t *testing.T) {
	store := setupTestStore(t)
	if err := store.db.Exec(`
		CREATE TRIGGER fail_receipted_provider_import
		BEFORE INSERT ON providers
		WHEN NEW.id = 'provider-fail-receipt'
		BEGIN
			SELECT RAISE(ABORT, 'forced receipted import failure');
		END;
	`).Error; err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	receipt := providerImportReceiptForTest(
		"import-rollback",
		"fingerprint-rollback",
		[]byte("{\"result\":\"must-not-commit\"}\n"),
		time.Now(),
	)
	err := store.ApplyProviderImport(context.Background(), &ProviderImportBundle{
		Creates: []ProviderImportCreate{{
			CandidateID: "candidate-fail",
			Provider:    importTestProvider(t, "provider-fail-receipt", "account-fail", nil),
		}},
		Receipt: receipt,
	})
	if err == nil || !strings.Contains(err.Error(), "forced receipted import failure") {
		t.Fatalf("ApplyProviderImport() error = %v, want injected failure", err)
	}
	if _, err := store.GetProviderImportReceipt(context.Background(), receipt.ImportID); !errors.Is(err, ErrProviderImportReceiptNotFound) {
		t.Fatalf("GetProviderImportReceipt(after rollback) error = %v, want not found", err)
	}
	assertProviderMissing(t, store, "provider-fail-receipt")
}

func TestProviderImportReceiptPersistsAcrossStoreReopen(t *testing.T) {
	clock := &mockClock{now: time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)}
	dbPath := filepath.Join(t.TempDir(), "receipt-reopen.db")
	receipt := providerImportReceiptForTest(
		"import-reopen",
		"fingerprint-reopen",
		[]byte("{\"result\":\"persisted\"}\n"),
		clock.Now(),
	)

	first, err := NewSQLiteStore(dbPath, clock, nil)
	if err != nil {
		t.Fatalf("NewSQLiteStore(first) error = %v", err)
	}
	if err := first.ApplyProviderImport(context.Background(), &ProviderImportBundle{Receipt: receipt}); err != nil {
		_ = first.Close()
		t.Fatalf("ApplyProviderImport() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close(first) error = %v", err)
	}

	reopened, err := NewSQLiteStore(dbPath, clock, nil)
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopened) error = %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Logf("close reopened store: %v", err)
		}
	})
	stored, err := reopened.GetProviderImportReceipt(context.Background(), receipt.ImportID)
	if err != nil {
		t.Fatalf("GetProviderImportReceipt(reopened) error = %v", err)
	}
	assertProviderImportReceipt(t, stored, receipt)

	err = reopened.ApplyProviderImport(context.Background(), &ProviderImportBundle{Receipt: receipt})
	var replay *ProviderImportReceiptReplayError
	if !errors.As(err, &replay) {
		t.Fatalf("ApplyProviderImport(reopened replay) error = %v, want typed replay", err)
	}
	assertProviderImportReceipt(t, &replay.Receipt, receipt)
}

func TestProviderImportReceiptExpiryDeletesAndAllowsImportIDReuse(t *testing.T) {
	clock := &mockClock{now: time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)}
	store, err := NewSQLiteStore(":memory:", clock, nil)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	original := &ProviderImportReceipt{
		ImportID:        "import-expiry",
		Fingerprint:     "fingerprint-before-expiry",
		ResponsePayload: []byte("{\"result\":\"before\"}\n"),
		ExpiresAt:       clock.Now().Add(time.Minute),
	}
	if err := store.ApplyProviderImport(context.Background(), &ProviderImportBundle{Receipt: original}); err != nil {
		t.Fatalf("ApplyProviderImport(original) error = %v", err)
	}
	clock.Advance(time.Minute)
	if _, err := store.GetProviderImportReceipt(context.Background(), original.ImportID); !errors.Is(err, ErrProviderImportReceiptNotFound) {
		t.Fatalf("GetProviderImportReceipt(expired) error = %v, want not found", err)
	}
	var count int64
	if err := store.db.Model(&ProviderImportReceipt{}).Where("import_id = ?", original.ImportID).Count(&count).Error; err != nil {
		t.Fatalf("count expired receipts: %v", err)
	}
	if count != 0 {
		t.Fatalf("expired receipt row count = %d, want 0", count)
	}

	replacement := &ProviderImportReceipt{
		ImportID:        original.ImportID,
		Fingerprint:     "fingerprint-after-expiry",
		ResponsePayload: []byte("{\"result\":\"after\"}\n"),
		ExpiresAt:       clock.Now().Add(time.Minute),
	}
	if err := store.ApplyProviderImport(context.Background(), &ProviderImportBundle{Receipt: replacement}); err != nil {
		t.Fatalf("ApplyProviderImport(replacement) error = %v", err)
	}
	stored, err := store.GetProviderImportReceipt(context.Background(), replacement.ImportID)
	if err != nil {
		t.Fatalf("GetProviderImportReceipt(replacement) error = %v", err)
	}
	assertProviderImportReceipt(t, stored, replacement)
}

func TestProviderImportReceiptReservationPrunesUnrelatedExpiredRows(t *testing.T) {
	clock := &mockClock{now: time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)}
	store, err := NewSQLiteStore(":memory:", clock, nil)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	expiring := &ProviderImportReceipt{
		ImportID:        "import-unrelated-expired",
		Fingerprint:     "fingerprint-expired",
		ResponsePayload: []byte("{\"result\":\"expired\"}\n"),
		ExpiresAt:       clock.Now().Add(time.Minute),
	}
	if err := store.ApplyProviderImport(context.Background(), &ProviderImportBundle{Receipt: expiring}); err != nil {
		t.Fatalf("ApplyProviderImport(expiring) error = %v", err)
	}
	clock.Advance(time.Minute)
	var before int64
	if err := store.db.Model(&ProviderImportReceipt{}).
		Where("import_id = ?", expiring.ImportID).
		Count(&before).Error; err != nil {
		t.Fatalf("count expired receipt before reservation: %v", err)
	}
	if before != 1 {
		t.Fatalf("expired receipt count before reservation = %d, want 1", before)
	}

	fresh := &ProviderImportReceipt{
		ImportID:        "import-fresh-unrelated",
		Fingerprint:     "fingerprint-fresh",
		ResponsePayload: []byte("{\"result\":\"fresh\"}\n"),
		ExpiresAt:       clock.Now().Add(time.Minute),
	}
	if err := store.ApplyProviderImport(context.Background(), &ProviderImportBundle{Receipt: fresh}); err != nil {
		t.Fatalf("ApplyProviderImport(fresh) error = %v", err)
	}
	var after int64
	if err := store.db.Model(&ProviderImportReceipt{}).
		Where("import_id = ?", expiring.ImportID).
		Count(&after).Error; err != nil {
		t.Fatalf("count expired receipt after reservation: %v", err)
	}
	if after != 0 {
		t.Fatalf("unrelated expired receipt count after reservation = %d, want 0", after)
	}
	stored, err := store.GetProviderImportReceipt(context.Background(), fresh.ImportID)
	if err != nil {
		t.Fatalf("GetProviderImportReceipt(fresh) error = %v", err)
	}
	assertProviderImportReceipt(t, stored, fresh)
}

func TestProviderImportReceiptReservationSerializesStoreInstances(t *testing.T) {
	clock := &mockClock{now: time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)}
	dbPath := filepath.Join(t.TempDir(), "receipt-concurrent.db")
	first, err := NewSQLiteStore(dbPath, clock, nil)
	if err != nil {
		t.Fatalf("NewSQLiteStore(first) error = %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := NewSQLiteStore(dbPath, clock, nil)
	if err != nil {
		t.Fatalf("NewSQLiteStore(second) error = %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })
	receipt := providerImportReceiptForTest(
		"import-concurrent",
		"fingerprint-concurrent",
		[]byte("{\"result\":\"one-commit\"}\n"),
		clock.Now(),
	)

	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, candidateStore := range []*SQLiteStore{first, second} {
		workers.Go(func() {
			<-start
			results <- candidateStore.ApplyProviderImport(context.Background(), &ProviderImportBundle{
				Receipt: cloneProviderImportReceipt(receipt),
			})
		})
	}
	close(start)
	workers.Wait()
	close(results)

	successes := 0
	replays := 0
	for result := range results {
		switch result {
		case nil:
			successes++
		default:
			var replay *ProviderImportReceiptReplayError
			if !errors.As(result, &replay) {
				t.Fatalf("concurrent ApplyProviderImport() error = %v, want replay", result)
			}
			assertProviderImportReceipt(t, &replay.Receipt, receipt)
			replays++
		}
	}
	if successes != 1 || replays != 1 {
		t.Fatalf("concurrent outcomes = %d success, %d replay; want 1 and 1", successes, replays)
	}
}

func TestProviderImportReceiptValidationAndMissingSemantics(t *testing.T) {
	store := setupTestStore(t)
	if _, err := store.GetProviderImportReceipt(context.Background(), "missing"); !errors.Is(err, ErrProviderImportReceiptNotFound) {
		t.Fatalf("GetProviderImportReceipt(missing) error = %v, want not found", err)
	}
	if _, err := store.GetProviderImportReceipt(context.Background(), "  "); err == nil {
		t.Fatal("GetProviderImportReceipt(blank) error = nil")
	}

	now := time.Now()
	tests := []struct {
		name    string
		receipt *ProviderImportReceipt
	}{
		{name: "blank import ID", receipt: providerImportReceiptForTest(" ", "fingerprint", []byte("{}\n"), now)},
		{name: "blank fingerprint", receipt: providerImportReceiptForTest("import", " ", []byte("{}\n"), now)},
		{name: "empty payload", receipt: providerImportReceiptForTest("import", "fingerprint", nil, now)},
		{name: "expired", receipt: &ProviderImportReceipt{
			ImportID:        "import",
			Fingerprint:     "fingerprint",
			ResponsePayload: []byte("{}\n"),
			ExpiresAt:       now.Add(-time.Minute),
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := store.ApplyProviderImport(context.Background(), &ProviderImportBundle{Receipt: tc.receipt}); err == nil {
				t.Fatal("ApplyProviderImport() error = nil")
			}
		})
	}
}

func TestProviderImportReceiptResponsePayloadLimit(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	now := time.Now()
	boundary := providerImportReceiptForTest(
		"import-payload-boundary",
		"fingerprint-boundary",
		bytes.Repeat([]byte{'x'}, MaxProviderImportReceiptResponsePayloadBytes),
		now,
	)
	if err := store.ApplyProviderImport(ctx, &ProviderImportBundle{Receipt: boundary}); err != nil {
		t.Fatalf("ApplyProviderImport(boundary) error = %v", err)
	}
	stored, err := store.GetProviderImportReceipt(ctx, boundary.ImportID)
	if err != nil {
		t.Fatalf("GetProviderImportReceipt(boundary) error = %v", err)
	}
	if len(stored.ResponsePayload) != MaxProviderImportReceiptResponsePayloadBytes {
		t.Fatalf("stored payload size = %d, want %d", len(stored.ResponsePayload), MaxProviderImportReceiptResponsePayloadBytes)
	}

	oversized := providerImportReceiptForTest(
		"import-payload-oversized",
		"fingerprint-oversized",
		bytes.Repeat([]byte{'x'}, MaxProviderImportReceiptResponsePayloadBytes+1),
		now,
	)
	assertProviderImportReceiptPayloadTooLarge(t, store.ApplyProviderImport(
		ctx,
		&ProviderImportBundle{Receipt: oversized},
	))
	if _, err := store.GetProviderImportReceipt(ctx, oversized.ImportID); !errors.Is(err, ErrProviderImportReceiptNotFound) {
		t.Fatalf("GetProviderImportReceipt(oversized) error = %v, want not found", err)
	}

	directReservation := cloneProviderImportReceipt(oversized)
	directReservation.ImportID = "import-payload-direct-reservation"
	assertProviderImportReceiptPayloadTooLarge(t, reserveProviderImportReceipt(store.db, directReservation, now))
	if _, err := store.GetProviderImportReceipt(ctx, directReservation.ImportID); !errors.Is(err, ErrProviderImportReceiptNotFound) {
		t.Fatalf("GetProviderImportReceipt(direct reservation) error = %v, want not found", err)
	}
}

type providerImportReceiptForwardingStore struct {
	internal.Store

	ctx      context.Context
	importID string
	receipt  *ProviderImportReceipt
	err      error
}

func (s *providerImportReceiptForwardingStore) GetProviderImportReceipt(
	ctx context.Context,
	importID string,
) (*ProviderImportReceipt, error) {
	s.ctx = ctx
	s.importID = importID
	return s.receipt, s.err
}

func TestCachedStoreGetProviderImportReceiptForwardsOrReportsUnsupported(t *testing.T) {
	wantErr := errors.New("receipt unavailable")
	forwarding := &providerImportReceiptForwardingStore{err: wantErr}
	cached := NewCachedStore(CachedStoreConfig{Store: forwarding})
	ctx := context.WithValue(context.Background(), providerImportReceiptContextKey{}, "operation")
	if _, err := cached.GetProviderImportReceipt(ctx, "import-forward"); !errors.Is(err, wantErr) {
		t.Fatalf("GetProviderImportReceipt() error = %v, want %v", err, wantErr)
	}
	if forwarding.ctx != ctx || forwarding.importID != "import-forward" {
		t.Fatalf("forwarded receipt request = (%v, %q)", forwarding.ctx, forwarding.importID)
	}

	unsupported := NewCachedStore(CachedStoreConfig{Store: &credentialMutationUnsupportedStore{}})
	if _, err := unsupported.GetProviderImportReceipt(ctx, "import-forward"); err == nil || !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("unsupported GetProviderImportReceipt() error = %v", err)
	}
}

type providerImportReceiptContextKey struct{}

func providerImportReceiptForTest(
	importID string,
	fingerprint string,
	payload []byte,
	now time.Time,
) *ProviderImportReceipt {
	return &ProviderImportReceipt{
		ImportID:        importID,
		Fingerprint:     fingerprint,
		ResponsePayload: append([]byte(nil), payload...),
		ExpiresAt:       now.Add(time.Hour),
	}
}

func assertProviderImportReceipt(
	t *testing.T,
	got *ProviderImportReceipt,
	want *ProviderImportReceipt,
) {
	t.Helper()
	if got == nil {
		t.Fatal("provider import receipt = nil")
	}
	if got.ImportID != want.ImportID || got.Fingerprint != want.Fingerprint ||
		!got.ExpiresAt.Equal(want.ExpiresAt) || !bytes.Equal(got.ResponsePayload, want.ResponsePayload) {
		t.Fatalf("provider import receipt = %#v, want %#v", got, want)
	}
}

func assertProviderImportReceiptPayloadTooLarge(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrProviderImportReceiptResponsePayloadTooLarge) {
		t.Fatalf("error = %v, want response payload too large sentinel", err)
	}
	var sizeErr *ProviderImportReceiptResponsePayloadTooLargeError
	if !errors.As(err, &sizeErr) {
		t.Fatalf("error = %v, want typed response payload size error", err)
	}
	if sizeErr.SizeBytes != MaxProviderImportReceiptResponsePayloadBytes+1 ||
		sizeErr.LimitBytes != MaxProviderImportReceiptResponsePayloadBytes {
		t.Fatalf("payload size error = %#v, want size %d and limit %d", sizeErr, MaxProviderImportReceiptResponsePayloadBytes+1, MaxProviderImportReceiptResponsePayloadBytes)
	}
}
