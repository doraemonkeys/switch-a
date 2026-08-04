package responseanalysis

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis/framing"
)

const analyzerTestTimeout = 3 * time.Second

type analyzerManualTimer struct {
	callback func()
	stopped  atomic.Bool
}

func (t *analyzerManualTimer) Stop() bool {
	return !t.stopped.Swap(true)
}

type analyzerManualScheduler struct {
	mu      sync.Mutex
	timers  []*analyzerManualTimer
	created chan int
}

func newAnalyzerManualScheduler() *analyzerManualScheduler {
	return &analyzerManualScheduler{created: make(chan int, 16)}
}

func (s *analyzerManualScheduler) AfterFunc(_ time.Duration, callback func()) Timer {
	timer := &analyzerManualTimer{callback: callback}
	s.mu.Lock()
	index := len(s.timers)
	s.timers = append(s.timers, timer)
	s.mu.Unlock()
	s.created <- index
	return timer
}

func (s *analyzerManualScheduler) fire(t *testing.T, index int) {
	t.Helper()
	for {
		s.mu.Lock()
		if index < len(s.timers) {
			callback := s.timers[index].callback
			s.mu.Unlock()
			callback()
			return
		}
		s.mu.Unlock()
		select {
		case <-s.created:
		case <-time.After(analyzerTestTimeout):
			t.Fatalf("timer %d was not created", index)
		}
	}
}

type analyzerSplitBody struct {
	mu         sync.Mutex
	chunks     [][]byte
	chunk      int
	offset     int
	closeCount atomic.Int32
}

func newAnalyzerSplitBody(raw []byte, split int) *analyzerSplitBody {
	chunks := make([][]byte, 0, 2)
	if split > 0 {
		chunks = append(chunks, append([]byte(nil), raw[:split]...))
	}
	if split < len(raw) {
		chunks = append(chunks, append([]byte(nil), raw[split:]...))
	}
	return &analyzerSplitBody{chunks: chunks}
}

func (b *analyzerSplitBody) Read(target []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.chunk >= len(b.chunks) {
		return 0, io.EOF
	}
	current := b.chunks[b.chunk]
	n := copy(target, current[b.offset:])
	b.offset += n
	if b.offset == len(current) {
		b.chunk++
		b.offset = 0
	}
	return n, nil
}

func (b *analyzerSplitBody) Close() error {
	b.closeCount.Add(1)
	return nil
}

type analyzerReadStep struct {
	data    []byte
	err     error
	started chan struct{}
	release <-chan struct{}
}

type analyzerSteppedBody struct {
	mu         sync.Mutex
	steps      []analyzerReadStep
	next       int
	closed     chan struct{}
	closeOnce  sync.Once
	closeCount atomic.Int32
}

