package responseanalysis_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	responseanalysis "github.com/doraemonkeys/switch-a/internal/responseanalysis"
)

const v5aWaitTimeout = 10 * time.Second

type v5aSegmentBody struct {
	mu             sync.Mutex
	segments       [][]byte
	segmentIndex   int
	segmentOffset  int
	trailer        http.Header
	finalTrailers  http.Header
	closeOnce      sync.Once
	closed         chan struct{}
	closeCount     atomic.Int32
	activeReads    atomic.Int32
	concurrentRead atomic.Bool
}

func newV5ASegmentBody(raw []byte, split int) *v5aSegmentBody {
	split = max(0, min(split, len(raw)))
	segments := make([][]byte, 0, 2)
	if split > 0 {
		segments = append(segments, raw[:split])
	}
	if split < len(raw) {
		segments = append(segments, raw[split:])
	}
	return &v5aSegmentBody{segments: segments, closed: make(chan struct{})}
}

func newV5APatternBody(raw []byte, pattern ...int) *v5aSegmentBody {
	segments := make([][]byte, 0, len(raw)/1024+1)
	for offset, index := 0, 0; offset < len(raw); index++ {
		size := pattern[index%len(pattern)]
		end := min(offset+size, len(raw))
		segments = append(segments, raw[offset:end])
		offset = end
	}
	return &v5aSegmentBody{segments: segments, closed: make(chan struct{})}
}

func (b *v5aSegmentBody) Read(target []byte) (int, error) {
	if b.activeReads.Add(1) != 1 {
		b.concurrentRead.Store(true)
	}
	defer b.activeReads.Add(-1)

	b.mu.Lock()
	defer b.mu.Unlock()
	for b.segmentIndex < len(b.segments) {
		current := b.segments[b.segmentIndex]
		n := copy(target, current[b.segmentOffset:])
		b.segmentOffset += n
		if b.segmentOffset == len(current) {
			b.segmentIndex++
			b.segmentOffset = 0
		}
		if n > 0 {
			return n, nil
		}
	}
	for key, values := range b.finalTrailers {
		b.trailer[key] = append([]string(nil), values...)
	}
	return 0, io.EOF
}

