package semantic

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"strings"
	"testing"
)

func TestReviewOuterCodingChecksum(t *testing.T) {
	inner := encoded(t, "deflate", []byte("{\"model\":\"verified\"}"))
	// A valid zlib member may end before the enclosing gzip stream. Enough padding
	// keeps the gzip checksum beyond zlib's read-ahead.
	inner = append(inner, bytes.Repeat([]byte("p"), 128<<10)...)
	wire := encoded(t, "gzip", inner)
	wire[len(wire)-8] ^= 1
	g, err := gzip.NewReader(bytes.NewReader(wire))
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(io.Discard, g)
	if err != gzip.ErrChecksum {
		t.Fatalf("fixture did not fail checksum: %v", err)
	}
	got := Project(context.Background(), bytes.NewReader(wire), Options{ContentEncodingValues: []string{"deflate, gzip"}, MaxDecodedBytes: 1 << 20})
	if got.Model.State != Unavailable || got.Model.Reason != ReasonContentDecoding {
		t.Fatalf("corrupt outer coding published model: %#v", got.Model)
	}
}

func TestReviewNestedReasoningRetention(t *testing.T) {
	value := "\"leaf\""
	for i := 0; i < 9; i++ {
		value = "{\"effort\":" + value + ",\"type\":" + value + "}"
	}
	body := "{\"reasoning\":" + value + "}"
	var captured jsonValue
	s := scanner{reader: bufio.NewReader(strings.NewReader(body)), rootMember: func(k string, v jsonValue) {
		if k == "reasoning" {
			captured = v
		}
	}}
	if err := s.document(); err != nil {
		t.Fatal(err)
	}
	var count func(jsonValue) int
	count = func(v jsonValue) int {
		n := len(v.fields)
		for _, f := range v.fields {
			n += count(f.first)
		}
		return n
	}
	retained := count(captured)
	t.Logf("body=%d bytes, retained field descriptors=%d", len(body), retained)
	if retained > 2 {
		t.Fatalf("wrong-type reasoning scalars retain their nested object trees: %d descriptors", retained)
	}
}
