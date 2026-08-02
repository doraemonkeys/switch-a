package proxy

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/proxy/capturebridge"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"go.uber.org/zap"
)

func TestCaptureResponseBodyPreservesReadBoundary(t *testing.T) {
	source := &singleReadEOFBody{payload: []byte("response")}
	body, observer := capturebridge.WrapHTTPResponseBody(source, requestcapture.Recorder{}, -1)
	buffer := make([]byte, len(source.payload))

	n, err := body.Read(buffer)

	if n != len(source.payload) {
		t.Fatalf("Read bytes = %d, want %d", n, len(source.payload))
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Read error = %v, want EOF", err)
	}
	if string(buffer) != "response" {
		t.Fatalf("Read payload = %q, want response", buffer)
	}
	if !observer.SourceComplete() {
		t.Fatal("observer did not retain the source EOF fact")
	}
	if source.readCalls != 1 {
		t.Fatalf("source reads = %d, want 1", source.readCalls)
	}
}

func TestCaptureResponseBodyPreservesWriterToFastPath(t *testing.T) {
	source := &writerToCaptureBody{payload: []byte("response")}
	body, observer := capturebridge.WrapHTTPResponseBody(source, requestcapture.Recorder{}, -1)

	written, err := io.Copy(io.Discard, body)

	if err != nil {
		t.Fatalf("io.Copy() error = %v", err)
	}
	if written != int64(len(source.payload)) {
		t.Fatalf("io.Copy() bytes = %d, want %d", written, len(source.payload))
	}
	if source.writerToCalls != 1 || source.readCalls != 0 {
		t.Fatalf("source calls = WriterTo:%d Read:%d, want WriterTo:1 Read:0", source.writerToCalls, source.readCalls)
	}
	if !observer.SourceComplete() {
		t.Fatal("successful WriterTo completion was not retained")
	}
}

func TestDisabledHTTPCaptureLifecycleDoesNotInspectMetadataOrAllocate(t *testing.T) {
	exchange := httpCaptureExchange{}
	response := &UpstreamResponse{
		Trailer: http.Header{
			"Authorization": {"Bearer must-not-be-inspected"},
		},
	}

	allocations := testing.AllocsPerRun(1000, func() {
		exchange.observeClientWrite(1)
		exchange.finish(
			response,
			requestcapture.SourceCompletionPartial,
			requestcapture.TerminationReasonReadError,
			requestcapture.FailureObservation{},
		)
	})

	if allocations != 0 {
		t.Fatalf("disabled HTTP capture lifecycle allocations = %v, want 0", allocations)
	}
}

