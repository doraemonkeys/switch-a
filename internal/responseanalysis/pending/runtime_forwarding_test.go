package pending

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
)

func TestExactHeadersBodyAndEOFPopulatedTrailers(t *testing.T) {
	config, budget := newTestConfig(t, newManualScheduler())
	trailer := make(http.Header)
	body := newSteppedBody(
		immediateStep("ab", nil),
		readStep{
			data: []byte("cd"), err: io.EOF, started: make(chan struct{}),
			onRead: func() {
				trailer["X-Final"] = []string{"one", "two"}
			},
		},
	)
	writer := newRecordingWriter()
	writer.outcomes = []writeOutcome{{n: 1}, {n: 1}, {n: 1}, {n: 1}}
	response := Start(context.Background(), config, StartInput[testObservation]{
		Mode: HoldMode(), StatusCode: http.StatusPartialContent,
		Header:  http.Header{"X-Repeat": {"alpha", "beta"}, "Trailer": {"X-Final"}},
		Trailer: trailer, Body: body, Writer: writer, Flush: true,
	})
	forwarding, err := response.Commit(TransitionExecutorDecision)
	if err != nil {
		t.Fatal(err)
	}
	completion := awaitCompletion(t, forwarding)
	snapshot := writer.snapshot()
	if len(snapshot.statuses) != 1 || snapshot.statuses[0] != http.StatusPartialContent || string(snapshot.body) != "abcd" || snapshot.flushes != 2 {
		t.Fatalf("writer=%#v", snapshot)
	}
	if got := snapshot.header.Values("X-Repeat"); len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("repeated header=%#v", got)
	}
	if got := snapshot.header.Values(http.TrailerPrefix + "X-Final"); len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("trailers=%#v header=%#v", got, snapshot.header)
	}
	if completion.Trailer.Get("X-Final") != "one" || completion.ClientBodyBytesWritten != 4 || completion.UpstreamBytesRead != 4 {
		t.Fatalf("completion=%#v", completion)
	}
	assertBudgetReleased(t, budget)
}

func TestClientWriteFailureCountsPartialBytesAndSuppressesFlushAndTrailers(t *testing.T) {
	config, budget := newTestConfig(t, newManualScheduler())
	trailer := http.Header{"X-Final": {"must-not-write"}}
	body := newSteppedBody(immediateStep("abc", io.EOF))
	writer := newRecordingWriter()
	writeFailure := errors.New("client disconnected")
	writer.outcomes = []writeOutcome{{n: 1, err: writeFailure}}
	response := Start(context.Background(), config, StartInput[testObservation]{
		Mode: HoldMode(), StatusCode: http.StatusOK, Header: http.Header{"Trailer": {"X-Final"}},
		Trailer: trailer, Body: body, Writer: writer, Flush: true,
	})
	forwarding, err := response.Commit(TransitionExecutorDecision)
	if err != nil {
		t.Fatal(err)
	}
	completion := awaitCompletion(t, forwarding)
	snapshot := writer.snapshot()
	if completion.Termination != TerminationClientWriteFailure || completion.ClientBodyBytesWritten != 1 || string(snapshot.body) != "a" || snapshot.flushes != 0 {
		t.Fatalf("completion=%#v writer=%#v", completion, snapshot)
	}
	if got := snapshot.header.Values(http.TrailerPrefix + "X-Final"); len(got) != 0 {
		t.Fatalf("write failure emitted trailers: %#v", got)
	}
	if body.closeCount.Load() != 1 {
		t.Fatalf("body close count=%d", body.closeCount.Load())
	}
	assertBudgetReleased(t, budget)
}

