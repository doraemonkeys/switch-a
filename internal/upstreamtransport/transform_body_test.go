package upstreamtransport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type conversionSource struct {
	payload  []byte
	framing  BodyFraming
	trailers http.Header
	open     func() (io.ReadCloser, error)
}

func (s conversionSource) Open() (io.ReadCloser, error) {
	if s.open != nil {
		return s.open()
	}
	return io.NopCloser(bytes.NewReader(s.payload)), nil
}
func (s conversionSource) Framing() BodyFraming  { return s.framing }
func (s conversionSource) Trailers() http.Header { return s.trailers.Clone() }
func conversionCopy(_ context.Context, source io.Reader, target io.Writer) error {
	_, err := io.Copy(target, source)
	return err
}
func conversionError(stage string, err error) error { return &conversionFailure{stage, err} }

type conversionFailure struct {
	stage string
	cause error
}

func (e *conversionFailure) Error() string { return e.stage + ": " + e.cause.Error() }
func (e *conversionFailure) Unwrap() error { return e.cause }

type countedBody struct {
	io.Reader
	closes atomic.Int32
}

func (b *countedBody) Close() error { b.closes.Add(1); return nil }

type blockedBody struct {
	closed   chan struct{}
	once     sync.Once
	reads    chan struct{}
	readOnce sync.Once
}

