package capturebridge

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/requestcapture/redaction"
)

func TestCredentialMaterialRecognizesWireAndProgrammaticHeaderShapes(t *testing.T) {
	t.Parallel()

	headers := http.Header{
		"authorization":       {"Bearer authorization-secret"},
		"Proxy_Authorization": {"Basic proxy-secret"},
		"Cookie":              {`session="cookie-secret"; preference=dark`},
		"Set-Cookie":          {"refresh=set-cookie-secret; Path=/; HttpOnly"},
		"x-api-key":           {"api-secret"},
		"X-Ignored":           {"public-value"},
	}
	sensitiveHeaders, evidence := CredentialMaterial(headers)
	if !sensitiveHeaders.Sealed() || sensitiveHeaders.Overflowed() {
		t.Fatalf("sensitive header evidence = sealed:%t overflowed:%t", sensitiveHeaders.Sealed(), sensitiveHeaders.Overflowed())
	}
	if !evidence.Sealed() || evidence.Overflowed() {
		t.Fatalf("credential evidence = sealed:%t overflowed:%t", evidence.Sealed(), evidence.Overflowed())
	}

	diagnostic := "authorization-secret proxy-secret cookie-secret dark set-cookie-secret api-secret public-value"
	sanitized := redaction.SanitizedTextWithEvidence(diagnostic, evidence, len(diagnostic)*2, "TEST")
	for _, secret := range []string{
		"authorization-secret",
		"proxy-secret",
		"cookie-secret",
		"dark",
		"set-cookie-secret",
		"api-secret",
	} {
		if strings.Contains(sanitized.Value, secret) {
			t.Fatalf("sanitized diagnostic retained %q: %q", secret, sanitized.Value)
		}
	}
	if !strings.Contains(sanitized.Value, "public-value") {
		t.Fatalf("non-credential diagnostic was removed: %q", sanitized.Value)
	}
}

func TestKnownSensitiveHeaderNameNormalizesWithoutBroadMatching(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		want      string
		sensitive bool
	}{
		{name: "case", input: "aUtHoRiZaTiOn", want: "Authorization", sensitive: true},
		{name: "underscore", input: " X_ACCESS_TOKEN ", want: "X-Access-Token", sensitive: true},
		{name: "same length mismatch", input: "X-Api-Kez", sensitive: false},
		{name: "different length", input: "Authorization-Extra", sensitive: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, sensitive := knownSensitiveHeaderName(test.input)
			if got != test.want || sensitive != test.sensitive {
				t.Fatalf("knownSensitiveHeaderName(%q) = (%q, %t), want (%q, %t)", test.input, got, sensitive, test.want, test.sensitive)
			}
		})
	}
}

func TestCredentialMaterialFailsClosedWhenEvidenceCapacityIsExceeded(t *testing.T) {
	t.Parallel()

	values := make([]string, 80)
	for index := range values {
		values[index] = "Bearer token-" + strings.Repeat("x", index+1)
	}
	_, evidence := CredentialMaterial(http.Header{"Authorization": values})
	if !evidence.Sealed() || !evidence.Overflowed() {
		t.Fatalf("overflow evidence = sealed:%t overflowed:%t", evidence.Sealed(), evidence.Overflowed())
	}
	if got := redaction.SanitizedTextWithEvidence("otherwise-safe", evidence, 64, "TEST").Value; got != redaction.RedactedValue {
		t.Fatalf("overflowed evidence did not fail closed: %q", got)
	}
}

func TestCaptureCredentialValueHandlesEmptyAndCookieComponents(t *testing.T) {
	t.Parallel()

	var evidence requestcapture.CredentialEvidence
	captureCredentialValue(&evidence, "Authorization", "  ")
	captureCredentialValue(&evidence, "Authorization", "opaque-without-scheme")
	captureCredentialValue(&evidence, "Cookie", `first=one; quoted="two"; flag`)
	captureCredentialValue(&evidence, "Set-Cookie", "only=first; ignored=second")
	evidence.Seal()

	diagnostic := "opaque-without-scheme one two flag first ignored=second"
	sanitized := redaction.SanitizedTextWithEvidence(diagnostic, evidence, len(diagnostic)*2, "TEST")
	for _, secret := range []string{"opaque-without-scheme", "one", "two", "flag", "first"} {
		if strings.Contains(sanitized.Value, secret) {
			t.Fatalf("cookie/auth component %q was retained: %q", secret, sanitized.Value)
		}
	}
	// Set-Cookie intentionally admits only its first cookie-pair; attributes are
	// protocol metadata and must not exhaust the bounded evidence set.
	if !strings.Contains(sanitized.Value, "ignored=second") {
		t.Fatalf("Set-Cookie attributes unexpectedly entered credential evidence: %q", sanitized.Value)
	}
}

