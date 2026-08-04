package pending

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
)

func TestValidateConfigRejectsEveryInvalidDependencyAndLimit(t *testing.T) {
	valid, _ := newTestConfig(t, newManualScheduler())
	tests := []struct {
		name   string
		mutate func(*Config[testObservation])
	}{
		{"process budget", func(config *Config[testObservation]) { config.ProcessBudget = nil }},
		{"scheduler", func(config *Config[testObservation]) { config.Scheduler = nil }},
		{"probe duration", func(config *Config[testObservation]) { config.ProbeDuration = 0 }},
		{"idle duration", func(config *Config[testObservation]) { config.IdleDuration = -1 }},
		{"request memory positive", func(config *Config[testObservation]) { config.RequestMemoryLimit = 0 }},
		{"request memory maximum", func(config *Config[testObservation]) { config.RequestMemoryLimit = maxRequestMemoryLimit + 1 }},
		{"decoded buffer", func(config *Config[testObservation]) { config.DecodedBufferBytes = 0 }},
		{"decoded buffer maximum", func(config *Config[testObservation]) { config.DecodedBufferBytes = maxPumpReadBufferBytes + 1 }},
		{"observation queue", func(config *Config[testObservation]) { config.ObservationQueueCapacity = 0 }},
		{"observation queue maximum", func(config *Config[testObservation]) {
			config.ObservationQueueCapacity = maxObservationQueueCapacity + 1
		}},
		{"command queue", func(config *Config[testObservation]) { config.CommandQueueCapacity = 0 }},
		{"command queue maximum", func(config *Config[testObservation]) { config.CommandQueueCapacity = maxCommandQueueCapacity + 1 }},
		{"observation inspect", func(config *Config[testObservation]) { config.Observations.Inspect = nil }},
		{"observation failure reason", func(config *Config[testObservation]) { config.Observations.FailureReason = nil }},
		{"observation clone", func(config *Config[testObservation]) { config.Observations.Clone = nil }},
		{"observation release", func(config *Config[testObservation]) { config.Observations.Release = nil }},
		{"failure classifier", func(config *Config[testObservation]) { config.FailureReason = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			test.mutate(&config)
			if err := ValidateConfig(config); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if err := ValidateConfig(valid); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidStartTransfersAndClosesBodyOwnership(t *testing.T) {
	valid, budget := newTestConfig(t, newManualScheduler())
	tests := []struct {
		name       string
		ctx        context.Context
		config     Config[testObservation]
		input      StartInput[testObservation]
		bodyAbsent bool
	}{
		{name: "invalid config", ctx: context.Background(), config: func() Config[testObservation] { c := valid; c.ProcessBudget = nil; return c }(), input: StartInput[testObservation]{Mode: HoldMode(), StatusCode: http.StatusOK}},
		{name: "nil context", config: valid, input: StartInput[testObservation]{Mode: HoldMode(), StatusCode: http.StatusOK}},
		{name: "missing mode", ctx: context.Background(), config: valid, input: StartInput[testObservation]{StatusCode: http.StatusOK}},
		{name: "mode carries reason", ctx: context.Background(), config: valid, input: StartInput[testObservation]{Mode: AnalysisMode{kind: modeHold, releaseReason: ReasonPassthroughOnly}, StatusCode: http.StatusOK}},
		{name: "status too low", ctx: context.Background(), config: valid, input: StartInput[testObservation]{Mode: HoldMode(), StatusCode: 99}},
		{name: "status too high", ctx: context.Background(), config: valid, input: StartInput[testObservation]{Mode: HoldMode(), StatusCode: 1000}},
		{name: "missing body", ctx: context.Background(), config: valid, input: StartInput[testObservation]{Mode: HoldMode(), StatusCode: http.StatusOK}, bodyAbsent: true},
		{name: "missing writer", ctx: context.Background(), config: valid, input: StartInput[testObservation]{Mode: HoldMode(), StatusCode: http.StatusOK}},
		{name: "missing analysis driver", ctx: context.Background(), config: valid, input: StartInput[testObservation]{Mode: ProbeMode(), StatusCode: http.StatusOK}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := newSteppedBody(immediateStep("unused", io.EOF))
			input := test.input
			if !test.bodyAbsent {
				input.Body = body
			}
			if test.name != "missing writer" {
				input.Writer = newRecordingWriter()
			}
			input.Header = http.Header{"X-Cloned": {"value"}}
			response := Start(test.ctx, test.config, input)
			boundary := response.AwaitBoundary()
			completion := awaitResponseCompletion(t, response)
			if boundary.State != StateDiscarded || boundary.Reason != ReasonAnalysisInternal || completion.Termination != TerminationInternalFailure {
				t.Fatalf("boundary=%#v completion=%#v", boundary, completion)
			}
			wantClosed := int32(1)
			if test.bodyAbsent {
				wantClosed = 0
			}
			if body.closeCount.Load() != wantClosed || completion.BodyClosed != !test.bodyAbsent {
				t.Fatalf("close_count=%d completion=%#v", body.closeCount.Load(), completion)
			}
		})
	}
	assertBudgetReleased(t, budget)
}

func TestClosedUnionsNilHandlesAndCachedValueHelpers(t *testing.T) {
	if HoldMode().Analyzes() || !ProbeMode().Analyzes() {
		t.Fatal("analysis-mode predicates are inconsistent")
	}
	for _, reason := range []BoundaryReason{ReasonNoRetryCandidate, ReasonPassthroughOnly} {
		mode, err := ObserveMode(reason)
		if err != nil || !mode.Analyzes() || mode.validate() != nil {
			t.Fatalf("observe mode %q = %#v, %v", reason, mode, err)
		}
	}
	if _, err := ObserveMode(ReasonSemanticMatch); err == nil {
		t.Fatal("accepted invalid observe-mode reason")
	}
	for _, mode := range []AnalysisMode{
		{kind: modeProbe, releaseReason: ReasonPassthroughOnly},
		{kind: modeObserve, releaseReason: ReasonSemanticMatch},
		{},
	} {
		if mode.validate() == nil {
			t.Fatalf("accepted invalid mode %#v", mode)
		}
	}
	if TransitionCause("invalid").validate() == nil {
		t.Fatal("accepted invalid transition cause")
	}

	var response *Response[testObservation]
	if boundary := response.AwaitBoundary(); boundary.State != 0 || boundary.Reason != "" || boundary.Forwarding != nil {
		t.Fatalf("nil boundary=%#v", boundary)
	}
	if _, err := response.Commit(TransitionExecutorDecision); !isAlreadyResolved(err, 0) {
		t.Fatalf("nil commit error=%v", err)
	}
	if _, err := response.Discard(TransitionExecutorDecision); !isAlreadyResolved(err, 0) {
		t.Fatalf("nil discard error=%v", err)
	}
	if _, err := response.Commit(TransitionCause("invalid")); err == nil {
		t.Fatal("nil commit accepted invalid cause")
	}
	if _, err := response.Discard(TransitionCause("invalid")); err == nil {
		t.Fatal("nil discard accepted invalid cause")
	}
	var forwarding *ForwardingResponse[testObservation]
	if got := forwarding.AwaitSemanticOrCompletion(); got.Matched || got.Completed || got.State != 0 {
		t.Fatalf("nil milestone=%#v", got)
	}
	if got := forwarding.Wait(); got.Header != nil || got.Trailer != nil || got.State != 0 {
		t.Fatalf("nil completion=%#v", got)
	}

	var nilResolved *AlreadyResolved
	if nilResolved.Error() == "" || (&AlreadyResolved{State: StateForwarding}).Error() == "" {
		t.Fatal("AlreadyResolved returned an empty diagnostic")
	}
	shared := &shared[testObservation]{}
	value := testObservation{ID: "value"}
	if got := shared.cloneObservation(value); got.ID != "value" || cloneHeader(nil) != nil {
		t.Fatalf("clone helpers returned %#v", got)
	}
}

func TestRealSchedulerAndAccountDefensivePaths(t *testing.T) {
	fired := make(chan struct{})
	timer := (RealScheduler{}).AfterFunc(0, func() { close(fired) })
	waitForSignal(t, fired, "real scheduler callback")
	_ = timer.Stop()

	var nilBudget *ProcessBudget
	if nilBudget.Limit() != 0 || nilBudget.Used() != 0 || nilBudget.Peak() != 0 {
		t.Fatal("nil budget returned nonzero facts")
	}
	if _, err := newRequestAccount(nil, 1); err == nil {
		t.Fatal("created request account without process budget")
	}
	process, err := NewProcessBudget(8)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newRequestAccount(process, 0); err == nil {
		t.Fatal("created request account without a positive limit")
	}
	if _, err := newRequestAccount(process, maxRequestMemoryLimit+1); err == nil {
		t.Fatal("created request account above the hard request limit")
	}
	var nilAccount *requestAccount
	if _, err := nilAccount.Reserve(allocation.ClassRawPrefix, 1); !errors.Is(err, ErrAccountClosed) {
		t.Fatalf("nil account reserve error=%v", err)
	}
	if _, _, err := nilAccount.reserveUpTo(allocation.ClassRawPrefix, 1, 1); !errors.Is(err, ErrAccountClosed) {
		t.Fatalf("nil account reserveUpTo error=%v", err)
	}
	nilAccount.close()
	if used, peak := nilAccount.snapshot(); used != 0 || peak != 0 {
		t.Fatalf("nil account snapshot=%d/%d", used, peak)
	}

	account, err := newRequestAccount(process, 8)
	if err != nil {
		t.Fatal(err)
	}
	for _, limits := range [][2]int{{0, 0}, {1, 0}, {1, 2}} {
		if _, _, err := account.reserveUpTo(allocation.ClassRawPrefix, limits[0], limits[1]); err == nil {
			t.Fatalf("reserveUpTo accepted %v", limits)
		}
	}
	reserved, capacity, err := account.reserveUpTo(allocation.ClassRawPrefix, 8, 1)
	if err != nil || capacity != 8 {
		t.Fatalf("reserveUpTo=%v/%d/%v", reserved, capacity, err)
	}
	account.close()
	account.close()
	reserved.Release()
	if _, _, err := account.reserveUpTo(allocation.ClassRawPrefix, 1, 1); !errors.Is(err, ErrAccountClosed) {
		t.Fatalf("closed reserveUpTo error=%v", err)
	}
	var nilGrant *grant
	nilGrant.Release()
	(&grant{}).Release()
	(emptyGrant{}).Release()
	if process.Used() != 0 || process.Limit() != 8 || process.Peak() != 8 {
		t.Fatalf("process facts=%d/%d/%d", process.Used(), process.Limit(), process.Peak())
	}
}

func TestUsageQueueSaturationRetainsLatestOwnership(t *testing.T) {
	queue := make(chan testObservation, 1)
	firstRelease := &releaseCounter{}
	lastRelease := &releaseCounter{}
	queue <- testObservation{ID: "first", release: firstRelease}

	coalesceUsage(queue, testObservation{ID: "last", release: lastRelease}, testObservationOps().Release)
	if got := <-queue; got.ID != "last" {
		t.Fatalf("coalesced usage=%#v", got)
	}
	if firstRelease.count.Load() != 1 || lastRelease.count.Load() != 0 {
		t.Fatalf("release counts first=%d last=%d", firstRelease.count.Load(), lastRelease.count.Load())
	}
}

func TestNilAnalysisDriverFailsOpenWithoutLosingRawBytes(t *testing.T) {
	config, budget := newTestConfig(t, newManualScheduler())
	body := newSteppedBody(immediateStep("raw", io.EOF))
	writer := newRecordingWriter()
	response := Start(context.Background(), config, StartInput[testObservation]{
		Mode: ProbeMode(), StatusCode: http.StatusOK, Body: body, Writer: writer,
		NewDriver: func(io.Reader, allocation.Reserver) (Driver[testObservation], error) {
			return nil, nil
		},
	})
	boundary := awaitBoundary(t, response)
	completion := awaitCompletion(t, boundary.Forwarding)
	if boundary.Reason != ReasonAnalysisInternal || completion.AnalysisFailure != ReasonAnalysisInternal || string(writer.snapshot().body) != "raw" {
		t.Fatalf("boundary=%#v completion=%#v writer=%#v", boundary, completion, writer.snapshot())
	}
	assertBudgetReleased(t, budget)
}

func TestRawSourceDefensiveReaderContracts(t *testing.T) {
	if got := normalizePumpError(errors.New("raw")); got == nil || !strings.Contains(got.Error(), "raw") {
		t.Fatalf("normalized error=%v", got)
	}
	if got := normalizePumpError(io.EOF); got != nil {
		t.Fatalf("EOF normalized to %v", got)
	}
	source := &rawSource[testObservation]{}
	if n, err := source.Read(nil); n != 0 || err != nil {
		t.Fatalf("zero-size read=%d/%v", n, err)
	}
}

func TestInvalidStartHeaderCloneIsIndependent(t *testing.T) {
	config, _ := newTestConfig(t, newManualScheduler())
	header := http.Header{"X-Value": {"original"}}
	body := io.NopCloser(strings.NewReader("unused"))
	response := Start(context.Background(), config, StartInput[testObservation]{
		Mode: HoldMode(), StatusCode: 0, Header: header, Body: body, Writer: newRecordingWriter(),
	})
	header.Set("X-Value", "mutated")
	completion := awaitResponseCompletion(t, response)
	if completion.Header.Get("X-Value") != "original" {
		t.Fatalf("invalid-start header=%#v", completion.Header)
	}
}

func TestRealSchedulerStopBeforeFire(t *testing.T) {
	fired := make(chan struct{}, 1)
	timer := (RealScheduler{}).AfterFunc(time.Hour, func() { fired <- struct{}{} })
	if !timer.Stop() {
		t.Fatal("fresh real timer did not stop")
	}
}
