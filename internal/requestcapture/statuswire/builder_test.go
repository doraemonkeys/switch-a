package statuswire

import (
	"encoding/json"
	"testing"
)

func TestBuilderEncodesBoundedStatusDocument(t *testing.T) {
	storage := make([]byte, 1024)
	builder := New(storage)
	builder.Literal(`{"text":`)
	text := "quote\" slash\\ controls\b\f\n\r\t\x01 invalid\xff"
	builder.Quoted(text)
	builder.Literal(`,"signed":`)
	builder.Int64(-12)
	builder.Literal(`,"unsigned":`)
	builder.Uint64(34)
	builder.Literal(`,"integer":`)
	builder.Int(56)
	builder.Literal(`,"timestamp":`)
	builder.Timestamp(1_234_567_890)
	builder.Byte(',')
	builder.Process(Process{
		Ceiling:   100,
		Charged:   90,
		Retained:  80,
		Pinned:    70,
		Releasing: 60,
		Temporary: 50,
	})
	builder.Byte('}')

	if builder.Overflowed() {
		t.Fatal("builder unexpectedly overflowed")
	}
	encoded := storage[:builder.Len()]
	if !json.Valid(encoded) {
		t.Fatalf("invalid JSON: %q", encoded)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if document["text"] != "quote\" slash\\ controls\b\f\n\r\t\x01 invalid�" {
		t.Fatalf("escaped text = %q", document["text"])
	}
	process := document["process_memory"].(map[string]any)
	if process["ceiling_bytes"] != float64(100) || process["temporary_bytes"] != float64(50) {
		t.Fatalf("process document = %#v", process)
	}
}

func TestQuotedBytesMatchesEncoding(t *testing.T) {
	for _, value := range []string{"plain", "\"\\\b\f\n\r\t\x00", "世界", "bad\xff"} {
		storage := make([]byte, QuotedBytes(value))
		builder := New(storage)
		builder.Quoted(value)
		if builder.Overflowed() || builder.Len() != len(storage) {
			t.Fatalf("Quoted(%q) length = %d overflow=%t, want %d", value, builder.Len(), builder.Overflowed(), len(storage))
		}
	}
}

func TestBuilderFailsClosedAfterOverflow(t *testing.T) {
	storage := make([]byte, 2)
	builder := New(storage)
	builder.Bytes([]byte("ab"))
	builder.Byte('c')
	builder.Literal("ignored")
	builder.Bytes([]byte("ignored"))
	if !builder.Overflowed() || builder.Len() != len(storage) || string(storage) != "ab" {
		t.Fatalf("overflow state: length=%d storage=%q overflow=%t", builder.Len(), storage, builder.Overflowed())
	}

	literal := New(make([]byte, 1))
	literal.Literal("too long")
	if !literal.Overflowed() || literal.Len() != 0 {
		t.Fatalf("literal overflow state: length=%d overflow=%t", literal.Len(), literal.Overflowed())
	}

	bytesBuilder := New(make([]byte, 1))
	bytesBuilder.Bytes([]byte("too long"))
	if !bytesBuilder.Overflowed() || bytesBuilder.Len() != 0 {
		t.Fatalf("bytes overflow state: length=%d overflow=%t", bytesBuilder.Len(), bytesBuilder.Overflowed())
	}
}
