package providerimport

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/providerauth"
)

func TestCommitProviderImportReceiptExpiresAtConfiguredTTL(t *testing.T) {
	candidate := providerImportReadyCandidate(
		"candidate-create",
		"account-create",
		"Expiring Receipt",
		providerImportStoredCredentialMarker,
	)
	service := &fakeProviderImportService{candidates: []providerauth.ChatGPTProviderImportCandidate{candidate}}
	importStore := &fakeProviderImportStore{}
	handler := newProviderImportTestHandler(newMockStore(), service, importStore)
	clock := &providerImportTestClock{now: time.Date(2026, time.July, 30, 15, 0, 0, 0, time.UTC)}
	const receiptTTL = 2 * time.Minute
	handler.providerImportReceipts = newProviderImportCommitReceiptRegistry(clock, receiptTTL)
	importStore.receiptClock = clock
	requestBody := `{
		"items":[{"candidate_id":"candidate-create","action":"create","provider_id":"new-provider","name":"Expiring Receipt","priority":0,"concurrency":0}]
	}`

	firstResponse := commitProviderImportRequest(t, handler, "expiring-receipt", requestBody)
	requireProviderImportStatus(t, firstResponse, http.StatusOK)

	// At the exact boundary the receipt is expired. The real draft has also been
	// consumed, so the handler must consult the source service rather than replay
	// stale success, and must not issue another durable write.
	clock.now = clock.now.Add(receiptTTL)
	service.claimErr = providerauth.ErrChatGPTProviderImportNotFound
	expiredResponse := commitProviderImportRequest(t, handler, "expiring-receipt", requestBody)

	requireProviderImportStatus(t, expiredResponse, http.StatusGone)
	assertProviderImportError(t, expiredResponse, ErrCodeConflict, "preview")
	if service.claimCalls != 2 || len(importStore.bundles) != 1 || len(service.finalizeCalls) != 1 {
		t.Fatalf(
			"post-TTL calls = (%d claim, %d apply, %d finalize), want receipt miss without duplicate persistence",
			service.claimCalls,
			len(importStore.bundles),
			len(service.finalizeCalls),
		)
	}
}

func TestProviderImportCommitRegistryWaitsForFailedOwnerBeforeChangingRequest(t *testing.T) {
	registry := newProviderImportCommitReceiptRegistry(nil, providerImportCommitReceiptTTL)
	if err := registry.acquire(context.Background(), "import", "first"); err != nil {
		t.Fatalf("acquire first owner: %v", err)
	}
	waiterStarted := make(chan struct{})
	waiterResult := make(chan error, 1)
	go func() {
		close(waiterStarted)
		waiterResult <- registry.acquire(context.Background(), "import", "second")
	}()
	<-waiterStarted
	select {
	case err := <-waiterResult:
		t.Fatalf("different request returned before owner outcome: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	registry.abort("import", "first")
	select {
	case err := <-waiterResult:
		if err != nil {
			t.Fatalf("acquire after failed owner: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter did not acquire after failed owner released import")
	}
	registry.abort("import", "second")
}

func TestProviderImportCommitRegistryDropsCompletedEntry(t *testing.T) {
	registry := newProviderImportCommitReceiptRegistry(nil, providerImportCommitReceiptTTL)
	if err := registry.acquire(context.Background(), "import", "fingerprint"); err != nil {
		t.Fatalf("acquire owner: %v", err)
	}
	registry.complete("import", "fingerprint")
	registry.mu.Lock()
	entryCount := len(registry.entries)
	registry.mu.Unlock()
	if entryCount != 0 {
		t.Fatalf("completed entries retained in process = %d, want 0", entryCount)
	}
}
