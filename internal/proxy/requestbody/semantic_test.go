package requestbody

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestDecoderDecodesSupportedContentCodings(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"model":"gpt-5.6-sol","reasoning":{"effort":"xhigh"}}`)
	tests := []struct {
		name     string
		wire     func(*testing.T, []byte) []byte
		codings  []string
		wantSame bool
	}{
		{name: "implicit identity", wire: unchanged, wantSame: true},
		{name: "explicit identity", wire: unchanged, codings: []string{" identity "}, wantSame: true},
		{name: "gzip", wire: encodeGzip, codings: []string{"GZip"}},
		{name: "deflate", wire: encodeDeflate, codings: []string{"deflate"}},
		{name: "zstd", wire: encodeZstd, codings: []string{"zstd"}},
		{
			name:    "encoding chain across header values",
			wire:    func(t *testing.T, body []byte) []byte { return encodeGzip(t, encodeZstd(t, body)) },
			codings: []string{" zstd", "gzip "},
		},
	}

	decoder := NewDecoder()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wire := test.wire(t, payload)
			decoded, err := decoder.Decode(wire, test.codings, int64(len(payload)))
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if !bytes.Equal(decoded, payload) {
				t.Fatalf("Decode() = %q, want %q", decoded, payload)
			}
			if test.wantSame && len(wire) > 0 && &decoded[0] != &wire[0] {
				t.Fatal("identity decoding must reuse the wire view")
			}
		})
	}
}

func TestDecoderReturnsEmptyWireWithoutOpeningDecoder(t *testing.T) {
	t.Parallel()
	decoded, err := NewDecoder().Decode(nil, []string{"zstd"}, 1)
	if err != nil || decoded != nil {
		t.Fatalf("Decode() = %v, %v; want nil, nil", decoded, err)
	}
}

func TestDecoderClassifiesFailures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		wire     []byte
		codings  []string
		limit    int64
		failure  Failure
		coding   string
		contains string
	}{
		{name: "invalid limit", wire: []byte("encoded"), codings: []string{"gzip"}, failure: FailureInvalidLimit, coding: "gzip", contains: "must be positive"},
		{name: "empty coding", wire: []byte("body"), codings: []string{"gzip,"}, limit: 16, failure: FailureInvalidEncoding, coding: "gzip,", contains: "empty coding"},
		{name: "too many layers", wire: []byte("body"), codings: []string{"identity,identity,identity,identity,identity"}, limit: 16, failure: FailureInvalidEncoding, coding: "identity,identity,identity,identity,identity", contains: "more than"},
		{name: "unsupported", wire: []byte("body"), codings: []string{"br"}, limit: 16, failure: FailureUnsupportedEncoding, coding: "br", contains: "not supported"},
		{name: "malformed gzip", wire: []byte("body"), codings: []string{"gzip"}, limit: 16, failure: FailureContentDecoding, coding: "gzip"},
		{name: "malformed deflate", wire: []byte("body"), codings: []string{"deflate"}, limit: 16, failure: FailureContentDecoding, coding: "deflate"},
		{name: "malformed zstd", wire: []byte("body"), codings: []string{"zstd"}, limit: 16, failure: FailureContentDecoding, coding: "zstd"},
	}

	decoder := NewDecoder()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := decoder.Decode(test.wire, test.codings, test.limit)
			var decodeErr *DecodeError
			if !errors.As(err, &decodeErr) {
				t.Fatalf("Decode() error = %v, want *DecodeError", err)
			}
			if decodeErr.Failure != test.failure || decodeErr.Coding != test.coding {
				t.Fatalf("DecodeError = (%q, %q), want (%q, %q)", decodeErr.Failure, decodeErr.Coding, test.failure, test.coding)
			}
			if test.contains != "" && !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("Decode() error = %q, want substring %q", err, test.contains)
			}
			if errors.Unwrap(err) == nil {
				t.Fatal("DecodeError must preserve its cause")
			}
		})
	}
}

func TestDecodeErrorFormattingExcludesHeaderCoding(t *testing.T) {
	t.Parallel()
	const secretCoding = "codeql-secret-content-coding"
	err := newDecodeError(FailureUnsupportedEncoding, secretCoding, errors.New("typed cause"))
	if rendered := err.Error(); strings.Contains(rendered, secretCoding) {
		t.Fatalf("DecodeError formatting contains header coding: %q", rendered)
	}
}

func TestDecoderBoundsDecodedOutput(t *testing.T) {
	t.Parallel()
	payload := bytes.Repeat([]byte("x"), 4096)
	_, err := NewDecoder().Decode(encodeZstd(t, payload), []string{"zstd"}, 128)
	var decodeErr *DecodeError
	if !errors.As(err, &decodeErr) || decodeErr.Failure != FailureDecodedBodyTooLarge {
		t.Fatalf("Decode() error = %v, want %q", err, FailureDecodedBodyTooLarge)
	}
}

func TestOpenDecoderPropagatesReadFailure(t *testing.T) {
	t.Parallel()
	reader := io.MultiReader(
		bytes.NewReader(encodeGzip(t, []byte("payload"))[:10]),
		errorReader{},
	)
	_, err := io.ReadAll(mustOpenDecoder(t, "gzip", reader))
	if err == nil {
		t.Fatal("gzip reader must propagate a truncated stream failure")
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func mustOpenDecoder(t *testing.T, coding string, source io.Reader) io.Reader {
	t.Helper()
	closers := make([]func(), 0, 1)
	reader, err := openDecoder(coding, source, 1024, &closers)
	if err != nil {
		t.Fatalf("openDecoder() error = %v", err)
	}
	t.Cleanup(func() { closeDecoders(closers) })
	return reader
}

func unchanged(_ *testing.T, body []byte) []byte { return body }

func encodeGzip(t *testing.T, body []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(body); err != nil {
		t.Fatalf("write gzip body: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close gzip body: %v", err)
	}
	return buffer.Bytes()
}

func encodeDeflate(t *testing.T, body []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zlib.NewWriter(&buffer)
	if _, err := writer.Write(body); err != nil {
		t.Fatalf("write deflate body: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close deflate body: %v", err)
	}
	return buffer.Bytes()
}

func encodeZstd(t *testing.T, body []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer, err := zstd.NewWriter(&buffer, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatalf("create zstd writer: %v", err)
	}
	if _, err := writer.Write(body); err != nil {
		t.Fatalf("write zstd body: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zstd body: %v", err)
	}
	return buffer.Bytes()
}
