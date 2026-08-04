package pending

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
)

const (
	testWaitTimeout  = 3 * time.Second
	testProbeTimeout = time.Minute
	testReadBuffer   = 32 * 1024
)

var errTestBodyClosed = errors.New("test body closed")

type testObservation struct {
	Kind    ObservationKind
	Reason  BoundaryReason
	ID      string
	Values  []string
	release *releaseCounter
}

type releaseCounter struct {
	released atomic.Bool
	count    atomic.Int32
}

func (r *releaseCounter) releaseOnce() {
	if r != nil && r.released.CompareAndSwap(false, true) {
		r.count.Add(1)
	}
}

func testObservationOps() ObservationOps[testObservation] {
	return ObservationOps[testObservation]{
		Inspect: func(observation testObservation) ObservationKind {
			return observation.Kind
		},
		FailureReason: func(observation testObservation) BoundaryReason {
			return observation.Reason
		},
		Clone: func(observation testObservation) testObservation {
			clone := observation
			clone.Values = append([]string(nil), observation.Values...)
			// Cached API values deliberately carry no release capability.
			clone.release = nil
			return clone
		},
		Release: func(observation *testObservation) {
			if observation == nil {
				return
			}
			observation.release.releaseOnce()
			*observation = testObservation{}
		},
	}
}

func testFailureReason(err error) BoundaryReason {
	if reason, ok := allocation.DenialReasonOf(err); ok {
		switch reason {
		case allocation.DenialRequestMemoryExhausted:
			return ReasonRequestMemoryExhausted
		case allocation.DenialProcessMemoryExhausted:
			return ReasonProcessMemoryExhausted
		}
	}
	return ReasonAnalysisInternal
}

type manualTimer struct {
	delay    time.Duration
	callback func()
	stopped  atomic.Bool
}

func (t *manualTimer) Stop() bool {
	return !t.stopped.Swap(true)
}

type manualScheduler struct {
	mu      sync.Mutex
	timers  []*manualTimer
	created chan int
}

func newManualScheduler() *manualScheduler {
	return &manualScheduler{created: make(chan int, 128)}
}

func (s *manualScheduler) AfterFunc(delay time.Duration, callback func()) Timer {
	timer := &manualTimer{delay: delay, callback: callback}
	s.mu.Lock()
	index := len(s.timers)
	s.timers = append(s.timers, timer)
	s.mu.Unlock()
	s.created <- index
	return timer
}

// fire invokes even stopped timers because real callbacks may already be in
// flight when Stop wins; generation checks, not Timer.Stop, provide safety.
func (s *manualScheduler) fire(t *testing.T, index int) {
	t.Helper()
	timer := s.waitFor(t, index)
	timer.callback()
}

func (s *manualScheduler) waitFor(t *testing.T, index int) *manualTimer {
	t.Helper()
	for {
		s.mu.Lock()
		if index < len(s.timers) {
			timer := s.timers[index]
			s.mu.Unlock()
			return timer
		}
		s.mu.Unlock()
		select {
		case <-s.created:
		case <-time.After(testWaitTimeout):
			t.Fatalf("timer %d was not created", index)
		}
	}
}

func (s *manualScheduler) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.timers)
}

type readStep struct {
	data    []byte
	err     error
	started chan struct{}
	release <-chan struct{}
	onRead  func()
}

func immediateStep(data string, err error) readStep {
	return readStep{data: []byte(data), err: err, started: make(chan struct{})}
}

type steppedBody struct {
	mu             sync.Mutex
	steps          []readStep
	next           int
	closed         chan struct{}
	closeOnce      sync.Once
	closeCount     atomic.Int32
	activeRead     atomic.Bool
	concurrentRead atomic.Bool
}

type splitBody struct {
	reader          *bytes.Reader
	maxRead         int
	closeCount      atomic.Int32
	activeRead      atomic.Bool
	concurrentReads atomic.Bool
}

func newSplitBody(body []byte, maxRead int) *splitBody {
	return &splitBody{reader: bytes.NewReader(body), maxRead: maxRead}
}

func (b *splitBody) Read(target []byte) (int, error) {
	if !b.activeRead.CompareAndSwap(false, true) {
		b.concurrentReads.Store(true)
		return 0, errors.New("concurrent split-body read")
	}
	defer b.activeRead.Store(false)
	if b.maxRead > 0 && len(target) > b.maxRead {
		target = target[:b.maxRead]
	}
	return b.reader.Read(target)
}

func (b *splitBody) Close() error {
	b.closeCount.Add(1)
	return nil
}

func newSteppedBody(steps ...readStep) *steppedBody {
	return &steppedBody{steps: steps, closed: make(chan struct{})}
}

