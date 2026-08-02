package querywire

import (
	"errors"
	"io"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/requestcapture/capturevalue"
)

func TestStreamStopsAtEveryReadinessBoundary(t *testing.T) {
	t.Parallel()

	errCanceled := errors.New("canceled")
	t.Run("write byte before buffering", func(t *testing.T) {
		stream := stream{check: func() error { return errCanceled }, dst: io.Discard}
		if err := stream.writeByte('x'); !errors.Is(err, errCanceled) {
			t.Fatalf("writeByte() error = %v", err)
		}
	})
	t.Run("write byte while flushing full buffer", func(t *testing.T) {
		stream := fullFailingStream(errCanceled)
		if err := stream.writeByte('x'); !errors.Is(err, errCanceled) {
			t.Fatalf("writeByte() error = %v", err)
		}
	})
	t.Run("write string before buffering", func(t *testing.T) {
		stream := stream{check: func() error { return errCanceled }, dst: io.Discard}
		if err := stream.writeString("x"); !errors.Is(err, errCanceled) {
			t.Fatalf("writeString() error = %v", err)
		}
	})
	t.Run("write string while flushing full buffer", func(t *testing.T) {
		stream := fullFailingStream(errCanceled)
		if err := stream.writeString("x"); !errors.Is(err, errCanceled) {
			t.Fatalf("writeString() error = %v", err)
		}
	})
	t.Run("write string after buffering", func(t *testing.T) {
		stream := stream{check: failCheckAt(2, errCanceled), dst: io.Discard}
		if err := stream.writeString("x"); !errors.Is(err, errCanceled) {
			t.Fatalf("writeString() error = %v", err)
		}
	})
	t.Run("write bytes before buffering", func(t *testing.T) {
		stream := stream{check: func() error { return errCanceled }, dst: io.Discard}
		if err := stream.writeBytes([]byte("x")); !errors.Is(err, errCanceled) {
			t.Fatalf("writeBytes() error = %v", err)
		}
	})
	t.Run("write bytes while flushing full buffer", func(t *testing.T) {
		stream := fullFailingStream(errCanceled)
		if err := stream.writeBytes([]byte("x")); !errors.Is(err, errCanceled) {
			t.Fatalf("writeBytes() error = %v", err)
		}
	})
	t.Run("write bytes after buffering", func(t *testing.T) {
		stream := stream{check: failCheckAt(2, errCanceled), dst: io.Discard}
		if err := stream.writeBytes([]byte("x")); !errors.Is(err, errCanceled) {
			t.Fatalf("writeBytes() error = %v", err)
		}
	})
	t.Run("flush before external write", func(t *testing.T) {
		stream := stream{check: func() error { return errCanceled }, dst: io.Discard, length: 1}
		if err := stream.flush(); !errors.Is(err, errCanceled) {
			t.Fatalf("flush() error = %v", err)
		}
	})
	t.Run("finish before newline", func(t *testing.T) {
		stream := stream{check: func() error { return errCanceled }, dst: io.Discard}
		if err := stream.finish(); !errors.Is(err, errCanceled) {
			t.Fatalf("finish() error = %v", err)
		}
	})
	t.Run("page document", func(t *testing.T) {
		err := WriteRecordPage(io.Discard, capturevalue.RecordPage{}, func() error { return errCanceled })
		if !errors.Is(err, errCanceled) {
			t.Fatalf("WriteRecordPage() error = %v", err)
		}
	})
	t.Run("detail document", func(t *testing.T) {
		err := WriteRecordDetail(io.Discard, capturevalue.RecordDetail{}, func() error { return errCanceled })
		if !errors.Is(err, errCanceled) {
			t.Fatalf("WriteRecordDetail() error = %v", err)
		}
	})
}

func TestDocumentWriterCoversRepeatedAndNullWireShapes(t *testing.T) {
	t.Parallel()

	page := capturevalue.RecordPage{
		Records: []capturevalue.RecordSummary{{RecordID: "record-1"}, {RecordID: "record-2"}},
		GatewayTraces: []capturevalue.GatewayTraceSummary{
			{
				GatewayTraceID: "trace-1",
				Entries: []capturevalue.TraceEntry{
					{EntryID: "entry-1"},
					{EntryID: "entry-2"},
				},
			},
			{GatewayTraceID: "trace-2", Entries: nil},
		},
	}
	if payload, _ := encodePage(t, page); len(payload) == 0 {
		t.Fatal("repeated page encoded an empty payload")
	}

	detail := capturevalue.RecordDetail{
		WebSocket: &capturevalue.WebSocketExchangeDetail{
			Messages: []capturevalue.MessageSnapshot{{MessageID: "message-1"}, {MessageID: "message-2"}},
		},
	}
	if payload, _ := encodeDetail(t, detail); len(payload) == 0 {
		t.Fatal("repeated websocket detail encoded an empty payload")
	}

	stream := stream{check: func() error { return nil }, dst: io.Discard}
	writer := documentWriter{sink: &stream}
	writeQueryHTTPResponseJSON(&writer, nil)
	writeQueryWebSocketCloseJSON(&writer, nil)
	writeWebSocketHandshakeJSON(&writer, nil)
	writeHeadersJSON(&writer, map[string][]string{"X-Nil": nil, "X-Many": {"first", "second"}})
	writeStringsJSON(&writer, nil)
	if writer.err != nil {
		t.Fatalf("null wire helpers error = %v", writer.err)
	}
	if err := stream.finish(); err != nil {
		t.Fatalf("flush null wire helpers: %v", err)
	}
}

func TestDocumentWriterPreservesFirstError(t *testing.T) {
	t.Parallel()

	errSink := errors.New("sink failed")
	writer := documentWriter{sink: &coverageTextSink{}, err: errSink}
	writer.raw("ignored")
	writer.string("ignored")
	writer.int64(1)
	writer.uint64(1)
	writer.time(time.Unix(0, 0))
	if !errors.Is(writer.err, errSink) {
		t.Fatalf("preexisting writer error = %v", writer.err)
	}

	writer = documentWriter{sink: &coverageTextSink{failByte: errSink}}
	writer.time(time.Unix(0, 0))
	if !errors.Is(writer.err, errSink) {
		t.Fatalf("timestamp opening quote error = %v", writer.err)
	}

	writer = documentWriter{sink: &coverageTextSink{failBytes: errSink}}
	writer.time(time.Unix(0, 0))
	if !errors.Is(writer.err, errSink) {
		t.Fatalf("timestamp body error = %v", writer.err)
	}
}

func fullFailingStream(err error) stream {
	return stream{
		check:  func() error { return nil },
		dst:    failingWriter{err: err},
		length: ChunkBytes,
	}
}

func failCheckAt(target int, failure error) Check {
	calls := 0
	return func() error {
		calls++
		if calls == target {
			return failure
		}
		return nil
	}
}

type coverageTextSink struct {
	failByte  error
	failBytes error
}

func (sink *coverageTextSink) writeByte(byte) error {
	return sink.failByte
}

func (*coverageTextSink) writeString(string) error {
	return nil
}

func (sink *coverageTextSink) writeBytes([]byte) error {
	return sink.failBytes
}
