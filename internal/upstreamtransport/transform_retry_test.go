package upstreamtransport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
)

type conversionRoundTripper func(*http.Request) (*http.Response, error)

func (f conversionRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
func TestConversionFailurePreemptsConcurrentNativeReopen(t *testing.T) {
	cause := errors.New("conversion implementation failed")
	original := conversionSource{payload: []byte("original"), framing: BodyFraming{HasBody: true, ContentLength: -1}}
	source, err := TransformSource(context.Background(), original, nil, func(context.Context, io.Reader, io.Writer) error { return cause }, conversionError)
	if err != nil {
		t.Fatal(err)
	}
	request, err := BuildRequest(context.Background(), http.MethodPost, "http://example.test", source, &http.Request{Header: make(http.Header)})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	retryEligible := false
	transport := NewWithRoundTripper(conversionRoundTripper(func(request *http.Request) (*http.Response, error) {
		calls++
		_, _ = io.Copy(io.Discard, request.Body)
		_ = request.Body.Close()
		return nil, errTransmissionReopen
	}))
	_, _, err = transport.Fetch(context.Background(), request, ExecutionOptions{Observe: func(event TransmissionEvent) { retryEligible = retryEligible || event.RetryEligible }})
	if !errors.Is(err, cause) || calls != 1 || retryEligible {
		t.Fatal(err, calls, retryEligible)
	}
}
func TestEncoderFailureRetainsPhysicalEvidence(t *testing.T) {
	original := &countedBody{Reader: &fragmentReader{fragments: [][]byte{[]byte("not gzip"), []byte(" more bytes")}}}
	reader, err := TransformReader(context.Background(), original, []string{"gzip"}, conversionCopy, conversionError)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(reader)
	_ = reader.Close()
	var evidence *TransformationError
	if !errors.As(err, &evidence) || evidence.OriginalSnippet == "" || evidence.Unwrap() == nil || evidence.Error() == "" {
		t.Fatal(err, evidence)
	}
	observation := &transformIOObservation{}
	observation.record(make([]byte, transformSnippetBytes), nil)
	observation.record([]byte("tail"), io.EOF)
	snippet, failure := observation.snapshot()
	if failure != nil || len(snippet) != transformSnippetBytes || snippet[len(snippet)-4:] != "tail" {
		t.Fatal(failure, len(snippet))
	}
}

type fragmentReader struct{ fragments [][]byte }

func (r *fragmentReader) Read(target []byte) (int, error) {
	if len(r.fragments) == 0 {
		return 0, io.EOF
	}
	n := copy(target, r.fragments[0])
	r.fragments[0] = r.fragments[0][n:]
	if len(r.fragments[0]) == 0 {
		r.fragments = r.fragments[1:]
	}
	return n, nil
}
