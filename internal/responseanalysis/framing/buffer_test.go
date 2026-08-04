package framing

import (
	"errors"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
)

func TestOwnedBufferAccountsReplacementPeak(t *testing.T) {
	t.Parallel()
	reserver := &trackingReserver{}
	buffer := newOwnedBuffer(reserver, allocation.ClassFramingBuffer)
	if err := buffer.appendBytes(make([]byte, initialOwnedBufferCapacity), 256); err != nil {
		t.Fatal(err)
	}
	if err := buffer.appendByte('x', 256); err != nil {
		t.Fatal(err)
	}

	active, peak, requests := reserver.snapshot()
	if cap(buffer.bytes()) != 128 || active != 128 || peak != 192 {
		t.Fatalf("len=%d cap=%d active=%d peak=%d", len(buffer.bytes()), cap(buffer.bytes()), active, peak)
	}
	if len(requests) != 2 || requests[0].capacity != 64 || requests[1].capacity != 128 {
		t.Fatalf("requests=%#v", requests)
	}

	buffer.release()
	buffer.release()
	if active, _, _ := reserver.snapshot(); active != 0 {
		t.Fatalf("active after release=%d", active)
	}
}

func TestOwnedBufferLargeGrowthBoundsReplacementPeak(t *testing.T) {
	t.Parallel()
	reserver := &trackingReserver{}
	buffer := newOwnedBuffer(reserver, allocation.ClassFramingBuffer)
	maximum := largeOwnedBufferCapacityThreshold + 4*largeOwnedBufferGrowthBlock
	want := make([]byte, largeOwnedBufferCapacityThreshold)
	if err := buffer.appendBytes(want, maximum); err != nil {
		t.Fatal(err)
	}
	if err := buffer.appendByte('x', maximum); err != nil {
		t.Fatal(err)
	}

	wantCapacity := largeOwnedBufferCapacityThreshold + largeOwnedBufferGrowthBlock
	wantPeak := largeOwnedBufferCapacityThreshold + wantCapacity
	active, peak, requests := reserver.snapshot()
	if len(buffer.bytes()) != len(want)+1 || buffer.bytes()[len(want)] != 'x' ||
		cap(buffer.bytes()) != wantCapacity || active != wantCapacity || peak != wantPeak {
		t.Fatalf("len=%d cap=%d active=%d peak=%d", len(buffer.bytes()), cap(buffer.bytes()), active, peak)
	}
	if len(requests) != 2 || requests[0].capacity != largeOwnedBufferCapacityThreshold || requests[1].capacity != wantCapacity {
		t.Fatalf("requests=%#v", requests)
	}

	buffer.release()
	if active, _, _ := reserver.snapshot(); active != 0 {
		t.Fatalf("active after release=%d", active)
	}
}

func TestOwnedBufferDenialPreservesOldAllocation(t *testing.T) {
	t.Parallel()
	reserver := &trackingReserver{denyAt: 2}
	buffer := newOwnedBuffer(reserver, allocation.ClassFramingBuffer)
	want := make([]byte, initialOwnedBufferCapacity)
	if err := buffer.appendBytes(want, 256); err != nil {
		t.Fatal(err)
	}
	err := buffer.appendByte('x', 256)
	if reason, ok := allocation.DenialReasonOf(err); !ok || reason != allocation.DenialRequestMemoryExhausted {
		t.Fatalf("error=%v reason=%q ok=%v", err, reason, ok)
	}
	if len(buffer.bytes()) != len(want) || cap(buffer.bytes()) != initialOwnedBufferCapacity {
		t.Fatalf("buffer changed after denial: len=%d cap=%d", len(buffer.bytes()), cap(buffer.bytes()))
	}
	if active, peak, _ := reserver.snapshot(); active != initialOwnedBufferCapacity || peak != initialOwnedBufferCapacity {
		t.Fatalf("active=%d peak=%d", active, peak)
	}
	buffer.release()
}