func TestCaptureSourceEndpointRequiresEOFOrExactDeclaredLength(t *testing.T) {
	tests := []struct {
		name          string
		observedBytes int64
		expectedBytes int64
		reachedEOF    bool
		readFailed    bool
		want          bool
	}{
		{name: "EOF", observedBytes: 3, expectedBytes: -1, reachedEOF: true, want: true},
		{name: "exact declared length", observedBytes: maxDrainBytes, expectedBytes: maxDrainBytes, want: true},
		{name: "unknown exact drain boundary", observedBytes: maxDrainBytes, expectedBytes: -1},
		{name: "declared length mismatch", observedBytes: maxDrainBytes, expectedBytes: maxDrainBytes + 1},
		{name: "read failure at declared endpoint", observedBytes: maxDrainBytes, expectedBytes: maxDrainBytes, readFailed: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := capturebridge.SourceEndpointComplete(
				test.observedBytes,
				test.expectedBytes,
				test.reachedEOF,
				test.readFailed,
			); got != test.want {
				t.Fatalf("captureSourceEndpointComplete() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestCaptureWriterToPreservesFlusherBranchAndTranscript(t *testing.T) {
	t.Run("flusher", func(t *testing.T) {
		rawSource := &flusherBranchingCaptureBody{}
		rawDestination := &captureFlushTranscriptWriter{}
		rawWritten, rawErr := io.Copy(rawDestination, rawSource)

		capturedSource := &flusherBranchingCaptureBody{}
		capturedBody, _ := capturebridge.WrapHTTPResponseBody(capturedSource, requestcapture.Recorder{}, -1)
		capturedDestination := &captureFlushTranscriptWriter{}
		capturedWritten, capturedErr := io.Copy(capturedDestination, capturedBody)

		if rawErr != nil || capturedErr != nil || rawWritten != capturedWritten ||
			rawSource.branch != capturedSource.branch ||
			rawDestination.String() != capturedDestination.String() ||
			strings.Join(rawDestination.events, ",") != strings.Join(capturedDestination.events, ",") {
			t.Fatalf(
				"WriterTo transcript diverged: raw=(%d,%v,%q,%q,%v) captured=(%d,%v,%q,%q,%v)",
				rawWritten,
				rawErr,
				rawSource.branch,
				rawDestination.String(),
				rawDestination.events,
				capturedWritten,
				capturedErr,
				capturedSource.branch,
				capturedDestination.String(),
				capturedDestination.events,
			)
		}
	})

	t.Run("non-flusher", func(t *testing.T) {
		rawSource := &flusherBranchingCaptureBody{}
		var rawDestination bytes.Buffer
		_, rawErr := io.Copy(&rawDestination, rawSource)

		capturedSource := &flusherBranchingCaptureBody{}
		capturedBody, _ := capturebridge.WrapHTTPResponseBody(capturedSource, requestcapture.Recorder{}, -1)
		var capturedDestination bytes.Buffer
		_, capturedErr := io.Copy(&capturedDestination, capturedBody)

		if rawErr != nil || capturedErr != nil || rawSource.branch != "plain" ||
			capturedSource.branch != rawSource.branch ||
			capturedDestination.String() != rawDestination.String() {
			t.Fatalf(
				"non-flusher WriterTo diverged: raw=(%v,%q,%q) captured=(%v,%q,%q)",
				rawErr,
				rawSource.branch,
				rawDestination.String(),
				capturedErr,
				capturedSource.branch,
				capturedDestination.String(),
			)
		}
	})
}

func TestCaptureWriterToPreservesResponseWriterInterfaceSet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		newWriter   func() (io.Writer, *captureResponseWriterTranscript)
		wantBranch  string
		wantFlushes int
	}{
		{
			name: "response writer only",
			newWriter: func() (io.Writer, *captureResponseWriterTranscript) {
				transcript := newCaptureResponseWriterTranscript()
				return captureResponseWriterOnly{captureResponseWriterTranscript: transcript}, transcript
			},
			wantBranch: "response_writer",
		},
		{
			name: "response writer and flusher",
			newWriter: func() (io.Writer, *captureResponseWriterTranscript) {
				transcript := newCaptureResponseWriterTranscript()
				return captureFlushingResponseWriter{captureResponseWriterTranscript: transcript}, transcript
			},
			wantBranch:  "response_writer+flusher",
			wantFlushes: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rawSource := &responseWriterBranchingCaptureBody{}
			rawWriter, rawTranscript := test.newWriter()
			rawWritten, rawErr := io.Copy(rawWriter, rawSource)

			capturedSource := &responseWriterBranchingCaptureBody{}
			capturedBody, _ := capturebridge.WrapHTTPResponseBody(capturedSource, requestcapture.Recorder{}, -1)
			capturedWriter, capturedTranscript := test.newWriter()
			capturedWritten, capturedErr := io.Copy(capturedWriter, capturedBody)

			if rawErr != nil || capturedErr != nil || rawWritten != capturedWritten ||
				rawSource.branch != test.wantBranch || capturedSource.branch != rawSource.branch ||
				rawTranscript.statusCode != http.StatusAccepted ||
				capturedTranscript.statusCode != rawTranscript.statusCode ||
				capturedTranscript.Header().Get("X-WriterTo-Branch") != rawTranscript.Header().Get("X-WriterTo-Branch") ||
				capturedTranscript.String() != rawTranscript.String() ||
				capturedTranscript.flushes != test.wantFlushes || rawTranscript.flushes != test.wantFlushes {
				t.Fatalf(
					"ResponseWriter parity diverged: raw=(%d,%v,%q,%d,%d,%q) captured=(%d,%v,%q,%d,%d,%q)",
					rawWritten, rawErr, rawSource.branch, rawTranscript.statusCode, rawTranscript.flushes, rawTranscript.String(),
					capturedWritten, capturedErr, capturedSource.branch, capturedTranscript.statusCode, capturedTranscript.flushes, capturedTranscript.String(),
				)
			}
		})
	}
}

func TestCaptureWriterToKeepsSourceObservationOrthogonalToShortWrite(t *testing.T) {
	t.Parallel()

	const payload = "writer-to-payload"
	provider := captureTestProvider("https://provider.invalid")
	manager, session := startCaptureTestManager(t, []requestcapture.ProviderIdentity{{
		ID: provider.ID, Name: provider.Name,
	}})
	defer manager.Close()
	gateway := manager.BeginGateway(requestcapture.GatewayStart{GatewayRequestID: "writer-to-short-write"})
	pctx := &proxyContext{
		apiType:             APITypeClaude,
		body:                []byte(`{"model":"claude-3"}`),
		capture:             gateway,
		captureParticipates: true,
	}
	exchange := (&Handler{logger: zap.NewNop()}).beginHTTPExchange(
		pctx,
		httpAttemptContext{provider: &provider, selectionMode: requestcapture.SelectionModeInitial},
		requestcapture.CredentialPhaseInitial,
		httptest.NewRequest(http.MethodPost, "https://provider.invalid/v1/messages", nil),
	)
	source := &writerToCaptureBody{payload: []byte(payload)}
	response := &UpstreamResponse{
		StatusCode:    http.StatusOK,
		Protocol:      "HTTP/1.1",
		Header:        make(http.Header),
		Body:          source,
		ContentLength: int64(len(payload)),
	}
	exchange.observeResponse(response)
	destination := &captureShortWriter{limit: 3, err: io.ErrClosedPipe}
	written, err := io.Copy(destination, response.Body)
	if written != 3 || !errors.Is(err, io.ErrClosedPipe) || destination.String() != payload[:3] {
		t.Fatalf("short write = bytes:%d error:%v payload:%q", written, err, destination.String())
	}
	if !exchange.responseObserver.SourceComplete() {
		t.Fatal("short downstream write erased already-observed declared source bytes")
	}
	exchange.finish(
		response,
		requestcapture.SourceCompletionComplete,
		requestcapture.TerminationReasonWriteError,
		requestcapture.FailureObservation{},
	)
	gateway.Finish(requestcapture.GatewayOutcome{})
	page, listErr := readCaptureTestPage(manager, session, requestcapture.ListQuery{Limit: 1})
	if listErr != nil || len(page.Records) != 1 ||
		page.Records[0].UpstreamObservedBytes != int64(len(payload)) ||
		page.Records[0].SourceCompletion != requestcapture.SourceCompletionComplete {
		t.Fatalf("captured short write = page:%#v error:%v", page, listErr)
	}
}

type flusherBranchingCaptureBody struct {
	branch string
}

func (*flusherBranchingCaptureBody) Read([]byte) (int, error) {
	return 0, errors.New("Read used instead of WriterTo")
}

func (*flusherBranchingCaptureBody) Close() error { return nil }

func (b *flusherBranchingCaptureBody) WriteTo(destination io.Writer) (int64, error) {
	payload := "plain"
	if flusher, ok := destination.(http.Flusher); ok {
		b.branch = "flusher"
		payload = b.branch
		flusher.Flush()
	} else {
		b.branch = payload
	}
	written, err := destination.Write([]byte(payload))
	return int64(written), err
}

type captureFlushTranscriptWriter struct {
	bytes.Buffer
	events []string
}

type responseWriterBranchingCaptureBody struct {
	branch string
}

func (*responseWriterBranchingCaptureBody) Read([]byte) (int, error) {
	return 0, errors.New("Read used instead of WriterTo")
}

func (*responseWriterBranchingCaptureBody) Close() error { return nil }

func (b *responseWriterBranchingCaptureBody) WriteTo(destination io.Writer) (int64, error) {
	responseWriter, hasResponseWriter := destination.(http.ResponseWriter)
	flusher, hasFlusher := destination.(http.Flusher)
	switch {
	case hasResponseWriter && hasFlusher:
		b.branch = "response_writer+flusher"
	case hasResponseWriter:
		b.branch = "response_writer"
	case hasFlusher:
		b.branch = "flusher"
	default:
		b.branch = "writer"
	}
	if hasResponseWriter {
		responseWriter.Header().Set("X-WriterTo-Branch", b.branch)
		responseWriter.WriteHeader(http.StatusAccepted)
	}
	if hasFlusher {
		flusher.Flush()
	}
	written, err := destination.Write([]byte(b.branch))
	return int64(written), err
}

type captureResponseWriterTranscript struct {
	bytes.Buffer
	header     http.Header
	statusCode int
	flushes    int
}

func newCaptureResponseWriterTranscript() *captureResponseWriterTranscript {
	return &captureResponseWriterTranscript{header: make(http.Header)}
}

func (w *captureResponseWriterTranscript) Header() http.Header { return w.header }

func (w *captureResponseWriterTranscript) WriteHeader(statusCode int) { w.statusCode = statusCode }

type captureResponseWriterOnly struct {
	*captureResponseWriterTranscript
}

type captureFlushingResponseWriter struct {
	*captureResponseWriterTranscript
}

func (w captureFlushingResponseWriter) Flush() { w.flushes++ }

type captureShortWriter struct {
	bytes.Buffer
	limit int
	err   error
}

func (w *captureShortWriter) Write(payload []byte) (int, error) {
	written := len(payload)
	if written > w.limit {
		written = w.limit
	}
	_, _ = w.Buffer.Write(payload[:written])
	return written, w.err
}

func (w *captureFlushTranscriptWriter) Write(payload []byte) (int, error) {
	w.events = append(w.events, "write:"+string(payload))
	return w.Buffer.Write(payload)
}

func (w *captureFlushTranscriptWriter) Flush() {
	w.events = append(w.events, "flush")
}

type singleReadEOFBody struct {
	payload   []byte
	readCalls int
}

func (b *singleReadEOFBody) Read(p []byte) (int, error) {
	b.readCalls++
	if b.readCalls > 1 {
		return 0, errors.New("unexpected additional read")
	}
	return copy(p, b.payload), io.EOF
}

func (b *singleReadEOFBody) Close() error { return nil }

type writerToCaptureBody struct {
	payload       []byte
	readCalls     int
	writerToCalls int
}

func (b *writerToCaptureBody) Read([]byte) (int, error) {
	b.readCalls++
	return 0, errors.New("Read used instead of WriterTo")
}

func (*writerToCaptureBody) Close() error { return nil }

func (b *writerToCaptureBody) WriteTo(w io.Writer) (int64, error) {
	b.writerToCalls++
	n, err := w.Write(b.payload)
	return int64(n), err
}

func TestFirstWriteResponseWriterRetainsDownstreamErrorOrigin(t *testing.T) {
	writeErr := errors.New("downstream write failed")
	writer := &firstWriteResponseWriter{
		ResponseWriter: &failingCaptureResponseWriter{err: writeErr},
	}

	n, err := writer.Write([]byte("payload"))

	if n != 3 {
		t.Fatalf("written = %d, want 3", n)
	}
	if !errors.Is(err, writeErr) {
		t.Fatalf("Write error = %v, want %v", err, writeErr)
	}
	if !errors.Is(writer.writeErr, writeErr) {
		t.Fatalf("observed write error = %v, want %v", writer.writeErr, writeErr)
	}
	if writer.bytesWritten != 3 {
		t.Fatalf("confirmed bytes = %d, want 3", writer.bytesWritten)
	}
}

type failingCaptureResponseWriter struct {
	header http.Header
	err    error
}

func (w *failingCaptureResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (*failingCaptureResponseWriter) WriteHeader(int) {}

func (w *failingCaptureResponseWriter) Write([]byte) (int, error) {
	return 3, w.err
}
