package framing

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
)

func TestSSEFramingAcrossEveryByteBoundary(t *testing.T) {
	t.Parallel()
	wire := []byte(": keepalive\r\nevent: error\r\ndata:{\"message\":\r\ndata: \"busy\"}\r\n\r\ndata: [DONE]")
	want := []Frame{
		{Event: "error", Data: []byte("{\"message\":\n\"busy\"}")},
		{Data: []byte("[DONE]"), Done: true},
	}

	for split := 0; split <= len(wire); split++ {
		split := split
		t.Run(strconv.Itoa(split), func(t *testing.T) {
			framer := NewSSE(1024)
			var got []Frame
			batch, err := framer.Feed(wire[:split], false)
			if err != nil {
				t.Fatalf("first feed: %v", err)
			}
			got = append(got, takeBatchFrames(t, &batch)...)
			batch, err = framer.Feed(wire[split:], true)
			if err != nil {
				t.Fatalf("second feed: %v", err)
			}
			got = append(got, takeBatchFrames(t, &batch)...)
			assertFrames(t, got, want)
			ReleaseFrames(got)
		})
	}

	framer := NewSSE(1024)
	var got []Frame
	for index, value := range wire {
		batch, err := framer.Feed([]byte{value}, index == len(wire)-1)
		if err != nil {
			t.Fatalf("byte %d: %v", index, err)
		}
		got = append(got, takeBatchFrames(t, &batch)...)
	}
	assertFrames(t, got, want)
	ReleaseFrames(got)
}

