package pending

import (
	"context"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
)

func TestResolutionCommitDiscardRace1000(t *testing.T) {
	config, budget := newTestConfig(t, newManualScheduler())
	type result struct {
		kind       commandKind
		forwarding *ForwardingResponse[testObservation]
		receipt    DiscardReceipt
		err        error
	}

	for iteration := 0; iteration < 1000; iteration++ {
		body := newSteppedBody(immediateStep("race body", io.EOF))
		writer := newRecordingWriter()
		response := Start(context.Background(), config, StartInput[testObservation]{
			Mode: HoldMode(), StatusCode: http.StatusOK, Header: http.Header{"X-Iteration": {"one", "two"}},
			Body: body, Writer: writer, Flush: true,
		})
		start := make(chan struct{})
		results := make(chan result, 2)
		go func() {
			<-start
			forwarding, err := response.Commit(TransitionExecutorDecision)
			results <- result{kind: commandCommit, forwarding: forwarding, err: err}
		}()
		go func() {
			<-start
			receipt, err := response.Discard(TransitionExecutorDecision)
			results <- result{kind: commandDiscard, receipt: receipt, err: err}
		}()
		close(start)

		var first, second result
		select {
		case first = <-results:
		case <-time.After(testWaitTimeout):
			t.Fatalf("iteration %d: first transition timed out", iteration)
		}
		select {
		case second = <-results:
		case <-time.After(testWaitTimeout):
			t.Fatalf("iteration %d: second transition timed out", iteration)
		}
		winner, loser := first, second
		if winner.err != nil {
			winner, loser = loser, winner
		}
		if winner.err != nil || loser.err == nil {
			t.Fatalf("iteration %d: results = %#v / %#v", iteration, first, second)
		}

		var completion Completion[testObservation]
		switch winner.kind {
		case commandCommit:
			if winner.forwarding == nil || !isAlreadyResolved(loser.err, StateForwarding) {
				t.Fatalf("iteration %d: commit winner=%#v loser=%v", iteration, winner, loser.err)
			}
			completion = awaitCompletion(t, winner.forwarding)
		case commandDiscard:
			if !winner.receipt.BodyClosed || !isAlreadyResolved(loser.err, StateDiscarded) {
				t.Fatalf("iteration %d: discard winner=%#v loser=%v", iteration, winner, loser.err)
			}
			completion = response.shared.cloneCompletion(response.shared.completion.wait())
		default:
			t.Fatalf("iteration %d: unknown winner %#v", iteration, winner)
		}

		snapshot := writer.snapshot()
		if len(snapshot.statuses) > 1 || body.closeCount.Load() != 1 || body.concurrentRead.Load() || writer.concurrentWrite.Load() {
			t.Fatalf("iteration %d: writer=%#v closes=%d concurrent_read=%t concurrent_write=%t", iteration, snapshot, body.closeCount.Load(), body.concurrentRead.Load(), writer.concurrentWrite.Load())
		}
		if completion.State == StateDiscarded {
			if len(snapshot.statuses) != 0 || len(snapshot.body) != 0 || snapshot.flushes != 0 || body.next != 0 {
				t.Fatalf("iteration %d: discard leaked client/body activity: %#v next=%d", iteration, snapshot, body.next)
			}
		} else if len(snapshot.statuses) != 1 || string(snapshot.body) != "race body" || snapshot.flushes != 1 {
			t.Fatalf("iteration %d: commit transcript = %#v", iteration, snapshot)
		}
		assertBudgetReleased(t, budget)
	}
}

