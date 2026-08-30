package upstreamtransport

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func TestNormalizeEventStreamDecodesSupportedRepresentations(t *testing.T) {
	payload := []byte("event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n")
	tests := []struct {
		name           string
		codings        []string
		encoding       string
		sourceEncoding string
	}{
		{name: "gzip", codings: []string{"gzip"}, encoding: "GZip", sourceEncoding: "gzip"},
		{name: "deflate", codings: []string{"deflate"}, encoding: "deflate", sourceEncoding: "deflate"},
		{name: "zstd", codings: []string{"zstd"}, encoding: "ZSTD", sourceEncoding: "zstd"},
		{name: "layered", codings: []string{"gzip", "zstd"}, encoding: "gzip, zstd", sourceEncoding: "gzip,zstd"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wire := encode(t, payload, test.codings)
			source := &trackedBody{Reader: bytes.NewReader(wire)}
			header := http.Header{
				"Content-Encoding": []string{test.encoding},
				"Content-Length":   []string{"999"},
				"Content-Type":     []string{"text/event-stream"},
				"Etag":             []string{"encoded-validator"},
				"X-Upstream":       []string{"kept"},
			}
			trailer := http.Header{"Digest": []string{"encoded-digest"}}
			normalized, err := NormalizeEventStream(ResponseHead{
				Header: header, Trailer: trailer, ContentLength: int64(len(wire)),
			}, source)
			if err != nil {
				t.Fatal(err)
			}
			if source.reads.Load() != 0 {
				t.Fatal("normalization read from upstream before the response coordinator started")
			}
			decoded, err := io.ReadAll(normalized.Body)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(decoded, payload) {
				t.Fatalf("decoded body = %q, want %q", decoded, payload)
			}
			if normalized.WireBytesRead() != int64(len(wire)) {
				t.Fatalf("wire bytes = %d, want %d", normalized.WireBytesRead(), len(wire))
			}
			if !normalized.Transformed || normalized.Head.ContentLength != -1 || normalized.Head.Trailer != nil {
				t.Fatalf("normalized metadata = transformed:%v length:%d trailer:%v", normalized.Transformed, normalized.Head.ContentLength, normalized.Head.Trailer)
			}
			if normalized.SourceEncoding != test.sourceEncoding {
				t.Fatalf("source encoding = %q, want %q", normalized.SourceEncoding, test.sourceEncoding)
			}
			for _, name := range invalidatedRepresentationHeaders {
				if got := normalized.Head.Header.Get(name); got != "" {
					t.Errorf("normalized %s = %q, want absent", name, got)
				}
			}
			if normalized.Head.Header.Get("Content-Type") != "text/event-stream" || normalized.Head.Header.Get("X-Upstream") != "kept" {
				t.Fatalf("normalized header = %#v", normalized.Head.Header)
			}
			if header.Get("Content-Encoding") != test.encoding || header.Get("Etag") != "encoded-validator" {
				t.Fatal("normalization mutated the source header snapshot")
			}
			if err := normalized.Body.Close(); err != nil {
				t.Fatal(err)
			}
			if source.closes.Load() != 1 {
				t.Fatalf("source closes = %d, want 1", source.closes.Load())
			}
		})
	}
}

func TestNormalizeEventStreamIdentityPreservesRepresentation(t *testing.T) {
	source := &trackedBody{Reader: bytes.NewReader([]byte("data: ok\n\n"))}
	header := http.Header{"Content-Encoding": []string{"identity"}, "Content-Length": []string{"10"}}
	trailer := http.Header{"X-Final": []string{"yes"}}
	normalized, err := NormalizeEventStream(ResponseHead{
		Header: header, Trailer: trailer, ContentLength: 10,
	}, source)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Body != source || normalized.Transformed || normalized.Head.ContentLength != 10 || normalized.Head.Trailer.Get("X-Final") != "yes" {
		t.Fatalf("identity normalization = %#v", normalized)
	}
	if normalized.WireBytesRead() != 0 {
		t.Fatalf("identity wire counter = %d, want disabled", normalized.WireBytesRead())
	}
	normalized.Head.Header.Set("X-Test", "changed")
	if header.Get("X-Test") != "" {
		t.Fatal("identity normalization exposed the source header to mutation")
	}
}

func TestNormalizeEventStreamRejectsInvalidOrUnsupportedCodingWithoutTakingBody(t *testing.T) {
	tests := []struct {
		name     string
		encoding string
		failure  Failure
	}{
		{name: "empty layer", encoding: "gzip,", failure: FailureInvalidEncoding},
		{name: "too many layers", encoding: "gzip,gzip,gzip,gzip,gzip", failure: FailureInvalidEncoding},
		{name: "brotli", encoding: "br", failure: FailureUnsupportedEncoding},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := &trackedBody{Reader: bytes.NewReader(nil)}
			_, err := NormalizeEventStream(ResponseHead{
				Header: http.Header{"Content-Encoding": []string{test.encoding}},
			}, source)
			var decodeErr *DecodeError
			if !errors.As(err, &decodeErr) || decodeErr.Failure != test.failure {
				t.Fatalf("error = %v, want failure %s", err, test.failure)
			}
			if source.reads.Load() != 0 || source.closes.Load() != 0 {
				t.Fatalf("source ownership changed: reads=%d closes=%d", source.reads.Load(), source.closes.Load())
			}
		})
	}
}