func TestHTTPBodyObservationIsFactsOnly(t *testing.T) {
	t.Parallel()

	wrapped, observation := WrapHTTPResponseBody(
		&trackingReadCloser{Reader: bytes.NewReader(nil)},
		requestcapture.Recorder{},
		0,
	)
	if _, ok := any(observation).(io.Reader); ok {
		t.Fatal("observation unexpectedly exposes io.Reader")
	}
	if _, ok := any(observation).(io.Closer); ok {
		t.Fatal("observation unexpectedly exposes io.Closer")
	}
	if _, ok := any(observation).(io.WriterTo); ok {
		t.Fatal("observation unexpectedly exposes io.WriterTo")
	}
	if _, ok := wrapped.(io.ReadCloser); !ok {
		t.Fatal("body wrapper lost its read/close capability")
	}
}

func TestHTTPBodyObservationTracksReadCompletionAndFailure(t *testing.T) {
	t.Parallel()

	normalSource := &trackingReadCloser{Reader: bytes.NewReader([]byte("response"))}
	normal, normalObserver := WrapHTTPResponseBody(normalSource, requestcapture.Recorder{}, int64(len("response")))
	payload, err := io.ReadAll(normal)
	if err != nil || string(payload) != "response" || !normalObserver.SourceComplete() {
		t.Fatalf("normal observation = payload:%q err:%v complete:%t", payload, err, normalObserver.SourceComplete())
	}
	if err := normal.Close(); err != nil || !normalSource.closed {
		t.Fatalf("normal close = err:%v closed:%t", err, normalSource.closed)
	}
	normalFacts := normalObserver.Facts()
	if normalFacts.ObservedBytes != int64(len("response")) || !normalFacts.ReachedEOF || normalFacts.ReadFailed {
		t.Fatalf("normal facts = %+v", normalFacts)
	}

	readErr := errors.New("read failed")
	failingSource := &singleReadCloser{payload: []byte("partial"), err: readErr}
	failing, failingObserver := WrapHTTPResponseBody(failingSource, requestcapture.Recorder{}, int64(len("partial")))
	buffer := make([]byte, 32)
	n, err := failing.Read(buffer)
	if n != len("partial") || !errors.Is(err, readErr) || failingObserver.SourceComplete() {
		t.Fatalf("failing observation = bytes:%d err:%v complete:%t", n, err, failingObserver.SourceComplete())
	}
	failingFacts := failingObserver.Facts()
	if failingFacts.ObservedBytes != int64(len("partial")) || failingFacts.ReachedEOF || !failingFacts.ReadFailed {
		t.Fatalf("failing facts = %+v", failingFacts)
	}
}

func TestHTTPBodyObservationCopiesShareRaceSafeFacts(t *testing.T) {
	t.Parallel()

	payload := bytes.Repeat([]byte("capture"), 16*1024)
	wrapped, observation := WrapHTTPResponseBody(
		&yieldingReadCloser{Reader: bytes.NewReader(payload)},
		requestcapture.Recorder{},
		int64(len(payload)),
	)
	observationCopy := observation
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, wrapped)
		done <- err
	}()

	var previous int64
	for {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("copy failed: %v", err)
			}
			facts := observationCopy.Facts()
			if facts.ObservedBytes != int64(len(payload)) || !facts.SourceComplete() {
				t.Fatalf("final copied observation = %+v", facts)
			}
			return
		default:
			facts := observationCopy.Facts()
			if facts.ObservedBytes < previous {
				t.Fatalf("observed bytes moved backwards: %d after %d", facts.ObservedBytes, previous)
			}
			previous = facts.ObservedBytes
			runtime.Gosched()
		}
	}
}

