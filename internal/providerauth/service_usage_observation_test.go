package providerauth

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
	"go.uber.org/zap/zaptest"
)

type usageObservationStore struct {
	mu              sync.Mutex
	session         *credentialsession.Session
	authStateWrites int
	getStarted      chan struct{}
	releaseGet      chan struct{}
	getStartOnce    sync.Once
}

func (s *usageObservationStore) GetCredentialSession(context.Context, string) (*credentialsession.Session, error) {
	if s.getStarted != nil {
		s.getStartOnce.Do(func() { close(s.getStarted) })
	}
	if s.releaseGet != nil {
		<-s.releaseGet
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.session.Clone(), nil
}

func (s *usageObservationStore) WithCredentialSessionMutations(
	ctx context.Context,
	_ []string,
) (context.Context, func(), error) {
	return ctx, func() {}, nil
}

func (s *usageObservationStore) UpdateCredentialSessionCAS(
	context.Context,
	string,
	int64,
	string,
	credentialsession.Subject,
	credentialsession.AuthState,
) (int64, error) {
	return 0, nil
}

func (s *usageObservationStore) UpdateCredentialSessionAuthState(
	_ context.Context,
	_ string,
	state credentialsession.AuthState,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authStateWrites++
	s.session.AuthState = state.Clone()
	return nil
}

func usageSession(kind credentialsession.Kind, usage *model.ProviderUsageSnapshot) *credentialsession.Session {
	status := credentialsession.DefaultAuthStatus(kind)
	if kind == credentialsession.KindChatGPT {
		status = credentialsession.AuthStatusActive
	}
	return &credentialsession.Session{
		ID: "session-1", Kind: kind, SecretData: "opaque", Version: 1,
		AuthState: credentialsession.AuthState{
			Status: status, PlanType: "plus",
			UsageSnapshot: credentialSessionUsageSnapshot(usage),
		},
	}
}

func TestObserveCredentialSessionUsageMergesResponseSnapshotWithoutChangingAuthLifecycle(t *testing.T) {
	currentAt := time.Date(2026, time.August, 4, 4, 0, 0, 0, time.UTC)
	weeklyReset := currentAt.Add(7 * 24 * time.Hour)
	store := &usageObservationStore{session: usageSession(credentialsession.KindChatGPT, &model.ProviderUsageSnapshot{
		FetchedAt: &currentAt, PlanType: "plus",
		OneWeek: &model.ProviderUsageWindow{
			UsedPercent: 20, WindowSeconds: 7 * 24 * 60 * 60, ResetAt: &weeklyReset,
		},
	})}
	service := NewService(Config{CredentialStore: store, Logger: zaptest.NewLogger(t)})
	observedAt := currentAt.Add(time.Minute)
	primaryReset := observedAt.Add(5 * time.Hour)

	err := service.ObserveCredentialSessionUsage(context.Background(), "session-1", &model.ProviderUsageSnapshot{
		FetchedAt: &observedAt, PlanType: "pro",
		FiveHour: &model.ProviderUsageWindow{
			UsedPercent: 35, WindowSeconds: 5 * 60 * 60, ResetAt: &primaryReset,
		},
	})
	if err != nil {
		t.Fatalf("ObserveCredentialSessionUsage error = %v", err)
	}
	if store.authStateWrites != 1 {
		t.Fatalf("auth state writes = %d, want 1", store.authStateWrites)
	}
	state := store.session.AuthState
	if state.Status != credentialsession.AuthStatusActive {
		t.Fatalf("Status = %q, want active", state.Status)
	}
	usage := providerUsageSnapshot(state.UsageSnapshot)
	if state.PlanType != "pro" || usage.PlanType != "pro" {
		t.Fatalf("plan types = (%q, %q), want pro", state.PlanType, usage.PlanType)
	}
	if usage.FiveHour == nil || usage.FiveHour.UsedPercent != 35 {
		t.Fatalf("FiveHour = %#v", usage.FiveHour)
	}
	if usage.OneWeek == nil || usage.OneWeek.UsedPercent != 20 {
		t.Fatalf("OneWeek = %#v, want preserved window", usage.OneWeek)
	}
}

func TestObserveCredentialSessionUsageCoalescesPerSession(t *testing.T) {
	currentAt := time.Date(2026, time.August, 4, 4, 0, 0, 0, time.UTC)
	store := &usageObservationStore{
		session:    usageSession(credentialsession.KindChatGPT, &model.ProviderUsageSnapshot{FetchedAt: &currentAt}),
		getStarted: make(chan struct{}), releaseGet: make(chan struct{}),
	}
	service := NewService(Config{CredentialStore: store, Logger: zaptest.NewLogger(t)})
	firstAt := currentAt.Add(time.Minute)
	secondAt := currentAt.Add(2 * time.Minute)
	latestAt := currentAt.Add(3 * time.Minute)
	done := make(chan error, 1)
	go func() {
		done <- service.ObserveCredentialSessionUsage(context.Background(), "session-1", usageSnapshotAt(firstAt, 10))
	}()
	<-store.getStarted
	if err := service.ObserveCredentialSessionUsage(context.Background(), "session-1", usageSnapshotAt(secondAt, 20)); err != nil {
		t.Fatal(err)
	}
	if err := service.ObserveCredentialSessionUsage(context.Background(), "session-1", usageSnapshotAt(latestAt, 30)); err != nil {
		t.Fatal(err)
	}
	close(store.releaseGet)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if store.authStateWrites != 2 {
		t.Fatalf("auth state writes = %d, want 2", store.authStateWrites)
	}
	usage := providerUsageSnapshot(store.session.AuthState.UsageSnapshot)
	if usage.FetchedAt == nil || !usage.FetchedAt.Equal(latestAt) || usage.FiveHour.UsedPercent != 30 {
		t.Fatalf("final usage = %#v, want latest observation", usage)
	}
}

func TestObserveCredentialSessionUsageIgnoresStaticAndIncompleteObservations(t *testing.T) {
	store := &usageObservationStore{session: usageSession(credentialsession.KindAPIKey, nil)}
	service := NewService(Config{CredentialStore: store, Logger: zaptest.NewLogger(t)})
	now := time.Now()
	for _, snapshot := range []*model.ProviderUsageSnapshot{nil, {}, {FetchedAt: &now}} {
		if err := service.ObserveCredentialSessionUsage(context.Background(), "session-1", snapshot); err != nil {
			t.Fatalf("ObserveCredentialSessionUsage(%#v) error = %v", snapshot, err)
		}
	}
	if store.authStateWrites != 0 {
		t.Fatalf("auth state writes = %d, want 0", store.authStateWrites)
	}
}

func TestMergeObservedProviderUsageRejectsStaleAndTooFrequentSnapshots(t *testing.T) {
	currentAt := time.Date(2026, time.August, 4, 4, 0, 0, 0, time.UTC)
	current := usageSnapshotAt(currentAt, 10)
	tests := []struct {
		name     string
		observed *model.ProviderUsageSnapshot
		reason   string
	}{
		{name: "missing time", observed: &model.ProviderUsageSnapshot{}, reason: "missing_observation_time"},
		{name: "equal", observed: usageSnapshotAt(currentAt, 20), reason: "stale_observation"},
		{name: "older", observed: usageSnapshotAt(currentAt.Add(-time.Minute), 20), reason: "stale_observation"},
		{name: "inside interval", observed: usageSnapshotAt(currentAt.Add(providerUsageObservationMinInterval-time.Second), 20), reason: "observation_interval"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			merged, changed, reason := mergeObservedProviderUsage(current, tt.observed)
			if changed || reason != tt.reason {
				t.Fatalf("changed/reason = (%t, %q), want (false, %q)", changed, reason, tt.reason)
			}
			if merged == current || merged.FiveHour == current.FiveHour {
				t.Fatal("merge returned aliased current snapshot")
			}
		})
	}
}

func usageSnapshotAt(at time.Time, usedPercent float64) *model.ProviderUsageSnapshot {
	return &model.ProviderUsageSnapshot{
		FetchedAt: &at,
		FiveHour:  &model.ProviderUsageWindow{UsedPercent: usedPercent, WindowSeconds: 5 * 60 * 60},
	}
}
