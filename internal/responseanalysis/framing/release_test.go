package framing

import "testing"

func TestJSONReleaseCleansPartialStateIdempotently(t *testing.T) {
	t.Parallel()
	reserver := &trackingReserver{}
	framer, err := NewJSONWithReserver(256, reserver)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := framer.Feed([]byte("partial JSON"), false)
	if err != nil || len(batch.Frames) != 0 {
		t.Fatalf("batch=%#v err=%v", batch, err)
	}
	batch.Release()
	if active, _, _ := reserver.snapshot(); active != initialOwnedBufferCapacity {
		t.Fatalf("active before release=%d", active)
	}

	framer.Release()
	framer.Release()
	if active, _, _ := reserver.snapshot(); active != 0 {
		t.Fatalf("active after repeated release=%d", active)
	}
	if len(framer.buffer.bytes()) != 0 || framer.buffer.grant != nil || !framer.ended {
		t.Fatalf("partial state survived release: %#v", framer)
	}
	_, err = framer.Feed(nil, false)
	assertReason(t, err, FailureInternal)
	(*JSON)(nil).Release()
}

func TestSSEReleaseCleansEveryPartialOwnerIdempotently(t *testing.T) {
	t.Parallel()
	reserver := &trackingReserver{}
	framer, err := NewSSEWithReserver(256, reserver)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := framer.Feed([]byte("event: error\ndata: first\npartial"), false)
	if err != nil || len(batch.Frames) != 0 {
		t.Fatalf("batch=%#v err=%v", batch, err)
	}
	batch.Release()
	wantActive := 2*initialOwnedBufferCapacity + len("error")
	if active, _, _ := reserver.snapshot(); active != wantActive {
		t.Fatalf("active before release=%d want=%d", active, wantActive)
	}

	framer.Release()
	framer.Release()
	if active, _, _ := reserver.snapshot(); active != 0 {
		t.Fatalf("active after repeated release=%d", active)
	}
	if len(framer.line.bytes()) != 0 || len(framer.data.bytes()) != 0 || framer.event.value != "" || framer.line.grant != nil || framer.data.grant != nil || framer.event.grant != nil || framer.hasData || framer.eventBytes != 0 || !framer.ended {
		t.Fatalf("partial state survived release: %#v", framer)
	}
	_, err = framer.Feed(nil, false)
	assertReason(t, err, FailureInternal)
	(*SSE)(nil).Release()
}

func TestFramerReleaseDoesNotReclaimTransferredBatch(t *testing.T) {
	t.Parallel()
	t.Run("JSON", func(t *testing.T) {
		reserver := &trackingReserver{}
		framer, err := NewJSONWithReserver(64, reserver)
		if err != nil {
			t.Fatal(err)
		}
		batch, err := framer.Feed([]byte("{}"), true)
		if err != nil || len(batch.Frames) != 1 {
			t.Fatalf("batch=%#v err=%v", batch, err)
		}
		wantActive := initialOwnedBufferCapacity + frameSlotBytes
		framer.Release()
		if active, _, _ := reserver.snapshot(); active != wantActive {
			t.Fatalf("transferred ownership released with framer: active=%d want=%d", active, wantActive)
		}
		batch.Release()
		if active, _, _ := reserver.snapshot(); active != 0 {
			t.Fatalf("active after batch release=%d", active)
		}
	})

	t.Run("SSE", func(t *testing.T) {
		reserver := &trackingReserver{}
		framer, err := NewSSEWithReserver(128, reserver)
		if err != nil {
			t.Fatal(err)
		}
		batch, err := framer.Feed([]byte("data: value\n\npartial"), false)
		if err != nil || len(batch.Frames) != 1 {
			t.Fatalf("batch=%#v err=%v", batch, err)
		}
		framer.Release()
		wantActive := initialOwnedBufferCapacity + frameSlotBytes
		if active, _, _ := reserver.snapshot(); active != wantActive {
			t.Fatalf("transferred ownership released with framer: active=%d want=%d", active, wantActive)
		}
		batch.Release()
		if active, _, _ := reserver.snapshot(); active != 0 {
			t.Fatalf("active after batch release=%d", active)
		}
	})
}

func TestStreamReleaseClearsOwnerBeforeDelegating(t *testing.T) {
	t.Parallel()
	reentrant := &reentrantReleaseFramer{}
	stream := &Stream{framer: reentrant}
	reentrant.owner = stream
	stream.Release()
	stream.Release()
	if reentrant.calls != 1 || stream.framer != nil {
		t.Fatalf("release calls=%d framer=%#v", reentrant.calls, stream.framer)
	}
	_, err := stream.Feed(nil, false)
	assertReason(t, err, FailureInternal)

	var nilStream *Stream
	nilStream.Release()
	invalid := New(Kind(255), 1)
	invalid.Release()
	invalid.Release()
	if invalid.framer != nil {
		t.Fatalf("invalid framer survived release: %#v", invalid.framer)
	}
}

func TestStreamReleaseDelegatesPartialGrantCleanup(t *testing.T) {
	t.Parallel()
	reserver := &trackingReserver{}
	stream, err := NewWithReserver(KindJSON, 64, reserver)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := stream.Feed([]byte("partial"), false)
	if err != nil {
		t.Fatal(err)
	}
	batch.Release()
	stream.Release()
	stream.Release()
	if active, _, _ := reserver.snapshot(); active != 0 {
		t.Fatalf("active after stream release=%d", active)
	}
}

type reentrantReleaseFramer struct {
	owner *Stream
	calls int
}

func (*reentrantReleaseFramer) Feed([]byte, bool) (Batch, error) {
	return Batch{}, nil
}

func (f *reentrantReleaseFramer) Release() {
	f.calls++
	f.owner.Release()
}