func TestOwnedBufferTransferMovesGrant(t *testing.T) {
	t.Parallel()
	reserver := &trackingReserver{}
	buffer := newOwnedBuffer(reserver, allocation.ClassFramingBuffer)
	if err := buffer.appendBytes([]byte("payload"), 64); err != nil {
		t.Fatal(err)
	}
	data, grant := buffer.transfer()
	buffer.release()
	if string(data) != "payload" || grant == nil {
		t.Fatalf("data=%q grant=%#v", data, grant)
	}
	if active, _, _ := reserver.snapshot(); active != initialOwnedBufferCapacity {
		t.Fatalf("transfer released active capacity: %d", active)
	}
	grant.Release()
	grant.Release()
	if active, _, _ := reserver.snapshot(); active != 0 {
		t.Fatalf("active after transferred release=%d", active)
	}
}

func TestOwnedBufferMoveCompactsWithoutASecondReservation(t *testing.T) {
	t.Parallel()
	reserver := &trackingReserver{}
	source := newOwnedBuffer(reserver, allocation.ClassFramingBuffer)
	destination := newOwnedBuffer(reserver, allocation.ClassFramingBuffer)
	if err := source.appendBytes([]byte("data: payload\r"), 128); err != nil {
		t.Fatal(err)
	}
	if err := source.moveCompactedTo(&destination, len("data: "), len("data: payload")); err != nil {
		t.Fatal(err)
	}

	active, peak, requests := reserver.snapshot()
	if string(destination.bytes()) != "payload" || cap(destination.bytes()) != initialOwnedBufferCapacity {
		t.Fatalf("destination=%q cap=%d", destination.bytes(), cap(destination.bytes()))
	}
	if len(source.bytes()) != 0 || source.grant != nil || active != initialOwnedBufferCapacity || peak != initialOwnedBufferCapacity || len(requests) != 1 {
		t.Fatalf("source=%#v active=%d peak=%d requests=%#v", source, active, peak, requests)
	}

	destination.release()
	if active, _, _ := reserver.snapshot(); active != 0 {
		t.Fatalf("active after release=%d", active)
	}
}

func TestOwnedBufferCombinedAppendIsTransactional(t *testing.T) {
	t.Parallel()
	reserver := &trackingReserver{denyAt: 2}
	buffer := newOwnedBuffer(reserver, allocation.ClassFramingBuffer)
	if err := buffer.appendBytes(make([]byte, initialOwnedBufferCapacity), 256); err != nil {
		t.Fatal(err)
	}
	want := append([]byte(nil), buffer.bytes()...)
	err := buffer.appendByteAndBytes('\n', []byte("next"), 256)
	if reason, ok := allocation.DenialReasonOf(err); !ok || reason != allocation.DenialRequestMemoryExhausted {
		t.Fatalf("error=%v reason=%q ok=%v", err, reason, ok)
	}
	if string(buffer.bytes()) != string(want) || cap(buffer.bytes()) != initialOwnedBufferCapacity {
		t.Fatalf("buffer changed after denial: len=%d cap=%d", len(buffer.bytes()), cap(buffer.bytes()))
	}
	buffer.release()
}

func TestOwnedBufferCombinedAppendUsesCapacityOrReservedReplacement(t *testing.T) {
	t.Parallel()
	t.Run("existing capacity", func(t *testing.T) {
		reserver := &trackingReserver{}
		buffer := newOwnedBuffer(reserver, allocation.ClassFramingBuffer)
		if err := buffer.appendBytes([]byte("first"), 128); err != nil {
			t.Fatal(err)
		}
		if err := buffer.appendByteAndBytes('\n', []byte("second"), 128); err != nil {
			t.Fatal(err)
		}
		if string(buffer.bytes()) != "first\nsecond" {
			t.Fatalf("buffer=%q", buffer.bytes())
		}
		if active, peak, requests := reserver.snapshot(); active != initialOwnedBufferCapacity || peak != initialOwnedBufferCapacity || len(requests) != 1 {
			t.Fatalf("active=%d peak=%d requests=%#v", active, peak, requests)
		}
		buffer.release()
	})

	t.Run("reserved replacement", func(t *testing.T) {
		reserver := &trackingReserver{}
		buffer := newOwnedBuffer(reserver, allocation.ClassFramingBuffer)
		if err := buffer.appendBytes(make([]byte, initialOwnedBufferCapacity), 256); err != nil {
			t.Fatal(err)
		}
		if err := buffer.appendByteAndBytes('\n', []byte("next"), 256); err != nil {
			t.Fatal(err)
		}
		if len(buffer.bytes()) != initialOwnedBufferCapacity+len("\nnext") || cap(buffer.bytes()) != 128 {
			t.Fatalf("len=%d cap=%d", len(buffer.bytes()), cap(buffer.bytes()))
		}
		if active, peak, requests := reserver.snapshot(); active != 128 || peak != 192 || len(requests) != 2 || requests[1].capacity != 128 {
			t.Fatalf("active=%d peak=%d requests=%#v", active, peak, requests)
		}
		buffer.release()
	})
}