func TestSSESupportsCommentsEmptyDataAndEOFEvent(t *testing.T) {
	t.Parallel()
	framer := NewSSE(128)
	batch, err := framer.Feed([]byte(":comment\nignored\nevent: custom\ndata\n\nevent: tail\ndata:  value\r"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer batch.Release()
	want := []Frame{{Event: "custom", Data: []byte{}}, {Event: "tail", Data: []byte(" value")}}
	assertFrames(t, batch.Frames, want)
}

func TestSSEEventLimitIsPerEvent(t *testing.T) {
	t.Parallel()
	large := strings.Repeat("x", 72*1024)
	wire := "data: one\n\ndata: two\n\ndata: " + large + "\n\n"
	framer := NewSSE(80 * 1024)
	batch, err := framer.Feed([]byte(wire), true)
	if err != nil {
		t.Fatal(err)
	}
	defer batch.Release()
	if len(batch.Frames) != 3 || len(batch.Frames[2].Data) != len(large) {
		t.Fatalf("batch = %#v", batch)
	}
}

func TestSSEFlagshipThirdEventFitsBoundedRequestAccount(t *testing.T) {
	t.Parallel()
	const (
		requestMemoryLimit = 256 * 1024
		thirdEventBytes    = 72 * 1024
	)
	prefix := "event: error\ndata: {\"type\":\"error\",\"padding\":\""
	suffix := "\",\"error\":{\"type\":\"OVERLOADED\",\"message\":\"RETRY LATER\"}}\r\n\r\n"
	third := prefix + strings.Repeat("x", thirdEventBytes-len(prefix)-len(suffix)) + suffix
	wire := "event: ping\r\ndata: {\"type\":\"ping\"}\r\n\r\n" +
		"event: message_start\r\ndata: {\"type\":\"message_start\"}\r\n\r\n" +
		third

	// The runtime accounts the fixed decoder workset at the process boundary;
	// this request account proves all retained framing and event capacities fit
	// the frozen per-response ceiling without hiding transient realloc peaks.
	reserver := newBoundedReserver(requestMemoryLimit)
	framer, err := NewSSEWithReserver(requestMemoryLimit, reserver)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := framer.Feed([]byte(wire), true)
	if err != nil {
		t.Fatalf("flagship event exceeded bounded account: %v", err)
	}
	if len(batch.Frames) != 3 || batch.Frames[2].Event != "error" || len(batch.Frames[2].Data) == 0 {
		batch.Release()
		t.Fatalf("frames=%#v", batch.Frames)
	}

	wantFraming := 0
	for index := range batch.Frames {
		wantFraming += cap(batch.Frames[index].Data) + len(batch.Frames[index].Event)
	}
	wantChannel := cap(batch.Frames) * frameSlotBytes
	snapshot := reserver.snapshot()
	if snapshot.denials != 0 || snapshot.peak > requestMemoryLimit {
		t.Fatalf("denials=%d peak=%d limit=%d requests=%#v", snapshot.denials, snapshot.peak, requestMemoryLimit, snapshot.requests)
	}
	if snapshot.byClass[allocation.ClassFramingBuffer] != wantFraming ||
		snapshot.byClass[allocation.ClassChannelPayload] != wantChannel ||
		snapshot.active != wantFraming+wantChannel {
		t.Fatalf("active=%d by_class=%#v want_framing=%d want_channel=%d", snapshot.active, snapshot.byClass, wantFraming, wantChannel)
	}

	batch.Release()
	framer.Release()
	snapshot = reserver.snapshot()
	if snapshot.active != 0 || snapshot.byClass[allocation.ClassFramingBuffer] != 0 || snapshot.byClass[allocation.ClassChannelPayload] != 0 {
		t.Fatalf("retained grants after cleanup: %#v", snapshot)
	}
}

func TestSSEIncrementalFlagshipFitsCompleteProbeBudget(t *testing.T) {
	t.Parallel()
	const (
		requestMemoryLimit      = 256 * 1024
		decodedBufferCapacity   = 32 * 1024
		rawPrefixChunkBytes     = 4 * 1024
		flagshipRawPrefixChunks = 18
		rawPrefixShadowCapacity = flagshipRawPrefixChunks * rawPrefixChunkBytes
		feedBytes               = 32 * 1024
		thirdEventBytes         = 72 * 1024
	)
	prefix := "event: error\r\ndata: {\"type\":\"error\",\"padding\":\""
	suffix := "\",\"error\":{\"type\":\"OVERLOADED\",\"message\":\"RETRY LATER\"}}\r\n\r\n"
	third := prefix + strings.Repeat("x", thirdEventBytes-len(prefix)-len(suffix)) + suffix
	wire := []byte("event: ping\r\ndata: {\"type\":\"ping\"}\r\n\r\n" +
		"event: message_start\r\ndata: {\"type\":\"message_start\"}\r\n\r\n" +
		third)

	// These shadow grants reproduce the other live owners at the 64-KiB line
	// boundary: one decoder work buffer and 18 runtime-sized raw-prefix chunks.
	reserver := newBoundedReserver(requestMemoryLimit)
	decodedGrant, err := reserver.Reserve(allocation.ClassDecodedBuffer, decodedBufferCapacity)
	if err != nil {
		t.Fatal(err)
	}
	defer decodedGrant.Release()
	rawGrant, err := reserver.Reserve(allocation.ClassRawPrefix, rawPrefixShadowCapacity)
	if err != nil {
		t.Fatal(err)
	}
	defer rawGrant.Release()
	framer, err := NewSSEWithReserver(requestMemoryLimit, reserver)
	if err != nil {
		t.Fatal(err)
	}
	defer framer.Release()

	var foundError bool
	for start := 0; start < len(wire); start += feedBytes {
		end := min(start+feedBytes, len(wire))
		batch, feedErr := framer.Feed(wire[start:end], end == len(wire))
		if feedErr != nil {
			snapshot := reserver.snapshot()
			batch.Release()
			t.Fatalf("incremental flagship failed: %v; active=%d peak=%d denials=%d requests=%#v", feedErr, snapshot.active, snapshot.peak, snapshot.denials, snapshot.requests)
		}
		for index := range batch.Frames {
			frame := batch.Frames[index]
			if frame.Event == "error" {
				foundError = true
				if len(frame.Data) == 0 || !bytes.Contains(frame.Data, []byte(`"message":"RETRY LATER"`)) {
					batch.Release()
					t.Fatalf("error frame=%#v", frame)
				}
			}
		}
		batch.Release()
	}
	if !foundError {
		t.Fatal("incremental flagship did not emit the terminal error frame")
	}
	snapshot := reserver.snapshot()
	t.Logf("complete probe peak=%d active=%d", snapshot.peak, snapshot.active)
	if snapshot.denials != 0 || snapshot.peak > requestMemoryLimit {
		t.Fatalf("denials=%d peak=%d limit=%d requests=%#v", snapshot.denials, snapshot.peak, requestMemoryLimit, snapshot.requests)
	}

	framer.Release()
	rawGrant.Release()
	decodedGrant.Release()
	if snapshot := reserver.snapshot(); snapshot.active != 0 {
		t.Fatalf("retained grants after cleanup: %#v", snapshot)
	}
}

func TestSSEMultilineMovesLargerLineStorage(t *testing.T) {
	t.Parallel()
	large := strings.Repeat("x", 72*1024)
	reserver := newBoundedReserver(256 * 1024)
	framer, err := NewSSEWithReserver(256*1024, reserver)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := framer.Feed([]byte("data: first\r\ndata: "+large+"\r\n\r\n"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer batch.Release()
	want := "first\n" + large
	if len(batch.Frames) != 1 || string(batch.Frames[0].Data) != want {
		t.Fatalf("frames=%#v", batch.Frames)
	}
	if snapshot := reserver.snapshot(); snapshot.denials != 0 || snapshot.peak > 256*1024 {
		t.Fatalf("snapshot=%#v", snapshot)
	}
}

func TestSSEPreservesCompletedFramesBeforeLaterLimitFailure(t *testing.T) {
	t.Parallel()
	first := "data: ok\n\n"
	framer := NewSSE(len(first))
	batch, err := framer.Feed([]byte(first+"data: too-large"), false)
	defer batch.Release()
	assertReason(t, err, FailureDecodedEventTooLarge)
	assertFrames(t, batch.Frames, []Frame{{Data: []byte("ok")}})
}

func TestSSELimitAndLifecycleFailures(t *testing.T) {
	t.Parallel()
	t.Run("oversized", func(t *testing.T) {
		framer := NewSSE(len("data:x\n\n") - 1)
		_, err := framer.Feed([]byte("data:x\n\n"), false)
		assertReason(t, err, FailureDecodedEventTooLarge)
	})
	t.Run("exact limit", func(t *testing.T) {
		wire := []byte("data:x\n\n")
		framer := NewSSE(len(wire))
		batch, err := framer.Feed(wire, true)
		defer batch.Release()
		if err != nil || len(batch.Frames) != 1 || string(batch.Frames[0].Data) != "x" {
			t.Fatalf("batch=%#v err=%v", batch, err)
		}
	})
	t.Run("invalid limit", func(t *testing.T) {
		_, err := NewSSE(0).Feed(nil, false)
		assertReason(t, err, FailureInternal)
	})
	t.Run("after eof", func(t *testing.T) {
		framer := NewSSE(10)
		batch, err := framer.Feed(nil, true)
		if err != nil {
			t.Fatal(err)
		}
		batch.Release()
		_, err = framer.Feed(nil, false)
		assertReason(t, err, FailureInternal)
	})
}

func TestSSETransfersFrameGrantsAndReleasesInternalState(t *testing.T) {
	t.Parallel()
	reserver := &trackingReserver{}
	framer, err := NewSSEWithReserver(128, reserver)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := framer.Feed([]byte("event: error\ndata: payload\n\n"), true)
	if err != nil || len(batch.Frames) != 1 {
		t.Fatalf("batch=%#v err=%v", batch, err)
	}
	if batch.Frames[0].Event != "error" || string(batch.Frames[0].Data) != "payload" {
		t.Fatalf("frame=%#v", batch.Frames[0])
	}
	active, _, _ := reserver.snapshot()
	if active != len("error")+initialOwnedBufferCapacity+frameSlotBytes {
		t.Fatalf("internal state was retained after EOF: active=%d", active)
	}
	batch.Release()
	batch.Release()
	if active, _, _ := reserver.snapshot(); active != 0 {
		t.Fatalf("active after frame release=%d", active)
	}
}

func TestSSEAllocationDenialReleasesPartialEvent(t *testing.T) {
	t.Parallel()
	reserver := &trackingReserver{denyAt: 2}
	framer, err := NewSSEWithReserver(128, reserver)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := framer.Feed([]byte("event:error\n"), false)
	if len(batch.Frames) != 0 {
		t.Fatalf("batch=%#v", batch)
	}
	if reason, ok := allocation.DenialReasonOf(err); !ok || reason != allocation.DenialRequestMemoryExhausted {
		t.Fatalf("error=%v reason=%q ok=%v", err, reason, ok)
	}
	if active, _, _ := reserver.snapshot(); active != 0 {
		t.Fatalf("active after denial=%d", active)
	}
}

func TestSSEBudgetedConstructorRejectsNilReserver(t *testing.T) {
	t.Parallel()
	if framer, err := NewSSEWithReserver(128, nil); framer != nil || !errors.Is(err, allocation.ErrNilReserver) {
		t.Fatalf("framer=%#v err=%v", framer, err)
	}
}

func assertFrames(t *testing.T, got, want []Frame) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d frames, want %d: %#v", len(got), len(want), got)
	}
	for index := range want {
		if got[index].Event != want[index].Event || got[index].Done != want[index].Done || !bytes.Equal(got[index].Data, want[index].Data) {
			t.Errorf("frame %d = %#v, want %#v", index, got[index], want[index])
		}
	}
}

func takeBatchFrames(t *testing.T, batch *Batch) []Frame {
	t.Helper()
	frames := make([]Frame, 0, len(batch.Frames))
	for index := range batch.Frames {
		frame, ok := batch.Take(index)
		if !ok {
			t.Fatalf("failed to take frame %d", index)
		}
		frames = append(frames, frame)
	}
	batch.Release()
	return frames
}

func assertReason(t *testing.T, err error, want FailureReason) {
	t.Helper()
	if err == nil || ReasonOf(err) != want {
		t.Fatalf("error = %v, reason = %q, want %q", err, ReasonOf(err), want)
	}
	var framed *Error
	if !errors.As(err, &framed) {
		t.Fatalf("error type = %T", err)
	}
}
