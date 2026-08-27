package websocketproxy

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth/codexquota"
	"go.uber.org/zap/zaptest"
)

type websocketUsageObservation struct {
	sessionID string
	snapshot  *model.ProviderUsageSnapshot
}

type websocketUsageObserver struct {
	observed chan websocketUsageObservation
}

func (o *websocketUsageObserver) ObserveCredentialSessionUsage(
	_ context.Context,
	sessionID string,
	snapshot *model.ProviderUsageSnapshot,
) error {
	o.observed <- websocketUsageObservation{sessionID: sessionID, snapshot: snapshot}
	return nil
}

func TestGatewaySchedulesCodexQuotaObservationFromHandshake(t *testing.T) {
	observer := &websocketUsageObserver{observed: make(chan websocketUsageObservation, 1)}
	gateway := &Gateway{usageObserver: observer, logger: zaptest.NewLogger(t)}
	provider := &model.Provider{ID: "provider-1"}
	credential := credentialsession.Snapshot{SessionID: "credential-1", Kind: credentialsession.KindChatGPT}
	observedAt := time.Date(2026, time.August, 4, 4, 0, 0, 0, time.UTC)

	gateway.scheduleProviderUsageObservation("request-1", provider, credential, http.Header{
		codexquota.HeaderSecondaryUsedPercent: {"40"},
	}, observedAt)

	select {
	case observation := <-observer.observed:
		if observation.sessionID != credential.SessionID || observation.snapshot.OneWeek == nil {
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
	provider := &model.Provider{ID: "provider-1"}
	credential := credentialsession.Snapshot{SessionID: "credential-1", Kind: credentialsession.KindChatGPT}

	gateway.scheduleProviderUsageObservation("request-1", provider, credential, http.Header{
		codexquota.HeaderPlanType: {"plus"},
	}, time.Now())

	select {
	case observation := <-observer.observed:
		t.Fatalf("unexpected observation: %#v", observation)
	case <-time.After(20 * time.Millisecond):
	}
}
