package store

import (
	"context"
	"errors"
	"testing"

	"switch-a/internal"
	"switch-a/internal/model"
)

type routingPolicyPassthroughStore struct {
	internal.Store
	listCalls   int
	getCalls    int
	createCalls int
	updateCalls int
	deleteCalls int
	gotAPIType  string
	gotID       uint
	gotPolicy   *model.RoutingPolicy
	listResult  []model.RoutingPolicy
	getResult   *model.RoutingPolicy
	err         error
}

func (s *routingPolicyPassthroughStore) ListRoutingPoliciesByAPIType(
	_ context.Context,
	apiType string,
) ([]model.RoutingPolicy, error) {
	s.listCalls++
	s.gotAPIType = apiType
	return s.listResult, s.err
}

func (s *routingPolicyPassthroughStore) ListRoutingPolicies(_ context.Context) ([]model.RoutingPolicy, error) {
	s.listCalls++
	return s.listResult, s.err
}

func (s *routingPolicyPassthroughStore) GetRoutingPolicy(_ context.Context, id uint) (*model.RoutingPolicy, error) {
	s.getCalls++
	s.gotID = id
	return s.getResult, s.err
}

func (s *routingPolicyPassthroughStore) CreateRoutingPolicy(_ context.Context, policy *model.RoutingPolicy) error {
	s.createCalls++
	s.gotPolicy = policy
	return s.err
}

func (s *routingPolicyPassthroughStore) UpdateRoutingPolicy(_ context.Context, policy *model.RoutingPolicy) error {
	s.updateCalls++
	s.gotPolicy = policy
	return s.err
}

func (s *routingPolicyPassthroughStore) DeleteRoutingPolicy(_ context.Context, id uint) error {
	s.deleteCalls++
	s.gotID = id
	return s.err
}

func TestCachedStore_RoutingPolicyMethodsPassthrough(t *testing.T) {
	t.Parallel()

	stub := &routingPolicyPassthroughStore{
		listResult: []model.RoutingPolicy{{ID: 1, APIType: "codex"}},
		getResult:  &model.RoutingPolicy{ID: 2, APIType: "claude"},
	}
	cached := NewCachedStore(CachedStoreConfig{Store: stub})
	ctx := context.Background()
	policy := &model.RoutingPolicy{ID: 3, APIType: "codex"}

	policies, err := cached.ListRoutingPoliciesByAPIType(ctx, "codex")
	if err != nil {
		t.Fatalf("ListRoutingPoliciesByAPIType error = %v", err)
	}
	if len(policies) != 1 || policies[0].APIType != "codex" {
		t.Fatalf("ListRoutingPoliciesByAPIType = %+v, want codex policy", policies)
	}

	policies, err = cached.ListRoutingPolicies(ctx)
	if err != nil {
		t.Fatalf("ListRoutingPolicies error = %v", err)
	}
	if len(policies) != 1 || policies[0].ID != 1 {
		t.Fatalf("ListRoutingPolicies = %+v, want policy ID 1", policies)
	}

	got, err := cached.GetRoutingPolicy(ctx, 2)
	if err != nil {
		t.Fatalf("GetRoutingPolicy error = %v", err)
	}
	if got == nil || got.APIType != "claude" {
		t.Fatalf("GetRoutingPolicy = %+v, want claude policy", got)
	}

	if err := cached.CreateRoutingPolicy(ctx, policy); err != nil {
		t.Fatalf("CreateRoutingPolicy error = %v", err)
	}
	if err := cached.UpdateRoutingPolicy(ctx, policy); err != nil {
		t.Fatalf("UpdateRoutingPolicy error = %v", err)
	}
	if err := cached.DeleteRoutingPolicy(ctx, 3); err != nil {
		t.Fatalf("DeleteRoutingPolicy error = %v", err)
	}

	if stub.listCalls != 2 {
		t.Fatalf("list calls = %d, want 2", stub.listCalls)
	}
	if stub.getCalls != 1 {
		t.Fatalf("get calls = %d, want 1", stub.getCalls)
	}
	if stub.createCalls != 1 || stub.updateCalls != 1 || stub.deleteCalls != 1 {
		t.Fatalf("calls = create:%d update:%d delete:%d, want 1 each", stub.createCalls, stub.updateCalls, stub.deleteCalls)
	}
	if stub.gotAPIType != "codex" {
		t.Fatalf("api type = %q, want codex", stub.gotAPIType)
	}
	if stub.gotPolicy != policy {
		t.Fatalf("policy pointer = %p, want %p", stub.gotPolicy, policy)
	}
	if stub.gotID != 3 {
		t.Fatalf("routing policy id = %d, want 3", stub.gotID)
	}
}

