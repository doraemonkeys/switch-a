package proxy

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"

	"go.uber.org/zap"
)

func TestCaptureCompletionTimestampDoesNotCrossNextPhysicalAttempt(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("failed first attempt"))
			return
		}
		// Make the distinction deterministic: the first exchange physically ended
		// before this second exchange even began, while deferred publication waits
		// until the whole gateway request returns.
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("retry succeeded"))
	}))
	defer upstream.Close()

	provider := captureTestProvider(upstream.URL)
	provider.MaxRetries = 1
	manager, session := startCaptureTestManager(t, []requestcapture.ProviderIdentity{{
		ID: provider.ID, Name: provider.Name,
	}})
	defer manager.Close()
	store := newMockStore()
	store.providers = []model.Provider{provider}
	handler := newProxyCodexTestHandler(t, Config{Store: store, Capture: manager, Logger: zap.NewNop()})

	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-3"}`))
	handler.ServeHTTP(httptest.NewRecorder(), request)
	page, err := readCaptureTestPage(manager, session, requestcapture.ListQuery{Limit: 10})
	if err != nil || len(page.Records) != 2 {
		t.Fatalf("records = %#v, error = %v", page.Records, err)
	}
	sort.Slice(page.Records, func(i, j int) bool {
		return page.Records[i].ExchangeIndex < page.Records[j].ExchangeIndex
	})
	first, retry := page.Records[0], page.Records[1]
	if first.CompletedAt == nil {
		t.Fatal("first exchange has no completion timestamp")
	}
	if first.CompletedAt.After(retry.StartedAt) {
		t.Fatalf("first physical completion = %v, retry start = %v; deferred Finish collapsed exchange timing", *first.CompletedAt, retry.StartedAt)
	}
}