func (b *analyzerSteppedBody) Read(target []byte) (int, error) {
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

func (b *analyzerSteppedBody) Close() error {
	b.closeCount.Add(1)
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

func newRuntimeAnalyzer(t *testing.T, budget *ProcessMemoryBudget, options AnalyzerOptions) *Analyzer {
	t.Helper()
	analyzer, err := NewAnalyzer(NewRegistry(), budget, options)
	if err != nil {
		t.Fatal(err)
	}
	return analyzer
}

func newRuntimeBudget(t *testing.T, limit int) *ProcessMemoryBudget {
	t.Helper()
	budget, err := NewProcessMemoryBudget(limit)
	if err != nil {
		t.Fatal(err)
	}
	return budget
}

func awaitAnalyzerBoundary(t *testing.T, response *PendingResponse) Boundary {
	t.Helper()
	ready := make(chan Boundary, 1)
	go func() { ready <- response.AwaitBoundary() }()
	select {
	case boundary := <-ready:
		return boundary
	case <-time.After(analyzerTestTimeout):
		t.Fatal("timed out awaiting analyzer boundary")
		return Boundary{}
	}
}

func awaitAnalyzerCompletion(t *testing.T, forwarding *ForwardingResponse) Completion {
	t.Helper()
	ready := make(chan Completion, 1)
	go func() { ready <- forwarding.Wait() }()
	select {
	case completion := <-ready:
		return completion
	case <-time.After(analyzerTestTimeout):
		t.Fatal("timed out awaiting analyzer completion")
		return Completion{}
	}
}

func awaitAnalyzerSemantic(t *testing.T, forwarding *ForwardingResponse) SemanticMilestone {
	t.Helper()
	ready := make(chan SemanticMilestone, 1)
	go func() { ready <- forwarding.AwaitSemanticOrCompletion() }()
	select {
	case milestone := <-ready:
		return milestone
	case <-time.After(analyzerTestTimeout):
		t.Fatal("timed out awaiting analyzer semantic milestone")
		return SemanticMilestone{}
	}
}

func gzipRuntimeBytes(t *testing.T, decoded []byte) []byte {
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

func gzipRuntimeBytesWithCorruptChecksum(t *testing.T, decoded []byte) []byte {
	t.Helper()
	const gzipTrailerBytes = 8
	const checksumCorruptionMask byte = 0xff

	wire := append([]byte(nil), gzipRuntimeBytes(t, decoded)...)
	// Corrupting only the CRC keeps the member and semantic payload valid, so
	// the regression exercises decoder-integrity precedence rather than syntax.
	wire[len(wire)-gzipTrailerBytes] ^= checksumCorruptionMask
	return wire
}

func TestAnalyzerRawIdentityEverySplit(t *testing.T) {
	decoded := []byte("event: error\ndata: {\"type\":\"error\",\"code\":\"server_is_overloaded\",\"message\":\"At Capacity\"}\n\n")
	tests := []struct {
		name           string
		encoding       string
		wire           []byte
		wantBoundary   BoundaryReason
		wantFailure    BoundaryReason
		semanticCommit bool
	}{
		{name: "identity", encoding: "identity", wire: decoded, wantBoundary: BoundarySemanticMatch, semanticCommit: true},
		{name: "gzip", encoding: "gzip", wire: gzipRuntimeBytes(t, decoded), wantBoundary: BoundarySemanticMatch, semanticCommit: true},
		{name: "malformed SSE fail-open", encoding: "identity", wire: []byte("data: not-json\n\n"), wantBoundary: BoundaryReason(FailureMalformedFrame), wantFailure: BoundaryReason(FailureMalformedFrame)},
		{name: "corrupt gzip fail-open", encoding: "gzip", wire: []byte("broken-gzip"), wantBoundary: BoundaryReason(FailureContentDecoding), wantFailure: BoundaryReason(FailureContentDecoding)},
		{name: "brotli fail-open", encoding: "br", wire: []byte{0x1b, 0x58, 0x00, 0x28, 0x2c}, wantBoundary: BoundaryReason(FailureUnsupportedEncoding), wantFailure: BoundaryReason(FailureUnsupportedEncoding)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for split := 0; split <= len(test.wire); split++ {
				budget := newRuntimeBudget(t, ResponseProbeMemoryBudget)
				analyzer := newRuntimeAnalyzer(t, budget, AnalyzerOptions{ProbeDuration: time.Hour})
				body := newAnalyzerSplitBody(test.wire, split)
				writer := httptest.NewRecorder()
				header := http.Header{
					"Content-Type":     {"text/event-stream"},
					"Content-Encoding": {test.encoding},
					"X-Repeat":         {"one", "two"},
				}
				response := analyzer.Start(context.Background(), StartInput{
					OperationID: "split", Mode: ProbeMode(), APIType: "codex", StatusCode: http.StatusBadGateway,
					Header: header, Body: body, Writer: writer,
					Match: func(fields SemanticFields) bool { return strings.EqualFold(fields.Message, "at capacity") },
				})
				boundary := awaitAnalyzerBoundary(t, response)
				if boundary.Reason != test.wantBoundary {
					t.Fatalf("split %d: boundary=%#v", split, boundary)
				}
				forwarding := boundary.Forwarding
				if test.semanticCommit {
					var err error
					forwarding, err = response.Commit(TransitionSemanticDecision)
					if err != nil {
						t.Fatalf("split %d: %v", split, err)
					}
				}
				completion := awaitAnalyzerCompletion(t, forwarding)
				if !bytes.Equal(writer.Body.Bytes(), test.wire) || writer.Code != http.StatusBadGateway {
					t.Fatalf("split %d: status=%d body=%x want=%x", split, writer.Code, writer.Body.Bytes(), test.wire)
				}
				if got := writer.Header().Values("X-Repeat"); len(got) != 2 || got[0] != "one" || got[1] != "two" {
					t.Fatalf("split %d: repeated headers=%#v", split, got)
				}
				if writer.Header().Get("Content-Encoding") != test.encoding || completion.AnalysisFailure != test.wantFailure {
					t.Fatalf("split %d: completion=%#v header=%#v", split, completion, writer.Header())
				}
				if body.closeCount.Load() != 1 || budget.Used() != 0 {
					t.Fatalf("split %d: closes=%d budget=%d", split, body.closeCount.Load(), budget.Used())
				}
			}
		})
	}
}

func TestCorruptGzipTrailerFailsOpenBeforeMatchingDecodedBytes(t *testing.T) {
	decoded := []byte("event: error\ndata: {\"type\":\"error\",\"code\":\"server_is_overloaded\",\"message\":\"At Capacity\"}\n\n")
	wire := gzipRuntimeBytesWithCorruptChecksum(t, decoded)
	budget := newRuntimeBudget(t, ResponseProbeMemoryBudget)
	analyzer := newRuntimeAnalyzer(t, budget, AnalyzerOptions{ProbeDuration: time.Hour})
	body := newAnalyzerSplitBody(wire, len(wire))
	writer := httptest.NewRecorder()
	response := analyzer.Start(context.Background(), StartInput{
		Mode: ProbeMode(), APIType: "codex", ContentType: "text/event-stream", ContentEncoding: "gzip",
		StatusCode: http.StatusOK, Header: http.Header{"Content-Encoding": {"gzip"}}, Body: body, Writer: writer,
		Match: func(fields SemanticFields) bool { return strings.EqualFold(fields.Message, "at capacity") },
	})

	boundary := awaitAnalyzerBoundary(t, response)
	if boundary.State != StateForwarding || boundary.Reason != BoundaryReason(FailureContentDecoding) || boundary.HasObservation {
		t.Fatalf("boundary=%#v", boundary)
	}
	completion := awaitAnalyzerCompletion(t, boundary.Forwarding)
	if completion.AnalysisFailure != BoundaryReason(FailureContentDecoding) || completion.HasSemanticObservation {
		t.Fatalf("completion=%#v", completion)
	}
	if !bytes.Equal(writer.Body.Bytes(), wire) || body.closeCount.Load() != 1 || budget.Used() != 0 {
		t.Fatalf("body=%x want=%x closes=%d budget=%d", writer.Body.Bytes(), wire, body.closeCount.Load(), budget.Used())
	}
}

func TestUnsupportedSSESelectionStillFlushesPassthrough(t *testing.T) {
	tests := []struct {
		name            string
		apiType         string
		contentEncoding string
		wantReason      BoundaryReason
	}{
		{name: "unsupported protocol", apiType: "custom:stream", contentEncoding: "identity", wantReason: BoundaryReason(FailureUnsupportedProtocol)},
		{name: "unsupported encoding", apiType: "codex", contentEncoding: "zstd", wantReason: BoundaryReason(FailureUnsupportedEncoding)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wire := []byte("data: passthrough\n\n")
			budget := newRuntimeBudget(t, ResponseProbeMemoryBudget)
			analyzer := newRuntimeAnalyzer(t, budget, AnalyzerOptions{ProbeDuration: time.Hour})
			writer := httptest.NewRecorder()
			response := analyzer.Start(context.Background(), StartInput{
				Mode: ProbeMode(), APIType: test.apiType, StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type":     {"Text/Event-Stream; Charset=UTF-8"},
					"Content-Encoding": {test.contentEncoding},
				},
				Body: io.NopCloser(bytes.NewReader(wire)), Writer: writer,
			})

			boundary := awaitAnalyzerBoundary(t, response)
			completion := awaitAnalyzerCompletion(t, boundary.Forwarding)
			if boundary.State != StateForwarding || boundary.Reason != test.wantReason || completion.AnalysisFailure != test.wantReason {
				t.Fatalf("boundary=%#v completion=%#v", boundary, completion)
			}
			if !bytes.Equal(writer.Body.Bytes(), wire) || !writer.Flushed || budget.Used() != 0 {
				t.Fatalf("body=%q flushed=%t budget=%d", writer.Body.Bytes(), writer.Flushed, budget.Used())
			}
		})
	}
}

