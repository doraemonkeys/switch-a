package responseanalysis

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/framing"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/tokenusage"
)

func TestAnalyzerConstructionValidationAndDefaults(t *testing.T) {
	if _, err := NewAnalyzer(NewRegistry(), nil, AnalyzerOptions{}); err == nil {
		t.Fatal("constructed analyzer without process budget")
	}
	budget := newRuntimeBudget(t, ResponseProbeMemoryBudget)
	tests := []struct {
		name    string
		options AnalyzerOptions
	}{
		{name: "negative probe duration", options: AnalyzerOptions{ProbeDuration: -1}},
		{name: "negative probe memory", options: AnalyzerOptions{ProbeMemoryLimit: -1}},
		{name: "probe memory above maximum", options: AnalyzerOptions{ProbeMemoryLimit: MaxProbeMemoryLimit + 1}},
		{name: "negative idle duration", options: AnalyzerOptions{IdleDuration: -1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewAnalyzer(NewRegistry(), budget, test.options); err == nil {
				t.Fatal("accepted invalid analyzer options")
			}
		})
	}

	analyzer, err := NewAnalyzer(NewRegistry(), budget, AnalyzerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if analyzer.probeDuration != DefaultProbeDuration || analyzer.probeMemoryLimit != DefaultProbeMemoryLimit || analyzer.scheduler == nil {
		t.Fatalf("defaults=%#v", analyzer)
	}
	defaultBudget, err := NewDefaultProcessMemoryBudget()
	if err != nil || defaultBudget.Limit() != ResponseProbeMemoryBudget {
		t.Fatalf("default budget=%#v error=%v", defaultBudget, err)
	}
	if HoldMode().Analyzes() {
		t.Fatal("hold facade unexpectedly analyzes")
	}
	if mode, err := ObserveMode(BoundaryPassthroughOnly); err != nil || !mode.Analyzes() {
		t.Fatalf("observe facade=%#v error=%v", mode, err)
	}
}

func TestAnalyzerHoldFacadeAndNilReceiverTransferOwnership(t *testing.T) {
	t.Run("hold", func(t *testing.T) {
		budget := newRuntimeBudget(t, ResponseProbeMemoryBudget)
		analyzer := newRuntimeAnalyzer(t, budget, AnalyzerOptions{})
		writer := httptest.NewRecorder()
		response := analyzer.Start(context.Background(), StartInput{
			Mode: HoldMode(), StatusCode: http.StatusAccepted,
			Body: io.NopCloser(strings.NewReader("held")), Writer: writer,
		})
		forwarding, err := response.Commit(TransitionExecutorDecision)
		if err != nil {
			t.Fatal(err)
		}
		completion := awaitAnalyzerCompletion(t, forwarding)
		if writer.Body.String() != "held" || completion.Termination != TerminationCompleted || budget.Used() != 0 {
			t.Fatalf("body=%q completion=%#v budget=%d", writer.Body.String(), completion, budget.Used())
		}
	})

	t.Run("hold SSE preserves streaming flush", func(t *testing.T) {
		budget := newRuntimeBudget(t, ResponseProbeMemoryBudget)
		analyzer := newRuntimeAnalyzer(t, budget, AnalyzerOptions{})
		writer := httptest.NewRecorder()
		response := analyzer.Start(context.Background(), StartInput{
			Mode: HoldMode(), StatusCode: http.StatusOK,
			Header: http.Header{"Content-Type": {"text/event-stream; charset=utf-8"}},
			Body:   io.NopCloser(strings.NewReader("data: held\n\n")), Writer: writer,
		})
		forwarding, err := response.Commit(TransitionExecutorDecision)
		if err != nil {
			t.Fatal(err)
		}
		completion := awaitAnalyzerCompletion(t, forwarding)
		if writer.Body.String() != "data: held\n\n" || !writer.Flushed || completion.Termination != TerminationCompleted || budget.Used() != 0 {
			t.Fatalf("body=%q flushed=%t completion=%#v budget=%d", writer.Body.String(), writer.Flushed, completion, budget.Used())
		}
	})

	t.Run("nil receiver", func(t *testing.T) {
		var analyzer *Analyzer
		writer := httptest.NewRecorder()
		response := analyzer.Start(context.Background(), StartInput{
			Mode: HoldMode(), StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader("unused")), Writer: writer,
		})
		boundary := awaitAnalyzerBoundary(t, response)
		if boundary.State != StateDiscarded || boundary.Reason != BoundaryReason(FailureAnalysisInternal) {
			t.Fatalf("boundary=%#v", boundary)
		}
	})
}

