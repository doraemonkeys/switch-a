package framing

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"testing"
)

func TestDecoderIdentityAndGzip(t *testing.T) {
	t.Parallel()
	payload := []byte("decoded payload")

	identity, err := NewDecoder(CodingIdentity, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	assertDecoded(t, identity, payload)

	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	decoded, err := NewDecoder(CodingGzip, bytes.NewReader(compressed.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	assertDecoded(t, decoded, payload)
}

func TestDecoderFailOpenReasons(t *testing.T) {
	t.Parallel()
	for name, coding := range map[string]ContentCoding{"brotli": CodingBrotli, "unknown": 99} {
		t.Run(name, func(t *testing.T) {
			_, err := NewDecoder(coding, bytes.NewReader(nil))
			assertReason(t, err, FailureUnsupportedEncoding)
		})
	}
	_, err := NewDecoder(CodingIdentity, nil)
	assertReason(t, err, FailureInternal)
	_, err = NewDecoder(CodingGzip, bytes.NewReader([]byte("not gzip")))
	assertReason(t, err, FailureContentDecoding)

	reader, err := NewDecoder(CodingGzip, bytes.NewReader([]byte{0x1f, 0x8b, 0x08, 0, 0, 0, 0, 0, 0, 0xff}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(reader)
	assertReason(t, err, FailureContentDecoding)
}

func TestDecodeReadCloserWrapsReadAndClose(t *testing.T) {
	t.Parallel()
	readFailure := errors.New("read failure")
	closeFailure := errors.New("close failure")
	closer := &failingCloser{err: closeFailure}
	reader := &Decoder{reader: failingReader{err: readFailure}, closer: closer}
	_, err := reader.Read(make([]byte, 1))
	assertReason(t, err, FailureContentDecoding)
	if !errors.Is(err, readFailure) {
		t.Fatal("read cause was lost")
	}
	if !errors.Is(reader.Close(), closeFailure) {
		t.Fatal("close cause was lost")
	}
	if !errors.Is(reader.Close(), closeFailure) || closer.calls != 1 {
		t.Fatalf("idempotent close calls=%d err=%v", closer.calls, reader.Close())
	}
	if err := (&Decoder{reader: bytes.NewReader(nil)}).Close(); err != nil {
		t.Fatal(err)
	}
	if err := (*Decoder)(nil).Close(); err != nil {
		t.Fatal(err)
	}
	_, err = (*Decoder)(nil).Read(nil)
	assertReason(t, err, FailureInternal)
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

type failingCloser struct {
	err   error
	calls int
}

func (c *failingCloser) Close() error {
	c.calls++
	return c.err
}

func assertDecoded(t *testing.T, reader io.ReadCloser, want []byte) {
	t.Helper()
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("decoded = %q, want %q", got, want)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
}
