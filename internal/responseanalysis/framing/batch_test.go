package framing

import (
	"errors"
	"math"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
)

func TestBatchAccountsBackingGrowthPeakAndRelease(t *testing.T) {
	t.Parallel()
	reserver := &trackingReserver{}
	batch := newBatch(reserver)
	for range 3 {
		frame := Frame{Event: "event"}
		if err := batch.append(&frame); err != nil {
			t.Fatal(err)
		}
		if frame.Event != "" {
			t.Fatal("append did not move frame ownership")
		}
	}

	active, peak, requests := reserver.snapshot()
	if len(batch.Frames) != 3 || cap(batch.Frames) != 4 {
		t.Fatalf("len=%d cap=%d", len(batch.Frames), cap(batch.Frames))
	}
	if active != 4*frameSlotBytes || peak != 6*frameSlotBytes {
		t.Fatalf("active=%d peak=%d", active, peak)
	}
	wantCapacities := []int{frameSlotBytes, 2 * frameSlotBytes, 4 * frameSlotBytes}
	if len(requests) != len(wantCapacities) {
		t.Fatalf("requests=%#v", requests)
	}
	for index, request := range requests {
		if request.class != allocation.ClassChannelPayload || request.capacity != wantCapacities[index] {
			t.Fatalf("request %d=%#v", index, request)
		}
	}

	batch.Release()
	batch.Release()
	if active, _, _ := reserver.snapshot(); active != 0 {
		t.Fatalf("active after release=%d", active)
	}
}

func TestBatchTakeTransfersOneFrameAndReleaseCleansRemainder(t *testing.T) {
	t.Parallel()
	reserver := &trackingReserver{}
	firstGrant, err := reserver.Reserve(allocation.ClassFramingBuffer, 11)
	if err != nil {
		t.Fatal(err)
	}
	secondGrant, err := reserver.Reserve(allocation.ClassFramingBuffer, 13)
	if err != nil {
		t.Fatal(err)
	}
	batch := newBatch(reserver)
	first := Frame{Data: []byte("first"), dataGrant: firstGrant}
	second := Frame{Data: []byte("second"), dataGrant: secondGrant}
	if err := batch.append(&first); err != nil {
		t.Fatal(err)
	}
	if err := batch.append(&second); err != nil {
		t.Fatal(err)
	}

	taken, ok := batch.Take(0)
	if !ok || string(taken.Data) != "first" {
		t.Fatalf("taken=%#v ok=%v", taken, ok)
	}
	if _, ok := batch.Take(-1); ok {
		t.Fatal("negative take index succeeded")
	}
	if _, ok := batch.Take(len(batch.Frames)); ok {
		t.Fatal("out-of-range take index succeeded")
	}
	batch.Release()
	if active, _, _ := reserver.snapshot(); active != 11 {
		t.Fatalf("only taken payload should remain active: %d", active)
	}
	taken.Release()
	taken.Release()
	if active, _, _ := reserver.snapshot(); active != 0 {
		t.Fatalf("active after taken frame release=%d", active)
	}
}

func TestBatchGrowthDenialPreservesExistingAndIncomingOwnership(t *testing.T) {
	t.Parallel()
	reserver := &trackingReserver{denyAt: 3}
	batch := newBatch(reserver)
	for range 2 {
		frame := Frame{Event: "retained"}
		if err := batch.append(&frame); err != nil {
			t.Fatal(err)
		}
	}
	incoming := Frame{Event: "incoming"}
	err := batch.append(&incoming)
	if reason, ok := allocation.DenialReasonOf(err); !ok || reason != allocation.DenialRequestMemoryExhausted {
		t.Fatalf("error=%v reason=%q ok=%v", err, reason, ok)
	}
	if len(batch.Frames) != 2 || cap(batch.Frames) != 2 || incoming.Event != "incoming" {
		t.Fatalf("batch=%#v incoming=%#v", batch, incoming)
	}
	if active, _, _ := reserver.snapshot(); active != 2*frameSlotBytes {
		t.Fatalf("active after denial=%d", active)
	}
	incoming.Release()
	batch.Release()
	if active, _, _ := reserver.snapshot(); active != 0 {
		t.Fatalf("active after cleanup=%d", active)
	}
}

func TestBatchRejectsInvalidDependenciesAndCapacity(t *testing.T) {
	t.Parallel()
	var nilBatch *Batch
	frame := Frame{}
	if err := nilBatch.append(&frame); !errors.Is(err, errNilBatch) {
		t.Fatalf("nil batch error=%v", err)
	}
	if _, ok := nilBatch.Take(0); ok {
		t.Fatal("nil batch take succeeded")
	}
	nilBatch.Release()

	batch := newBatch(nil)
	if err := batch.append(nil); !errors.Is(err, errNilBatchFrame) {
		t.Fatalf("nil frame error=%v", err)
	}
	if err := batch.append(&frame); !errors.Is(err, allocation.ErrNilReserver) {
		t.Fatalf("nil reserver error=%v", err)
	}
	batch = newBatch(&trackingReserver{nilAt: 1})
	if err := batch.append(&frame); !errors.Is(err, allocation.ErrNilGrant) {
		t.Fatalf("nil grant error=%v", err)
	}

	maximum := math.MaxInt / frameSlotBytes
	for _, test := range []struct {
		current  int
		required int
	}{
		{current: -1, required: 1},
		{current: 0, required: 0},
		{current: maximum, required: maximum + 1},
	} {
		if _, err := nextBatchCapacity(test.current, test.required); !errors.Is(err, errBatchCapacityOverflow) {
			t.Fatalf("current=%d required=%d error=%v", test.current, test.required, err)
		}
	}
	if capacity, err := nextBatchCapacity(2, 2); err != nil || capacity != 2 {
		t.Fatalf("capacity=%d err=%v", capacity, err)
	}
}
