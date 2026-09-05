package store

import (
	"context"
	"maps"
	"sync"
	"testing"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/defaults"
	"github.com/doraemonkeys/switch-a/internal/model"
)

type delayedConfigReadStore struct {
	internal.Store
	mu            sync.Mutex
	values        map[string]string
	readStarted   chan struct{}
	releaseRead   chan struct{}
	delayNextRead bool
}

func (s *delayedConfigReadStore) GetConfig(_ context.Context, key string) (string, error) {
	s.mu.Lock()
	value := s.values[key]
	delay := s.delayNextRead
	s.delayNextRead = false
	s.mu.Unlock()
	if delay {
		close(s.readStarted)
		<-s.releaseRead
	}
	return value, nil
}

func (s *delayedConfigReadStore) SetConfig(ctx context.Context, key, value string) error {
	return s.SetConfigs(ctx, map[string]string{key: value})
}

func (s *delayedConfigReadStore) SetConfigs(_ context.Context, values map[string]string) error {
	s.mu.Lock()
	maps.Copy(s.values, values)
	s.mu.Unlock()
	return nil
}

func (s *delayedConfigReadStore) InitDefaultConfig(ctx context.Context) error {
	return s.SetConfig(ctx, defaults.ConfigKeyConversationRecoveryPolicy, defaults.DefaultConversationRecoveryPolicy)
}

func (s *delayedConfigReadStore) ApplyConfigImport(ctx context.Context, bundle *ConfigImportBundle) error {
	return s.SetConfigs(ctx, bundle.Settings)
}

func TestCachedStore_ConcurrentReadCannotUndoConfigInvalidation(t *testing.T) {
	key := defaults.ConfigKeyConversationRecoveryPolicy
	oldPolicy := string(model.ConversationRecoverySwitchAccountPreserveConversation)
	newPolicy := defaults.DefaultConversationRecoveryPolicy
	for _, method := range []string{"SetConfig", "SetConfigs", "InvalidateConfig", "InvalidateAllConfig", "InitDefaultConfig", "ApplyConfigImport"} {
		t.Run(method, func(t *testing.T) {
			ctx := context.Background()
			source := &delayedConfigReadStore{
				values:        map[string]string{key: oldPolicy},
				readStarted:   make(chan struct{}),
				releaseRead:   make(chan struct{}),
				delayNextRead: true,
			}
			cached := NewCachedStore(CachedStoreConfig{Store: source})
			oldRead := make(chan string, 1)
			go func() {
				value, _ := cached.GetConfig(ctx, key)
				oldRead <- value
			}()
			<-source.readStarted

			var err error
			switch method {
			case "SetConfig":
				err = cached.SetConfig(ctx, key, newPolicy)
			case "SetConfigs":
				err = cached.SetConfigs(ctx, map[string]string{key: newPolicy})
			case "InvalidateConfig":
				err = source.SetConfig(ctx, key, newPolicy)
				cached.InvalidateConfig(key)
			case "InvalidateAllConfig":
				err = source.SetConfig(ctx, key, newPolicy)
				cached.InvalidateAllConfig()
			case "InitDefaultConfig":
				err = cached.InitDefaultConfig(ctx)
			case "ApplyConfigImport":
				err = cached.ApplyConfigImport(ctx, &ConfigImportBundle{Settings: map[string]string{key: newPolicy}})
			}
			if err != nil {
				close(source.releaseRead)
				t.Fatal(err)
			}
			// Fill a fresh entry before the stale read returns to prove that the
			// stale result cannot overwrite a newer request's cache entry either.
			current, currentErr := cached.GetConfig(ctx, key)
			close(source.releaseRead)
			previous := <-oldRead
			if currentErr != nil || current != newPolicy {
				t.Fatalf("new request = %q, %v; want %q", current, currentErr, newPolicy)
			}
			if previous != oldPolicy {
				t.Fatalf("in-flight snapshot = %q; want %q", previous, oldPolicy)
			}
			after, err := cached.GetConfig(ctx, key)
			if err != nil || after != newPolicy {
				t.Fatalf("later request = %q, %v; stale read repopulated cache", after, err)
			}
		})
	}
}