func TestOwnedBufferCombinedAppendRejectsInvalidDependenciesAndLimits(t *testing.T) {
	t.Parallel()
	limited := newOwnedBuffer(allocation.NoopReserver{}, allocation.ClassFramingBuffer)
	if err := limited.appendByteAndBytes('\n', nil, 0); !errors.Is(err, errOwnedBufferLimit) {
		t.Fatalf("limit error=%v", err)
	}
	nilReserver := newOwnedBuffer(nil, allocation.ClassFramingBuffer)
	if err := nilReserver.appendByteAndBytes('\n', nil, 1); !errors.Is(err, allocation.ErrNilReserver) {
		t.Fatalf("nil reserver error=%v", err)
	}
	nilGrant := newOwnedBuffer(&trackingReserver{nilAt: 1}, allocation.ClassFramingBuffer)
	if err := nilGrant.appendByteAndBytes('\n', nil, 1); !errors.Is(err, allocation.ErrNilGrant) {
		t.Fatalf("nil grant error=%v", err)
	}
}

func TestOwnedBufferMoveRejectsBusyOrInvalidTargets(t *testing.T) {
	t.Parallel()
	reserver := &trackingReserver{}
	source := newOwnedBuffer(reserver, allocation.ClassFramingBuffer)
	destination := newOwnedBuffer(reserver, allocation.ClassFramingBuffer)
	if err := source.appendBytes([]byte("source"), 64); err != nil {
		t.Fatal(err)
	}
	if err := destination.appendBytes([]byte("destination"), 64); err != nil {
		t.Fatal(err)
	}
	if err := source.moveCompactedTo(&destination, 0, len(source.bytes())); !errors.Is(err, errOwnedBufferMoveTargetBusy) {
		t.Fatalf("busy target error=%v", err)
	}
	destination.release()
	if err := source.moveCompactedTo(&destination, -1, len(source.bytes())); !errors.Is(err, errOwnedBufferMove) {
		t.Fatalf("invalid range error=%v", err)
	}
	source.release()
}

func TestOwnedBufferRejectsInvalidDependenciesAndLimits(t *testing.T) {
	t.Parallel()
	nilReserver := newOwnedBuffer(nil, allocation.ClassFramingBuffer)
	if err := nilReserver.appendByte('x', 1); !errors.Is(err, allocation.ErrNilReserver) {
		t.Fatalf("nil reserver error=%v", err)
	}
	nilGrant := newOwnedBuffer(&trackingReserver{nilAt: 1}, allocation.ClassFramingBuffer)
	if err := nilGrant.appendByte('x', 1); !errors.Is(err, allocation.ErrNilGrant) {
		t.Fatalf("nil grant error=%v", err)
	}
	limited := newOwnedBuffer(allocation.NoopReserver{}, allocation.ClassFramingBuffer)
	if err := limited.appendBytes([]byte("xx"), 1); !errors.Is(err, errOwnedBufferLimit) {
		t.Fatalf("limit error=%v", err)
	}
}

func TestOwnedTextReplacementAccountsPeakAndTransfer(t *testing.T) {
	t.Parallel()
	reserver := &trackingReserver{}
	text := newOwnedText(reserver, allocation.ClassFramingBuffer)
	if err := text.replace([]byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := text.replace([]byte("second")); err != nil {
		t.Fatal(err)
	}
	value, grant := text.transfer()
	if value != "second" || grant == nil {
		t.Fatalf("value=%q grant=%#v", value, grant)
	}
	if active, peak, _ := reserver.snapshot(); active != len("second") || peak != len("first")+len("second") {
		t.Fatalf("active=%d peak=%d", active, peak)
	}
	grant.Release()
	text.release()
	if active, _, _ := reserver.snapshot(); active != 0 {
		t.Fatalf("active=%d", active)
	}
}
