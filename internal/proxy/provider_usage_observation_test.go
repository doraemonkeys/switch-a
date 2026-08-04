package proxy

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth/codexquota"
	"go.uber.org/zap/zaptest"
)

type recordedProviderUsageObservation struct {
	providerID string
	snapshot   *model.ProviderUsageSnapshot
}

type recordingProviderUsageObserver struct {
	observed chan recordedProviderUsageObservation
}

func (o *recordingProviderUsageObserver) ObserveProviderUsage(
	_ context.Context,
	providerID string,
	snapshot *model.ProviderUsageSnapshot,
) error {
	o.observed <- recordedProviderUsageObservation{providerID: providerID, snapshot: snapshot}
	return nil
}

func TestHandlerCapturesAndPersistsLatestCodexQuotaObservation(t *testing.T) {
	observer := &recordingProviderUsageObserver{observed: make(chan recordedProviderUsageObservation, 1)}
	handler := &Handler{usageObserver: observer, logger: zaptest.NewLogger(t)}
	pctx := &proxyContext{requestID: "request-1"}
	provider := &model.Provider{ID: "provider-1", CredentialType: model.ProviderCredentialTypeChatGPT}
	firstAt := time.Date(2026, time.August, 4, 4, 0, 0, 0, time.UTC)
	latestAt := firstAt.Add(time.Second)

	handler.captureProviderUsageObservation(pctx, provider, http.Header{
		codexquota.HeaderPrimaryUsedPercent: {"10"},
	}, firstAt, "operation-1")
	handler.captureProviderUsageObservation(pctx, provider, http.Header{
		codexquota.HeaderPrimaryUsedPercent: {"20"},
	}, latestAt, "operation-2")
	handler.scheduleProviderUsagePersistence(pctx)

	select {
	case observation := <-observer.observed:
		if observation.providerID != provider.ID {
			t.Fatalf("providerID = %q, want %q", observation.providerID, provider.ID)
		}
		if observation.snapshot.FetchedAt == nil || !observation.snapshot.FetchedAt.Equal(latestAt) {
			t.Fatalf("FetchedAt = %v, want %v", observation.snapshot.FetchedAt, latestAt)
		}
		if observation.snapshot.FiveHour == nil || observation.snapshot.FiveHour.UsedPercent != 20 {
			t.Fatalf("FiveHour = %#v", observation.snapshot.FiveHour)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for usage observation")
	}
}

func TestHandlerIgnoresQuotaHeadersForUnmanagedProviders(t *testing.T) {
	observer := &recordingProviderUsageObserver{observed: make(chan recordedProviderUsageObservation, 1)}
	handler := &Handler{usageObserver: observer, logger: zaptest.NewLogger(t)}
	pctx := &proxyContext{requestID: "request-1"}
	header := http.Header{codexquota.HeaderPrimaryUsedPercent: {"10"}}

	handler.captureProviderUsageObservation(pctx, &model.Provider{
		ID: "api-1", CredentialType: model.ProviderCredentialTypeAPIKey,
	}, header, time.Now(), "operation-1")
	handler.captureProviderUsageObservation(pctx, &model.Provider{
		ID: "chatgpt-1", CredentialType: model.ProviderCredentialTypeChatGPT,
	}, http.Header{codexquota.HeaderPrimaryUsedPercent: {"invalid"}}, time.Now(), "operation-2")
	handler.scheduleProviderUsagePersistence(pctx)

	select {
	case observation := <-observer.observed:
		t.Fatalf("unexpected observation: %#v", observation)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestNewHandlerWiresProviderUsageObserver(t *testing.T) {
	observer := &recordingProviderUsageObserver{observed: make(chan recordedProviderUsageObservation, 1)}
	handler := NewHandler(Config{
		Store: newMockStore(), Logger: zaptest.NewLogger(t), UsageObserver: observer,
	})
	if handler.usageObserver != observer {
		t.Fatal("NewHandler did not retain UsageObserver")
	}
}
