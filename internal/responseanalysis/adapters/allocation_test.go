package adapters

import (
	"errors"
	"sync"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/framing"
)

type adapterReservation struct {
	class    allocation.Class
	capacity int
}

type adapterTrackingReserver struct {
	mu       sync.Mutex
	calls    []adapterReservation
	active   int
	released int
	denyAt   int
	nilAt    int
}

func (r *adapterTrackingReserver) Reserve(class allocation.Class, capacity int) (allocation.Grant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, adapterReservation{class: class, capacity: capacity})
	call := len(r.calls)
	if call == r.denyAt {
		return nil, &allocation.Denial{
			Reason:            allocation.DenialRequestMemoryExhausted,
			Class:             class,
			RequestedCapacity: capacity,
		}
	}
	if call == r.nilAt {
		return nil, nil
	}
	r.active++
	return &adapterTrackingGrant{owner: r}, nil
}

func (r *adapterTrackingReserver) snapshot() (calls []adapterReservation, active, released int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]adapterReservation(nil), r.calls...), r.active, r.released
}

type adapterTrackingGrant struct {
	once  sync.Once
	owner *adapterTrackingReserver
}

func (g *adapterTrackingGrant) Release() {
	if g == nil || g.owner == nil {
		return
	}
	g.once.Do(func() {
		g.owner.mu.Lock()
		defer g.owner.mu.Unlock()
		g.owner.active--
		g.owner.released++
	})
}

func TestResultOwnsWorstCaseSemanticReservationsUntilRelease(t *testing.T) {
	reserver := &adapterTrackingReserver{}
	dispatcher, err := NewWithReserver(
		apicontract.ErrorFamilyAnthropicMessages,
		framing.KindJSON,
		testLimits,
		reserver,
	)
	if err != nil {
		t.Fatal(err)
	}
	result := dispatcher.Observe(framing.Frame{Data: []byte(
		"{\"type\":\"error\",\"usage\":{\"input_tokens\":1,\"output_tokens\":2,\"service_tier\":\" PREMIUM \",\"cache_creation_input_tokens\":1},\"error\":{\"type\":\" SERVER \",\"code\":503,\"message\":\" Busy \",\"reason\":\" LIMIT \"}}",
	)})
	if result.Class != EventError || result.Fields == nil || result.Usage == nil {
		t.Fatalf("result = %#v", result)
	}
	if result.Fields.Type != "SERVER" || result.Fields.Code != "503" ||
		result.Fields.Message != "Busy" || result.Fields.Reason != "LIMIT" ||
		result.Usage.ServiceTier != "premium" || result.Usage.CacheCreation == nil {
		t.Fatalf("trimmed result = fields %#v usage %#v", result.Fields, result.Usage)
	}
	calls, active, _ := reserver.snapshot()
	if active != allocation.BundleGrantCapacity || result.resources.Len() != allocation.BundleGrantCapacity {
		t.Fatalf("retained grants = active %d bundle %d; calls %#v", active, result.resources.Len(), calls)
	}
	for _, call := range calls {
		if call.class != allocation.ClassSemanticFields || call.capacity <= 0 {
			t.Fatalf("reservation = %#v", call)
		}
	}

	result.Release()
	result.Release()
	_, active, released := reserver.snapshot()
	if active != 0 || released != len(calls) {
		t.Fatalf("after release: active=%d released=%d calls=%d", active, released, len(calls))
	}
}

