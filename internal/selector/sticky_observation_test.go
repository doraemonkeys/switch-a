package selector

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestStickyEvictionObservationExcludesRoutingIdentity(t *testing.T) {
	const (
		operationID = "123e4567-e89b-42d3-a456-426614174999"
		ipMarker    = "raw-ip-marker-203.0.113.77"
		userMarker  = "raw-user-header-marker-bearer-secret"
	)
	store := newMockStore()
	store.providers = []model.Provider{
		{ID: "p1", Enabled: true, Concurrency: 1, APITypes: []model.ProviderAPIType{{ProviderID: "p1", APIType: "claude"}}},
		{ID: "p2", Enabled: true, Priority: 1, APITypes: []model.ProviderAPIType{{ProviderID: "p2", APIType: "claude"}}},
	}
	clock := &mockClock{now: time.Now()}
	sticky := NewMemoryStickyCache(clock)
	limiter := NewConcurrencyLimiter()
	heldLease, acquired := limiter.Acquire("p1", 1)
	if !acquired {
		t.Fatal("failed to prefill sticky provider concurrency")
	}
	t.Cleanup(func() { heldLease.Release() })
	core, logs := observer.New(zap.DebugLevel)
	selector := NewSelector(Config{
		Store:       store,
		StickyCache: sticky,
		Limiter:     limiter,
		Clock:       clock,
		Logger:      zap.New(core),
	})
	req := &model.SelectRequest{
		OperationID: operationID,
		ClientIP:    ipMarker,
		User:        userMarker,
		APIType:     "claude",
		StickyMode:  model.StickyModeAPIType,
	}
	sticky.Set(BuildContinuityKey(req), "p1", time.Minute)

	result, err := selector.SelectWithMetadata(context.Background(), req)
	if err != nil {
		t.Fatalf("SelectWithMetadata() error = %v", err)
	}
	defer result.Lease.Release()
	if result.Provider().ID != "p2" {
		t.Fatalf("selected provider = %q, want p2", result.Provider().ID)
	}

	entries := logs.FilterMessage("selector.sticky_binding_decision").All()
	if len(entries) != 1 {
		t.Fatalf("sticky decision log count = %d, want 1", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["operation_id"] != operationID ||
		fields["provider_id"] != "p1" ||
		fields["api_type"] != "claude" ||
		fields["decision"] != string(stickyBindingDecisionEvicted) ||
		fields["reason"] != string(stickyBindingDecisionReasonProviderConcurrencyExhausted) {
		t.Fatalf("sticky decision fields = %#v", fields)
	}
	if _, present := fields["client_ip"]; present {
		t.Fatal("sticky decision log retained client_ip field")
	}
	if _, present := fields["user"]; present {
		t.Fatal("sticky decision log retained user field")
	}
	encoded := entries[0].Message
	for key, value := range fields {
		encoded += key + "=" + fmt.Sprint(value)
	}
	if strings.Contains(encoded, ipMarker) || strings.Contains(encoded, userMarker) {
		t.Fatalf("sticky decision log exposed raw routing identity: %q", encoded)
	}
}