func TestNormalizedEventStreamReportsCorruptWire(t *testing.T) {
	source := &trackedBody{Reader: bytes.NewReader([]byte("not gzip"))}
	normalized, err := NormalizeEventStream(ResponseHead{
		Header: http.Header{"Content-Encoding": []string{"gzip"}},
	}, source)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(normalized.Body)
	var decodeErr *DecodeError
	if !errors.As(err, &decodeErr) || decodeErr.Failure != FailureContentDecoding {
		t.Fatalf("read error = %v, want content decoding failure", err)
	}
	if source.reads.Load() == 0 {
		t.Fatal("lazy decoder did not read the source")
	}
	if err := normalized.Body.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizedGzipEventStreamProducesDataBeforeUpstreamEOF(t *testing.T) {
	first := []byte("event: response.created\ndata: {\"type\":\"response.created\"}\n\n")
	second := []byte("event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n")
	pipeReader, pipeWriter := io.Pipe()
	source := &trackedBody{Reader: pipeReader}
	normalized, err := NormalizeEventStream(ResponseHead{
		Header: http.Header{"Content-Encoding": []string{"gzip"}},
	}, source)
	if err != nil {
		t.Fatal(err)
	}

	releaseEOF := make(chan struct{})
	writerDone := make(chan error, 1)
	go func() {
		writer := gzip.NewWriter(pipeWriter)
		if _, writeErr := writer.Write(first); writeErr != nil {
			writerDone <- writeErr
			return
		}
		if flushErr := writer.Flush(); flushErr != nil {
			writerDone <- flushErr
			return
		}
		<-releaseEOF
		if _, writeErr := writer.Write(second); writeErr != nil {
			writerDone <- writeErr
			return
		}
		writerDone <- errors.Join(writer.Close(), pipeWriter.Close())
	}()

	firstRead := make(chan error, 1)
	firstBuffer := make([]byte, len(first))
	go func() {
		_, readErr := io.ReadFull(normalized.Body, firstBuffer)
		firstRead <- readErr
	}()
	select {
	case readErr := <-firstRead:
		if readErr != nil {
			close(releaseEOF)
			t.Fatal(readErr)
		}
	case <-time.After(time.Second):
		close(releaseEOF)
		t.Fatal("first decoded SSE event was withheld until upstream EOF")
	}
	if !bytes.Equal(firstBuffer, first) {
		close(releaseEOF)
		t.Fatalf("first decoded event = %q, want %q", firstBuffer, first)
	}
	close(releaseEOF)
	remainder, err := io.ReadAll(normalized.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(remainder, second) {
		t.Fatalf("remaining decoded event = %q, want %q", remainder, second)
	}
	if err := <-writerDone; err != nil {
		t.Fatal(err)
	}
	if err := normalized.Body.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizedEventStreamCloseInterruptsDecoderInitialization(t *testing.T) {
	pipeReader, pipeWriter := io.Pipe()
	source := &trackedBody{Reader: pipeReader}
	normalized, err := NormalizeEventStream(ResponseHead{
		Header: http.Header{"Content-Encoding": []string{"gzip"}},
	}, source)
	if err != nil {
		t.Fatal(err)
	}
	readDone := make(chan error, 1)
	go func() {
		_, readErr := normalized.Body.Read(make([]byte, 1))
		readDone <- readErr
	}()
	deadline := time.After(time.Second)
	for source.reads.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("decoder did not begin its upstream read")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := normalized.Body.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case readErr := <-readDone:
		if readErr == nil {
			t.Fatal("interrupted decoder read returned no error")
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not unblock decoder initialization")
	}
	if source.closes.Load() != 1 {
		t.Fatalf("source closes = %d, want 1", source.closes.Load())
	}
	_ = pipeWriter.Close()
}

type trackedBody struct {
	io.Reader
	reads  atomic.Int32
	closes atomic.Int32
}

func (b *trackedBody) Read(target []byte) (int, error) {
	b.reads.Add(1)
	return b.Reader.Read(target)
}

func (b *trackedBody) Close() error {
	b.closes.Add(1)
	if closer, ok := b.Reader.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

func encode(t *testing.T, payload []byte, codings []string) []byte {
	t.Helper()
	wire := append([]byte(nil), payload...)
	for _, coding := range codings {
		var buffer bytes.Buffer
		switch coding {
		case "gzip":
			writer := gzip.NewWriter(&buffer)
			mustWriteAndClose(t, writer, wire)
		case "deflate":
			writer := zlib.NewWriter(&buffer)
			mustWriteAndClose(t, writer, wire)
		case "zstd":
			writer, err := zstd.NewWriter(&buffer, zstd.WithEncoderConcurrency(1))
			if err != nil {
				t.Fatal(err)
			}
			mustWriteAndClose(t, writer, wire)
		default:
			t.Fatalf("unsupported test coding %q", coding)
		}
		wire = buffer.Bytes()
	}
	return wire
}

type writeCloser interface {
	io.Writer
	Close() error
}

func mustWriteAndClose(t *testing.T, writer writeCloser, payload []byte) {
	t.Helper()
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
}
