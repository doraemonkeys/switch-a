package providerimport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/providerauth"
	"github.com/doraemonkeys/switch-a/internal/store"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestProviderImportReceiptRegistrySerializesAndHandsOff(t *testing.T) {
	t.Parallel()
	registry := newProviderImportCommitReceiptRegistry(nil, 0)
	if registry.ttl != providerImportCommitReceiptTTL {
		t.Fatalf("default ttl = %s", registry.ttl)
	}
	if err := registry.acquire(context.Background(), "import", "first"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := registry.acquire(ctx, "import", "second"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter error = %v", err)
	}

	acquired := make(chan struct{})
	go func() {
		if err := registry.acquire(context.Background(), "import", "second"); err == nil {
			close(acquired)
		}
	}()
	registry.abort("import", "wrong")
	select {
	case <-acquired:
		t.Fatal("mismatched abort released the owner")
	case <-time.After(10 * time.Millisecond):
	}
	registry.abort("import", "first")
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("waiter did not acquire after abort")
	}
	registry.complete("import", "wrong")
	registry.complete("import", "second")
	registry.complete("missing", "second")
	registry.abort("missing", "second")
}

func TestCommitProviderImportWaiterHonorsRequestCancellation(t *testing.T) {
	t.Parallel()
	h := newProviderImportTestHandler(nil, &providerImportTestDrafts{}, &providerImportTestStore{}, nil, nil)
	requestBody := `{"items":[{"candidate_id":"candidate","action":"update","provider_id":"provider"}]}`
	request := providerImportTestCommitRequest(t, "busy-import", requestBody)
	var normalized ProviderImportCommitRequest
	if err := jsonDecodeProviderImportRequest(requestBody, &normalized); err != nil {
		t.Fatal(err)
	}
	if err := validateProviderImportCommitRequest(&normalized); err != nil {
		t.Fatal(err)
	}
	fingerprint := providerImportCommitRequestFingerprint(normalized)
	if err := h.providerImportReceipts.acquire(context.Background(), "busy-import", fingerprint); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(request.Context())
	cancel()
	request = request.WithContext(ctx)
	w := httptest.NewRecorder()
	h.CommitProviderImport(w, request)
	requireProviderImportStatus(t, w, http.StatusRequestTimeout)
	h.providerImportReceipts.abort("busy-import", fingerprint)
}

func TestCommitProviderImportRechecksDurableReceiptAfterAcquiringOwnership(t *testing.T) {
	t.Parallel()
	weight := DefaultWeight
	retries := DefaultProviderMaxRetries
	backoff := defaultProviderImportCreateSettings().Backoff
	req := ProviderImportCommitRequest{Items: []ProviderImportCommitItem{{
		CandidateID: "candidate", Action: providerImportActionCreate, ProviderID: "provider", Name: "Provider",
		Weight: &weight, MaxRetries: &retries, Backoff: &backoff,
	}}}
	fingerprint := providerImportCommitRequestFingerprint(req)
	payload := []byte(`{"import_id":"recheck"}` + "\n")
	importStore := &providerImportTestStore{receiptFunc: func(importID string, call int) (*store.ProviderImportReceipt, error) {
		if call == 1 {
			return nil, store.ErrProviderImportReceiptNotFound
		}
		return &store.ProviderImportReceipt{ImportID: importID, Fingerprint: fingerprint, ResponsePayload: payload}, nil
	}}
	h := newProviderImportTestHandler(nil, &providerImportTestDrafts{}, importStore, nil, zap.NewNop())
	w := httptest.NewRecorder()
	h.CommitProviderImport(w, providerImportTestCommitRequest(t, "recheck", `{"items":[{"candidate_id":"candidate","action":"create","provider_id":"provider","name":"Provider"}]}`))
	requireProviderImportStatus(t, w, http.StatusOK)
	if w.Body.String() != string(payload) || importStore.receiptCalls != 2 {
		t.Fatalf("payload=%q receipt calls=%d", w.Body.String(), importStore.receiptCalls)
	}
}

func TestCommitProviderImportReturnsPreviewConflictBeforeVerification(t *testing.T) {
	t.Parallel()
	candidate := providerImportTestCandidate(t, "candidate", "account", `{"access_token":"new"}`, providerauth.ChatGPTProviderImportCandidateDisposition{
		State: providerauth.ChatGPTProviderImportCandidateStateExisting, ExpectedSessionID: "missing", ExpectedCredentialVersion: 1,
	})
	drafts := &providerImportTestDrafts{candidates: []providerauth.ChatGPTProviderImportCandidate{candidate}}
	h := newProviderImportTestHandler(&providerImportTestCatalog{}, drafts, &providerImportTestStore{}, nil, zap.NewNop())
	w := httptest.NewRecorder()
	h.CommitProviderImport(w, providerImportTestCommitRequest(t, "conflict", `{"items":[{"candidate_id":"candidate","action":"update","provider_id":"missing"}]}`))
	requireProviderImportStatus(t, w, http.StatusConflict)
	if len(drafts.verified) != 0 || !strings.Contains(w.Body.String(), "provider_not_found") {
		t.Fatalf("verified=%d body=%s", len(drafts.verified), w.Body.String())
	}
}