func TestInsideOutsideProbeWindowJSONPair(t *testing.T) {
	prefix := []byte(`{"type":"error","error":{"message":"RE`)
	tail := []byte(`TRY"}}`)
	full := append(append([]byte(nil), prefix...), tail...)
	match := func(fields SemanticFields) bool { return strings.EqualFold(fields.Message, "retry") }

	t.Run("inside window is held", func(t *testing.T) {
		budget := newRuntimeBudget(t, ResponseProbeMemoryBudget)
		analyzer := newRuntimeAnalyzer(t, budget, AnalyzerOptions{ProbeDuration: time.Hour})
		writer := httptest.NewRecorder()
		response := analyzer.Start(context.Background(), StartInput{
			Mode: ProbeMode(), APIType: "codex", ContentType: "application/json", StatusCode: http.StatusOK,
			Body: io.NopCloser(bytes.NewReader(full)), Writer: writer, Match: match,
		})
		boundary := awaitAnalyzerBoundary(t, response)
		if boundary.Reason != BoundarySemanticMatch || writer.Body.Len() != 0 {
			t.Fatalf("boundary=%#v writer=%#v", boundary, writer)
		}
		if _, err := response.Discard(TransitionSemanticDecision); err != nil {
			t.Fatal(err)
		}
		if writer.Body.Len() != 0 || budget.Used() != 0 {
			t.Fatalf("discard leaked body=%q budget=%d", writer.Body.String(), budget.Used())
		}
	})

	t.Run("outside window remains a late fact", func(t *testing.T) {
		scheduler := newAnalyzerManualScheduler()
		budget := newRuntimeBudget(t, ResponseProbeMemoryBudget)
		analyzer := newRuntimeAnalyzer(t, budget, AnalyzerOptions{ProbeDuration: time.Hour, Scheduler: scheduler})
		releaseTail := make(chan struct{})
		tailStarted := make(chan struct{})
		body := &analyzerSteppedBody{
			steps: []analyzerReadStep{
				{data: prefix},
				{data: tail, err: io.EOF, started: tailStarted, release: releaseTail},
			},
			closed: make(chan struct{}),
		}
		writer := httptest.NewRecorder()
		response := analyzer.Start(context.Background(), StartInput{
			Mode: ProbeMode(), APIType: "codex", ContentType: "application/json", StatusCode: http.StatusOK,
			Body: body, Writer: writer, Match: match,
		})
		select {
		case <-tailStarted:
		case <-time.After(analyzerTestTimeout):
			t.Fatal("incomplete JSON did not remain inconclusive")
		}
		scheduler.fire(t, 0)
		boundary := awaitAnalyzerBoundary(t, response)
		if boundary.Reason != BoundaryProbeDurationElapsed || !bytes.Equal(writer.Body.Bytes(), prefix) {
			t.Fatalf("boundary=%#v body=%q", boundary, writer.Body.Bytes())
		}
		close(releaseTail)
		milestone := awaitAnalyzerSemantic(t, boundary.Forwarding)
		completion := awaitAnalyzerCompletion(t, boundary.Forwarding)
		if !milestone.Matched || milestone.State != StateForwarding || milestone.Observation.Fields == nil || milestone.Observation.Fields.Message != "RETRY" {
			t.Fatalf("milestone=%#v", milestone)
		}
		if !bytes.Equal(writer.Body.Bytes(), full) || completion.State != StateForwarding || budget.Used() != 0 {
			t.Fatalf("body=%q completion=%#v budget=%d", writer.Body.Bytes(), completion, budget.Used())
		}
	})
}