func (b *steppedBody) Read(target []byte) (int, error) {
	if !b.activeRead.CompareAndSwap(false, true) {
		b.concurrentRead.Store(true)
		return 0, errors.New("concurrent body read")
	}
	defer b.activeRead.Store(false)

	b.mu.Lock()
	if b.next >= len(b.steps) {
		b.mu.Unlock()
		return 0, io.EOF
	}
	step := &b.steps[b.next]
	b.next++
	b.mu.Unlock()
	if step.started != nil {
		close(step.started)
	}
	if step.release != nil {
		select {
		case <-step.release:
		case <-b.closed:
			return 0, errTestBodyClosed
		}
	}
	select {
	case <-b.closed:
		return 0, errTestBodyClosed
	default:
	}
	if step.onRead != nil {
		step.onRead()
	}
	if len(step.data) > len(target) {
		return 0, errors.New("test step exceeds read buffer")
	}
	return copy(target, step.data), step.err
}

func (b *steppedBody) Close() error {
	b.closeCount.Add(1)
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

type writeOutcome struct {
	n   int
	err error
}

type recordingWriter struct {
	mu              sync.Mutex
	header          http.Header
	statuses        []int
	body            bytes.Buffer
	outcomes        []writeOutcome
	nextOutcome     int
	writeStarted    chan struct{}
	writeStartedOne sync.Once
	writeRelease    <-chan struct{}
	flushes         int
	activeWrite     atomic.Bool
	concurrentWrite atomic.Bool
}

func newRecordingWriter() *recordingWriter {
	return &recordingWriter{header: make(http.Header), writeStarted: make(chan struct{})}
}

func (w *recordingWriter) Header() http.Header {
	return w.header
}

func (w *recordingWriter) WriteHeader(statusCode int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.statuses = append(w.statuses, statusCode)
}

func (w *recordingWriter) Write(body []byte) (int, error) {
	if !w.activeWrite.CompareAndSwap(false, true) {
		w.concurrentWrite.Store(true)
		return 0, errors.New("concurrent client write")
	}
	defer w.activeWrite.Store(false)
	w.writeStartedOne.Do(func() { close(w.writeStarted) })
	if w.writeRelease != nil {
		<-w.writeRelease
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	outcome := writeOutcome{n: len(body)}
	if w.nextOutcome < len(w.outcomes) {
		outcome = w.outcomes[w.nextOutcome]
		w.nextOutcome++
	}
	if outcome.n >= 0 && outcome.n <= len(body) {
		_, _ = w.body.Write(body[:outcome.n])
	}
	return outcome.n, outcome.err
}

func (w *recordingWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushes++
}

type writerSnapshot struct {
	header   http.Header
	statuses []int
	body     []byte
	flushes  int
}

type traceRecorder struct {
	mu     sync.Mutex
	events []TraceEvent
}

func (r *traceRecorder) Trace(event TraceEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *traceRecorder) snapshot() []TraceEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]TraceEvent(nil), r.events...)
}

func (w *recordingWriter) snapshot() writerSnapshot {
	w.mu.Lock()
	defer w.mu.Unlock()
	return writerSnapshot{
		header:   cloneHeader(w.header),
		statuses: append([]int(nil), w.statuses...),
		body:     append([]byte(nil), w.body.Bytes()...),
		flushes:  w.flushes,
	}
}

type nonFlushingWriter struct {
	inner *recordingWriter
}

func (w nonFlushingWriter) Header() http.Header            { return w.inner.Header() }
func (w nonFlushingWriter) WriteHeader(statusCode int)     { w.inner.WriteHeader(statusCode) }
func (w nonFlushingWriter) Write(body []byte) (int, error) { return w.inner.Write(body) }

type scriptedDriver struct {
	source      io.Reader
	byRead      map[int][]testObservation
	readCount   int
	closedCount *atomic.Int32
}

type forcedErrorDriver struct {
	source io.Reader
	err    error
}

func (d *forcedErrorDriver) Read(decoded []byte, _ func(testObservation) bool) (int, error) {
	n, err := d.source.Read(decoded)
	if n > 0 {
		return n, d.err
	}
	return n, err
}

func (*forcedErrorDriver) Close() error { return nil }

func (d *scriptedDriver) Read(decoded []byte, emit func(testObservation) bool) (int, error) {
	n, err := d.source.Read(decoded)
	d.readCount++
	for _, observation := range d.byRead[d.readCount] {
		if !emit(observation) {
			return n, ErrAnalysisStopped
		}
	}
	return n, err
}

func (d *scriptedDriver) Close() error {
	if d.closedCount != nil {
		d.closedCount.Add(1)
	}
	return nil
}

