package codexhttp

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
)

func TestSSEGatePreparesResponseReferenceAcrossFragments(t *testing.T) {
	gate, continuity := testSSEGate(t)
	raw := []byte("event: response.created\r\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"response-ref\"}}\r\n\r\n")
	split := len(raw) - 3
	gate.Append(raw[:split])
	if event, ready, err := gate.PrepareNext(context.Background(), false); err != nil || ready {
		t.Fatalf("incomplete event = (%#v, %t, %v)", event, ready, err)
	}
	if len(continuity.prepareCalls) != 0 || continuity.commitCalls != 0 {
		t.Fatalf("incomplete prepare/commit = %d/%d", len(continuity.prepareCalls), continuity.commitCalls)
	}

	gate.Append(raw[split:])
	event, ready, err := gate.PrepareNext(context.Background(), false)
	if err != nil || !ready {
		t.Fatalf("complete event = (%#v, %t, %v)", event, ready, err)
	}
	if !bytes.Equal(event.ReplayBytes(), raw) {
		t.Fatal("SSE gate changed response wire bytes")
	}
	if len(continuity.prepareCalls) != 1 || continuity.commitCalls != 0 {
		t.Fatalf("pending prepare/commit = %d/%d", len(continuity.prepareCalls), continuity.commitCalls)
	}
	if err := event.Visibility().Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if continuity.commitCalls != 1 {
		t.Fatalf("visible commit calls = %d, want 1", continuity.commitCalls)
	}
	gate.Consume(len(event.ReplayBytes()))
	if gate.BufferedBytes() != 0 {
		t.Fatalf("buffered bytes = %d, want 0", gate.BufferedBytes())
	}
}

func TestSSEGateDiscardDoesNotBindResponseReference(t *testing.T) {
	gate, continuity := testSSEGate(t)
	raw := []byte("data: {\"type\":\"response.metadata\",\"response_id\":\"metadata-ref\"}\n\n")
	gate.Append(raw)
	event, ready, err := gate.PrepareNext(context.Background(), false)
	if err != nil || !ready || event.Visibility() == nil {
		t.Fatalf("prepared event = (%#v, %t, %v)", event, ready, err)
	}
	gate.Discard()
	if continuity.commitCalls != 0 {
		t.Fatalf("discard committed response reference %d times", continuity.commitCalls)
	}
}

func testSSEGate(t *testing.T) (*SSEGate, *continuityRecorder) {
	t.Helper()
	clientScope := testClientScope(t, "sse-client")
	candidate, applied := testCandidate(t, "route-sse", "provider.test", "sse-subject")
	continuity := &continuityRecorder{
		resolveErr:  &codexcontinuity.Error{Kind: codexcontinuity.ErrorUnknown},
		validateErr: &codexcontinuity.Error{Kind: codexcontinuity.ErrorUnknown},
	}
	runtime := newAlwaysOnTestRuntime(t, Config{
		ClientScopes: testScopeDigester{
			current:    clientScope,
			candidates: []codexidentity.ClientScope{clientScope},
		},
		Continuity: continuity,
	})
	request := httptest.NewRequest(http.MethodPost, "http://gateway.test/codex/v1/responses", nil)
	request.Header.Set("Authorization", "Bearer client-secret")
	request.Header.Set("Thread-Id", "sse-request-anchor")
	operation, err := runtime.Begin(context.Background(), request, codexAPIType, "sse-operation", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewRequest(http.MethodPost, "https://provider.test/v1/responses", nil)
	attempt, err := operation.PrepareAttempt(context.Background(), upstream, candidate, applied)
	if err != nil {
		t.Fatal(err)
	}
	gate := attempt.NewSSEGate()
	if gate == nil {
		t.Fatal("continuity-enabled attempt did not create an SSE gate")
	}
	return gate, continuity
}