func TestResultResourceTransferIsMoveOwned(t *testing.T) {
	reserver := &adapterTrackingReserver{}
	dispatcher, err := NewWithReserver(
		apicontract.ErrorFamilyOpenAIChatCompletions,
		framing.KindJSON,
		testLimits,
		reserver,
	)
	if err != nil {
		t.Fatal(err)
	}
	result := dispatcher.Observe(framing.Frame{Data: []byte(
		"{\"error\":{\"type\":\"SERVER_ERROR\",\"message\":\"BUSY\"}}",
	)})
	if result.Class != EventError {
		t.Fatalf("result = %#v", result)
	}
	resources := result.TakeResources()
	if resources.Len() == 0 {
		t.Fatal("semantic ownership was not transferred")
	}
	result.Release()
	if _, active, _ := reserver.snapshot(); active == 0 {
		t.Fatal("source release freed moved resources")
	}
	resources.Release()
	resources.Release()
	if _, active, _ := reserver.snapshot(); active != 0 {
		t.Fatalf("active grants = %d", active)
	}
}

func TestSemanticReservationDenialFailsOpenAndReleasesEarlierGrants(t *testing.T) {
	for _, denyAt := range []int{1, 5} {
		reserver := &adapterTrackingReserver{denyAt: denyAt}
		dispatcher, err := NewWithReserver(
			apicontract.ErrorFamilyAnthropicMessages,
			framing.KindJSON,
			testLimits,
			reserver,
		)
		if err != nil {
			t.Fatal(err)
		}
		result := dispatcher.Observe(framing.Frame{Data: []byte(
			"{\"type\":\"error\",\"error\":{\"type\":\"SERVER\",\"code\":503,\"message\":\"BUSY\",\"reason\":\"LIMIT\"}}",
		)})
		if result.Class != EventFailOpen || result.Fields != nil || result.Usage != nil {
			t.Fatalf("deny call %d result = %#v", denyAt, result)
		}
		reason, ok := allocation.DenialReasonOf(result.AllocationError)
		if !ok || reason != allocation.DenialRequestMemoryExhausted {
			t.Fatalf("deny call %d allocation error = %v", denyAt, result.AllocationError)
		}
		if _, active, _ := reserver.snapshot(); active != 0 {
			t.Fatalf("deny call %d leaked %d grants", denyAt, active)
		}
	}
}

func TestNilGrantAndNilReserverAreStableAllocationFailures(t *testing.T) {
	if dispatcher, err := NewWithReserver(
		apicontract.ErrorFamilyAnthropicMessages,
		framing.KindJSON,
		testLimits,
		nil,
	); dispatcher != nil || !errors.Is(err, allocation.ErrNilReserver) {
		t.Fatalf("nil reserver = %#v, %v", dispatcher, err)
	}

	reserver := &adapterTrackingReserver{nilAt: 1}
	dispatcher, err := NewWithReserver(
		apicontract.ErrorFamilyAnthropicMessages,
		framing.KindJSON,
		testLimits,
		reserver,
	)
	if err != nil {
		t.Fatal(err)
	}
	result := dispatcher.Observe(framing.Frame{Data: []byte(
		"{\"type\":\"error\",\"error\":{\"type\":\"SERVER\",\"message\":\"BUSY\"}}",
	)})
	if result.Class != EventFailOpen || !errors.Is(result.AllocationError, allocation.ErrNilGrant) {
		t.Fatalf("nil grant result = %#v", result)
	}
}

func TestDiscardedFractionalGoogleCodeReleasesTransientReservation(t *testing.T) {
	reserver := &adapterTrackingReserver{}
	dispatcher, err := NewWithReserver(
		apicontract.ErrorFamilyGoogleGenerateContent,
		framing.KindJSON,
		testLimits,
		reserver,
	)
	if err != nil {
		t.Fatal(err)
	}
	result := dispatcher.Observe(framing.Frame{Data: []byte(
		"{\"error\":{\"code\":42.9,\"message\":\"ordinary output\"}}",
	)})
	if result.Class != EventClientVisible || result.Fields != nil {
		t.Fatalf("result = %#v", result)
	}
	result.Release()
	calls, active, released := reserver.snapshot()
	if len(calls) != 1 || active != 0 || released != 1 {
		t.Fatalf("transient accounting = calls %#v active %d released %d", calls, active, released)
	}
}