func TestCommitPropagatesBufferedPrefixWriteFailure(t *testing.T) {
	config, budget := newTestConfig(t, newManualScheduler())
	body := newSteppedBody(
		immediateStep("match", nil),
		readStep{started: make(chan struct{}), release: make(chan struct{})},
	)
	writer := newRecordingWriter()
	writer.outcomes = []writeOutcome{{n: 0}}
	response := Start(context.Background(), config, StartInput[testObservation]{
		Mode: ProbeMode(), StatusCode: http.StatusOK, Body: body, Writer: writer,
		NewDriver: scriptedFactory(map[int][]testObservation{1: {{Kind: ObservationSemanticMatch}}}, nil),
	})
	boundary := awaitBoundary(t, response)
	if boundary.Reason != ReasonSemanticMatch {
		t.Fatalf("boundary=%#v", boundary)
	}
	forwarding, err := response.Commit(TransitionSemanticDecision)
	if forwarding == nil || !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("forwarding=%#v error=%v", forwarding, err)
	}
	completion := awaitCompletion(t, forwarding)
	if completion.Termination != TerminationClientWriteFailure || completion.ClientBodyBytesWritten != 0 {
		t.Fatalf("completion=%#v", completion)
	}
	if got := writer.snapshot(); len(got.statuses) != 1 || len(got.body) != 0 || got.flushes != 0 {
		t.Fatalf("writer=%#v", got)
	}
	if body.closeCount.Load() != 1 {
		t.Fatalf("body closes=%d", body.closeCount.Load())
	}
	assertBudgetReleased(t, budget)
}

func TestWriterFullWriteLoopAndHelpers(t *testing.T) {
	writeFailure := errors.New("write failure")
	tests := []struct {
		name         string
		outcomes     []writeOutcome
		wantWritten  int
		wantBody     string
		wantError    error
		wantAnyError bool
	}{
		{name: "short nil writes complete", outcomes: []writeOutcome{{n: 1}, {n: 2}}, wantWritten: 3, wantBody: "abc"},
		{name: "partial error counts bytes", outcomes: []writeOutcome{{n: 2, err: writeFailure}}, wantWritten: 2, wantBody: "ab", wantError: writeFailure},
		{name: "zero nil is short write", outcomes: []writeOutcome{{n: 0}}, wantError: io.ErrShortWrite},
		{name: "negative count", outcomes: []writeOutcome{{n: -1}}, wantAnyError: true},
		{name: "oversized count", outcomes: []writeOutcome{{n: 4}}, wantAnyError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			writer := newRecordingWriter()
			writer.outcomes = test.outcomes
			written, err := writeFull(writer, []byte("abc"))
			if written != test.wantWritten || string(writer.snapshot().body) != test.wantBody {
				t.Fatalf("written=%d body=%q", written, writer.snapshot().body)
			}
			if test.wantError != nil && !errors.Is(err, test.wantError) {
				t.Fatalf("error=%v, want %v", err, test.wantError)
			}
			if test.wantError == nil && test.wantAnyError && err == nil {
				t.Fatal("expected an error")
			}
			if test.wantError == nil && !test.wantAnyError && err != nil {
				t.Fatal(err)
			}
		})
	}

	writer := newRecordingWriter()
	writer.header.Set("Old", "remove")
	writeResponseHead(writer, http.StatusCreated, http.Header{"X-Repeat": {"one", "two"}})
	flushResponse(writer, false)
	flushResponse(writer, true)
	flushResponse(nonFlushingWriter{inner: writer}, true)
	writeTrailers(writer, http.Header{"X-Final": {"one", "two"}})
	writeTrailers(writer, http.Header{"X-Final": {"replacement"}})
	writeTrailers(writer, nil)
	writeTrailers(nil, http.Header{"Ignored": {"value"}})
	snapshot := writer.snapshot()
	if snapshot.header.Get("Old") != "" || len(snapshot.header.Values("X-Repeat")) != 2 ||
		snapshot.header.Get(http.TrailerPrefix+"X-Final") != "replacement" || snapshot.flushes != 1 ||
		len(snapshot.statuses) != 1 || snapshot.statuses[0] != http.StatusCreated {
		t.Fatalf("helper transcript=%#v", snapshot)
	}
}

