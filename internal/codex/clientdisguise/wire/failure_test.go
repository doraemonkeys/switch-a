package wire

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	disguise "github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

type partialErrorReader struct {
	prefix string
	err    error
}

func (r *partialErrorReader) Read(p []byte) (int, error) {
	if r.prefix != "" {
		n := copy(p, r.prefix)
		r.prefix = r.prefix[n:]
		return n, nil
	}
	return 0, r.err
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) / 2, nil }
func TestSampleSnapshotAndTransportFailure(t *testing.T) {
	target := testSession().target
	target.Transport = &disguise.TransportSample{Config: []byte(`{"http_protocol":"http1","alpn":["http/1.1"]}`)}
	s := NewSession(&memoryMapper{}, target, "client", "op")
	target.Transport.Config[0] = '!'
	config, err := s.TransportConfig()
	if err != nil || config.HTTPProtocol != "http1" {
		t.Fatal(config, err)
	}
	if _, err = s.WebSocketTransportConfig(); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"http_protocol":"http2"}`, `{"alpn":["http/1.1","h2"]}`, `{"alpn":["h2"]}`, `{"unsupported":true}`} {
		s = testSession()
		s.target.Transport = &disguise.TransportSample{Config: []byte(raw)}
		_, err = s.WebSocketTransportConfig()
		var failure *Failure
		if !errors.As(err, &failure) || failure.Stage != "transport" {
			t.Fatal(raw, err)
		}
	}
	s = testSession()
	if _, err = s.TransportConfig(); err != nil {
		t.Fatal(err)
	}
	s.target.Policy.Enabled = false
	if _, err = s.TransportConfig(); err != nil {
		t.Fatal(err)
	}
	bare := NewSession(nil, disguise.TargetSnapshot{}, "", "")
	if got, err := bare.Headers(context.Background(), nil); err != nil || got != nil {
		t.Fatal(got, err)
	}
	original := source("opaque")
	body, err := bare.RequestBody(context.Background(), original, nil)
	if err != nil || body.Framing().ContentLength != 6 {
		t.Fatal(err)
	}
	if cloneMap(nil) != nil {
		t.Fatal("nil map")
	}
	if got := snippet(strings.Repeat("x", snippetLimit+1)); len(got) != snippetLimit {
		t.Fatal(len(got))
	}
}
func TestIOFailuresNeverPoisonConversionSession(t *testing.T) {
	cause := errors.New("connection interrupted")
	for _, contentType := range []string{"application/json", "text/event-stream"} {
		prefix := `{"thread_id":`
		if contentType == "text/event-stream" {
			prefix = "event: response.created\ndata: " + prefix
		}
		s := testSession()
		err := s.RestoreStream(context.Background(), &partialErrorReader{prefix: prefix, err: cause}, io.Discard, contentType)
		if !errors.Is(err, cause) || s.Failure() != nil {
			t.Fatal(contentType, err, s.Failure())
		}
		s = testSession()
		head := upstreamtransport.ResponseHead{Header: http.Header{"Content-Type": {contentType}}}
		_, body, err := s.RestoreResponse(context.Background(), head, io.NopCloser(&partialErrorReader{prefix: prefix, err: cause}))
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.ReadAll(body)
		_ = body.Close()
		if !errors.Is(err, cause) || s.Failure() != nil {
			t.Fatal(contentType, err, s.Failure())
		}
		s = testSession()
		payload := "{}"
		if contentType == "text/event-stream" {
			payload = "event: response.created\ndata: {}\n\n"
		}
		err = s.RestoreStream(context.Background(), strings.NewReader(payload), failingWriter{cause}, contentType)
		if !errors.Is(err, cause) || s.Failure() != nil {
			t.Fatal(contentType, err, s.Failure())
		}
		err = s.RestoreStream(context.Background(), strings.NewReader(payload), shortWriter{}, contentType)
		if !errors.Is(err, io.ErrShortWrite) || s.Failure() != nil {
			t.Fatal(contentType, err, s.Failure())
		}
	}
	s := testSession()
	_, body, err := s.RestoreResponse(context.Background(), upstreamtransport.ResponseHead{Header: http.Header{"Content-Type": {"application/json"}}}, io.NopCloser(strings.NewReader("{}")))
	if err != nil {
		t.Fatal(err)
	}
	_ = body.Close()
	if s.Failure() != nil {
		t.Fatal(s.Failure())
	}
}
func TestProtocolEnvelopeClassificationAndBoundaryFailures(t *testing.T) {
	for _, raw := range []string{`{}`, `{"foo":1,"type":"future.event"}`, `{"foo":1,"type":7}`, `{"foo":}`, `{"foo":1,,}`, `{"foo":1}`} {
		got, err := testSession().ServerFrame(context.Background(), []byte(raw))
		if err != nil || string(got) != raw {
			t.Fatal(string(got), err)
		}
	}
	for _, raw := range []string{"", " \n\t", `{"type":"response.create","turn_id":""}`, `{"a":[],"b":{},"c":[[],{}]}`} {
		got, err := testSession().RequestJSON(context.Background(), []byte(raw))
		if err != nil || string(got) != raw {
			t.Fatal(string(got), err)
		}
	}
	malformed := []string{"[", "{", `{"a"`, `{"a":1`, `{"a":"\`, `{"a":"unterminated`, `{"a":null,`, `[1,`, `[1`, `{"a":truX}`, `{"a":1E+}`, `{"a":1e-2f}`, `{"a":.1}`}
	for _, raw := range malformed {
		_, err := testSession().RequestJSON(context.Background(), []byte(raw))
		if err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	s := testSession()
	var output bytes.Buffer
	err := s.RestoreStream(context.Background(), errorReader{io.ErrUnexpectedEOF}, &output, "text/event-stream")
	if !errors.Is(err, io.ErrUnexpectedEOF) || s.Failure() != nil {
		t.Fatal(err)
	}
	s = testSession()
	head := upstreamtransport.ResponseHead{Header: http.Header{"Content-Type": {"application/json"}, "Content-Encoding": {"br"}}}
	if _, _, err = s.RestoreResponse(context.Background(), head, io.NopCloser(strings.NewReader("{}"))); err == nil {
		t.Fatal("unsupported response encoding")
	}
	s = testSession()
	s.mapper.(*memoryMapper).fail = errors.New("mapping")
	head.Header = http.Header{"Thread-Id": {"thread"}}
	if _, _, err = s.RestoreResponse(context.Background(), head, io.NopCloser(strings.NewReader("{}"))); err == nil {
		t.Fatal("response header failure")
	}
	s = testSession()
	s.target.Policy.Enabled = false
	original := []byte("opaque")
	got, err := s.ServerSSE(context.Background(), original)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatal(err)
	}
	s = testSession()
	if err = s.RestoreStream(context.Background(), strings.NewReader("opaque"), &output, "not; valid"); err != nil {
		t.Fatal(err)
	}
}