func TestDiscardClosesBlockedReadAndJoins(t *testing.T) {
	scheduler := newManualScheduler()
	config, budget := newTestConfig(t, scheduler)
	readStarted := make(chan struct{})
	neverRelease := make(chan struct{})
	body := newSteppedBody(readStep{started: readStarted, release: neverRelease})
	writer := newRecordingWriter()
	var driverClosed atomic.Int32
	response := Start(context.Background(), config, StartInput[testObservation]{
		Mode: ProbeMode(), StatusCode: http.StatusOK, Body: body, Writer: writer,
		NewDriver: scriptedFactory(nil, &driverClosed),
	})
	waitForSignal(t, readStarted, "blocked upstream read")

	type discardResult struct {
		receipt DiscardReceipt
		err     error
	}
	discarded := make(chan discardResult, 1)
	go func() {
		receipt, err := response.Discard(TransitionSemanticDecision)
		discarded <- discardResult{receipt: receipt, err: err}
	}()
	var result discardResult
	select {
	case result = <-discarded:
	case <-time.After(testWaitTimeout):
		t.Fatal("discard did not close the body and join the blocked pump")
	}
	if result.err != nil || !result.receipt.BodyClosed || body.activeRead.Load() || driverClosed.Load() != 1 {
		t.Fatalf("discard=%#v active_read=%t driver_closes=%d", result, body.activeRead.Load(), driverClosed.Load())
	}
	boundary := awaitBoundary(t, response)
	if boundary.State != StateDiscarded || body.closeCount.Load() != 1 || body.concurrentRead.Load() {
		t.Fatalf("boundary=%#v closes=%d concurrent_read=%t", boundary, body.closeCount.Load(), body.concurrentRead.Load())
	}
	snapshot := writer.snapshot()
	if len(snapshot.statuses) != 0 || len(snapshot.body) != 0 || snapshot.flushes != 0 {
		t.Fatalf("discard wrote to client: %#v", snapshot)
	}
	assertBudgetReleased(t, budget)
}

func TestDiscardWinningWhileSemanticEventIsPendingStopsPump(t *testing.T) {
	scheduler := newManualScheduler()
	config, budget := newTestConfig(t, scheduler)
	inspectStarted := make(chan struct{})
	releaseInspect := make(chan struct{})
	inspect := config.Observations.Inspect
	config.Observations.Inspect = func(observation testObservation) ObservationKind {
		close(inspectStarted)
		<-releaseInspect
		return inspect(observation)
	}

	body := newSteppedBody(immediateStep("match", nil))
	writer := newRecordingWriter()
	released := &releaseCounter{}
	var driver *scriptedDriver
	response := Start(context.Background(), config, StartInput[testObservation]{
		Mode: ProbeMode(), StatusCode: http.StatusOK, Body: body, Writer: writer,
		NewDriver: func(source io.Reader, _ allocation.Reserver) (Driver[testObservation], error) {
			driver = &scriptedDriver{
				source: source,
				byRead: map[int][]testObservation{
					1: {{Kind: ObservationSemanticMatch, release: released}},
				},
			}
			return driver, nil
		},
	})
	waitForSignal(t, inspectStarted, "pending semantic event")

	type result struct {
		receipt DiscardReceipt
		err     error
	}
	discarded := make(chan result, 1)
	go func() {
		receipt, err := response.Discard(TransitionSemanticDecision)
		discarded <- result{receipt: receipt, err: err}
	}()
	boundary := awaitBoundary(t, response)
	if boundary.State != StateDiscarded {
		t.Fatalf("boundary=%#v", boundary)
	}
	close(releaseInspect)

	var discardedResult result
	select {
	case discardedResult = <-discarded:
	case <-time.After(testWaitTimeout):
		t.Fatal("discard did not stop the pending semantic handoff")
	}
	completion := awaitResponseCompletion(t, response)
	if discardedResult.err != nil || !discardedResult.receipt.BodyClosed || driver.readCount != 1 {
		t.Fatalf("discard=%#v driver_reads=%d", discardedResult, driver.readCount)
	}
	if completion.State != StateDiscarded || completion.Termination != TerminationDiscarded || released.count.Load() != 1 {
		t.Fatalf("completion=%#v observation_releases=%d", completion, released.count.Load())
	}
	if got := writer.snapshot(); len(got.statuses) != 0 || len(got.body) != 0 {
		t.Fatalf("discard wrote client bytes: %#v", got)
	}
	if body.closeCount.Load() != 1 || body.concurrentRead.Load() {
		t.Fatalf("closes=%d concurrent_read=%t", body.closeCount.Load(), body.concurrentRead.Load())
	}
	assertBudgetReleased(t, budget)
}

