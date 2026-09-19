package responseanalysis

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/responsefacts"
)

type completionBoundaryWriter struct {
	*httptest.ResponseRecorder
	remaining int
	cancel    context.CancelFunc
	fail      bool
}

func (w *completionBoundaryWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(p)
	w.remaining -= n
	if w.remaining == 0 {
		if w.cancel != nil {
			w.cancel()
		}
		if w.fail {
			return n, io.ErrClosedPipe
		}
	}
	return n, err
}

func completionBudgetBody(padding int) string {
	// Responses repeats tool definitions and usage attribution in the terminal
	// envelope. Its size is independent of the amount of generated output.
	return "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"tools\":[{\"description\":\"" +
		strings.Repeat("x", padding) +
		"\"}],\"usage\":{\"input_tokens\":172419,\"output_tokens\":793,\"total_tokens\":173212}}}\n\n"
}

func TestLargeCompletionRetainsFactsAcrossDownstreamTermination(t *testing.T) {
	observe, err := ObserveMode(BoundaryNoRetryCandidate)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []struct {
		name  string
		value AnalysisMode
	}{
		{"observe", observe}, {"probe_with_semantic_gate", ProbeAndGateMode()},
	} {
		for _, encoding := range []string{"identity", "gzip"} {
			for _, size := range []int{130 * 1024, MaxDecodedEventBytes - 1024} {
				for _, ending := range []string{"eof", "cancel", "write_failure"} {
					t.Run(fmt.Sprintf("%s/%s/%d/%s", mode.name, encoding, size, ending), func(t *testing.T) {
						raw := []byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" + completionBudgetBody(size))
						wire := raw
						if encoding == "gzip" {
							wire = gzipRuntimeBytes(t, raw)
						}
						ctx, cancel := context.WithCancel(t.Context())
						defer cancel()
						writer := &completionBoundaryWriter{
							ResponseRecorder: httptest.NewRecorder(), remaining: len(wire), fail: ending == "write_failure",
						}
						if ending == "cancel" {
							writer.cancel = cancel
						}
						body := &completionReadCounter{Reader: strings.NewReader(string(wire))}
						observer := &responsefacts.CompletionObserver{}
						budget := newRuntimeBudget(t, ResponseProbeMemoryBudget)
						analyzer := newRuntimeAnalyzer(t, budget, AnalyzerOptions{ProbeDuration: time.Hour})
						response := analyzer.Start(ctx, StartInput{
							Mode: mode.value, APIType: "codex", ContentType: "text/event-stream", ContentEncoding: encoding,
							StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}, "Content-Encoding": {encoding}},
							Body: body, Writer: writer, ObserveCompletion: observer.Observe,
						})
						boundary := awaitAnalyzerBoundary(t, response)
						completion := awaitAnalyzerCompletion(t, boundary.Forwarding)
						assertCompletionBudgetFacts(t, observer, completion)
						if !bytes.Equal(writer.Body.Bytes(), wire) {
							t.Fatal("wire response changed")
						}
						if budget.Used() != 0 {
							t.Fatalf("retained %d analysis bytes", budget.Used())
						}
						if ending != "eof" && body.reads > (len(wire)+PumpReadBufferBytes-1)/PumpReadBufferBytes {
							t.Fatalf("read upstream again after downstream termination: %d", body.reads)
						}
					})
				}
			}
		}
	}
}

func assertCompletionBudgetFacts(t *testing.T, observer *responsefacts.CompletionObserver, completion Completion) {
	t.Helper()
	if completion.AnalysisFailure != "" || observer.Snapshot().EventType != "response.completed" {
		t.Fatalf("completion event=%q analysis_failure=%q", observer.Snapshot().EventType, completion.AnalysisFailure)
	}
	if !completion.HasUsageObservation || completion.UsageObservation.Usage == nil {
		t.Fatal("terminal usage was lost")
	}
	defer completion.UsageObservation.Release()
	if total := completion.UsageObservation.Usage.TotalTokens; !total.Present || total.Value != 173212 {
		t.Fatalf("total tokens=%+v", total)
	}
}

func TestProbeRetentionReleaseKeepsCompletionAndUsageAnalysis(t *testing.T) {
	raw := "data: {\"type\":\"response.created\",\"response\":{\"instructions\":\"" +
		strings.Repeat("x", 4096) + "\"}}\n\n" + completionBudgetBody(130*1024)
	for _, mode := range []AnalysisMode{ProbeMode(), ProbeAndGateMode()} {
		observer := &responsefacts.CompletionObserver{}
		budget := newRuntimeBudget(t, ResponseProbeMemoryBudget)
		analyzer := newRuntimeAnalyzer(t, budget, AnalyzerOptions{ProbeDuration: time.Hour, ProbeMemoryLimit: 64})
		writer := httptest.NewRecorder()
		response := analyzer.Start(t.Context(), StartInput{
			Mode: mode, APIType: "codex", ContentType: "text/event-stream", StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(raw)), Writer: writer, ObserveCompletion: observer.Observe,
		})
		boundary := awaitAnalyzerBoundary(t, response)
		completion := awaitAnalyzerCompletion(t, boundary.Forwarding)
		if boundary.Reason != BoundaryRequestMemoryExhausted {
			t.Fatalf("release reason=%q", boundary.Reason)
		}
		assertCompletionBudgetFacts(t, observer, completion)
		if writer.Body.String() != raw || budget.Used() != 0 {
			t.Fatalf("wire_bytes=%d retained=%d", writer.Body.Len(), budget.Used())
		}
	}
}

func TestCancellationPreservesAnalysisFailureWithoutInventingCompletion(t *testing.T) {
	checksumFailure := gzipRuntimeBytes(t, []byte(completionBudgetBody(0)))
	checksumFailure[len(checksumFailure)-8] ^= 1
	mode, err := ObserveMode(BoundaryNoRetryCandidate)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, encoding string
		wire           []byte
		failure        BoundaryReason
	}{
		{"malformed JSON", "identity", []byte("data: {invalid}\n\n"), BoundaryMalformedFrame},
		{"checksum mismatch", "gzip", checksumFailure, BoundaryContentDecoding},
		{"unfinished event", "identity", []byte(strings.TrimSuffix(completionBudgetBody(0), "\n\n")), ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			writer := &completionBoundaryWriter{ResponseRecorder: httptest.NewRecorder(), remaining: len(test.wire), cancel: cancel}
			observer := &responsefacts.CompletionObserver{}
			budget := newRuntimeBudget(t, ResponseProbeMemoryBudget)
			analyzer := newRuntimeAnalyzer(t, budget, AnalyzerOptions{})
			response := analyzer.Start(ctx, StartInput{
				Mode: mode, APIType: "codex", ContentType: "text/event-stream", ContentEncoding: test.encoding,
				StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(test.wire)), Writer: writer,
				ObserveCompletion: observer.Observe,
			})
			boundary := awaitAnalyzerBoundary(t, response)
			completion := awaitAnalyzerCompletion(t, boundary.Forwarding)
			if completion.AnalysisFailure != test.failure || observer.Snapshot().EventType != "" || completion.HasUsageObservation {
				t.Fatalf("failure=%q completion=%+v", completion.AnalysisFailure, observer.Snapshot())
			}
			if !bytes.Equal(writer.Body.Bytes(), test.wire) || budget.Used() != 0 {
				t.Fatal("forwarding or resource release changed")
			}
		})
	}
}
