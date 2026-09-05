package wire

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

type testSource struct {
	payload  []byte
	framing  upstreamtransport.BodyFraming
	trailers http.Header
	open     func() (io.ReadCloser, error)
}

func (s testSource) Open() (io.ReadCloser, error) {
	if s.open != nil {
		return s.open()
	}
	return io.NopCloser(bytes.NewReader(s.payload)), nil
}
func (s testSource) Framing() upstreamtransport.BodyFraming { return s.framing }
func (s testSource) Trailers() http.Header                  { return s.trailers.Clone() }
func source(payload string) testSource {
	return testSource{payload: []byte(payload), framing: upstreamtransport.BodyFraming{HasBody: true, ContentLength: int64(len(payload)), Complete: true}}
}
func TestSSEKnownEventsPreserveWireAndOpaqueText(t *testing.T) {
	s := testSession()
	ctx := context.Background()
	mapped, err := s.RequestJSON(ctx, []byte(`{"thread_id":"thread"}`))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct{ input, want string }{
		{": comment\r\nevent: response.created\r\ndata: " + string(mapped) + "\r\nid: abc\r\n\r\n", ": comment\r\nevent: response.created\r\ndata: {\"thread_id\":\"thread\"}\r\nid: abc\r\n\r\n"},
		{"event: response.created\ndata: {\ndata: \"thread_id\":" + quote(strings.TrimSuffix(strings.TrimPrefix(string(mapped), `{"thread_id":"`), `"}`)) + "}\n\n", "event: response.created\ndata: {\ndata: \"thread_id\":\"thread\"}\n\n"},
		{"event:future\ndata: " + string(mapped) + "\n\n", "event:future\ndata: " + string(mapped) + "\n\n"},
		{"data: [DONE]\n\n", "data: [DONE]\n\n"},
		{"data\n\n", "data\n\n"},
		{"event: response.created\rdata:" + string(mapped) + "\r\r", "event: response.created\rdata:{\"thread_id\":\"thread\"}\r\r"},
		{"data: {\"type\":\"future.event\",\"text\":\"" + string(bytes.Trim(mapped, `{}`)) + "\"}\n\n", "data: {\"type\":\"future.event\",\"text\":\"" + string(bytes.Trim(mapped, `{}`)) + "\"}\n\n"},
	}
	for _, test := range tests {
		got, err := s.ServerSSE(ctx, []byte(test.input))
		if err != nil || string(got) != test.want {
			t.Fatalf("got %q want %q err %v", got, test.want, err)
		}
		var output bytes.Buffer
		if err = s.RestoreStream(ctx, strings.NewReader(test.input), &output, "text/event-stream; charset=utf-8"); err != nil || output.String() != test.want {
			t.Fatalf("stream got %q want %q err %v", output.String(), test.want, err)
		}
	}
	var output bytes.Buffer
	if err = s.RestoreStream(ctx, strings.NewReader("opaque"), &output, "text/plain"); err != nil || output.String() != "opaque" {
		t.Fatal(err, output.String())
	}
	s = testSession()
	_, err = s.ServerSSE(ctx, []byte("event: response.created\ndata: {\"thread_id\":1}\n\n"))
	var failure *Failure
	if !errors.As(err, &failure) {
		t.Fatal(err)
	}
}
func TestRequestBodyStreamsBeforeEOFAndRetainsOriginalForReopen(t *testing.T) {
	s := testSession()
	ctx := context.Background()
	prefix := `{"thread_id":"thread","input":"` + strings.Repeat("x", 16384)
	tail := `"}`
	release := make(chan struct{})
	original := source(prefix + tail)
	original.framing.Complete = false
	original.open = func() (io.ReadCloser, error) {
		reader, writer := io.Pipe()
		go func() {
			_, _ = io.WriteString(writer, prefix)
			<-release
			_, _ = io.WriteString(writer, tail)
			_ = writer.Close()
		}()
		return reader, nil
	}
	derived, err := s.RequestBody(ctx, original, nil)
	if err != nil {
		t.Fatal(err)
	}
	if derived.Framing().ContentLength != -1 {
		t.Fatal(derived.Framing())
	}
	reader, err := derived.Open()
	if err != nil {
		t.Fatal(err)
	}
	first := make(chan []byte, 1)
	go func() { buffer := make([]byte, 128); n, _ := reader.Read(buffer); first <- buffer[:n] }()
	select {
	case got := <-first:
		if !bytes.Contains(got, []byte("mapped-login-client-thread-thread")) {
			t.Fatalf("%s", got)
		}
	case <-time.After(2 * time.Second):
		close(release)
		_ = reader.Close()
		t.Fatal("waited for complete upload")
	}
	close(release)
	if _, err = io.Copy(io.Discard, reader); err != nil {
		t.Fatal(err)
	}
	_ = reader.Close()
	reopen, err := derived.Open()
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(reopen)
	_ = reopen.Close()
	if err != nil || !bytes.Contains(got, []byte("mapped-login-client-thread-thread")) {
		t.Fatal(err)
	}
	if string(original.payload) != prefix+tail {
		t.Fatal("original modified")
	}
}
func TestRequestBodyStickyLateFailureAndEncoding(t *testing.T) {
	s := testSession()
	derived, err := s.RequestBody(context.Background(), source(`{"thread_id":"thread","input":[1,}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := derived.Open()
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(reader)
	_ = reader.Close()
	var failure *Failure
	if !errors.As(err, &failure) || s.Failure() != failure {
		t.Fatal(err)
	}
	_, err = derived.Open()
	if err != failure {
		t.Fatal("reopen bypassed failure", err)
	}
	s = testSession()
	_, err = s.RequestBody(context.Background(), source("{}"), []string{"br"})
	if !errors.As(err, &failure) || failure.Stage != "encoding" {
		t.Fatal(err)
	}
	s = testSession()
	var encoded bytes.Buffer
	writer := gzip.NewWriter(&encoded)
	_, _ = io.WriteString(writer, `{"thread_id":"thread"}`)
	_ = writer.Close()
	original := source(encoded.String())
	original.trailers = http.Header{"X-End": {"yes"}}
	derived, err = s.RequestBody(context.Background(), original, []string{"gzip"})
	if err != nil {
		t.Fatal(err)
	}
	reader, err = derived.Open()
	if err != nil {
		t.Fatal(err)
	}
	decoder, err := gzip.NewReader(reader)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(decoder)
	_ = decoder.Close()
	_ = reader.Close()
	if err != nil || !bytes.Contains(got, []byte("mapped-login-client-thread-thread")) || derived.Trailers().Get("X-End") != "yes" {
		t.Fatal(string(got), err)
	}
}
func TestRestoreResponseOwnershipFramingAndCoding(t *testing.T) {
	s := testSession()
	ctx := context.Background()
	mapped, err := s.RequestJSON(ctx, []byte(`{"thread_id":"thread"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, coding := range []string{"", "gzip"} {
		payload := mapped
		if coding != "" {
			var encoded bytes.Buffer
			writer := gzip.NewWriter(&encoded)
			_, _ = writer.Write(payload)
			_ = writer.Close()
			payload = encoded.Bytes()
		}
		head := upstreamtransport.ResponseHead{ContentLength: int64(len(payload)), Header: http.Header{"Content-Type": {"application/json"}, "Content-Encoding": {coding}, "Content-Length": {"999"}, "Etag": {"original"}, "Thread-Id": {"mapped-login-client-thread-thread"}}, SourceHeader: http.Header{"Thread-Id": {"mapped-login-client-thread-thread"}}, Trailer: http.Header{"X-End": {"yes"}}}
		restored, body, err := s.RestoreResponse(ctx, head, io.NopCloser(bytes.NewReader(payload)))
		if err != nil {
			t.Fatal(err)
		}
		if restored.ContentLength != -1 || restored.Header.Get("Content-Length") != "" || restored.Header.Get("Etag") != "" || restored.Header.Get("Content-Encoding") != coding || restored.Header.Get("Thread-Id") != "thread" || restored.SourceHeader.Get("Thread-Id") == "thread" || restored.Trailer.Get("X-End") != "yes" {
			t.Fatal(restored)
		}
		var decoded io.Reader = body
		if coding != "" {
			decoder, decodeErr := gzip.NewReader(body)
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			defer decoder.Close()
			decoded = decoder
		}
		result, err := io.ReadAll(decoded)
		_ = body.Close()
		if err != nil || string(result) != `{"thread_id":"thread"}` {
			t.Fatal(string(result), err)
		}
	}
	head := upstreamtransport.ResponseHead{Header: http.Header{"Content-Type": {"image/png"}}}
	original := io.NopCloser(strings.NewReader("opaque"))
	_, body, err := s.RestoreResponse(ctx, head, original)
	if err != nil || body != original {
		t.Fatal(err)
	}
	_ = body.Close()
	s.target.Policy.Enabled = false
	_, body, err = s.RestoreResponse(ctx, head, original)
	if err != nil || body != original {
		t.Fatal(err)
	}
}