func TestDiscardWinningWhileFailOpenEventIsPendingStopsPump(t *testing.T) {
	config, budget := newTestConfig(t, newManualScheduler())
	inspectStarted := make(chan struct{})
	releaseInspect := make(chan struct{})
	inspect := config.Observations.Inspect
	config.Observations.Inspect = func(observation testObservation) ObservationKind {
		close(inspectStarted)
		<-releaseInspect
		return inspect(observation)
	}
	secondRead := make(chan struct{})
	body := newSteppedBody(
		immediateStep("failure", nil),
		readStep{data: []byte("must not read"), err: io.EOF, started: secondRead},
	)
	writer := newRecordingWriter()
	released := &releaseCounter{}
	response := Start(context.Background(), config, StartInput[testObservation]{
		Mode: ProbeMode(), StatusCode: http.StatusOK, Body: body, Writer: writer,
		NewDriver: scriptedFactory(map[int][]testObservation{
			1: {{Kind: ObservationFailOpen, Reason: ReasonAnalysisInternal, release: released}},
		}, nil),
	})
	waitForSignal(t, inspectStarted, "pending fail-open event")

	type result struct {
		receipt DiscardReceipt
		err     error
	}
	discarded := make(chan result, 1)
	go func() {
		receipt, err := response.Discard(TransitionSemanticDecision)
		discarded <- result{receipt: receipt, err: err}
	}()
	if boundary := awaitBoundary(t, response); boundary.State != StateDiscarded {
		t.Fatalf("boundary=%#v", boundary)
	}
	close(releaseInspect)

	var discardedResult result
	select {
	case discardedResult = <-discarded:
	case <-time.After(testWaitTimeout):
		t.Fatal("discard did not stop the pending fail-open handoff")
	}
	select {
	case <-secondRead:
		t.Fatal("pump read again after discard won")
	default:
	}
	completion := awaitResponseCompletion(t, response)
	if discardedResult.err != nil || !discardedResult.receipt.BodyClosed || completion.State != StateDiscarded || completion.Termination != TerminationDiscarded {
		t.Fatalf("discard=%#v completion=%#v", discardedResult, completion)
	}
	if released.count.Load() != 1 || body.closeCount.Load() != 1 {
		t.Fatalf("observation_releases=%d closes=%d", released.count.Load(), body.closeCount.Load())
	}
	if got := writer.snapshot(); len(got.statuses) != 0 || len(got.body) != 0 {
		t.Fatalf("discard wrote client bytes: %#v", got)
	}
	assertBudgetReleased(t, budget)
}

func TestCoordinatorProbeTimerWhileReadBlocked(t *testing.T) {
	scheduler := newManualScheduler()
	config, budget := newTestConfig(t, scheduler)
	releaseRead := make(chan struct{})
	step := readStep{data: []byte("ordered tail"), err: io.EOF, started: make(chan struct{}), release: releaseRead}
	body := newSteppedBody(step)
	writer := newRecordingWriter()
	var driverClosed atomic.Int32
	response := Start(context.Background(), config, StartInput[testObservation]{
		Mode: ProbeMode(), StatusCode: http.StatusCreated,
		Header: http.Header{"X-Repeat": {"one", "two"}}, Body: body, Writer: writer,
		NewDriver: scriptedFactory(nil, &driverClosed),
	})
	waitForSignal(t, step.started, "blocked upstream read")
	probeTimer := scheduler.waitFor(t, 0)
	if probeTimer.delay != testProbeTimeout {
		t.Fatalf("probe delay=%v", probeTimer.delay)
	}
	scheduler.fire(t, 0)
	boundary := awaitBoundary(t, response)
	if boundary.State != StateForwarding || boundary.Reason != ReasonProbeDurationElapsed || boundary.Forwarding == nil {
		t.Fatalf("boundary=%#v", boundary)
	}
	if body.closeCount.Load() != 0 || !probeTimer.stopped.Load() {
		t.Fatalf("probe release closed body=%d or left timer active=%t", body.closeCount.Load(), !probeTimer.stopped.Load())
	}
	beforeTail := writer.snapshot()
	if len(beforeTail.statuses) != 1 || len(beforeTail.body) != 0 {
		t.Fatalf("head was not committed before blocked read returned: %#v", beforeTail)
	}

	// A callback already in flight after Stop must not reopen probing or close
	// the body. It is deliberately delivered before the original Read returns.
	scheduler.fire(t, 0)
	if body.closeCount.Load() != 0 {
		t.Fatalf("stale probe callback closed body %d times", body.closeCount.Load())
	}
	close(releaseRead)
	completion := awaitCompletion(t, boundary.Forwarding)
	snapshot := writer.snapshot()
	if string(snapshot.body) != "ordered tail" || len(snapshot.statuses) != 1 || driverClosed.Load() != 1 {
		t.Fatalf("writer=%#v driver_closes=%d", snapshot, driverClosed.Load())
	}
	if completion.Termination != TerminationCompleted || completion.UpstreamBytesRead != int64(len("ordered tail")) || completion.ClientBodyBytesWritten != int64(len("ordered tail")) {
		t.Fatalf("completion=%#v", completion)
	}
	if body.closeCount.Load() != 1 || body.concurrentRead.Load() {
		t.Fatalf("closes=%d concurrent_read=%t", body.closeCount.Load(), body.concurrentRead.Load())
	}
	assertBudgetReleased(t, budget)
}