func TestMemoryDenialsFailOpenWithExactRawBytes(t *testing.T) {
	tests := []struct {
		name         string
		requestLimit int
		processLimit int
		wantReason   BoundaryReason
		wantPeak     int
		factoryCalls int32
	}{
		{
			name: "decoded buffer request denial", requestLimit: testReadBuffer - 1, processLimit: 4 * 1024 * 1024,
			wantReason: ReasonRequestMemoryExhausted,
		},
		{
			name: "decoded buffer process denial", requestLimit: 256 * 1024, processLimit: testReadBuffer - 1,
			wantReason: ReasonProcessMemoryExhausted,
		},
		{
			name: "raw prefix request denial after partial retention", requestLimit: testReadBuffer + 2, processLimit: 4 * 1024 * 1024,
			wantReason: ReasonRequestMemoryExhausted, wantPeak: testReadBuffer + 2, factoryCalls: 1,
		},
		{
			name: "raw prefix process denial after partial retention", requestLimit: 256 * 1024, processLimit: testReadBuffer + 2,
			wantReason: ReasonProcessMemoryExhausted, wantPeak: testReadBuffer + 2, factoryCalls: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scheduler := newManualScheduler()
			config, _ := newTestConfig(t, scheduler)
			budget, err := NewProcessBudget(test.processLimit)
			if err != nil {
				t.Fatal(err)
			}
			config.ProcessBudget = budget
			config.RequestMemoryLimit = test.requestLimit
			body := newSteppedBody(immediateStep("raw", io.EOF))
			writer := newRecordingWriter()
			var factoryCalls atomic.Int32
			response := Start(context.Background(), config, StartInput[testObservation]{
				Mode: ProbeMode(), StatusCode: http.StatusOK, Body: body, Writer: writer,
				NewDriver: func(source io.Reader, _ allocation.Reserver) (Driver[testObservation], error) {
					factoryCalls.Add(1)
					return &scriptedDriver{source: source}, nil
				},
			})
			boundary := awaitBoundary(t, response)
			completion := awaitCompletion(t, boundary.Forwarding)
			if boundary.State != StateForwarding || boundary.Reason != test.wantReason || completion.AnalysisFailure != test.wantReason {
				t.Fatalf("boundary=%#v completion=%#v", boundary, completion)
			}
			if string(writer.snapshot().body) != "raw" || completion.ClientBodyBytesWritten != 3 || completion.UpstreamBytesRead != 3 {
				t.Fatalf("writer=%#v completion=%#v", writer.snapshot(), completion)
			}
			if completion.PeakRequestBytes != test.wantPeak || factoryCalls.Load() != test.factoryCalls {
				t.Fatalf("peak=%d factory_calls=%d", completion.PeakRequestBytes, factoryCalls.Load())
			}
			assertBudgetReleased(t, budget)
		})
	}
}

func TestRawPrefixMemoryDenialPreservesSameReadFailure(t *testing.T) {
	config, budget := newTestConfig(t, newManualScheduler())
	config.RequestMemoryLimit = testReadBuffer + 2
	upstreamFailure := errors.New("upstream reset with final bytes")
	secondRead := immediateStep("unexpected second read", io.EOF)
	body := newSteppedBody(
		immediateStep("raw", upstreamFailure),
		secondRead,
	)
	writer := newRecordingWriter()
	response := Start(context.Background(), config, StartInput[testObservation]{
		Mode: ProbeMode(), StatusCode: http.StatusOK, Body: body, Writer: writer,
		NewDriver: scriptedFactory(nil, nil),
	})

	boundary := awaitBoundary(t, response)
	completion := awaitCompletion(t, boundary.Forwarding)
	if boundary.State != StateForwarding || boundary.Reason != ReasonRequestMemoryExhausted {
		t.Fatalf("boundary=%#v", boundary)
	}
	if completion.AnalysisFailure != ReasonRequestMemoryExhausted || completion.Termination != TerminationUpstreamReadFailure {
		t.Fatalf("completion=%#v", completion)
	}
	if got := writer.snapshot(); string(got.body) != "raw" || len(got.statuses) != 1 {
		t.Fatalf("writer=%#v", got)
	}
	if completion.UpstreamBytesRead != 3 || completion.ClientBodyBytesWritten != 3 {
		t.Fatalf("completion byte counts=%#v", completion)
	}
	select {
	case <-secondRead.started:
		t.Fatal("forward-only transition issued another upstream read")
	default:
	}
	if body.closeCount.Load() != 1 || body.concurrentRead.Load() {
		t.Fatalf("closes=%d concurrent_read=%t", body.closeCount.Load(), body.concurrentRead.Load())
	}
	assertBudgetReleased(t, budget)
}

