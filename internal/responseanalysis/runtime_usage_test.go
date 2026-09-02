package responseanalysis

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalyzerCapturesUsageOrthogonallyAndOverlaysSSESamples(t *testing.T) {
	t.Parallel()
	sse, err := os.ReadFile(filepath.Join("testdata", "token-usage", "anthropic-message-usage.sse"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		apiType     string
		contentType string
		body        string
		wantPrompt  int64
		wantOutput  int64
		wantRead    int64
		wantWrite   int64
	}{
		{
			name: "client-visible JSON", apiType: "grok", contentType: "application/json",
			body:       `{"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`,
			wantPrompt: 2, wantOutput: 1,
		},
		{
			name: "error JSON", apiType: "grok", contentType: "application/json",
			body:       `{"error":{"message":"upstream rejected request"},"usage":{"prompt_tokens":3,"completion_tokens":0,"total_tokens":3}}`,
			wantPrompt: 3, wantOutput: 0,
		},
		{
			name: "control plus cumulative SSE", apiType: "claude", contentType: "text/event-stream",
			body: string(sse), wantPrompt: 10, wantOutput: 4, wantRead: 3, wantWrite: 2,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			budget := newRuntimeBudget(t, ResponseProbeMemoryBudget)
			analyzer := newRuntimeAnalyzer(t, budget, AnalyzerOptions{})
			mode, err := ObserveMode(BoundaryNoRetryCandidate)
			if err != nil {
				t.Fatal(err)
			}
			writer := httptest.NewRecorder()
			response := analyzer.Start(context.Background(), StartInput{
				Mode: mode, APIType: test.apiType, ContentType: test.contentType,
				StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(test.body)), Writer: writer,
			})
			boundary := awaitAnalyzerBoundary(t, response)
			if boundary.Forwarding == nil {
				t.Fatalf("boundary = %#v", boundary)
			}
			completion := awaitAnalyzerCompletion(t, boundary.Forwarding)
			if !completion.HasUsageObservation || completion.UsageObservation.Usage == nil {
				t.Fatalf("completion = %#v", completion)
			}
			usage := completion.UsageObservation.Usage
			if !usage.PromptTokens.Present || usage.PromptTokens.Value != test.wantPrompt ||
				!usage.CompletionTokens.Present || usage.CompletionTokens.Value != test.wantOutput {
				t.Fatalf("usage = %#v", usage)
			}
			if test.wantRead != 0 && (!usage.CacheReadInputTokens.Present || usage.CacheReadInputTokens.Value != test.wantRead) {
				t.Fatalf("cache read = %#v", usage.CacheReadInputTokens)
			}
			if test.wantWrite != 0 && (usage.CacheCreation == nil || !usage.CacheCreation.InputTokens.Present || usage.CacheCreation.InputTokens.Value != test.wantWrite) {
				t.Fatalf("cache creation = %#v", usage.CacheCreation)
			}
			if test.name == "control plus cumulative SSE" && usage.TotalTokens.Present {
				t.Fatalf("missing total was derived: %#v", usage.TotalTokens)
			}
		})
	}
}

func TestAnalyzerPreservesOrderedPartialUsageAcrossSSEQueueSaturation(t *testing.T) {
	t.Parallel()
	frames := []string{
		`data: {"choices":[],"usage":{"prompt_tokens":1}}`,
		`data: {"choices":[],"usage":{"prompt_tokens":2}}`,
		`data: {"choices":[],"usage":{"completion_tokens":3}}`,
		`data: {"choices":[],"usage":{"total_tokens":4}}`,
		`data: {"choices":[],"usage":{"completion_tokens":5}}`,
		`data: [DONE]`,
	}
	body := strings.Join(frames, "\n\n") + "\n\n"
	budget := newRuntimeBudget(t, ResponseProbeMemoryBudget)
	analyzer := newRuntimeAnalyzer(t, budget, AnalyzerOptions{})
	mode, err := ObserveMode(BoundaryNoRetryCandidate)
	if err != nil {
		t.Fatal(err)
	}
	response := analyzer.Start(context.Background(), StartInput{
		Mode: mode, APIType: "grok", ContentType: "text/event-stream",
		StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Writer: httptest.NewRecorder(),
	})
	boundary := awaitAnalyzerBoundary(t, response)
	completion := awaitAnalyzerCompletion(t, boundary.Forwarding)
	if !completion.HasUsageObservation || completion.UsageObservation.Usage == nil {
		t.Fatalf("completion = %#v", completion)
	}
	usage := completion.UsageObservation.Usage
	if usage.PromptTokens.Value != 2 || !usage.PromptTokens.Present ||
		usage.CompletionTokens.Value != 5 || !usage.CompletionTokens.Present ||
		usage.TotalTokens.Value != 4 || !usage.TotalTokens.Present {
		t.Fatalf("ordered partial usage = %#v", usage)
	}
}