func TestCoordinatorTimerMailboxPreservesLiveGenerationUnderStaleBurst(t *testing.T) {
	const staleCallbackBurst = 8

	scheduler := newManualScheduler()
	config, budget := newTestConfig(t, scheduler)
	config.IdleDuration = 30 * time.Second
	releaseWrite := make(chan struct{})
	stalled := readStep{started: make(chan struct{}), release: make(chan struct{})}
	body := newSteppedBody(immediateStep("prefix", nil), stalled)
	writer := newRecordingWriter()
	writer.writeRelease = releaseWrite
	response := Start(context.Background(), config, StartInput[testObservation]{
		Mode: ProbeMode(), StatusCode: http.StatusOK, Body: body, Writer: writer,
		NewDriver: scriptedFactory(nil, nil),
	})
	waitForSignal(t, stalled.started, "second stalled upstream read")
	staleIdle := scheduler.waitFor(t, 1)
	liveIdle := scheduler.waitFor(t, 2)
	if !staleIdle.stopped.Load() || liveIdle.stopped.Load() {
		t.Fatalf("stale_idle_stopped=%t live_idle_stopped=%t", staleIdle.stopped.Load(), liveIdle.stopped.Load())
	}

	// The probe callback deliberately holds the coordinator in the client write.
	// Replaying an invalidated idle callback then proves callback pressure cannot
	// occupy the one bounded slot reserved for the live idle generation.
	scheduler.fire(t, 0)
	waitForSignal(t, writer.writeStarted, "blocked probe-release write")
	for range staleCallbackBurst {
		scheduler.fire(t, 1)
	}
	scheduler.fire(t, 2)
	close(releaseWrite)

	boundary := awaitBoundary(t, response)
	completion := awaitCompletion(t, boundary.Forwarding)
	if boundary.State != StateForwarding || boundary.Reason != ReasonProbeDurationElapsed {
		t.Fatalf("boundary=%#v", boundary)
	}
	if completion.Termination != TerminationUpstreamReadFailure || completion.UpstreamBytesRead != int64(len("prefix")) {
		t.Fatalf("completion=%#v", completion)
	}
	if got := writer.snapshot(); string(got.body) != "prefix" || len(got.statuses) != 1 {
		t.Fatalf("writer=%#v", got)
	}
	if body.closeCount.Load() != 1 || body.activeRead.Load() || body.concurrentRead.Load() {
		t.Fatalf("closes=%d active=%t concurrent=%t", body.closeCount.Load(), body.activeRead.Load(), body.concurrentRead.Load())
	}
	assertBudgetReleased(t, budget)
}

