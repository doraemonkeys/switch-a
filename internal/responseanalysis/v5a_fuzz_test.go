package responseanalysis_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"

	responseanalysis "github.com/doraemonkeys/switch-a/internal/responseanalysis"
)

func FuzzV5AAnalyzerExactRawAcrossChunksAndFailOpen(f *testing.F) {
	validError := []byte("event: error\r\ndata: {\"type\":\"error\",\"code\":\"busy\",\"message\":\"retry\"}\r\n\r\n")
	ordinary := []byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"retry\"}\n\n")
	f.Add(validError, byte(0), uint16(1), byte(1))
	f.Add(ordinary, byte(0), uint16(17), byte(7))
	f.Add(v5aGzip(f, validError), byte(1), uint16(23), byte(3))
	f.Add([]byte("corrupt gzip"), byte(1), uint16(4), byte(2))
	f.Add([]byte{0x1b, 0x58, 0x00, 0x28, 0x2c}, byte(2), uint16(2), byte(4))
	f.Add([]byte("unknown coding remains raw"), byte(3), uint16(9), byte(5))

	f.Fuzz(func(t *testing.T, wire []byte, codingSelector byte, splitSeed uint16, writeSeed byte) {
		const maxFuzzWireBytes = 128 * 1024
		if len(wire) > maxFuzzWireBytes {
			t.Skip()
		}
		encodings := [...]string{"identity", "gzip", "br", "zstd"}
		encoding := encodings[int(codingSelector)%len(encodings)]
		split := 0
		if len(wire) > 0 {
			split = int(splitSeed) % (len(wire) + 1)
		}

		budget := v5aBudget(t, responseanalysis.ResponseProbeMemoryBudget)
		analyzer := v5aAnalyzer(t, budget, responseanalysis.AnalyzerOptions{
			ProbeDuration:    time.Hour,
			ProbeMemoryLimit: responseanalysis.MaxProbeMemoryLimit,
		})
		body := newV5ASegmentBody(wire, split)
		writer := newV5AWriter(int(writeSeed%17) + 1)
		response := analyzer.Start(context.Background(), responseanalysis.StartInput{
			Mode: responseanalysis.ProbeMode(), APIType: "codex", ContentType: "text/event-stream", ContentEncoding: encoding,
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type":     {"text/event-stream"},
				"Content-Encoding": {encoding},
				"X-Repeat":         {"one", "two"},
			},
			Body: body, Writer: writer,
			// A false matcher exercises semantic envelopes without pausing the
			// coordinator, keeping the invariant focused on transport ownership.
			Match: func(responseanalysis.SemanticFields) bool { return false },
		})

		boundary := v5aAwaitBoundary(t, response)
		if boundary.State != responseanalysis.StateForwarding || boundary.Forwarding == nil {
			t.Fatalf("boundary=%#v", boundary)
		}
		completion := v5aAwaitCompletion(t, boundary.Forwarding)
		snapshot := writer.snapshot()
		if !bytes.Equal(snapshot.body, wire) {
			t.Fatalf("wire changed for encoding %q split %d: got=%x want=%x", encoding, split, snapshot.body, wire)
		}
		if len(snapshot.statuses) != 1 || snapshot.statuses[0] != http.StatusOK ||
			snapshot.header.Get("Content-Encoding") != encoding {
			t.Fatalf("response head changed: %#v", snapshot)
		}
		if completion.ClientBodyBytesWritten != int64(len(wire)) ||
			body.closeCount.Load() != 1 || body.concurrentRead.Load() ||
			writer.concurrentWrite.Load() || budget.Used() != 0 {
			t.Fatalf("lifecycle completion=%#v closes=%d read_race=%t write_race=%t budget=%d", completion, body.closeCount.Load(), body.concurrentRead.Load(), writer.concurrentWrite.Load(), budget.Used())
		}
	})
}