func TestProviderImportSupportEdges(t *testing.T) {
	t.Parallel()
	if providerImportCredentialMutationIDs(nil) != nil {
		t.Fatal("nil bundle returned mutation IDs")
	}
	h := &Handler{}
	if !h.tryAcquireProviderImportBodyRead() {
		t.Fatal("nil read gate should admit")
	}
	h.cancelProviderImportAfterPreviewFailure("")
	h.cancelProviderImportAfterPreviewFailure("ignored")
	h.logProviderImportError("ignored", "", errors.New("ignored"))
	h.logProviderImportCommitted("ignored", ProviderImportCommitSummary{}, time.Now())

	deadlineErr := &providerImportBodyReadDeadlineError{cause: errors.New("unsupported")}
	if !strings.Contains(deadlineErr.Error(), "unsupported") || !errors.Is(deadlineErr, errProviderImportBodyReadDeadlineUnavailable) {
		t.Fatalf("deadline error = %v", deadlineErr)
	}
	if boundedProviderImportText("value", 0) != "" {
		t.Fatal("zero text bound accepted content")
	}
	if providerImportItemMessage(providerauth.ChatGPTProviderImportCandidateStateInvalid, nil) == "" {
		t.Fatal("invalid item lacked recovery message")
	}
	if providerImportUnreservedID("", &providerauth.ProviderAuthView{Email: " User+tag@example.com "}) != "user-tag" {
		t.Fatal("email local-part fallback was not canonicalized")
	}
	if providerImportUnreservedID("", nil) != providerImportFallbackID {
		t.Fatal("anonymous fallback ID changed")
	}

	allocator := newProviderImportIDAllocator(nil, 1)
	if allocator.allocate("Fresh", nil) != "fresh" || allocator.allocate("Fresh", nil) != "fresh-2" {
		t.Fatal("allocator did not reserve and suffix a fresh ID")
	}
	longBase := strings.Repeat("a", maxProviderImportGeneratedIDBaseLength+10)
	if got := allocator.allocate(longBase, nil); len(got) != maxProviderImportGeneratedIDBaseLength {
		t.Fatalf("bounded generated ID length = %d", len(got))
	}
}

func TestReadProviderImportBodyDefaultsAndLogsClearFailure(t *testing.T) {
	t.Parallel()
	core, logs := observer.New(zap.DebugLevel)
	h := &Handler{logger: zap.New(core)}
	var mu sync.Mutex
	deadlines := make([]time.Time, 0, 2)
	h.providerImportSetReadDeadline = func(_ http.ResponseWriter, deadline time.Time) error {
		mu.Lock()
		defer mu.Unlock()
		deadlines = append(deadlines, deadline)
		if deadline.IsZero() {
			return errors.New("clear failed")
		}
		return nil
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("payload"))
	raw, err := h.readProviderImportBody(w, r)
	if err != nil || string(raw) != "payload" || len(deadlines) != 2 || logs.FilterMessage("failed to clear provider import body read deadline").Len() != 1 {
		t.Fatalf("raw=%q err=%v deadlines=%v logs=%v", raw, err, deadlines, logs.All())
	}
}

func TestProviderImportFingerprintComparatorTies(t *testing.T) {
	t.Parallel()
	weight, retries := 1, 1
	backoff := defaultProviderImportCreateSettings().Backoff
	items := []ProviderImportCommitItem{
		{CandidateID: "same", Action: providerImportActionUpdate, ProviderID: "z"},
		{CandidateID: "same", Action: providerImportActionCreate, ProviderID: "b", Name: "B", Weight: &weight, MaxRetries: &retries, Backoff: &backoff},
		{CandidateID: "same", Action: providerImportActionCreate, ProviderID: "a", Name: "A", Weight: &weight, MaxRetries: &retries, Backoff: &backoff},
	}
	if len(providerImportCommitRequestFingerprint(ProviderImportCommitRequest{Items: items})) != 64 {
		t.Fatal("fingerprint was not emitted")
	}
}

func jsonDecodeProviderImportRequest(raw string, target *ProviderImportCommitRequest) error {
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(raw))
	return decodeProviderImportCommitRequest(httptest.NewRecorder(), request, target)
}