func TestPassthroughOnlyZeroProbeAllocationAndStreamsBeforeEOF(t *testing.T) {
	scheduler := newManualScheduler()
	config, budget := newTestConfig(t, scheduler)
	releaseEOF := make(chan struct{})
	first := immediateStep("first event", nil)
	last := readStep{err: io.EOF, started: make(chan struct{}), release: releaseEOF}
	body := newSteppedBody(first, last)
	writer := newRecordingWriter()
	mode, err := ObserveMode(ReasonPassthroughOnly)
	if err != nil {
		t.Fatal(err)
	}
	response := Start(context.Background(), config, StartInput[testObservation]{
		Mode: mode, StatusCode: http.StatusOK, Body: body, Writer: writer, Flush: true,
		NewDriver: scriptedFactory(nil, nil),
	})
	boundary := awaitBoundary(t, response)
	if boundary.State != StateForwarding || boundary.Reason != ReasonPassthroughOnly {
		t.Fatalf("boundary=%#v", boundary)
	}
	waitForSignal(t, writer.writeStarted, "first passthrough client write")
	waitForSignal(t, last.started, "EOF-latched second read")
	if scheduler.count() != 0 {
		t.Fatalf("passthrough armed %d timers", scheduler.count())
	}
	if got := budget.Used(); got != testReadBuffer {
		t.Fatalf("passthrough live budget=%d, want decoded buffer only %d", got, testReadBuffer)
	}
	beforeEOF := writer.snapshot()
	if string(beforeEOF.body) != "first event" || beforeEOF.flushes != 1 {
		t.Fatalf("passthrough did not stream before EOF: %#v", beforeEOF)
	}
	close(releaseEOF)
	completion := awaitCompletion(t, boundary.Forwarding)
	if completion.PeakRequestBytes != testReadBuffer || completion.Termination != TerminationCompleted {
		t.Fatalf("completion=%#v", completion)
	}
	assertBudgetReleased(t, budget)
}

func TestIdleTimerDistinguishesBackpressureFromStalledRead(t *testing.T) {
	scheduler := newManualScheduler()
	config, budget := newTestConfig(t, scheduler)
	config.IdleDuration = 30 * time.Second
	releaseWrite := make(chan struct{})
	first := immediateStep("a", nil)
	stalled := readStep{started: make(chan struct{}), release: make(chan struct{})}
	body := newSteppedBody(first, stalled)
	writer := newRecordingWriter()
	writer.writeRelease = releaseWrite
	mode, err := ObserveMode(ReasonNoRetryCandidate)
	if err != nil {
		t.Fatal(err)
	}
	response := Start(context.Background(), config, StartInput[testObservation]{
		Mode: mode, StatusCode: http.StatusOK, Body: body, Writer: writer,
		NewDriver: scriptedFactory(nil, nil),
	})
	boundary := awaitBoundary(t, response)
	waitForSignal(t, writer.writeStarted, "backpressured client write")
	firstIdle := scheduler.waitFor(t, 0)
	if !firstIdle.stopped.Load() {
		t.Fatal("idle timer remained active while the coordinator was blocked on the client writer")
	}
	scheduler.fire(t, 0)
	if body.closeCount.Load() != 0 {
		t.Fatalf("stale idle callback closed body during client backpressure: %d", body.closeCount.Load())
	}
	close(releaseWrite)
	waitForSignal(t, stalled.started, "second stalled upstream read")
	secondIdle := scheduler.waitFor(t, 1)
	if secondIdle.stopped.Load() {
		t.Fatal("current idle timer stopped before stalled read completed")
	}
	scheduler.fire(t, 1)
	completion := awaitCompletion(t, boundary.Forwarding)
	if completion.Termination != TerminationUpstreamReadFailure || completion.UpstreamBytesRead != 1 || completion.ClientBodyBytesWritten != 1 {
		t.Fatalf("completion=%#v", completion)
	}
	if body.closeCount.Load() != 1 || body.activeRead.Load() || body.concurrentRead.Load() {
		t.Fatalf("closes=%d active=%t concurrent=%t", body.closeCount.Load(), body.activeRead.Load(), body.concurrentRead.Load())
	}
	if got := writer.snapshot(); string(got.body) != "a" || len(got.statuses) != 1 {
		t.Fatalf("writer=%#v", got)
	}
	assertBudgetReleased(t, budget)
}