func scriptedFactory(byRead map[int][]testObservation, closed *atomic.Int32) DriverFactory[testObservation] {
	return func(source io.Reader, _ allocation.Reserver) (Driver[testObservation], error) {
		return &scriptedDriver{source: source, byRead: byRead, closedCount: closed}, nil
	}
}

func newTestConfig(t *testing.T, scheduler Scheduler) (Config[testObservation], *ProcessBudget) {
	t.Helper()
	budget, err := NewProcessBudget(4 * 1024 * 1024)
	if err != nil {
		t.Fatal(err)
	}
	return Config[testObservation]{
		ProcessBudget:            budget,
		Scheduler:                scheduler,
		ProbeDuration:            testProbeTimeout,
		RequestMemoryLimit:       256 * 1024,
		DecodedBufferBytes:       testReadBuffer,
		ObservationQueueCapacity: 4,
		CommandQueueCapacity:     1,
		Observations:             testObservationOps(),
		FailureReason:            testFailureReason,
	}, budget
}

func awaitBoundary(t *testing.T, response *Response[testObservation]) Boundary[testObservation] {
	t.Helper()
	result := make(chan Boundary[testObservation], 1)
	go func() { result <- response.AwaitBoundary() }()
	select {
	case boundary := <-result:
		return boundary
	case <-time.After(testWaitTimeout):
		t.Fatal("timed out awaiting response boundary")
		return Boundary[testObservation]{}
	}
}

func awaitCompletion(t *testing.T, forwarding *ForwardingResponse[testObservation]) Completion[testObservation] {
	t.Helper()
	result := make(chan Completion[testObservation], 1)
	go func() { result <- forwarding.Wait() }()
	select {
	case completion := <-result:
		return completion
	case <-time.After(testWaitTimeout):
		t.Fatal("timed out awaiting response completion")
		return Completion[testObservation]{}
	}
}

func awaitSemantic(t *testing.T, forwarding *ForwardingResponse[testObservation]) SemanticMilestone[testObservation] {
	t.Helper()
	result := make(chan SemanticMilestone[testObservation], 1)
	go func() { result <- forwarding.AwaitSemanticOrCompletion() }()
	select {
	case milestone := <-result:
		return milestone
	case <-time.After(testWaitTimeout):
		t.Fatal("timed out awaiting semantic milestone")
		return SemanticMilestone[testObservation]{}
	}
}

func awaitResponseCompletion(t *testing.T, response *Response[testObservation]) Completion[testObservation] {
	t.Helper()
	result := make(chan Completion[testObservation], 1)
	go func() {
		completion := response.shared.completion.wait()
		result <- response.shared.cloneCompletion(completion)
	}()
	select {
	case completion := <-result:
		return completion
	case <-time.After(testWaitTimeout):
		t.Fatal("timed out awaiting discarded response completion")
		return Completion[testObservation]{}
	}
}

func waitForSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(testWaitTimeout):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func assertBudgetReleased(t *testing.T, budget *ProcessBudget) {
	t.Helper()
	if got := budget.Used(); got != 0 {
		t.Fatalf("process budget still owns %d bytes", got)
	}
	budget.mu.Lock()
	accounts := len(budget.accounts)
	budget.mu.Unlock()
	if accounts != 0 {
		t.Fatalf("process budget still owns %d request accounts", accounts)
	}
}

