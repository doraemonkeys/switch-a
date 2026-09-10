package officialversion

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type memoryStore struct {
	state                        State
	enabled                      bool
	readErr, saveErr, enabledErr error
}

func (m *memoryStore) OfficialVersion(context.Context) (State, error) { return m.state, m.readErr }
func (m *memoryStore) SaveOfficialVersion(_ context.Context, s State) error {
	if m.saveErr == nil {
		m.state = s
	}
	return m.saveErr
}
func (m *memoryStore) HasOfficialVersionFollowers(context.Context) (bool, error) {
	return m.enabled, m.enabledErr
}

type fetchFunc func(context.Context) (Release, error)

func (f fetchFunc) Latest(ctx context.Context) (Release, error) { return f(ctx) }
func stable(v string) Release {
	return Release{Version: v, Tag: tagPrefix + v, URL: RepositoryURL + "/releases/tag/" + tagPrefix + v}
}

func TestSyncLifecycle(t *testing.T) {
	now := time.Date(2026, 9, 10, 1, 0, 0, 0, time.UTC)
	repo := &memoryStore{}
	calls := 0
	version := "0.150.0"
	var failure error
	service := NewService(repo, fetchFunc(func(ctx context.Context) (Release, error) {
		calls++
		if _, ok := ctx.Deadline(); !ok {
			t.Error("unbounded fetch")
		}
		return stable(version), failure
	}), nil)
	service.now = func() time.Time { return now }
	if _, err := service.Sync(context.Background(), false); err != nil || calls != 0 {
		t.Fatal(err, calls)
	}
	repo.enabled = true
	got, err := service.Sync(context.Background(), false)
	if err != nil || got.Release.Version != version || calls != 1 {
		t.Fatal(got, err, calls)
	}
	now = now.Add(time.Hour)
	if _, err := service.Sync(context.Background(), false); err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
	version = "0.149.0"
	got, err = service.Sync(context.Background(), true)
	if err != nil || got.Release.Version != "0.150.0" {
		t.Fatal(got, err)
	}
	failure = errors.New("GitHub rate limit")
	got, err = service.Sync(context.Background(), true)
	if !errors.Is(err, failure) || got.LastError == "" || got.Release.Version != "0.150.0" {
		t.Fatal(got, err)
	}
	before := calls
	now = now.Add(RetryInterval - time.Second)
	_, _ = service.Sync(context.Background(), false)
	if calls != before {
		t.Fatal(calls)
	}
	now = now.Add(time.Second)
	failure = nil
	version = "0.151.0"
	got, err = service.Sync(context.Background(), false)
	if err != nil || got.LastError != "" || got.Release.Version != version || calls != before+1 {
		t.Fatal(got, err, calls)
	}
	// A new service instance sees the persisted check and does not refetch on restart.
	restarted := NewService(repo, service.fetcher, nil)
	restarted.now = service.now
	_, _ = restarted.Sync(context.Background(), false)
	if calls != before+1 {
		t.Fatal(calls)
	}
}
func TestSyncFailures(t *testing.T) {
	failure := errors.New("database failure")
	for _, field := range []string{"read", "save", "enabled", "invalid"} {
		t.Run(field, func(t *testing.T) {
			repo := &memoryStore{enabled: true}
			fetcher := fetchFunc(func(context.Context) (Release, error) { return stable("0.150.0"), nil })
			switch field {
			case "read":
				repo.readErr = failure
			case "save":
				repo.saveErr = failure
			case "enabled":
				repo.enabledErr = failure
			case "invalid":
				fetcher = func(context.Context) (Release, error) { return stable("0.150.0-alpha.1"), nil }
			}
			_, err := NewService(repo, fetcher, nil).Sync(context.Background(), false)
			if err == nil {
				t.Fatal("missing failure")
			}
		})
	}
}
func TestSyncSerializesManualAndBackgroundChecks(t *testing.T) {
	repo := &memoryStore{enabled: true}
	entered, finish := make(chan struct{}), make(chan struct{})
	calls := 0
	s := NewService(repo, fetchFunc(func(context.Context) (Release, error) {
		calls++
		close(entered)
		<-finish
		return stable("0.150.0"), nil
	}), nil)
	var wg sync.WaitGroup
	wg.Go(func() { _, _ = s.Sync(context.Background(), true) })
	<-entered
	wg.Go(func() { _, _ = s.Sync(context.Background(), false) })
	close(finish)
	wg.Wait()
	if calls != 1 {
		t.Fatal(calls)
	}
	state, err := s.State(context.Background())
	if err != nil || state.Release.Version != "0.150.0" {
		t.Fatal(state, err)
	}
}
func TestRunStopsAndPollsOnlyWithFollowers(t *testing.T) {
	repo := &memoryStore{enabled: true}
	entered := make(chan struct{})
	s := NewService(repo, fetchFunc(func(ctx context.Context) (Release, error) { close(entered); <-ctx.Done(); return Release{}, ctx.Err() }), nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()
	<-entered
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sync worker did not stop")
	}
}