func TestCachedStore_RoutingPolicyMethodsReturnNilWhenUnsupported(t *testing.T) {
	t.Parallel()

	cached := NewCachedStore(CachedStoreConfig{
		Store: &configOnlyStore{configs: map[string]string{}},
	})
	ctx := context.Background()

	policies, err := cached.ListRoutingPoliciesByAPIType(ctx, "codex")
	if err != nil {
		t.Fatalf("ListRoutingPoliciesByAPIType error = %v", err)
	}
	if policies != nil {
		t.Fatalf("ListRoutingPoliciesByAPIType = %+v, want nil", policies)
	}

	policies, err = cached.ListRoutingPolicies(ctx)
	if err != nil {
		t.Fatalf("ListRoutingPolicies error = %v", err)
	}
	if policies != nil {
		t.Fatalf("ListRoutingPolicies = %+v, want nil", policies)
	}

	policy, err := cached.GetRoutingPolicy(ctx, 1)
	if err != nil {
		t.Fatalf("GetRoutingPolicy error = %v", err)
	}
	if policy != nil {
		t.Fatalf("GetRoutingPolicy = %+v, want nil", policy)
	}

	if err := cached.CreateRoutingPolicy(ctx, &model.RoutingPolicy{ID: 1}); err != nil {
		t.Fatalf("CreateRoutingPolicy error = %v, want nil", err)
	}
	if err := cached.UpdateRoutingPolicy(ctx, &model.RoutingPolicy{ID: 1}); err != nil {
		t.Fatalf("UpdateRoutingPolicy error = %v, want nil", err)
	}
	if err := cached.DeleteRoutingPolicy(ctx, 1); err != nil {
		t.Fatalf("DeleteRoutingPolicy error = %v, want nil", err)
	}
}

func TestCachedStore_RoutingPolicyMethodsReturnUnderlyingError(t *testing.T) {
	t.Parallel()

	expected := errors.New("routing failed")
	stub := &routingPolicyPassthroughStore{err: expected}
	cached := NewCachedStore(CachedStoreConfig{Store: stub})
	ctx := context.Background()

	if _, err := cached.ListRoutingPoliciesByAPIType(ctx, "codex"); !errors.Is(err, expected) {
		t.Fatalf("ListRoutingPoliciesByAPIType error = %v, want %v", err, expected)
	}
	if _, err := cached.ListRoutingPolicies(ctx); !errors.Is(err, expected) {
		t.Fatalf("ListRoutingPolicies error = %v, want %v", err, expected)
	}
	if _, err := cached.GetRoutingPolicy(ctx, 1); !errors.Is(err, expected) {
		t.Fatalf("GetRoutingPolicy error = %v, want %v", err, expected)
	}
	if err := cached.CreateRoutingPolicy(ctx, &model.RoutingPolicy{ID: 1}); !errors.Is(err, expected) {
		t.Fatalf("CreateRoutingPolicy error = %v, want %v", err, expected)
	}
	if err := cached.UpdateRoutingPolicy(ctx, &model.RoutingPolicy{ID: 1}); !errors.Is(err, expected) {
		t.Fatalf("UpdateRoutingPolicy error = %v, want %v", err, expected)
	}
	if err := cached.DeleteRoutingPolicy(ctx, 1); !errors.Is(err, expected) {
		t.Fatalf("DeleteRoutingPolicy error = %v, want %v", err, expected)
	}
}