func TestE2EFlagshipThirdSSEErrorAt72KiBIsAbsorbed(t *testing.T) {
	const thirdEventBytes = 72 * 1024
	prefix := "event: error\r\ndata: {\"type\":\"error\",\"padding\":\""
	suffix := "\",\"code\":\"server_is_overloaded\",\"message\":\"Our servers are currently overloaded at capacity\",\"param\":\"capacity\"}\r\n\r\n"
	third := prefix + strings.Repeat("x", thirdEventBytes-len(prefix)-len(suffix)) + suffix
	wire := []byte(
		"event: response.created\r\ndata: {\"type\":\"response.created\"}\r\n\r\n" +
			"event: response.in_progress\r\ndata: {\"type\":\"response.in_progress\"}\r\n\r\n" +
			third,
	)
	budget := newRuntimeBudget(t, ResponseProbeMemoryBudget)
	analyzer := newRuntimeAnalyzer(t, budget, AnalyzerOptions{})
	body := newAnalyzerSplitBody(wire, len(wire)/2)
	writer := httptest.NewRecorder()
	response := analyzer.Start(context.Background(), StartInput{
		OperationID: "flagship", Mode: ProbeMode(), APIType: "codex", ContentType: "text/event-stream",
		StatusCode: http.StatusOK, Body: body, Writer: writer,
		Match: func(fields SemanticFields) bool {
			return strings.EqualFold(fields.Code, "server_is_overloaded") && strings.Contains(strings.ToLower(fields.Message), "overloaded at capacity")
		},
	})
	boundary := awaitAnalyzerBoundary(t, response)
	if boundary.Reason != BoundarySemanticMatch || !boundary.HasObservation || boundary.Observation.Fields == nil {
		completion := Completion{}
		if boundary.Forwarding != nil {
			completion = awaitAnalyzerCompletion(t, boundary.Forwarding)
		}
		t.Fatalf("state=%d reason=%q has_observation=%t fields=%#v client_bytes=%d budget_used=%d peak_request=%d peak_process=%d analysis_failure=%q termination=%q", boundary.State, boundary.Reason, boundary.HasObservation, boundary.Observation.Fields, writer.Body.Len(), budget.Used(), completion.PeakRequestBytes, completion.PeakProcessBytes, completion.AnalysisFailure, completion.Termination)
	}
	if boundary.Observation.Fields.Type != "error" || boundary.Observation.Fields.Message != "Our servers are currently overloaded at capacity" {
		t.Fatalf("fields=%#v", boundary.Observation.Fields)
	}
	if writer.Body.Len() != 0 {
		t.Fatalf("client received %d bytes before retry decision", writer.Body.Len())
	}
	if _, err := response.Discard(TransitionSemanticDecision); err != nil {
		t.Fatal(err)
	}
	if writer.Body.Len() != 0 || body.closeCount.Load() != 1 || budget.Used() != 0 {
		t.Fatalf("body_bytes=%d closes=%d budget=%d", writer.Body.Len(), body.closeCount.Load(), budget.Used())
	}
}

