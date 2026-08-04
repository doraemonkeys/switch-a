package framing

import (
	"bytes"
	"testing"
)

func FuzzFramerSplitInvariant(f *testing.F) {
	f.Add(byte(KindSSE), []byte("event: error\ndata: {\"message\":\"busy\"}\n\n"), uint16(7), uint16(256))
	f.Add(byte(KindJSON), []byte(`{"error":{"message":"busy"}}`), uint16(3), uint16(256))
	f.Add(byte(KindSSE), []byte("data: first\r\ndata: second"), uint16(0), uint16(64))

	f.Fuzz(func(t *testing.T, kindByte byte, wire []byte, splitSeed, limitSeed uint16) {
		kind := KindJSON
		if kindByte%2 == 0 {
			kind = KindSSE
		}
		limit := int(limitSeed%1024) + 1
		split := 0
		if len(wire) > 0 {
			split = int(splitSeed) % (len(wire) + 1)
		}

		wholeBatch, wholeErr := New(kind, limit).Feed(wire, true)
		wholeFrames := takeBatchFrames(t, &wholeBatch)
		chunked := New(kind, limit)
		firstBatch, firstErr := chunked.Feed(wire[:split], false)
		var chunkedFrames []Frame
		chunkedFrames = append(chunkedFrames, takeBatchFrames(t, &firstBatch)...)
		chunkedErr := firstErr
		if firstErr == nil {
			secondBatch, secondErr := chunked.Feed(wire[split:], true)
			chunkedFrames = append(chunkedFrames, takeBatchFrames(t, &secondBatch)...)
			chunkedErr = secondErr
		}

		if (wholeErr == nil) != (chunkedErr == nil) {
			t.Fatalf("error presence differs: whole=%v chunked=%v", wholeErr, chunkedErr)
		}
		if wholeErr != nil && ReasonOf(wholeErr) != ReasonOf(chunkedErr) {
			t.Fatalf("reason differs: whole=%v chunked=%v", wholeErr, chunkedErr)
		}
		if !equalFrames(wholeFrames, chunkedFrames) {
			t.Fatalf("frames differ: whole=%#v chunked=%#v", wholeFrames, chunkedFrames)
		}
		ReleaseFrames(wholeFrames)
		ReleaseFrames(chunkedFrames)
	})
}

func equalFrames(left, right []Frame) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Event != right[index].Event || left[index].Done != right[index].Done || !bytes.Equal(left[index].Data, right[index].Data) {
			return false
		}
	}
	return true
}