func TestSemanticBarrierAndCachedResultsAreDeeplyImmutable(t *testing.T) {
	scheduler := newManualScheduler()
	config, budget := newTestConfig(t, scheduler)
	releaseTail := make(chan struct{})
	trailer := make(http.Header)
	first := immediateStep("match", nil)
	last := readStep{
		data: []byte("tail"), err: io.EOF, started: make(chan struct{}), release: releaseTail,
		onRead: func() { trailer.Set("X-Final", "done") },
	}
	body := newSteppedBody(first, last)
	writer := newRecordingWriter()
	released := &releaseCounter{}
	observation := testObservation{
		Kind: ObservationSemanticMatch, ID: "semantic", Values: []string{"original"}, release: released,
	}
	inputHeader := http.Header{"X-Start": {"original"}}
	response := Start(context.Background(), config, StartInput[testObservation]{
		Mode: ProbeMode(), StatusCode: http.StatusOK, Header: inputHeader, Trailer: trailer,
		Body: body, Writer: writer, NewDriver: scriptedFactory(map[int][]testObservation{1: {observation}}, nil),
	})
	inputHeader.Set("X-Start", "mutated")
	boundary := awaitBoundary(t, response)
	if boundary.State != StateProbing || boundary.Reason != ReasonSemanticMatch || !boundary.HasObservation {
		t.Fatalf("boundary=%#v", boundary)
	}
	select {
	case <-last.started:
		t.Fatal("pump began another read before the semantic decision")
	default:
	}
	boundary.Observation.Values[0] = "mutated"
	again := response.AwaitBoundary()
	if again.Observation.Values[0] != "original" {
		t.Fatalf("cached boundary was mutated: %#v", again)
	}

	// Deliver the stopped callback to prove the semantic barrier invalidated
	// its generation before the executor resolves the response.
	scheduler.fire(t, 0)
	forwarding, err := response.Commit(TransitionSemanticDecision)
	if err != nil {
		t.Fatal(err)
	}
	waitForSignal(t, last.started, "post-commit tail read")
	close(releaseTail)
	milestone := awaitSemantic(t, forwarding)
	if !milestone.Matched || milestone.Observation.Values[0] != "original" {
		t.Fatalf("milestone=%#v", milestone)
	}
	milestone.Observation.Values[0] = "mutated"
	if got := forwarding.AwaitSemanticOrCompletion(); got.Observation.Values[0] != "original" {
		t.Fatalf("cached milestone was mutated: %#v", got)
	}
	completion := awaitCompletion(t, forwarding)
	if !completion.HasSemanticObservation || completion.SemanticObservation.Values[0] != "original" ||
		completion.Header.Get("X-Start") != "original" || completion.Trailer.Get("X-Final") != "done" {
		t.Fatalf("completion=%#v", completion)
	}
	completion.SemanticObservation.Values[0] = "mutated"
	completion.Header.Set("X-Start", "mutated")
	completion.Trailer.Set("X-Final", "mutated")
	if got := forwarding.Wait(); got.SemanticObservation.Values[0] != "original" || got.Header.Get("X-Start") != "original" || got.Trailer.Get("X-Final") != "done" {
		t.Fatalf("cached completion was mutated: %#v", got)
	}

	const concurrentWaiters = 32
	results := make(chan Completion[testObservation], concurrentWaiters)
	for range concurrentWaiters {
		go func() {
			value := forwarding.Wait()
			value.SemanticObservation.Values[0] = "private mutation"
			value.Header.Set("X-Start", "private mutation")
			results <- value
		}()
	}
	for range concurrentWaiters {
		select {
		case value := <-results:
			if value.Trailer.Get("X-Final") != "done" {
				t.Fatalf("concurrent cached result=%#v", value)
			}
		case <-time.After(testWaitTimeout):
			t.Fatal("concurrent cached Wait timed out")
		}
	}
	if got := forwarding.Wait(); got.SemanticObservation.Values[0] != "original" || got.Header.Get("X-Start") != "original" {
		t.Fatalf("concurrent callers mutated cache: %#v", got)
	}
	if released.count.Load() != 1 || string(writer.snapshot().body) != "matchtail" {
		t.Fatalf("observation releases=%d writer=%#v", released.count.Load(), writer.snapshot())
	}
	assertBudgetReleased(t, budget)
}

