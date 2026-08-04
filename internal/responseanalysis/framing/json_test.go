package framing

import (
	"bytes"
	"errors"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
)

func TestJSONEmitsOneAtomicValue(t *testing.T) {
	t.Parallel()
	framer := NewJSON(64)
	batch, err := framer.Feed([]byte(" {\"ok\":"), false)
	if err != nil || len(batch.Frames) != 0 {
		t.Fatalf("partial batch=%#v err=%v", batch, err)
	}
	batch.Release()
	batch, err = framer.Feed([]byte("true} \n"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer batch.Release()
	assertFrames(t, batch.Frames, []Frame{{Data: []byte("{\"ok\":true}")}})
}

func TestJSONDefersSyntaxValidationToBoundedAdapterScanner(t *testing.T) {
	t.Parallel()
	for name, wire := range map[string]string{
		"incomplete": "{\"ok\":",
		"malformed":  "{nope}",
		"multiple":   "{} []",
		"empty":      "   ",
	} {
		t.Run(name, func(t *testing.T) {
			batch, err := NewJSON(64).Feed([]byte(wire), true)
			if err != nil || len(batch.Frames) != 1 {
				t.Fatalf("batch=%#v err=%v", batch, err)
			}
			batch.Release()
		})
	}
	t.Run("invalid UTF-8", func(t *testing.T) {
		wire := []byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}
		batch, err := NewJSON(64).Feed(wire, true)
		if err != nil || len(batch.Frames) != 1 || !bytes.Equal(batch.Frames[0].Data, wire) {
			t.Fatalf("batch=%#v err=%v", batch, err)
		}
		batch.Release()
	})
}

func TestJSONLimitAndLifecycle(t *testing.T) {
	t.Parallel()
	t.Run("oversized", func(t *testing.T) {
		_, err := NewJSON(2).Feed([]byte("{} "), false)
		assertReason(t, err, FailureDecodedEventTooLarge)
	})
	t.Run("invalid limit", func(t *testing.T) {
		_, err := NewJSON(0).Feed(nil, false)
		assertReason(t, err, FailureInternal)
	})
	t.Run("after eof", func(t *testing.T) {
		framer := NewJSON(4)
		batch, err := framer.Feed([]byte("{}"), true)
		if err != nil {
			t.Fatal(err)
		}
		batch.Release()
		_, err = framer.Feed(nil, false)
		assertReason(t, err, FailureInternal)
	})
}

func TestFramingFactoryAndErrorContract(t *testing.T) {
	t.Parallel()
	if _, ok := New(KindJSON, 10).framer.(*JSON); !ok {
		t.Fatal("JSON factory returned wrong type")
	}
	if _, ok := New(KindSSE, 10).framer.(*SSE); !ok {
		t.Fatal("SSE factory returned wrong type")
	}
	_, err := New(Kind(99), 10).Feed(nil, false)
	assertReason(t, err, FailureInternal)

	cause := errors.New("cause")
	framed := &Error{Reason: FailureMalformedFrame, Cause: cause}
	if framed.Error() != "malformed_protocol_frame: cause" || !errors.Is(framed, cause) {
		t.Fatalf("framed error = %q", framed.Error())
	}
	if (&Error{Reason: FailureMalformedFrame}).Error() != "malformed_protocol_frame" {
		t.Fatal("reason-only error changed")
	}
	var nilError *Error
	if nilError.Error() != "response framing failure" || nilError.Unwrap() != nil {
		t.Fatal("nil error methods must be safe")
	}
	if ReasonOf(errors.New("foreign")) != FailureInternal {
		t.Fatal("foreign errors must not invent a stable reason")
	}
}

func TestJSONTransfersAtomicAllocationWithoutCopy(t *testing.T) {
	t.Parallel()
	reserver := &trackingReserver{}
	framer, err := NewJSONWithReserver(64, reserver)
	if err != nil {
		t.Fatal(err)
	}
	wire := []byte(" {\"ok\":true} \n")
	if batch, err := framer.Feed(wire, false); err != nil || len(batch.Frames) != 0 {
		t.Fatalf("batch=%#v err=%v", batch, err)
	}
	wantAddress := &framer.buffer.data[1]
	batch, err := framer.Feed(nil, true)
	if err != nil || len(batch.Frames) != 1 {
		t.Fatalf("batch=%#v err=%v", batch, err)
	}
	if string(batch.Frames[0].Data) != `{"ok":true}` || &batch.Frames[0].Data[0] != wantAddress {
		t.Fatalf("data=%q was copied or not trimmed", batch.Frames[0].Data)
	}
	if active, _, requests := reserver.snapshot(); active != initialOwnedBufferCapacity+frameSlotBytes || requests[len(requests)-1].class != allocation.ClassChannelPayload {
		t.Fatalf("active before batch release=%d requests=%#v", active, requests)
	}
	batch.Release()
	batch.Release()
	if active, _, _ := reserver.snapshot(); active != 0 {
		t.Fatalf("active after batch release=%d", active)
	}
}

func TestJSONReleasesAllocationOnReservationFailure(t *testing.T) {
	t.Parallel()
	t.Run("batch denied", func(t *testing.T) {
		reserver := &trackingReserver{denyAt: 2}
		framer, err := NewJSONWithReserver(64, reserver)
		if err != nil {
			t.Fatal(err)
		}
		batch, err := framer.Feed([]byte("{}"), true)
		if len(batch.Frames) != 0 {
			t.Fatalf("batch=%#v", batch)
		}
		if reason, ok := allocation.DenialReasonOf(err); !ok || reason != allocation.DenialRequestMemoryExhausted {
			t.Fatalf("error=%v reason=%q ok=%v", err, reason, ok)
		}
		if active, _, _ := reserver.snapshot(); active != 0 {
			t.Fatalf("active=%d", active)
		}
	})

	t.Run("growth denied", func(t *testing.T) {
		reserver := &trackingReserver{denyAt: 2}
		framer, err := NewJSONWithReserver(256, reserver)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := framer.Feed(make([]byte, 64), false); err != nil {
			t.Fatal(err)
		}
		_, err = framer.Feed([]byte("x"), false)
		if reason, ok := allocation.DenialReasonOf(err); !ok || reason != allocation.DenialRequestMemoryExhausted {
			t.Fatalf("error=%v reason=%q ok=%v", err, reason, ok)
		}
		if active, _, _ := reserver.snapshot(); active != 0 {
			t.Fatalf("active=%d", active)
		}
	})
}

func TestBudgetedFramingFactoryValidatesDependency(t *testing.T) {
	t.Parallel()
	if stream, err := NewWithReserver(KindJSON, 64, nil); stream != nil || !errors.Is(err, allocation.ErrNilReserver) {
		t.Fatalf("stream=%#v err=%v", stream, err)
	}
	if framer, err := NewJSONWithReserver(64, nil); framer != nil || !errors.Is(err, allocation.ErrNilReserver) {
		t.Fatalf("framer=%#v err=%v", framer, err)
	}
	stream, err := NewWithReserver(Kind(99), 64, allocation.NoopReserver{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = stream.Feed(nil, false)
	assertReason(t, err, FailureInternal)
	_, err = (*Stream)(nil).Feed(nil, false)
	assertReason(t, err, FailureInternal)
}