func TestAnalysisFailuresAndUpstreamFailuresRemainDistinct(t *testing.T) {
	t.Run("driver failure fails open", func(t *testing.T) {
		config, budget := newTestConfig(t, newManualScheduler())
		body := newSteppedBody(immediateStep("raw", nil))
		writer := newRecordingWriter()
		response := Start(context.Background(), config, StartInput[testObservation]{
			Mode: ProbeMode(), StatusCode: http.StatusOK, Body: body, Writer: writer,
			NewDriver: func(source io.Reader, _ allocation.Reserver) (Driver[testObservation], error) {
				return &forcedErrorDriver{source: source, err: errors.New("decoder failed")}, nil
			},
		})
		boundary := awaitBoundary(t, response)
		completion := awaitCompletion(t, boundary.Forwarding)
		if boundary.Reason != ReasonAnalysisInternal || completion.AnalysisFailure != ReasonAnalysisInternal || completion.Termination != TerminationCompleted {
			t.Fatalf("boundary=%#v completion=%#v", boundary, completion)
		}
		if got := writer.snapshot(); string(got.body) != "raw" {
			t.Fatalf("writer=%#v", got)
		}
		assertBudgetReleased(t, budget)
	})

	t.Run("upstream read failure is not decoding failure", func(t *testing.T) {
		config, budget := newTestConfig(t, newManualScheduler())
		upstreamFailure := errors.New("upstream reset")
		body := newSteppedBody(immediateStep("raw", upstreamFailure))
		writer := newRecordingWriter()
		response := Start(context.Background(), config, StartInput[testObservation]{
			Mode: ProbeMode(), StatusCode: http.StatusOK, Body: body, Writer: writer,
			NewDriver: scriptedFactory(nil, nil),
		})
		boundary := awaitBoundary(t, response)
		completion := awaitCompletion(t, boundary.Forwarding)
		if boundary.Reason != ReasonUpstreamReadFailure || completion.Termination != TerminationUpstreamReadFailure || completion.AnalysisFailure != "" {
			t.Fatalf("boundary=%#v completion=%#v", boundary, completion)
		}
		if got := writer.snapshot(); string(got.body) != "raw" {
			t.Fatalf("writer=%#v", got)
		}
		assertBudgetReleased(t, budget)
	})

	t.Run("decisive fail-open observation", func(t *testing.T) {
		config, budget := newTestConfig(t, newManualScheduler())
		body := newSteppedBody(immediateStep("raw", io.EOF))
		writer := newRecordingWriter()
		response := Start(context.Background(), config, StartInput[testObservation]{
			Mode: ProbeMode(), StatusCode: http.StatusOK, Body: body, Writer: writer,
			NewDriver: scriptedFactory(map[int][]testObservation{1: {{Kind: ObservationFailOpen, Reason: ReasonMalformedFrame}}}, nil),
		})
		boundary := awaitBoundary(t, response)
		completion := awaitCompletion(t, boundary.Forwarding)
		if boundary.Reason != ReasonMalformedFrame || completion.AnalysisFailure != ReasonMalformedFrame || string(writer.snapshot().body) != "raw" {
			t.Fatalf("boundary=%#v completion=%#v writer=%#v", boundary, completion, writer.snapshot())
		}
		assertBudgetReleased(t, budget)
	})
}

