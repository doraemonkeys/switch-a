package proxy

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth/codexquota"
	"go.uber.org/zap/zaptest"
)

type recordedProviderUsageObservation struct {
	sessionID string
	snapshot  *model.ProviderUsageSnapshot
}

type recordingProviderUsageObserver struct {
	observed chan recordedProviderUsageObservation
}

func (o *recordingProviderUsageObserver) ObserveCredentialSessionUsage(
	_ context.Context,
	sessionID string,
	snapshot *model.ProviderUsageSnapshot,
) error {
	o.observed <- recordedProviderUsageObservation{sessionID: sessionID, snapshot: snapshot}
	return nil
}

func chatGPTUsageAttempt(t *testing.T, providerID, sessionID string) httpAttemptContext {
	t.Helper()
	subject, err := credentialsession.AccountSubject("acct-usage")
	if err != nil {
		t.Fatal(err)
	}
	finalURL, err := url.Parse("https://chatgpt.com/backend-api/codex/responses")
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := codexidentity.NewAuthorityResolver().Resolve(credentialsession.RouteSnapshot{
		RouteTargetID: providerID, APIType: "codex",
		Credential: credentialsession.Snapshot{
			SessionID: sessionID, Vendor: "openai", Kind: credentialsession.KindChatGPT,
			SecretData: "opaque", Version: 1, Subject: subject,
			AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive},
		},
	}, "codex", finalURL)
	if err != nil {
		t.Fatal(err)
	}
	return httpAttemptContext{provider: &model.Provider{ID: providerID}, candidate: candidate}
}

func TestHandlerCapturesAndPersistsLatestCodexQuotaObservation(t *testing.T) {
	observer := &recordingProviderUsageObserver{observed: make(chan recordedProviderUsageObservation, 1)}
	handler := &Handler{usageObserver: observer, logger: zaptest.NewLogger(t)}
	pctx := &proxyContext{requestID: "request-1"}
	attempt := chatGPTUsageAttempt(t, "provider-1", "session-1")
	firstAt := time.Date(2026, time.August, 4, 4, 0, 0, 0, time.UTC)
	latestAt := firstAt.Add(time.Second)

	handler.captureProviderUsageObservation(pctx, attempt, http.Header{
		codexquota.HeaderPrimaryUsedPercent: {"10"},
	}, firstAt, "operation-1")
	handler.captureProviderUsageObservation(pctx, attempt, http.Header{
		codexquota.HeaderPrimaryUsedPercent: {"20"},
	}, latestAt, "operation-2")
	handler.scheduleProviderUsagePersistence(pctx)

	select {
	case observation := <-observer.observed:
		if observation.sessionID != "session-1" {
			t.Fatalf("sessionID = %q, want session-1", observation.sessionID)
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

	handler.captureProviderUsageObservation(pctx, httpAttemptContext{
		provider: &model.Provider{ID: "api-1"},
	}, header, time.Now(), "operation-1")
	handler.captureProviderUsageObservation(
		pctx,
		chatGPTUsageAttempt(t, "chatgpt-1", "session-1"),
		http.Header{codexquota.HeaderPrimaryUsedPercent: {"invalid"}},
		time.Now(),
		"operation-2",
	)
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
