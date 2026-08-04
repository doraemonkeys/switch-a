package allocation

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestNoopReserverReturnsIdempotentGrant(t *testing.T) {
	t.Parallel()
	grant, err := (NoopReserver{}).Reserve(ClassFramingBuffer, 128)
	if err != nil || grant == nil {
		t.Fatalf("grant=%#v err=%v", grant, err)
	}
	grant.Release()
	grant.Release()
}

func TestDenialReasonContract(t *testing.T) {
	t.Parallel()
	cause := errors.New("budget full")
	denial := &Denial{
		Reason:            DenialProcessMemoryExhausted,
		Class:             ClassDecoderWorkingSet,
		RequestedCapacity: 4096,
		Cause:             cause,
	}
	reason, ok := DenialReasonOf(denial)
	if !ok || reason != DenialProcessMemoryExhausted {
		t.Fatalf("reason=%q ok=%v", reason, ok)
	}
	if !errors.Is(denial, cause) {
		t.Fatal("denial lost its cause")
	}
	for _, fragment := range []string{string(DenialProcessMemoryExhausted), string(ClassDecoderWorkingSet), "4096", cause.Error()} {
		if !strings.Contains(denial.Error(), fragment) {
			t.Fatalf("error %q omitted %q", denial.Error(), fragment)
		}
	}
}

func TestDenialReasonRejectsUnknownAndForeignErrors(t *testing.T) {
	t.Parallel()
	if reason, ok := DenialReasonOf(errors.New("foreign")); ok || reason != "" {
		t.Fatalf("foreign reason=%q ok=%v", reason, ok)
	}
	unknown := &Denial{Reason: "invented", Class: ClassFramingBuffer, RequestedCapacity: 1}
	if reason, ok := DenialReasonOf(unknown); ok || reason != "" {
		t.Fatalf("unknown reason=%q ok=%v", reason, ok)
	}
	var nilDenial *Denial
	if nilDenial.Error() == "" || nilDenial.Unwrap() != nil {
		t.Fatal("nil denial methods must remain safe")
	}
}

func TestBundleMoveTakeAndIdempotentRelease(t *testing.T) {
	t.Parallel()
	var source Bundle
	grants := make([]*bundleCountingGrant, BundleGrantCapacity)
	for index := range grants {
		grants[index] = &bundleCountingGrant{}
		if err := source.Add(grants[index]); err != nil {
			t.Fatal(err)
		}
	}
	if source.Len() != BundleGrantCapacity {
		t.Fatalf("len=%d", source.Len())
	}
	extra := &bundleCountingGrant{}
	if err := source.Add(extra); !errors.Is(err, ErrBundleFull) {
		t.Fatalf("full error=%v", err)
	}

	taken := source.Take()
	if source.Len() != 0 || taken.Len() != BundleGrantCapacity {
		t.Fatalf("source=%d taken=%d", source.Len(), taken.Len())
	}
	var destination Bundle
	if err := taken.MoveTo(&destination); err != nil {
		t.Fatal(err)
	}
	if taken.Len() != 0 || destination.Len() != BundleGrantCapacity {
		t.Fatalf("taken=%d destination=%d", taken.Len(), destination.Len())
	}
	destination.Release()
	destination.Release()
	for index, grant := range grants {
		if grant.calls != 1 {
			t.Fatalf("grant %d released %d times", index, grant.calls)
		}
	}
	if extra.calls != 0 {
		t.Fatal("failed Add transferred ownership")
	}
}

func TestBundleMoveFailureIsTransactional(t *testing.T) {
	t.Parallel()
	var source, destination Bundle
	if err := source.Add(noopGrant{}); err != nil {
		t.Fatal(err)
	}
	for range BundleGrantCapacity {
		if err := destination.Add(noopGrant{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := source.MoveTo(&destination); !errors.Is(err, ErrBundleFull) {
		t.Fatalf("move error=%v", err)
	}
	if source.Len() != 1 || destination.Len() != BundleGrantCapacity {
		t.Fatalf("source=%d destination=%d", source.Len(), destination.Len())
	}
	if err := source.MoveTo(nil); !errors.Is(err, ErrNilBundleTarget) {
		t.Fatalf("nil target error=%v", err)
	}
	if err := source.MoveTo(&source); err != nil || source.Len() != 1 {
		t.Fatalf("self move len=%d err=%v", source.Len(), err)
	}
	source.Release()
	destination.Release()
}

func TestBundleGroupingAllocatesNothing(t *testing.T) {
	allocations := testing.AllocsPerRun(1000, func() {
		var source Bundle
		for range BundleGrantCapacity {
			if err := source.Add(noopGrant{}); err != nil {
				panic(err)
			}
		}
		taken := source.Take()
		var destination Bundle
		if err := taken.MoveTo(&destination); err != nil {
			panic(err)
		}
		destination.Release()
	})
	if allocations != 0 {
		t.Fatalf("bundle grouping allocated %.2f times", allocations)
	}
}

type bundleCountingGrant struct {
	once  sync.Once
	calls int
}

func (g *bundleCountingGrant) Release() {
	g.once.Do(func() { g.calls++ })
}