func TestTinyReadPrefixUsesBoundedChunksAndExactBytes(t *testing.T) {
	config, budget := newTestConfig(t, newManualScheduler())
	raw := bytes.Repeat([]byte("z"), 10_000)
	body := newSplitBody(raw, 1)
	writer := newRecordingWriter()
	response := Start(context.Background(), config, StartInput[testObservation]{
		Mode: ProbeMode(), StatusCode: http.StatusOK, Body: body, Writer: writer,
		NewDriver: scriptedFactory(nil, nil),
	})
	boundary := awaitBoundary(t, response)
	completion := awaitCompletion(t, boundary.Forwarding)
	if boundary.Reason != ReasonUpstreamEOFNoMatch || !bytes.Equal(writer.snapshot().body, raw) {
		t.Fatalf("boundary=%#v body_bytes=%d", boundary, len(writer.snapshot().body))
	}
	wantRawCapacity := ((len(raw) + rawPrefixChunkBytes - 1) / rawPrefixChunkBytes) * rawPrefixChunkBytes
	if completion.PeakRequestBytes != testReadBuffer+wantRawCapacity || completion.UpstreamBytesRead != int64(len(raw)) || completion.ClientBodyBytesWritten != int64(len(raw)) {
		t.Fatalf("completion=%#v want_peak=%d", completion, testReadBuffer+wantRawCapacity)
	}
	if body.closeCount.Load() != 1 || body.concurrentReads.Load() {
		t.Fatalf("closes=%d concurrent=%t", body.closeCount.Load(), body.concurrentReads.Load())
	}
	assertBudgetReleased(t, budget)
}

func TestObservationOrderingUsageCoalescingAndRelease(t *testing.T) {
	t.Run("control usage and visible boundary", func(t *testing.T) {
		config, budget := newTestConfig(t, newManualScheduler())
		body := newSteppedBody(immediateStep("visible", io.EOF))
		writer := newRecordingWriter()
		ignoredRelease := &releaseCounter{}
		unknownRelease := &releaseCounter{}
		usageRelease := &releaseCounter{}
		visibleRelease := &releaseCounter{}
		observations := []testObservation{
			{Kind: ObservationIgnore, ID: "ignored", release: ignoredRelease},
			{Kind: ObservationKind(255), ID: "unknown", release: unknownRelease},
			{Kind: ObservationUsage, ID: "usage", release: usageRelease},
			{Kind: ObservationClientVisible, ID: "visible", release: visibleRelease},
		}
		response := Start(context.Background(), config, StartInput[testObservation]{
			Mode: ProbeMode(), StatusCode: http.StatusOK, Body: body, Writer: writer,
			NewDriver: scriptedFactory(map[int][]testObservation{1: observations}, nil),
		})
		boundary := awaitBoundary(t, response)
		completion := awaitCompletion(t, boundary.Forwarding)
		milestone := awaitSemantic(t, boundary.Forwarding)
		if boundary.Reason != ReasonClientVisibleEvent || !boundary.HasObservation || boundary.Observation.ID != "visible" {
			t.Fatalf("boundary=%#v", boundary)
		}
		if !milestone.Completed || milestone.Matched || !completion.HasUsageObservation || completion.UsageObservation.ID != "usage" {
			t.Fatalf("milestone=%#v completion=%#v", milestone, completion)
		}
		for name, counter := range map[string]*releaseCounter{
			"ignored": ignoredRelease, "unknown": unknownRelease, "usage": usageRelease, "visible": visibleRelease,
		} {
			if counter.count.Load() != 1 {
				t.Errorf("%s release count=%d", name, counter.count.Load())
			}
		}
		if string(writer.snapshot().body) != "visible" {
			t.Fatalf("writer=%#v", writer.snapshot())
		}
		assertBudgetReleased(t, budget)
	})

	t.Run("latest usage replaces prior ownership", func(t *testing.T) {
		config, budget := newTestConfig(t, newManualScheduler())
		firstRelease := &releaseCounter{}
		lastRelease := &releaseCounter{}
		body := newSteppedBody(immediateStep("a", nil), immediateStep("b", io.EOF))
		writer := newRecordingWriter()
		mode, err := ObserveMode(ReasonNoRetryCandidate)
		if err != nil {
			t.Fatal(err)
		}
		response := Start(context.Background(), config, StartInput[testObservation]{
			Mode: mode, StatusCode: http.StatusOK, Body: body, Writer: writer,
			NewDriver: scriptedFactory(map[int][]testObservation{
				1: {{Kind: ObservationUsage, ID: "first", release: firstRelease}},
				2: {{Kind: ObservationUsage, ID: "last", release: lastRelease}},
			}, nil),
		})
		boundary := awaitBoundary(t, response)
		completion := awaitCompletion(t, boundary.Forwarding)
		if !completion.HasUsageObservation || completion.UsageObservation.ID != "last" || firstRelease.count.Load() != 1 || lastRelease.count.Load() != 1 {
			t.Fatalf("completion=%#v first_release=%d last_release=%d", completion, firstRelease.count.Load(), lastRelease.count.Load())
		}
		assertBudgetReleased(t, budget)
	})
}

