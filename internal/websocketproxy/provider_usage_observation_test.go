package websocketproxy

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth/codexquota"
	"go.uber.org/zap/zaptest"
)

type websocketUsageObservation struct {
	providerID string
	snapshot   *model.ProviderUsageSnapshot
}

type websocketUsageObserver struct {
	observed chan websocketUsageObservation
}

func (o *websocketUsageObserver) ObserveProviderUsage(
	_ context.Context,
	providerID string,
	snapshot *model.ProviderUsageSnapshot,
) error {
	o.observed <- websocketUsageObservation{providerID: providerID, snapshot: snapshot}
	return nil
}

func TestGatewaySchedulesCodexQuotaObservationFromHandshake(t *testing.T) {
	observer := &websocketUsageObserver{observed: make(chan websocketUsageObservation, 1)}
	gateway := &Gateway{usageObserver: observer, logger: zaptest.NewLogger(t)}
	provider := &model.Provider{ID: "provider-1", CredentialType: model.ProviderCredentialTypeChatGPT}
	observedAt := time.Date(2026, time.August, 4, 4, 0, 0, 0, time.UTC)

	gateway.scheduleProviderUsageObservation("request-1", provider, http.Header{
		codexquota.HeaderSecondaryUsedPercent: {"40"},
	}, observedAt)

	select {
	case observation := <-observer.observed:
		if observation.providerID != provider.ID || observation.snapshot.OneWeek == nil {
			t.Fatalf("observation = %#v", observation)
		}
		if observation.snapshot.OneWeek.UsedPercent != 40 {
			t.Fatalf("UsedPercent = %v, want 40", observation.snapshot.OneWeek.UsedPercent)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for WebSocket usage observation")
	}
}

func TestGatewayIgnoresUsageObservationWithoutTrustedWindow(t *testing.T) {
	observer := &websocketUsageObserver{observed: make(chan websocketUsageObservation, 1)}
	gateway := &Gateway{usageObserver: observer, logger: zaptest.NewLogger(t)}
	provider := &model.Provider{ID: "provider-1", CredentialType: model.ProviderCredentialTypeChatGPT}

	gateway.scheduleProviderUsageObservation("request-1", provider, http.Header{
		codexquota.HeaderPlanType: {"plus"},
	}, time.Now())

	select {
	case observation := <-observer.observed:
		t.Fatalf("unexpected observation: %#v", observation)
	case <-time.After(20 * time.Millisecond):
	}
}
