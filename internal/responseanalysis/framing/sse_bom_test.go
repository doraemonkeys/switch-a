package framing

import (
	"strconv"
	"testing"
)

func TestSSEBOMAcrossEveryByteBoundary(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		wire string
		want []Frame
	}{
		{name: "data", wire: "\ufeffdata: first\n\n", want: []Frame{{Data: []byte("first")}}},
		{name: "CRLF", wire: "\ufeffdata: 中文\r\n\r\n", want: []Frame{{Data: []byte("中文")}}},
		{name: "EOF", wire: "\ufeffdata: tail", want: []Frame{{Data: []byte("tail")}}},
		{name: "empty data", wire: "\ufeffdata:\n\n", want: []Frame{{Data: []byte{}}}},
		{name: "bare data", wire: "\ufeffdata\n\n", want: []Frame{{Data: []byte{}}}},
		{name: "event", wire: "\ufeffevent: error\ndata: busy\n\n", want: []Frame{{Event: "error", Data: []byte("busy")}}},
		{name: "multiline data", wire: "\ufeffdata:first\ndata: second\n\n", want: []Frame{{Data: []byte("first\nsecond")}}},
		{name: "comment", wire: "\ufeff: keepalive\ndata: first\n\n", want: []Frame{{Data: []byte("first")}}},
		{name: "empty first line", wire: "\ufeff\r\ndata: first\n\n", want: []Frame{{Data: []byte("first")}}},
		{name: "done", wire: "\ufeffdata: [DONE]\n\n", want: []Frame{{Data: []byte("[DONE]"), Done: true}}},
		{name: "only BOM", wire: "\ufeff"},
		{name: "truncated first byte", wire: "\xef"},
		{name: "truncated second byte", wire: "\xef\xbb"},
		{name: "partial prefix is not BOM", wire: "\xef\xbbdata: ignored\ndata: kept\n\n", want: []Frame{{Data: []byte("kept")}}},
		{name: "only one BOM", wire: "\ufeff\ufeffdata: ignored\ndata: kept\n\n", want: []Frame{{Data: []byte("kept")}}},
		{name: "after blank line", wire: "\n\ufeffdata: ignored\ndata: kept\n\n", want: []Frame{{Data: []byte("kept")}}},
		{name: "after comment", wire: ": keepalive\n\ufeffdata: ignored\ndata: kept\n\n", want: []Frame{{Data: []byte("kept")}}},
		{name: "after event", wire: "\ufeffdata: first\n\n\ufeffdata: ignored\ndata: kept\n\n", want: []Frame{{Data: []byte("first")}, {Data: []byte("kept")}}},
		{name: "data value", wire: "\ufeffdata: \ufeffkept\n\n", want: []Frame{{Data: []byte("\ufeffkept")}}},
		{name: "event value", wire: "\ufeffevent: \ufeffkept\ndata: value\n\n", want: []Frame{{Event: "\ufeffkept", Data: []byte("value")}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wire := []byte(test.wire)
			for split := 0; split <= len(wire); split++ {
				t.Run(strconv.Itoa(split), func(t *testing.T) {
					assertSSEBOMChunks(t, [][]byte{nil, wire[:split], nil, wire[split:]}, test.want)
				})
			}
			t.Run("one byte per read", func(t *testing.T) {
				chunks := make([][]byte, len(wire))
				for index := range wire {
					chunks[index] = wire[index : index+1]
				}
				assertSSEBOMChunks(t, chunks, test.want)
			})
		})
	}
}

func TestSSEBOMDispatchesBeforeEOF(t *testing.T) {
	t.Parallel()
	const maxEventBytes = 1024
	framer := NewSSE(maxEventBytes)
	defer framer.Release()
	batch, err := framer.Feed([]byte("\ufeffdata: first\n\n"), false)
	defer batch.Release()
	if err != nil {
		t.Fatal(err)
	}
	assertFrames(t, batch.Frames, []Frame{{Data: []byte("first")}})
}

func assertSSEBOMChunks(t *testing.T, chunks [][]byte, want []Frame) {
	t.Helper()
	const maxEventBytes = 1024
	reserver := &trackingReserver{}
	framer, err := NewSSEWithReserver(maxEventBytes, reserver)
	if err != nil {
		t.Fatal(err)
	}
	var got []Frame
	t.Cleanup(func() {
		ReleaseFrames(got)
		framer.Release()
		if active, _, _ := reserver.snapshot(); active != 0 {
			t.Fatalf("retained framing bytes after release=%d", active)
		}
	})
	for _, chunk := range chunks {
		batch, err := framer.Feed(chunk, false)
		if err != nil {
			batch.Release()
			t.Fatal(err)
		}
		got = append(got, takeBatchFrames(t, &batch)...)
	}
	batch, err := framer.Feed(nil, true)
	if err != nil {
		batch.Release()
		t.Fatal(err)
	}
	got = append(got, takeBatchFrames(t, &batch)...)
	assertFrames(t, got, want)
}