func TestInitialFailOpenSkipsDriverAndStreamsRaw(t *testing.T) {
	config, budget := newTestConfig(t, newManualScheduler())
	body := newSteppedBody(immediateStep("unsupported raw", io.EOF))
	writer := newRecordingWriter()
	var factoryCalls atomic.Int32
	response := Start(context.Background(), config, StartInput[testObservation]{
		Mode: ProbeMode(), StatusCode: http.StatusOK, Body: body, Writer: writer,
		InitialFailure: ReasonUnsupportedEncoding,
		NewDriver: func(source io.Reader, _ allocation.Reserver) (Driver[testObservation], error) {
			factoryCalls.Add(1)
			return &scriptedDriver{source: source}, nil
		},
	})
	boundary := awaitBoundary(t, response)
	completion := awaitCompletion(t, boundary.Forwarding)
	if boundary.Reason != ReasonUnsupportedEncoding || completion.AnalysisFailure != ReasonUnsupportedEncoding || factoryCalls.Load() != 0 {
		t.Fatalf("boundary=%#v completion=%#v factory_calls=%d", boundary, completion, factoryCalls.Load())
	}
	if string(writer.snapshot().body) != "unsupported raw" || completion.PeakRequestBytes != 0 {
		t.Fatalf("writer=%#v completion=%#v", writer.snapshot(), completion)
	}
	assertBudgetReleased(t, budget)
}

func TestLateDuplicateObservationsRemainFactsOnly(t *testing.T) {
	config, budget := newTestConfig(t, newManualScheduler())
	firstSemantic := &releaseCounter{}
	duplicateSemantic := &releaseCounter{}
	visible := &releaseCounter{}
	body := newSteppedBody(immediateStep("a", nil), immediateStep("b", io.EOF))
	writer := newRecordingWriter()
	mode, err := ObserveMode(ReasonNoRetryCandidate)
	if err != nil {
		t.Fatal(err)
	}
	response := Start(context.Background(), config, StartInput[testObservation]{
		Mode: mode, StatusCode: http.StatusOK, Body: body, Writer: writer,
		NewDriver: scriptedFactory(map[int][]testObservation{
			1: {{Kind: ObservationSemanticMatch, ID: "first", release: firstSemantic}},
			2: {
				{Kind: ObservationSemanticMatch, ID: "duplicate", release: duplicateSemantic},
				{Kind: ObservationClientVisible, ID: "visible", release: visible},
			},
		}, nil),
	})
	boundary := awaitBoundary(t, response)
	milestone := awaitSemantic(t, boundary.Forwarding)
	completion := awaitCompletion(t, boundary.Forwarding)
	if boundary.Reason != ReasonNoRetryCandidate || milestone.Observation.ID != "first" || completion.SemanticObservation.ID != "first" {
		t.Fatalf("boundary=%#v milestone=%#v completion=%#v", boundary, milestone, completion)
	}
	if firstSemantic.count.Load() != 1 || duplicateSemantic.count.Load() != 1 || visible.count.Load() != 1 || string(writer.snapshot().body) != "ab" {
		t.Fatalf("releases=%d/%d/%d writer=%#v", firstSemantic.count.Load(), duplicateSemantic.count.Load(), visible.count.Load(), writer.snapshot())
	}
	assertBudgetReleased(t, budget)
}