func TestHoldCommitAndDiscardOwnTheBodyExactlyOnce(t *testing.T) {
	t.Run("commit", func(t *testing.T) {
		scheduler := newManualScheduler()
		config, budget := newTestConfig(t, scheduler)
		body := newSteppedBody(immediateStep("exact body", io.EOF))
		writer := newRecordingWriter()
		inputHeader := http.Header{"X-Repeat": {"one", "two"}}
		response := Start(context.Background(), config, StartInput[testObservation]{
			Mode:       HoldMode(),
			StatusCode: http.StatusAccepted,
			Header:     inputHeader,
			Body:       body,
			Writer:     writer,
		})
		inputHeader.Set("X-Repeat", "mutated after start")
		if body.next != 0 || scheduler.count() != 0 {
			t.Fatal("hold mode read or armed a timer before resolution")
		}
		forwarding, err := response.Commit(TransitionExecutorDecision)
		if err != nil {
			t.Fatal(err)
		}
		completion := awaitCompletion(t, forwarding)
		snapshot := writer.snapshot()
		if len(snapshot.statuses) != 1 || snapshot.statuses[0] != http.StatusAccepted || string(snapshot.body) != "exact body" {
			t.Fatalf("writer transcript = %#v", snapshot)
		}
		if got := snapshot.header.Values("X-Repeat"); len(got) != 2 || got[0] != "one" || got[1] != "two" {
			t.Fatalf("repeated headers = %#v", got)
		}
		if completion.State != StateForwarding || completion.Termination != TerminationCompleted || body.closeCount.Load() != 1 {
			t.Fatalf("completion=%#v close_count=%d", completion, body.closeCount.Load())
		}
		if _, err := response.Commit(TransitionExecutorDecision); !isAlreadyResolved(err, StateForwarding) {
			t.Fatalf("duplicate commit error = %v", err)
		}
		assertBudgetReleased(t, budget)
	})

	t.Run("discard", func(t *testing.T) {
		config, budget := newTestConfig(t, newManualScheduler())
		body := newSteppedBody(immediateStep("must not be read", io.EOF))
		writer := newRecordingWriter()
		response := Start(context.Background(), config, StartInput[testObservation]{
			Mode: HoldMode(), StatusCode: http.StatusOK, Body: body, Writer: writer,
		})
		receipt, err := response.Discard(TransitionExecutorDecision)
		if err != nil {
			t.Fatal(err)
		}
		if body.next != 0 || body.closeCount.Load() != 1 || !receipt.BodyClosed {
			t.Fatalf("body next=%d closes=%d receipt=%#v", body.next, body.closeCount.Load(), receipt)
		}
		snapshot := writer.snapshot()
		if len(snapshot.statuses) != 0 || len(snapshot.body) != 0 || snapshot.flushes != 0 {
			t.Fatalf("discard wrote to client: %#v", snapshot)
		}
		if _, err := response.Commit(TransitionExecutorDecision); !isAlreadyResolved(err, StateDiscarded) {
			t.Fatalf("post-discard commit error = %v", err)
		}
		assertBudgetReleased(t, budget)
	})
}

func TestSemanticDiscardReceiptCarriesFinalizedCoordinatorFacts(t *testing.T) {
	config, budget := newTestConfig(t, newManualScheduler())
	releaseTail := make(chan struct{})
	body := newSteppedBody(
		immediateStep("match", nil),
		readStep{data: []byte("unread tail"), err: io.EOF, started: make(chan struct{}), release: releaseTail},
	)
	response := Start(context.Background(), config, StartInput[testObservation]{
		OperationID: "semantic-discard-facts", Mode: ProbeMode(), StatusCode: http.StatusOK,
		Body: body, Writer: newRecordingWriter(),
		NewDriver: scriptedFactory(map[int][]testObservation{
			1: {{Kind: ObservationSemanticMatch, ID: "semantic"}},
		}, nil),
	})
	boundary := awaitBoundary(t, response)
	if boundary.State != StateProbing || boundary.Reason != ReasonSemanticMatch {
		t.Fatalf("boundary = %#v", boundary)
	}
	receipt, err := response.Discard(TransitionSemanticDecision)
	if err != nil {
		t.Fatalf("Discard() error = %v", err)
	}
	completion := awaitResponseCompletion(t, response)
	if receipt.UpstreamBytesRead == 0 || receipt.DecodedBytesAnalyzed == 0 || receipt.PeakRequestBytes == 0 || receipt.PeakProcessBytes == 0 {
		t.Fatalf("receipt lost observed metrics: %#v", receipt)
	}
	if receipt.ClientBodyBytesWritten != 0 || receipt.HeadersCommitted {
		t.Fatalf("discard claimed client visibility: %#v", receipt)
	}
	if receipt.Cause != TransitionSemanticDecision || receipt.BoundaryReason != ReasonSemanticMatch || !receipt.BodyClosed {
		t.Fatalf("receipt identity/finalization = %#v", receipt)
	}
	if receipt.UpstreamBytesRead != completion.UpstreamBytesRead ||
		receipt.DecodedBytesAnalyzed != completion.DecodedBytesAnalyzed ||
		receipt.ClientBodyBytesWritten != completion.ClientBodyBytesWritten ||
		receipt.PeakRequestBytes != completion.PeakRequestBytes ||
		receipt.PeakProcessBytes != completion.PeakProcessBytes ||
		receipt.HeadersCommitted != completion.HeadersCommitted ||
		receipt.BodyClosed != completion.BodyClosed ||
		receipt.BoundaryReason != completion.BoundaryReason ||
		receipt.AnalysisFailure != completion.AnalysisFailure {
		t.Fatalf("receipt %#v does not reproduce completion %#v", receipt, completion)
	}
	close(releaseTail)
	assertBudgetReleased(t, budget)
}

func isAlreadyResolved(err error, state ResolutionState) bool {
	var resolved *AlreadyResolved
	return errors.As(err, &resolved) && resolved.State == state
}