func TestOrderingMatchTimerCancel(t *testing.T) {
	t.Run("match first then cancel", func(t *testing.T) {
		scheduler := newManualScheduler()
		config, budget := newTestConfig(t, scheduler)
		ctx, cancel := context.WithCancel(context.Background())
		body := newSteppedBody(
			immediateStep("match", nil),
			readStep{started: make(chan struct{}), release: make(chan struct{})},
		)
		writer := newRecordingWriter()
		released := &releaseCounter{}
		response := Start(ctx, config, StartInput[testObservation]{
			Mode: ProbeMode(), StatusCode: http.StatusOK, Body: body, Writer: writer,
			NewDriver: scriptedFactory(map[int][]testObservation{1: {{Kind: ObservationSemanticMatch, release: released}}}, nil),
		})
		boundary := awaitBoundary(t, response)
		if boundary.Reason != ReasonSemanticMatch {
			t.Fatalf("boundary=%#v", boundary)
		}
		cancel()
		completion := awaitResponseCompletion(t, response)
		scheduler.fire(t, 0)
		if completion.State != StateDiscarded || completion.Termination != TerminationClientCancelled || released.count.Load() != 1 {
			t.Fatalf("completion=%#v releases=%d", completion, released.count.Load())
		}
		if got := writer.snapshot(); len(got.statuses) != 0 || len(got.body) != 0 {
			t.Fatalf("cancel after match wrote client bytes: %#v", got)
		}
		assertBudgetReleased(t, budget)
	})

	t.Run("timer first then late match then cancel", func(t *testing.T) {
		scheduler := newManualScheduler()
		config, budget := newTestConfig(t, scheduler)
		ctx, cancel := context.WithCancel(context.Background())
		releaseFirst := make(chan struct{})
		first := readStep{data: []byte("match"), started: make(chan struct{}), release: releaseFirst}
		stalled := readStep{started: make(chan struct{}), release: make(chan struct{})}
		body := newSteppedBody(first, stalled)
		writer := newRecordingWriter()
		response := Start(ctx, config, StartInput[testObservation]{
			Mode: ProbeMode(), StatusCode: http.StatusOK, Body: body, Writer: writer,
			NewDriver: scriptedFactory(map[int][]testObservation{1: {{Kind: ObservationSemanticMatch, ID: "late"}}}, nil),
		})
		waitForSignal(t, first.started, "first blocked read")
		scheduler.fire(t, 0)
		boundary := awaitBoundary(t, response)
		if boundary.State != StateForwarding || boundary.Reason != ReasonProbeDurationElapsed {
			t.Fatalf("boundary=%#v", boundary)
		}
		close(releaseFirst)
		milestone := awaitSemantic(t, boundary.Forwarding)
		if !milestone.Matched || milestone.State != StateForwarding || milestone.Observation.ID != "late" {
			t.Fatalf("late milestone=%#v", milestone)
		}
		waitForSignal(t, stalled.started, "post-match stalled read")
		cancel()
		completion := awaitCompletion(t, boundary.Forwarding)
		if completion.State != StateForwarding || completion.Termination != TerminationClientCancelled || !completion.HasSemanticObservation {
			t.Fatalf("completion=%#v", completion)
		}
		if got := writer.snapshot(); string(got.body) != "match" || len(got.statuses) != 1 {
			t.Fatalf("writer=%#v", got)
		}
		assertBudgetReleased(t, budget)
	})

	t.Run("cancel first", func(t *testing.T) {
		scheduler := newManualScheduler()
		config, budget := newTestConfig(t, scheduler)
		ctx, cancel := context.WithCancel(context.Background())
		blocked := readStep{started: make(chan struct{}), release: make(chan struct{})}
		body := newSteppedBody(blocked)
		writer := newRecordingWriter()
		response := Start(ctx, config, StartInput[testObservation]{
			Mode: ProbeMode(), StatusCode: http.StatusOK, Body: body, Writer: writer,
			NewDriver: scriptedFactory(nil, nil),
		})
		waitForSignal(t, blocked.started, "cancelled blocked read")
		cancel()
		boundary := awaitBoundary(t, response)
		completion := awaitResponseCompletion(t, response)
		scheduler.fire(t, 0)
		if boundary.State != StateDiscarded || boundary.Reason != ReasonClientCancelled || completion.Termination != TerminationClientCancelled {
			t.Fatalf("boundary=%#v completion=%#v", boundary, completion)
		}
		if got := writer.snapshot(); len(got.statuses) != 0 || len(got.body) != 0 {
			t.Fatalf("cancel-first wrote client bytes: %#v", got)
		}
		assertBudgetReleased(t, budget)
	})
}