func TestRuntimeObservationOperationsAndDriverDefensivePaths(t *testing.T) {
	match := func(fields SemanticFields) bool { return strings.EqualFold(fields.Message, "match") }
	inspect := observationInspector(match)
	tests := []struct {
		observation Observation
		want        pendingObservationKind
	}{
		{Observation{Class: EventControl}, pendingObservationIgnore},
		{Observation{Class: EventUsage}, pendingObservationUsage},
		{Observation{Class: EventError}, pendingObservationClientVisible},
		{Observation{Class: EventError, Fields: &SemanticFields{Message: "MATCH"}}, pendingObservationSemanticMatch},
		{Observation{Class: EventClientVisible}, pendingObservationClientVisible},
		{Observation{Class: EventFailOpen}, pendingObservationFailOpen},
		{Observation{Class: EventClass("unknown")}, pendingObservationIgnore},
	}
	for _, test := range tests {
		if got := pendingObservationKind(inspect(test.observation)); got != test.want {
			t.Errorf("inspect(%#v)=%d want=%d", test.observation, got, test.want)
		}
	}
	if got := observationInspector(nil)(Observation{Class: EventError, Fields: &SemanticFields{Message: "match"}}); pendingObservationKind(got) != pendingObservationClientVisible {
		t.Fatalf("nil matcher classified error as %d", got)
	}
	if got := observationFailureReason(Observation{}); got != BoundaryReason(FailureAnalysisInternal) {
		t.Fatalf("empty failure reason=%q", got)
	}
	if got := observationFailureReason(Observation{AnalysisReason: FailureMalformedFrame}); got != BoundaryReason(FailureMalformedFrame) {
		t.Fatalf("mapped failure reason=%q", got)
	}

	source := Observation{
		Class:  EventError,
		Fields: &SemanticFields{Type: "Type", Code: "Code", Message: "Message", Reason: "Reason"},
		Usage:  &tokenusage.TokenUsage{PromptTokens: tokenusage.ObservedCount{Value: 1, Present: true}, ServiceTier: "Tier"},
	}
	clone := cloneRuntimeObservation(source)
	source.Fields.Message = "mutated"
	source.Usage.ServiceTier = "mutated"
	if clone.Fields == source.Fields || clone.Usage == source.Usage || clone.Fields.Message != "Message" || clone.Usage.ServiceTier != "Tier" {
		t.Fatalf("clone=%#v source=%#v", clone, source)
	}
	if empty := cloneRuntimeObservation(Observation{Class: EventControl}); empty.Fields != nil || empty.Usage != nil {
		t.Fatalf("empty clone=%#v", empty)
	}

	var nilDriver *runtimeDriver
	if _, err := nilDriver.Read(make([]byte, 1), func(Observation) bool { return true }); framing.ReasonOf(err) != framing.FailureInternal {
		t.Fatalf("nil driver error=%v", err)
	}
	if err := nilDriver.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := (&runtimeDriver{}).Read(make([]byte, 1), nil); framing.ReasonOf(err) != framing.FailureInternal {
		t.Fatalf("incomplete driver error=%v", err)
	}
	if driver, err := newRuntimeDriver(Protocol{}, strings.NewReader(""), allocation.NoopReserver{}); driver != nil || err == nil {
		t.Fatalf("unresolved driver=%#v error=%v", driver, err)
	}

	protocol, failure := NewRegistry().Resolve("codex", "application/json", "identity")
	if failure != "" {
		t.Fatal(failure)
	}
	decoder, err := protocol.NewDecoder(strings.NewReader("{}"), allocation.NoopReserver{})
	if err != nil {
		t.Fatal(err)
	}
	driver := &runtimeDriver{decoder: decoder}
	if err := driver.Close(); err != nil {
		t.Fatal(err)
	}
	if err := driver.Close(); err != nil {
		t.Fatal(err)
	}
	stream, err := protocol.NewStream(allocation.NoopReserver{})
	if err != nil {
		t.Fatal(err)
	}
	if err := (&runtimeDriver{stream: stream}).Close(); err != nil {
		t.Fatal(err)
	}
}

// Local aliases keep this defensive table independent from the pending
// package's concrete generic API while still exercising every facade mapping.
type pendingObservationKind uint8

const (
	pendingObservationIgnore        pendingObservationKind = 0
	pendingObservationUsage         pendingObservationKind = 1
	pendingObservationSemanticMatch pendingObservationKind = 2
	pendingObservationClientVisible pendingObservationKind = 3
	pendingObservationFailOpen      pendingObservationKind = 4
)