func TestWriterToObserverPreservesDestinationInterfaces(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		destination        interfaceDestination
		wantResponseWriter bool
		wantFlusher        bool
	}{
		{name: "plain", destination: &plainDestination{}},
		{name: "flusher", destination: &flushingDestination{}, wantFlusher: true},
		{name: "response writer", destination: newResponseDestination(false), wantResponseWriter: true},
		{name: "response writer and flusher", destination: newResponseDestination(true), wantResponseWriter: true, wantFlusher: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := &interfaceInspectingWriterToBody{payload: []byte("payload")}
			wrapped, observer := WrapHTTPResponseBody(source, requestcapture.Recorder{}, int64(len(source.payload)))
			written, err := io.Copy(test.destination, wrapped)
			if err != nil || written != int64(len(source.payload)) || test.destination.String() != string(source.payload) {
				t.Fatalf("copy = bytes:%d err:%v payload:%q", written, err, test.destination.String())
			}
			if source.sawResponseWriter != test.wantResponseWriter || source.sawFlusher != test.wantFlusher {
				t.Fatalf("interfaces = response-writer:%t flusher:%t, want response-writer:%t flusher:%t", source.sawResponseWriter, source.sawFlusher, test.wantResponseWriter, test.wantFlusher)
			}
			if !observer.SourceComplete() {
				t.Fatal("successful WriterTo did not prove source completion")
			}
			if err := wrapped.Close(); err != nil || !source.closed {
				t.Fatalf("WriterTo close = err:%v closed:%t", err, source.closed)
			}
		})
	}
}

func TestWriterToObserverPropagatesDestinationFailure(t *testing.T) {
	t.Parallel()

	writeErr := errors.New("destination failed")
	source := &interfaceInspectingWriterToBody{payload: []byte("payload")}
	wrapped, observer := WrapHTTPResponseBody(source, requestcapture.Recorder{}, int64(len(source.payload)))
	written, err := io.Copy(&failingDestination{limit: 2, err: writeErr}, wrapped)
	if written != 2 || !errors.Is(err, writeErr) {
		t.Fatalf("WriterTo failure = bytes:%d err:%v", written, err)
	}
	facts := observer.Facts()
	if facts.ObservedBytes != int64(len(source.payload)) || facts.ReadFailed || !facts.SourceComplete() {
		t.Fatalf("source observation = %+v, want bytes:%d complete:true", facts, len(source.payload))
	}
}

func TestWriterToObserverDistinguishesSourceFailureFromDestinationFailure(t *testing.T) {
	t.Parallel()

	readErr := errors.New("source failed")
	source := &failingWriterToBody{payload: []byte("payload"), err: readErr}
	wrapped, observation := WrapHTTPResponseBody(source, requestcapture.Recorder{}, int64(len(source.payload)))
	written, err := io.Copy(io.Discard, wrapped)
	if written != int64(len(source.payload)) || !errors.Is(err, readErr) {
		t.Fatalf("WriterTo source failure = bytes:%d err:%v", written, err)
	}
	facts := observation.Facts()
	if facts.ObservedBytes != int64(len(source.payload)) || !facts.ReadFailed || facts.SourceComplete() {
		t.Fatalf("source failure facts = %+v", facts)
	}
}

func TestSourceEndpointCompleteUsesOrthogonalWireFacts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		observed      int64
		expected      int64
		reachedEOF    bool
		readFailed    bool
		wantCompleted bool
	}{
		{name: "EOF", expected: -1, reachedEOF: true, wantCompleted: true},
		{name: "declared length", observed: 7, expected: 7, wantCompleted: true},
		{name: "unknown incomplete", observed: 7, expected: -1},
		{name: "short declared body", observed: 6, expected: 7},
		{name: "read failure overrides EOF", observed: 7, expected: 7, reachedEOF: true, readFailed: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := SourceEndpointComplete(test.observed, test.expected, test.reachedEOF, test.readFailed); got != test.wantCompleted {
				t.Fatalf("SourceEndpointComplete() = %t, want %t", got, test.wantCompleted)
			}
		})
	}
}