func TestGzipDecoderProcessDenialFailsOpenAndReleasesExactly(t *testing.T) {
	decoded := []byte("event: error\ndata: {\"type\":\"error\",\"code\":\"busy\",\"message\":\"retry\"}\n\n")
	wire := gzipRuntimeBytes(t, decoded)
	processLimit := PumpReadBufferBytes + framing.GzipDecoderWorkingMemoryBytes - 1
	budget := newRuntimeBudget(t, processLimit)
	analyzer := newRuntimeAnalyzer(t, budget, AnalyzerOptions{ProbeDuration: time.Hour})
	body := newAnalyzerSplitBody(wire, len(wire)/2)
	writer := httptest.NewRecorder()
	response := analyzer.Start(context.Background(), StartInput{
		Mode: ProbeMode(), APIType: "codex", ContentType: "text/event-stream", ContentEncoding: "gzip",
		StatusCode: http.StatusOK, Header: http.Header{"Content-Encoding": {"gzip"}}, Body: body, Writer: writer,
		Match: func(SemanticFields) bool { return true },
	})
	boundary := awaitAnalyzerBoundary(t, response)
	completion := awaitAnalyzerCompletion(t, boundary.Forwarding)
	if boundary.Reason != BoundaryReason(FailureProcessMemoryExhausted) || completion.AnalysisFailure != BoundaryReason(FailureProcessMemoryExhausted) {
		t.Fatalf("boundary=%#v completion=%#v", boundary, completion)
	}
	if !bytes.Equal(writer.Body.Bytes(), wire) || writer.Header().Get("Content-Encoding") != "gzip" || budget.Used() != 0 {
		t.Fatalf("body=%x want=%x header=%#v budget=%d", writer.Body.Bytes(), wire, writer.Header(), budget.Used())
	}
}