func (b *v5aSegmentBody) Close() error {
	b.closeCount.Add(1)
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

type v5aReadStep struct {
	data    []byte
	err     error
	started chan struct{}
	release <-chan struct{}
}

type v5aBlockingBody struct {
	mu             sync.Mutex
	steps          []v5aReadStep
	next           int
	closeOnce      sync.Once
	closed         chan struct{}
	closeCount     atomic.Int32
	activeReads    atomic.Int32
	concurrentRead atomic.Bool
}

func newV5ABlockingBody(steps ...v5aReadStep) *v5aBlockingBody {
	return &v5aBlockingBody{steps: steps, closed: make(chan struct{})}
}

func (b *v5aBlockingBody) Read(target []byte) (int, error) {
	if b.activeReads.Add(1) != 1 {
		b.concurrentRead.Store(true)
	}
	defer b.activeReads.Add(-1)

	b.mu.Lock()
	if b.next >= len(b.steps) {
		b.mu.Unlock()
		return 0, io.EOF
	}
	step := b.steps[b.next]
	b.next++
	b.mu.Unlock()

	if step.started != nil {
		close(step.started)
	}
	if step.release != nil {
		select {
		case <-step.release:
		case <-b.closed:
			return 0, io.ErrClosedPipe
		}
	}
	return copy(target, step.data), step.err
}

func (b *v5aBlockingBody) Close() error {
	b.closeCount.Add(1)
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

type v5aWriterSnapshot struct {
	header     http.Header
	statuses   []int
	body       []byte
	flushes    int
	writeCalls int
}

type v5aTransitionResult struct {
	kind       string
	forwarding *responseanalysis.ForwardingResponse
	receipt    responseanalysis.DiscardReceipt
	err        error
}

type v5aWriter struct {
	header          http.Header
	maxWrite        int
	writeGate       <-chan struct{}
	writeStarted    chan struct{}
	writeStartedOne sync.Once

	mu              sync.Mutex
	statuses        []int
	body            bytes.Buffer
	flushes         int
	writeCalls      int
	activeWrites    atomic.Int32
	concurrentWrite atomic.Bool
}

func newV5AWriter(maxWrite int) *v5aWriter {
	return &v5aWriter{header: make(http.Header), maxWrite: maxWrite, writeStarted: make(chan struct{})}
}

func (w *v5aWriter) Header() http.Header {
	return w.header
}

func (w *v5aWriter) WriteHeader(statusCode int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.statuses = append(w.statuses, statusCode)
}

func (w *v5aWriter) Write(body []byte) (int, error) {
	w.writeStartedOne.Do(func() { close(w.writeStarted) })
	if w.writeGate != nil {
		<-w.writeGate
	}
	if w.activeWrites.Add(1) != 1 {
		w.concurrentWrite.Store(true)
	}
	defer w.activeWrites.Add(-1)

	n := len(body)
	if w.maxWrite > 0 {
		n = min(n, w.maxWrite)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writeCalls++
	_, _ = w.body.Write(body[:n])
	return n, nil
}

func (w *v5aWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushes++
}

func (w *v5aWriter) snapshot() v5aWriterSnapshot {
	w.mu.Lock()
	defer w.mu.Unlock()
	header := make(http.Header, len(w.header))
	for key, values := range w.header {
		header[key] = append([]string(nil), values...)
	}
	return v5aWriterSnapshot{
		header:     header,
		statuses:   append([]int(nil), w.statuses...),
		body:       append([]byte(nil), w.body.Bytes()...),
		flushes:    w.flushes,
		writeCalls: w.writeCalls,
	}
}

type v5aManualTimer struct {
	callback func()
	stopped  atomic.Bool
}

func (t *v5aManualTimer) Stop() bool {
	return !t.stopped.Swap(true)
}

type v5aManualScheduler struct {
	mu      sync.Mutex
	timers  []*v5aManualTimer
	created chan struct{}
}

func newV5AManualScheduler() *v5aManualScheduler {
	return &v5aManualScheduler{created: make(chan struct{}, 32)}
}

func (s *v5aManualScheduler) AfterFunc(_ time.Duration, callback func()) responseanalysis.Timer {
	timer := &v5aManualTimer{callback: callback}
	s.mu.Lock()
	s.timers = append(s.timers, timer)
	s.mu.Unlock()
	select {
	case s.created <- struct{}{}:
	default:
	}
	return timer
}

func (s *v5aManualScheduler) waitForCount(t *testing.T, count int) {
	t.Helper()
	for {
		s.mu.Lock()
		current := len(s.timers)
		s.mu.Unlock()
		if current >= count {
			return
		}
		select {
		case <-s.created:
		case <-time.After(v5aWaitTimeout):
			t.Fatalf("scheduler created %d timers, want at least %d", current, count)
		}
	}
}

func (s *v5aManualScheduler) fire(t *testing.T, index int) {
	t.Helper()
	s.waitForCount(t, index+1)
	s.mu.Lock()
	callback := s.timers[index].callback
	s.mu.Unlock()
	callback()
}

func v5aBudget(t testing.TB, limit int) *responseanalysis.ProcessMemoryBudget {
	t.Helper()
	budget, err := responseanalysis.NewProcessMemoryBudget(limit)
	if err != nil {
		t.Fatal(err)
	}
	return budget
}

func v5aAnalyzer(t testing.TB, budget *responseanalysis.ProcessMemoryBudget, options responseanalysis.AnalyzerOptions) *responseanalysis.Analyzer {
	t.Helper()
	analyzer, err := responseanalysis.NewAnalyzer(responseanalysis.NewRegistry(), budget, options)
	if err != nil {
		t.Fatal(err)
	}
	return analyzer
}

func v5aAwaitBoundary(t *testing.T, response *responseanalysis.PendingResponse) responseanalysis.Boundary {
	t.Helper()
	result := make(chan responseanalysis.Boundary, 1)
	go func() { result <- response.AwaitBoundary() }()
	select {
	case boundary := <-result:
		return boundary
	case <-time.After(v5aWaitTimeout):
		t.Fatal("timed out awaiting response-analysis boundary")
		return responseanalysis.Boundary{}
	}
}

func v5aAwaitCompletion(t *testing.T, forwarding *responseanalysis.ForwardingResponse) responseanalysis.Completion {
	t.Helper()
	result := make(chan responseanalysis.Completion, 1)
	go func() { result <- forwarding.Wait() }()
	select {
	case completion := <-result:
		return completion
	case <-time.After(v5aWaitTimeout):
		t.Fatal("timed out awaiting response-analysis completion")
		return responseanalysis.Completion{}
	}
}

func v5aGzip(t testing.TB, decoded []byte) []byte {
	t.Helper()
	var wire bytes.Buffer
	writer := gzip.NewWriter(&wire)
	if _, err := writer.Write(decoded); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return wire.Bytes()
}

func v5aIsAlreadyResolved(err error, state responseanalysis.ResolutionState) bool {
	var resolved *responseanalysis.AlreadyResolved
	return errors.As(err, &resolved) && resolved.State == state
}

func TestV5AEverySplitMultilineCRLFEofTailAndOrdinaryOutput(t *testing.T) {
	errorWire := []byte(
		": heartbeat\r\n" +
			"event: response.created\r\n" +
			"data:{\"type\":\"response.created\"}\r\n\r\n" +
			"event: error\r\n" +
			"data: {\"type\":\"error\",\r\n" +
			"data:\"code\":\"server_is_overloaded\",\r\n" +
			"data: \"message\":\"At capacity\"}\r\n",
	)
	for split := 0; split <= len(errorWire); split++ {
		budget := v5aBudget(t, responseanalysis.ResponseProbeMemoryBudget)
		analyzer := v5aAnalyzer(t, budget, responseanalysis.AnalyzerOptions{ProbeDuration: time.Hour})
		body := newV5ASegmentBody(errorWire, split)
		writer := newV5AWriter(0)
		response := analyzer.Start(context.Background(), responseanalysis.StartInput{
			Mode: responseanalysis.ProbeMode(), APIType: "codex", ContentType: "text/event-stream",
			StatusCode: http.StatusOK, Body: body, Writer: writer,
			Match: func(fields responseanalysis.SemanticFields) bool {
				return fields.Code == "server_is_overloaded" && fields.Message == "At capacity"
			},
		})

		boundary := v5aAwaitBoundary(t, response)
		if boundary.Reason != responseanalysis.BoundarySemanticMatch || !boundary.HasObservation {
			t.Fatalf("split %d: boundary=%#v", split, boundary)
		}
		receipt, err := response.Discard(responseanalysis.TransitionSemanticDecision)
		if err != nil {
			t.Fatalf("split %d: discard: %v", split, err)
		}
		snapshot := writer.snapshot()
		if len(snapshot.statuses) != 0 || len(snapshot.body) != 0 || receipt.ClientBodyBytesWritten != 0 {
			t.Fatalf("split %d leaked client output: writer=%#v receipt=%#v", split, snapshot, receipt)
		}
		if body.closeCount.Load() != 1 || body.concurrentRead.Load() || budget.Used() != 0 {
			t.Fatalf("split %d lifecycle: closes=%d concurrent=%t budget=%d", split, body.closeCount.Load(), body.concurrentRead.Load(), budget.Used())
		}
	}

	ordinaryWire := []byte(
		"event: response.output_text.delta\r\n" +
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"Our servers are currently overloaded at capacity\"}\r\n\r\n",
	)
	for split := 0; split <= len(ordinaryWire); split++ {
		budget := v5aBudget(t, responseanalysis.ResponseProbeMemoryBudget)
		analyzer := v5aAnalyzer(t, budget, responseanalysis.AnalyzerOptions{ProbeDuration: time.Hour})
		body := newV5ASegmentBody(ordinaryWire, split)
		writer := newV5AWriter(0)
		var matcherCalls atomic.Int32
		response := analyzer.Start(context.Background(), responseanalysis.StartInput{
			Mode: responseanalysis.ProbeMode(), APIType: "codex", ContentType: "text/event-stream",
			StatusCode: http.StatusOK, Body: body, Writer: writer,
			Match: func(responseanalysis.SemanticFields) bool {
				matcherCalls.Add(1)
				return true
			},
		})

		boundary := v5aAwaitBoundary(t, response)
		if boundary.Reason != responseanalysis.BoundaryClientVisibleEvent || boundary.Forwarding == nil {
			t.Fatalf("ordinary split %d: boundary=%#v", split, boundary)
		}
		completion := v5aAwaitCompletion(t, boundary.Forwarding)
		snapshot := writer.snapshot()
		if matcherCalls.Load() != 0 || !bytes.Equal(snapshot.body, ordinaryWire) {
			t.Fatalf("ordinary split %d scanned output or changed bytes: matcher_calls=%d body=%q", split, matcherCalls.Load(), snapshot.body)
		}
		if completion.Termination != responseanalysis.TerminationCompleted || body.closeCount.Load() != 1 || budget.Used() != 0 {
			t.Fatalf("ordinary split %d lifecycle: completion=%#v closes=%d budget=%d", split, completion, body.closeCount.Load(), budget.Used())
		}
	}
}

func TestV5AFlagshipThirdSSEErrorNear72KiBRemainsAbsorbable(t *testing.T) {
	const thirdEventBytes = 72 * 1024
	prefix := "event: error\r\ndata: {\"type\":\"error\",\"padding\":\""
	suffix := "\",\"code\":\"server_is_overloaded\",\"message\":\"Our servers are currently overloaded at capacity\"}\r\n\r\n"
	third := prefix + strings.Repeat("x", thirdEventBytes-len(prefix)-len(suffix)) + suffix
	wire := []byte(
		"event: response.created\r\ndata: {\"type\":\"response.created\"}\r\n\r\n" +
			"event: response.in_progress\r\ndata: {\"type\":\"response.in_progress\"}\r\n\r\n" +
			third,
	)

	budget := v5aBudget(t, responseanalysis.ResponseProbeMemoryBudget)
	analyzer := v5aAnalyzer(t, budget, responseanalysis.AnalyzerOptions{})
	body := newV5APatternBody(wire, 1, 7, 257, 4093, 31)
	writer := newV5AWriter(0)
	response := analyzer.Start(context.Background(), responseanalysis.StartInput{
		Mode: responseanalysis.ProbeMode(), APIType: "codex", ContentType: "text/event-stream",
		StatusCode: http.StatusOK, Body: body, Writer: writer,
		Match: func(fields responseanalysis.SemanticFields) bool {
			return fields.Code == "server_is_overloaded" &&
				fields.Message == "Our servers are currently overloaded at capacity"
		},
	})

	boundary := v5aAwaitBoundary(t, response)
	if boundary.Reason != responseanalysis.BoundarySemanticMatch || !boundary.HasObservation {
		t.Fatalf("flagship boundary=%#v", boundary)
	}
	if snapshot := writer.snapshot(); len(snapshot.statuses) != 0 || len(snapshot.body) != 0 {
		t.Fatalf("flagship became visible before decision: %#v", snapshot)
	}
	receipt, err := response.Discard(responseanalysis.TransitionSemanticDecision)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.UpstreamBytesRead != int64(len(wire)) || receipt.ClientBodyBytesWritten != 0 {
		t.Fatalf("flagship receipt=%#v wire_bytes=%d", receipt, len(wire))
	}
	if receipt.PeakRequestBytes > responseanalysis.DefaultProbeMemoryLimit {
		t.Fatalf("flagship peak request bytes=%d exceeds default=%d", receipt.PeakRequestBytes, responseanalysis.DefaultProbeMemoryLimit)
	}
	if body.closeCount.Load() != 1 || body.concurrentRead.Load() || budget.Used() != 0 {
		t.Fatalf("flagship lifecycle: closes=%d concurrent=%t budget=%d", body.closeCount.Load(), body.concurrentRead.Load(), budget.Used())
	}
}

func TestV5ACompressionBombFailOpenAndExactProcessCap(t *testing.T) {
	t.Run("gzip expansion fails open with exact raw bytes", func(t *testing.T) {
		decoded := []byte("data: " + strings.Repeat("x", responseanalysis.MaxDecodedEventBytes+8192) + "\n\n")
		wire := v5aGzip(t, decoded)
		budget := v5aBudget(t, responseanalysis.ResponseProbeMemoryBudget)
		analyzer := v5aAnalyzer(t, budget, responseanalysis.AnalyzerOptions{
			ProbeDuration:    time.Hour,
			ProbeMemoryLimit: responseanalysis.MaxProbeMemoryLimit,
		})
		body := newV5APatternBody(wire, 1, 5, 97, 509)
		writer := newV5AWriter(7)
		response := analyzer.Start(context.Background(), responseanalysis.StartInput{
			Mode: responseanalysis.ProbeMode(), APIType: "codex", ContentType: "text/event-stream", ContentEncoding: "gzip",
			StatusCode: http.StatusOK, Header: http.Header{"Content-Encoding": {"gzip"}},
			Body: body, Writer: writer, Match: func(responseanalysis.SemanticFields) bool { return true },
		})

		boundary := v5aAwaitBoundary(t, response)
		if boundary.Reason != responseanalysis.BoundaryDecodedEventTooLarge || boundary.Forwarding == nil {
			t.Fatalf("compression bomb boundary=%#v", boundary)
		}
		completion := v5aAwaitCompletion(t, boundary.Forwarding)
		snapshot := writer.snapshot()
		if completion.AnalysisFailure != responseanalysis.BoundaryDecodedEventTooLarge || !bytes.Equal(snapshot.body, wire) {
			t.Fatalf("compression bomb completion=%#v body_bytes=%d wire_bytes=%d", completion, len(snapshot.body), len(wire))
		}
		if completion.PeakRequestBytes > responseanalysis.MaxProbeMemoryLimit || body.closeCount.Load() != 1 || budget.Used() != 0 {
			t.Fatalf("compression bomb lifecycle: peak=%d closes=%d budget=%d", completion.PeakRequestBytes, body.closeCount.Load(), budget.Used())
		}
	})

	t.Run("simultaneous probes stop at the exact process ceiling", func(t *testing.T) {
		const liveProbeCount = 2
		processLimit := liveProbeCount * responseanalysis.PumpReadBufferBytes
		budget := v5aBudget(t, processLimit)
		analyzer := v5aAnalyzer(t, budget, responseanalysis.AnalyzerOptions{ProbeDuration: time.Hour})

		release := make(chan struct{})
		responses := make([]*responseanalysis.PendingResponse, 0, liveProbeCount)
		writers := make([]*v5aWriter, 0, liveProbeCount)
		for range liveProbeCount {
			started := make(chan struct{})
			body := newV5ABlockingBody(v5aReadStep{started: started, release: release, err: io.EOF})
			writer := newV5AWriter(0)
			responses = append(responses, analyzer.Start(context.Background(), responseanalysis.StartInput{
				Mode: responseanalysis.ProbeMode(), APIType: "codex", ContentType: "application/json",
				StatusCode: http.StatusOK, Body: body, Writer: writer,
			}))
			writers = append(writers, writer)
			v5aWaitForSignal(t, started, "probe read to block")
		}
		if budget.Used() != processLimit || budget.Peak() != processLimit {
			t.Fatalf("live probes used=%d peak=%d want=%d", budget.Used(), budget.Peak(), processLimit)
		}

		deniedWire := []byte("{\"ordinary\":true}")
		deniedBody := newV5ASegmentBody(deniedWire, len(deniedWire)/2)
		deniedWriter := newV5AWriter(2)
		denied := analyzer.Start(context.Background(), responseanalysis.StartInput{
			Mode: responseanalysis.ProbeMode(), APIType: "codex", ContentType: "application/json",
			StatusCode: http.StatusOK, Body: deniedBody, Writer: deniedWriter,
		})
		boundary := v5aAwaitBoundary(t, denied)
		if boundary.Reason != responseanalysis.BoundaryProcessMemoryExhausted {
			t.Fatalf("process denial boundary=%#v", boundary)
		}
		completion := v5aAwaitCompletion(t, boundary.Forwarding)
		if !bytes.Equal(deniedWriter.snapshot().body, deniedWire) ||
			completion.AnalysisFailure != responseanalysis.BoundaryProcessMemoryExhausted {
			t.Fatalf("process denial completion=%#v body=%q", completion, deniedWriter.snapshot().body)
		}

		close(release)
		for index, response := range responses {
			probeBoundary := v5aAwaitBoundary(t, response)
			if probeBoundary.State != responseanalysis.StateForwarding || probeBoundary.Forwarding == nil {
				t.Fatalf("probe %d state=%d reason=%q", index, probeBoundary.State, probeBoundary.Reason)
			}
			probeCompletion := v5aAwaitCompletion(t, probeBoundary.Forwarding)
			if probeCompletion.Termination != responseanalysis.TerminationCompleted || len(writers[index].snapshot().body) != 0 {
				t.Fatalf("probe %d completion=%#v writer=%#v", index, probeCompletion, writers[index].snapshot())
			}
		}
		if budget.Used() != 0 || budget.Peak() != processLimit {
			t.Fatalf("process cap cleanup used=%d peak=%d want_peak=%d", budget.Used(), budget.Peak(), processLimit)
		}
	})
}

func TestV5AExactWireIdentityWithShortWritesAndTrailers(t *testing.T) {
	decoded := []byte("event: error\r\ndata: {\"type\":\"error\",\"code\":\"busy\",\"message\":\"retry\"}\r\n\r\n")
	tests := []struct {
		name         string
		encoding     string
		wire         func(*testing.T) []byte
		wantBoundary responseanalysis.BoundaryReason
	}{
		{name: "identity", encoding: "identity", wire: func(*testing.T) []byte { return append([]byte(nil), decoded...) }, wantBoundary: responseanalysis.BoundarySemanticMatch},
		{name: "gzip", encoding: "gzip", wire: func(t *testing.T) []byte { return v5aGzip(t, decoded) }, wantBoundary: responseanalysis.BoundarySemanticMatch},
		{name: "brotli fail-open", encoding: "br", wire: func(*testing.T) []byte { return []byte{0x1b, 0x58, 0x00, 0x28, 0x2c, 0x7f} }, wantBoundary: responseanalysis.BoundaryUnsupportedEncoding},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wire := test.wire(t)
			trailer := make(http.Header)
			body := newV5ASegmentBody(wire, len(wire)/2)
			body.trailer = trailer
			body.finalTrailers = http.Header{
				"X-Final-Checksum": {"sha256:abc"},
				"X-Final-Trace":    {"one", "two"},
			}
			writer := newV5AWriter(3)
			budget := v5aBudget(t, responseanalysis.ResponseProbeMemoryBudget)
			analyzer := v5aAnalyzer(t, budget, responseanalysis.AnalyzerOptions{ProbeDuration: time.Hour})
			response := analyzer.Start(context.Background(), responseanalysis.StartInput{
				Mode: responseanalysis.ProbeMode(), APIType: "codex", ContentType: "text/event-stream", ContentEncoding: test.encoding,
				StatusCode: http.StatusPartialContent,
				Header: http.Header{
					"Content-Type":     {"text/event-stream"},
					"Content-Encoding": {test.encoding},
					"Trailer":          {"X-Final-Checksum", "X-Final-Trace"},
					"X-Repeat":         {"one", "two"},
				},
				Trailer: trailer, Body: body, Writer: writer,
				Match: func(fields responseanalysis.SemanticFields) bool { return fields.Message == "retry" },
			})

			boundary := v5aAwaitBoundary(t, response)
			if boundary.Reason != test.wantBoundary {
				t.Fatalf("boundary=%#v", boundary)
			}
			forwarding := boundary.Forwarding
			if test.wantBoundary == responseanalysis.BoundarySemanticMatch {
				var err error
				forwarding, err = response.Commit(responseanalysis.TransitionSemanticDecision)
				if err != nil {
					t.Fatal(err)
				}
			}
			completion := v5aAwaitCompletion(t, forwarding)
			snapshot := writer.snapshot()
			if len(snapshot.statuses) != 1 || snapshot.statuses[0] != http.StatusPartialContent {
				t.Fatalf("statuses=%#v", snapshot.statuses)
			}
			if !bytes.Equal(snapshot.body, wire) || snapshot.writeCalls <= 1 {
				t.Fatalf("short-write identity: calls=%d body=%x want=%x", snapshot.writeCalls, snapshot.body, wire)
			}
			if got := snapshot.header.Values("X-Repeat"); len(got) != 2 || got[0] != "one" || got[1] != "two" {
				t.Fatalf("repeated headers=%#v", got)
			}
			if got := snapshot.header.Values(http.TrailerPrefix + "X-Final-Trace"); len(got) != 2 || got[0] != "one" || got[1] != "two" {
				t.Fatalf("forwarded trailers=%#v header=%#v", got, snapshot.header)
			}
			if completion.Trailer.Get("X-Final-Checksum") != "sha256:abc" ||
				completion.ClientBodyBytesWritten != int64(len(wire)) ||
				completion.Termination != responseanalysis.TerminationCompleted {
				t.Fatalf("completion=%#v", completion)
			}
			if body.closeCount.Load() != 1 || body.concurrentRead.Load() ||
				writer.concurrentWrite.Load() || budget.Used() != 0 {
				t.Fatalf("lifecycle closes=%d read_race=%t write_race=%t budget=%d", body.closeCount.Load(), body.concurrentRead.Load(), writer.concurrentWrite.Load(), budget.Used())
			}
		})
	}
}

func TestV5ABlockedReadTimerGenerationAndBackpressure(t *testing.T) {
	scheduler := newV5AManualScheduler()
	budget := v5aBudget(t, responseanalysis.ResponseProbeMemoryBudget)
	analyzer := v5aAnalyzer(t, budget, responseanalysis.AnalyzerOptions{
		ProbeDuration: time.Hour,
		IdleDuration:  time.Hour,
		Scheduler:     scheduler,
	})

	firstReadStarted := make(chan struct{})
	releaseFirstRead := make(chan struct{})
	secondReadStarted := make(chan struct{})
	neverReleaseSecondRead := make(chan struct{})
	body := newV5ABlockingBody(
		v5aReadStep{data: []byte(": heartbeat\r\n\r\n"), started: firstReadStarted, release: releaseFirstRead},
		v5aReadStep{started: secondReadStarted, release: neverReleaseSecondRead},
	)
	releaseClientWrite := make(chan struct{})
	writer := newV5AWriter(0)
	writer.writeGate = releaseClientWrite
	response := analyzer.Start(context.Background(), responseanalysis.StartInput{
		Mode: responseanalysis.ProbeMode(), APIType: "codex", ContentType: "text/event-stream",
		StatusCode: http.StatusOK, Body: body, Writer: writer,
	})

	v5aWaitForSignal(t, firstReadStarted, "first blocked upstream read")
	scheduler.waitForCount(t, 2) // probe timer, then first-read idle timer
	scheduler.fire(t, 0)
	boundary := v5aAwaitBoundary(t, response)
	if boundary.Reason != responseanalysis.BoundaryProbeDurationElapsed || boundary.Forwarding == nil {
		t.Fatalf("probe timer boundary=%#v", boundary)
	}

	close(releaseFirstRead)
	v5aWaitForSignal(t, writer.writeStarted, "backpressured client write")
	for range 8 {
		scheduler.fire(t, 1)
	}
	scheduler.fire(t, 0)
	select {
	case <-secondReadStarted:
		t.Fatal("upstream advanced while the client writer was blocked")
	default:
	}
	if body.closeCount.Load() != 0 {
		t.Fatalf("stale timers closed body during backpressure: %d", body.closeCount.Load())
	}

	close(releaseClientWrite)
	v5aWaitForSignal(t, secondReadStarted, "second blocked upstream read")
	scheduler.waitForCount(t, 3)
	for range 8 {
		scheduler.fire(t, 1)
	}
	scheduler.fire(t, 2)

	completion := v5aAwaitCompletion(t, boundary.Forwarding)
	snapshot := writer.snapshot()
	if completion.Termination != responseanalysis.TerminationUpstreamReadFailure ||
		completion.ReadTermination != responseanalysis.ReadTerminationIdleTimeout {
		t.Fatalf("completion=%#v", completion)
	}
	if string(snapshot.body) != ": heartbeat\r\n\r\n" || len(snapshot.statuses) != 1 {
		t.Fatalf("writer transcript=%#v", snapshot)
	}
	if body.closeCount.Load() != 1 || body.activeReads.Load() != 0 || body.concurrentRead.Load() ||
		writer.concurrentWrite.Load() || budget.Used() != 0 {
		t.Fatalf("lifecycle closes=%d active_reads=%d read_race=%t write_race=%t budget=%d", body.closeCount.Load(), body.activeReads.Load(), body.concurrentRead.Load(), writer.concurrentWrite.Load(), budget.Used())
	}
}

func TestV5ASemanticResolutionRace1000(t *testing.T) {
	const iterations = 1000
	wire := []byte("event: error\ndata: {\"type\":\"error\",\"code\":\"busy\",\"message\":\"retry\"}\n\n")
	budget := v5aBudget(t, responseanalysis.ResponseProbeMemoryBudget)
	analyzer := v5aAnalyzer(t, budget, responseanalysis.AnalyzerOptions{ProbeDuration: time.Hour})

	for iteration := range iterations {
		body := newV5ASegmentBody(wire, (iteration*17)%(len(wire)+1))
		writer := newV5AWriter((iteration % 7) + 1)
		response := analyzer.Start(context.Background(), responseanalysis.StartInput{
			Mode: responseanalysis.ProbeMode(), APIType: "codex", ContentType: "text/event-stream",
			StatusCode: http.StatusOK, Header: http.Header{"X-Repeat": {"one", "two"}},
			Body: body, Writer: writer, Match: func(responseanalysis.SemanticFields) bool { return true },
		})
		boundary := v5aAwaitBoundary(t, response)
		if boundary.Reason != responseanalysis.BoundarySemanticMatch {
			t.Fatalf("iteration %d boundary=%#v", iteration, boundary)
		}

		start := make(chan struct{})
		results := make(chan v5aTransitionResult, 2)
		go func() {
			<-start
			forwarding, err := response.Commit(responseanalysis.TransitionSemanticDecision)
			results <- v5aTransitionResult{kind: "commit", forwarding: forwarding, err: err}
		}()
		go func() {
			<-start
			receipt, err := response.Discard(responseanalysis.TransitionSemanticDecision)
			results <- v5aTransitionResult{kind: "discard", receipt: receipt, err: err}
		}()
		close(start)
		first := v5aAwaitTransitionResult(t, results)
		second := v5aAwaitTransitionResult(t, results)
		winner, loser := first, second
		if winner.err != nil {
			winner, loser = loser, winner
		}
		if winner.err != nil || loser.err == nil {
			t.Fatalf("iteration %d transitions=%#v / %#v", iteration, first, second)
		}

		snapshot := v5aWriterSnapshot{}
		switch winner.kind {
		case "commit":
			if winner.forwarding == nil || !v5aIsAlreadyResolved(loser.err, responseanalysis.StateForwarding) {
				t.Fatalf("iteration %d commit winner=%#v loser=%v", iteration, winner, loser.err)
			}
			completion := v5aAwaitCompletion(t, winner.forwarding)
			snapshot = writer.snapshot()
			if completion.State != responseanalysis.StateForwarding ||
				len(snapshot.statuses) != 1 || !bytes.Equal(snapshot.body, wire) {
				t.Fatalf("iteration %d commit completion=%#v writer=%#v", iteration, completion, snapshot)
			}
		case "discard":
			if !winner.receipt.BodyClosed || !v5aIsAlreadyResolved(loser.err, responseanalysis.StateDiscarded) {
				t.Fatalf("iteration %d discard winner=%#v loser=%v", iteration, winner, loser.err)
			}
			snapshot = writer.snapshot()
			if len(snapshot.statuses) != 0 || len(snapshot.body) != 0 {
				t.Fatalf("iteration %d discard leaked writer=%#v", iteration, snapshot)
			}
		default:
			t.Fatalf("iteration %d unknown winner=%#v", iteration, winner)
		}
		if len(snapshot.statuses) > 1 || body.closeCount.Load() != 1 || body.concurrentRead.Load() ||
			writer.concurrentWrite.Load() || budget.Used() != 0 {
			t.Fatalf("iteration %d lifecycle statuses=%#v closes=%d read_race=%t write_race=%t budget=%d", iteration, snapshot.statuses, body.closeCount.Load(), body.concurrentRead.Load(), writer.concurrentWrite.Load(), budget.Used())
		}
	}
}

func v5aAwaitTransitionResult(t *testing.T, results <-chan v5aTransitionResult) v5aTransitionResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(v5aWaitTimeout):
		t.Fatal("timed out awaiting resolution race result")
		return v5aTransitionResult{}
	}
}

func v5aWaitForSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(v5aWaitTimeout):
		t.Fatalf("timed out waiting for %s", description)
	}
}