func TestModePredicatesRejectUnknownValues(t *testing.T) {
	t.Parallel()

	if got := ModeForRecorder(requestcapture.Recorder{}); got != ModeNone {
		t.Fatalf("zero recorder mode = %d, want %d", got, ModeNone)
	}
	for _, test := range []struct {
		mode            Mode
		participates    bool
		capturesPayload bool
	}{
		{mode: ModeNone},
		{mode: ModeTransition, participates: true},
		{mode: ModePayload, participates: true, capturesPayload: true},
		{mode: Mode(255)},
	} {
		if test.mode.Participates() != test.participates || test.mode.CapturesPayload() != test.capturesPayload {
			t.Fatalf("mode %d = participates:%t payload:%t", test.mode, test.mode.Participates(), test.mode.CapturesPayload())
		}
	}
}

type trackingReadCloser struct {
	*bytes.Reader
	closed bool
}

type yieldingReadCloser struct {
	*bytes.Reader
}

func (r *yieldingReadCloser) Read(destination []byte) (int, error) {
	runtime.Gosched()
	if len(destination) > 1 {
		destination = destination[:1]
	}
	return r.Reader.Read(destination)
}

func (*yieldingReadCloser) Close() error { return nil }

func (r *trackingReadCloser) Close() error {
	r.closed = true
	return nil
}

type singleReadCloser struct {
	payload []byte
	err     error
	done    bool
}

func (r *singleReadCloser) Read(destination []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return copy(destination, r.payload), r.err
}

func (*singleReadCloser) Close() error { return nil }

type interfaceDestination interface {
	io.Writer
	String() string
}

type plainDestination struct{ bytes.Buffer }

type flushingDestination struct {
	bytes.Buffer
	flushes int
}

func (d *flushingDestination) Flush() { d.flushes++ }

type responseDestination struct {
	bytes.Buffer
	header  http.Header
	status  int
	flushes int
	flusher bool
}

func newResponseDestination(flusher bool) interfaceDestination {
	if flusher {
		return &flushingResponseDestination{responseDestination: responseDestination{header: make(http.Header), flusher: true}}
	}
	return &responseDestination{header: make(http.Header)}
}

func (d *responseDestination) Header() http.Header { return d.header }

func (d *responseDestination) WriteHeader(status int) { d.status = status }

type flushingResponseDestination struct{ responseDestination }

func (d *flushingResponseDestination) Flush() { d.flushes++ }

type interfaceInspectingWriterToBody struct {
	payload           []byte
	closed            bool
	sawResponseWriter bool
	sawFlusher        bool
}

type failingWriterToBody struct {
	payload []byte
	err     error
}

func (*failingWriterToBody) Read([]byte) (int, error) {
	return 0, errors.New("Read used instead of WriterTo")
}

func (*failingWriterToBody) Close() error { return nil }

func (b *failingWriterToBody) WriteTo(destination io.Writer) (int64, error) {
	written, writeErr := destination.Write(b.payload)
	if writeErr != nil {
		return int64(written), writeErr
	}
	return int64(written), b.err
}

func (*interfaceInspectingWriterToBody) Read([]byte) (int, error) {
	return 0, errors.New("Read used instead of WriterTo")
}

func (b *interfaceInspectingWriterToBody) Close() error {
	b.closed = true
	return nil
}

func (b *interfaceInspectingWriterToBody) WriteTo(destination io.Writer) (int64, error) {
	if responseWriter, ok := destination.(http.ResponseWriter); ok {
		b.sawResponseWriter = true
		responseWriter.Header().Set("X-Capture-Bridge", "observed")
		responseWriter.WriteHeader(http.StatusAccepted)
	}
	if flusher, ok := destination.(http.Flusher); ok {
		b.sawFlusher = true
		flusher.Flush()
	}
	written, err := destination.Write(b.payload)
	return int64(written), err
}

type failingDestination struct {
	limit int
	err   error
}

func (d *failingDestination) Write(payload []byte) (int, error) {
	return min(d.limit, len(payload)), d.err
}