func TestForwardingFailOpenWithoutReasonUsesStableInternalReason(t *testing.T) {
	config, budget := newTestConfig(t, newManualScheduler())
	body := newSteppedBody(immediateStep("raw", io.EOF))
	writer := newRecordingWriter()
	mode, err := ObserveMode(ReasonNoRetryCandidate)
	if err != nil {
		t.Fatal(err)
	}
	response := Start(context.Background(), config, StartInput[testObservation]{
		Mode: mode, StatusCode: http.StatusOK, Body: body, Writer: writer,
		NewDriver: scriptedFactory(map[int][]testObservation{1: {{Kind: ObservationFailOpen}}}, nil),
	})
	boundary := awaitBoundary(t, response)
	completion := awaitCompletion(t, boundary.Forwarding)
	if completion.AnalysisFailure != ReasonAnalysisInternal || string(writer.snapshot().body) != "raw" {
		t.Fatalf("completion=%#v writer=%#v", completion, writer.snapshot())
	}
	assertBudgetReleased(t, budget)
}

func TestProbeIdleTimeoutCommitsThenClosesStalledRead(t *testing.T) {
	scheduler := newManualScheduler()
	config, budget := newTestConfig(t, scheduler)
	config.IdleDuration = 20 * time.Second
	blocked := readStep{started: make(chan struct{}), release: make(chan struct{})}
	body := newSteppedBody(blocked)
	writer := newRecordingWriter()
	response := Start(context.Background(), config, StartInput[testObservation]{
		Mode: ProbeMode(), StatusCode: http.StatusGatewayTimeout, Body: body, Writer: writer,
		NewDriver: scriptedFactory(nil, nil),
	})
	waitForSignal(t, blocked.started, "probe idle read")
	probeTimer := scheduler.waitFor(t, 0)
	idleTimer := scheduler.waitFor(t, 1)
	if idleTimer.delay != config.IdleDuration {
		t.Fatalf("idle delay=%v", idleTimer.delay)
	}
	scheduler.fire(t, 1)
	boundary := awaitBoundary(t, response)
	completion := awaitCompletion(t, boundary.Forwarding)
	if boundary.Reason != ReasonUpstreamReadFailure || completion.Termination != TerminationUpstreamReadFailure || len(writer.snapshot().statuses) != 1 {
		t.Fatalf("boundary=%#v completion=%#v writer=%#v", boundary, completion, writer.snapshot())
	}
	if !probeTimer.stopped.Load() || body.closeCount.Load() != 1 {
		t.Fatalf("probe_stopped=%t closes=%d", probeTimer.stopped.Load(), body.closeCount.Load())
	}
	assertBudgetReleased(t, budget)
}

func TestStructuredTraceCapturesLinearizedMilestones(t *testing.T) {
	config, budget := newTestConfig(t, newManualScheduler())
	recorder := &traceRecorder{}
	config.Trace = recorder
	body := newSteppedBody(immediateStep("trace", io.EOF))
	writer := newRecordingWriter()
	response := Start(context.Background(), config, StartInput[testObservation]{
		OperationID: "operation-42", Mode: HoldMode(), StatusCode: http.StatusOK, Body: body, Writer: writer,
	})
	forwarding, err := response.Commit(TransitionExecutorDecision)
	if err != nil {
		t.Fatal(err)
	}
	_ = awaitCompletion(t, forwarding)
	events := recorder.snapshot()
	if len(events) != 2 || events[0].Name != traceProbeReleased || events[1].Name != traceResponseFinalized {
		t.Fatalf("trace events=%#v", events)
	}
	for _, event := range events {
		if event.OperationID != "operation-42" || event.State != StateForwarding || event.RequestBytes < 0 || event.ProcessBytes < 0 {
			t.Fatalf("trace event=%#v", event)
		}
	}
	assertBudgetReleased(t, budget)
}
