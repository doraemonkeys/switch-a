package selector

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"go.uber.org/zap"
)

func TestSelector_RetireProviderGeneration(t *testing.T) {
	store := newMockStore()
	clock := &mockClock{now: time.Now()}
	limiter := NewConcurrencyLimiter()
	logger := zap.NewNop()

	sel := NewSelector(Config{
		Store:   store,
		Limiter: limiter,
		Clock:   clock,
		Logger:  logger,
	})

	// The mutation boundary retires the old capability before a replacement can
	// be acquired, so stale releases cannot affect the replacement generation.
	oldLease, acquired := limiter.Acquire("p1", 5)
	if !acquired {
		t.Fatal("failed to acquire original generation")
	}

	mutationCalled := false
	if err := sel.RetireProviderGeneration("p1", func() error {
		mutationCalled = true
		return nil
	}); err != nil {
		t.Fatalf("RetireProviderGeneration() error = %v", err)
	}
	if !mutationCalled {
		t.Fatal("lifecycle mutation was not executed")
	}

	newLease, acquired := limiter.Acquire("p1", 1)
	if !acquired {
		t.Error("expected to acquire slot after retirement")
	}
	if newLease.Generation() == oldLease.Generation() {
		t.Fatalf("generation = %d, want replacement for retired generation", newLease.Generation())
	}
	oldLease.Release()
	newLease.Release()
}

func TestSelector_RetireProviderGeneration_NoLimiter(t *testing.T) {
	store := newMockStore()
	clock := &mockClock{now: time.Now()}
	logger := zap.NewNop()

	sel := NewSelector(Config{
		Store:   store,
		Limiter: nil,
		Clock:   clock,
		Logger:  logger,
	})

	mutationCalled := false
	if err := sel.RetireProviderGeneration("p1", func() error {
		mutationCalled = true
		return nil
	}); err != nil {
		t.Fatalf("RetireProviderGeneration() error = %v", err)
	}
	if !mutationCalled {
		t.Fatal("lifecycle mutation was not executed without a limiter")
	}
}

func TestSelector_RetireAllProviderGenerationsPersistsRetirementOnMutationFailure(t *testing.T) {
	limiter := NewConcurrencyLimiter()
	selector := NewSelector(Config{Store: newMockStore(), Limiter: limiter, Logger: zap.NewNop()})
	first, firstAcquired := limiter.Acquire("p1", 1)
	second, secondAcquired := limiter.Acquire("p2", 1)
	if !firstAcquired || !secondAcquired {
		t.Fatal("failed to acquire original provider generations")
	}
	defer first.Release()
	defer second.Release()

	wantErr := errors.New("persistence failed")
	if err := selector.RetireAllProviderGenerations(func() error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("RetireAllProviderGenerations() error = %v, want %v", err, wantErr)
	}

	replacementFirst, acquired := limiter.Acquire("p1", 1)
	if !acquired {
		t.Fatal("failed to acquire first replacement generation")
	}
	defer replacementFirst.Release()
	replacementSecond, acquired := limiter.Acquire("p2", 1)
	if !acquired {
		t.Fatal("failed to acquire second replacement generation")
	}
	defer replacementSecond.Release()
	if replacementFirst.Generation() == first.Generation() || replacementSecond.Generation() == second.Generation() {
		t.Fatal("failed mutation restored an authorization generation")
	}
}

func TestSelector_SelectFromGroup_ConcurrencyRetry(t *testing.T) {
	groupID := "g1"
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "p1", Name: "Provider 1", Enabled: true, GroupID: &groupID, Priority: 1, Concurrency: 1, APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude"}}},
		{ID: "p2", Name: "Provider 2", Enabled: true, GroupID: &groupID, Priority: 2, Concurrency: 1, APITypes: []model.ProviderAPIType{{ProviderID: "p2", APIType: "claude"}}},
	}
	store.groups = map[string]*model.Group{
		"g1": {ID: "g1", Name: "Group 1", Strategy: "priority", Priority: 1, Enabled: true},
	}

	clock := &mockClock{now: time.Now()}
	limiter := NewConcurrencyLimiter()
	heldLease, acquired := limiter.Acquire("p1", 1)
	if !acquired {
		t.Fatal("failed to prefill p1 concurrency")
	}
	t.Cleanup(func() { heldLease.Release() })

	sel := NewSelector(Config{
		Store:   store,
		Limiter: limiter,
		Clock:   clock,
		Logger:  zap.NewNop(),
	})
	req := &model.SelectRequest{
		ClientIP: "192.168.1.1",
		User:     "user1",
		APIType:  "claude",
	}

	provider, err := sel.selectForTest(t, context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.ID != "p2" {
		t.Errorf("expected p2 (p1 at limit), got %s", provider.ID)
	}
}