func TestProbeBudgetCompressionExpansionIsBoundedAndRawExact(t *testing.T) {
	decoded := []byte("data: " + strings.Repeat("x", MaxDecodedEventBytes+1) + "\n\n")
	wire := gzipRuntimeBytes(t, decoded)
	tests := []struct {
		name        string
		memoryLimit int
		wantReason  BoundaryReason
	}{
		{
			name:       "production request ceiling fails open first",
			wantReason: BoundaryReason(FailureRequestMemoryExhausted),
		},
		{
			name:        "controlled maximum reaches decoded event cap",
			memoryLimit: MaxProbeMemoryLimit,
			wantReason:  BoundaryReason(FailureDecodedEventTooLarge),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			budget := newRuntimeBudget(t, ResponseProbeMemoryBudget)
			analyzer := newRuntimeAnalyzer(t, budget, AnalyzerOptions{ProbeDuration: time.Hour, ProbeMemoryLimit: test.memoryLimit})
			body := newAnalyzerSplitBody(wire, len(wire)/2)
			writer := httptest.NewRecorder()
			response := analyzer.Start(context.Background(), StartInput{
				Mode: ProbeMode(), APIType: "codex", ContentType: "text/event-stream", ContentEncoding: "gzip",
				StatusCode: http.StatusOK, Header: http.Header{"Content-Encoding": {"gzip"}},
				Body: body, Writer: writer, Match: func(SemanticFields) bool { return true },
			})
			boundary := awaitAnalyzerBoundary(t, response)
			completion := awaitAnalyzerCompletion(t, boundary.Forwarding)
			if boundary.Reason != test.wantReason || completion.AnalysisFailure != test.wantReason {
				t.Fatalf("boundary=%#v completion_reason=%q", boundary, completion.AnalysisFailure)
			}
			if !bytes.Equal(writer.Body.Bytes(), wire) || completion.ClientBodyBytesWritten != int64(len(wire)) || budget.Used() != 0 {
				t.Fatalf("client_bytes=%d wire_bytes=%d completion=%#v budget=%d", writer.Body.Len(), len(wire), completion, budget.Used())
			}
		})
	}
}