func (b *blockedBody) Read([]byte) (int, error) {
	b.readOnce.Do(func() { close(b.reads) })
	<-b.closed
	return 0, io.ErrClosedPipe
}
func (b *blockedBody) Close() error { b.once.Do(func() { close(b.closed) }); return nil }
func TestTransformBodyCodingStacksPreserveRepresentation(t *testing.T) {
	payload := []byte(strings.Repeat("original payload", 4096))
	for _, codings := range [][]string{nil, {"identity"}, {"gzip"}, {"deflate"}, {"zstd"}, {"gzip", "deflate", "zstd"}} {
		var encoded bytes.Buffer
		encoder, finish, err := encodingWriter(&encoded, codings)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = encoder.Write(payload); err != nil {
			t.Fatal(err)
		}
		if err = finish(); err != nil {
			t.Fatal(err)
		}
		source := &countedBody{Reader: bytes.NewReader(encoded.Bytes())}
		result, err := TransformReader(context.Background(), source, []string{strings.Join(codings, ",")}, conversionCopy, conversionError)
		if err != nil {
			t.Fatal(err)
		}
		decoded := newDecodingBody(result, codings, strings.Join(codings, ","))
		got, err := io.ReadAll(decoded)
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatal(err)
		}
		if err = decoded.Close(); err != nil {
			t.Fatal(err)
		}
		if source.closes.Load() != 1 {
			t.Fatalf("source closed %d times", source.closes.Load())
		}
	}
}
func TestTransformReaderLazyCloseAndCancellationJoin(t *testing.T) {
	for _, cancelContext := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		original := &blockedBody{closed: make(chan struct{}), reads: make(chan struct{})}
		result, err := TransformReader(ctx, original, nil, conversionCopy, conversionError)
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-original.reads:
			t.Fatal("preparation read body")
		default:
		}
		readDone := make(chan struct{})
		go func() { _, _ = io.Copy(io.Discard, result); close(readDone) }()
		select {
		case <-original.reads:
		case <-time.After(time.Second):
			t.Fatal("read never started")
		}
		if cancelContext {
			cancel()
		} else {
			if err = result.Close(); err != nil {
				t.Fatal(err)
			}
		}
		select {
		case <-readDone:
		case <-time.After(time.Second):
			t.Fatal("cancellation did not join read")
		}
		_ = result.Close()
		cancel()
	}
	original := &countedBody{Reader: strings.NewReader("never read")}
	result, err := TransformReader(context.Background(), original, nil, conversionCopy, conversionError)
	if err != nil {
		t.Fatal(err)
	}
	_ = result.Close()
	_ = result.Close()
	if original.closes.Load() != 1 {
		t.Fatal(original.closes.Load())
	}
	if _, err = result.Read(make([]byte, 1)); err == nil {
		t.Fatal("read after close")
	}
}
func TestTransformSourceFramingReopenAndTerminalFailure(t *testing.T) {
	original := conversionSource{payload: []byte("hello"), framing: BodyFraming{ProtocolMajor: 2, HasBody: true, ContentLength: 5, TrailerKeys: []string{"X-End"}, Complete: true}, trailers: http.Header{"X-End": {"done"}}}
	derived, err := TransformSource(context.Background(), original, nil, conversionCopy, conversionError)
	if err != nil {
		t.Fatal(err)
	}
	framing := derived.Framing()
	if framing.ContentLength != -1 || !framing.HasBody || derived.Trailers().Get("X-End") != "done" {
		t.Fatal(framing)
	}
	framing.TrailerKeys[0] = "mutated"
	if original.framing.TrailerKeys[0] != "X-End" {
		t.Fatal("framing alias")
	}
	for range 2 {
		reader, openErr := derived.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}
		got, readErr := io.ReadAll(reader)
		_ = reader.Close()
		if readErr != nil || string(got) != "hello" {
			t.Fatal(string(got), readErr)
		}
	}
	cause := errors.New("bad conversion")
	derived, err = TransformSource(context.Background(), original, nil, func(context.Context, io.Reader, io.Writer) error { return cause }, conversionError)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := derived.Open()
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(reader)
	_ = reader.Close()
	if !errors.Is(err, cause) {
		t.Fatal(err)
	}
	_, again := derived.Open()
	if again != err {
		t.Fatal("terminal failure bypassed")
	}
	original.open = func() (io.ReadCloser, error) { return nil, cause }
	derived, err = TransformSource(context.Background(), original, nil, conversionCopy, conversionError)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = derived.Open(); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	empty := conversionSource{framing: BodyFraming{Complete: true}}
	derived, err = TransformSource(context.Background(), empty, nil, conversionCopy, conversionError)
	if err != nil || derived.Framing().ContentLength != 0 {
		t.Fatal(err)
	}
	derived, err = TransformSource(context.Background(), nil, nil, conversionCopy, conversionError)
	if err != nil || derived != nil {
		t.Fatal(err)
	}
}
func TestTransformEncodingAndFailureOwnership(t *testing.T) {
	for _, encoding := range [][]string{{"br"}, {"gzip,,deflate"}, {"gzip,gzip,gzip,gzip,gzip"}} {
		original := &countedBody{Reader: strings.NewReader("original")}
		if _, err := TransformReader(context.Background(), original, encoding, conversionCopy, conversionError); err == nil {
			t.Fatal("invalid coding")
		}
		if original.closes.Load() != 0 {
			t.Fatal("failed prepare took ownership")
		}
	}
	if _, err := TransformReader(context.Background(), nil, nil, conversionCopy, conversionError); err == nil {
		t.Fatal("nil body")
	}
	original := &countedBody{Reader: strings.NewReader("invalid gzip")}
	result, err := TransformReader(context.Background(), original, []string{"gzip"}, conversionCopy, conversionError)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(result)
	_ = result.Close()
	var failure *conversionFailure
	if !errors.As(err, &failure) {
		t.Fatal(err)
	}
	if _, _, err = encodingWriter(io.Discard, []string{"br"}); err == nil {
		t.Fatal("invalid encoder")
	}
	derived, err := TransformSource(context.Background(), conversionSource{}, []string{"br"}, conversionCopy, conversionError)
	if err == nil || derived != nil {
		t.Fatal("source coding validation")
	}
}
func TestSetBodySourcePreservesPreparedHeadersAndExactBodyOwnership(t *testing.T) {
	request, err := http.NewRequest(http.MethodPost, "http://example.test/path?q=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	old := &countedBody{Reader: strings.NewReader("old")}
	request.Body = old
	request.Header = http.Header{"Authorization": {"Bearer prepared"}, "Cookie": {"prepared=1"}, "Content-Length": {"3"}, "Content-Encoding": {"gzip"}}
	source := conversionSource{payload: []byte("derived"), framing: BodyFraming{HasBody: true, ContentLength: -1}}
	if err = SetBodySource(request, source); err != nil {
		t.Fatal(err)
	}
	if old.closes.Load() != 1 || request.Header.Get("Authorization") != "Bearer prepared" || request.Header.Get("Cookie") != "prepared=1" || request.Header.Get("Content-Encoding") != "gzip" || request.Header.Get("Content-Length") != "" || request.ContentLength != -1 || requestBodySource(request) == nil {
		t.Fatal(request)
	}
	got, err := io.ReadAll(request.Body)
	_ = request.Body.Close()
	if err != nil || string(got) != "derived" {
		t.Fatal(err)
	}
	if err = SetBodySource(nil, source); err == nil {
		t.Fatal("nil request")
	}
}
