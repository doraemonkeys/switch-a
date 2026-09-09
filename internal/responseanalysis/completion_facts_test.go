package responseanalysis

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/responsefacts"
)

type completionFailingWriter struct{ *httptest.ResponseRecorder }

func (w *completionFailingWriter) Write(_ []byte) (int, error) { return 3, io.ErrClosedPipe }

// Already-read evidence remains observable, but failure must not trigger more reads.
type completionReadCounter struct {
	*strings.Reader
	reads int
}

func (b *completionReadCounter) Read(p []byte) (int, error) { b.reads++; return b.Reader.Read(p) }
func (b *completionReadCounter) Close() error               { return nil }

func TestUpstreamCompletionSurvivesDownstreamFailure(t *testing.T) {
	for _, tc := range []struct{ name, apiType, body, event string }{
		{"responses", "codex", "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"status\":\"completed\"}}\n\n", "response.completed"},
		{"responses after delta", "codex", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r2\",\"status\":\"completed\"}}\n\n", "response.completed"},
		{"anthropic", "claude", "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", "message_stop"},
		{"chat", "grok", "data: [DONE]\n\n", "[DONE]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			budget := newRuntimeBudget(t, 8*1024*1024)
			analyzer := newRuntimeAnalyzer(t, budget, AnalyzerOptions{ProbeDuration: time.Hour})
			observed := &responsefacts.CompletionObserver{}
			writer := &completionFailingWriter{httptest.NewRecorder()}
			mode, err := ObserveMode(BoundaryNoRetryCandidate)
			if err != nil {
				t.Fatal(err)
			}
			body := &completionReadCounter{Reader: strings.NewReader(tc.body)}
			response := analyzer.Start(t.Context(), StartInput{
				Mode: mode, APIType: tc.apiType, ContentType: "text/event-stream",
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}},
				Body: body, Writer: writer, ObserveCompletion: observed.Observe,
			})
			boundary := awaitAnalyzerBoundary(t, response)
			completion := awaitAnalyzerCompletion(t, boundary.Forwarding)
			if completion.Termination != TerminationClientWriteFailure {
				t.Fatalf("missing write failure: %+v", completion)
			}
			if body.reads != 1 {
				t.Fatalf("continued upstream reads after downstream failure: %d", body.reads)
			}
			if observed.Snapshot().EventType != tc.event {
				t.Fatalf("upstream event lost after write failure: %+v", observed.Snapshot())
			}
		})
	}
}
