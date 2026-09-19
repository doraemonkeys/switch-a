package proxy

import (
	"bytes"
	"io"
	"net/http"
	"testing"
	"testing/iotest"

	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis"
)

func TestSSEBOMErrorRetriesBeforeClientVisibility(t *testing.T) {
	events := &x3EventLog{}
	provider := x3Provider("p1")
	lease := x3NewLease(provider, events)
	selector := &x3Selector{initial: provider, initialLease: lease, events: events}
	firstWire := []byte("\ufeffdata: {\"type\":\"error\",\"code\":\"busy\",\"message\":\"retry-me\"}\n\n")
	secondWire := []byte(`{"id":"successful-retry"}`)
	// Network reads may split every BOM byte without changing the retry decision.
	firstBody := io.NopCloser(iotest.OneByteReader(bytes.NewReader(firstWire)))
	secondBody := x3NewTrackedBody(secondWire, "close:second", events)
	transport := &x3ScriptedTransport{events: events, steps: []x3TransportStep{
		x3HTTPResponseStep(http.StatusOK, "text/event-stream", "", firstBody, len(firstWire)),
		x3HTTPResponseStep(http.StatusOK, "application/json", "", secondBody, len(secondWire)),
	}}
	rules := x3CompiledRuleSet(t, 11, x3RetryOnlyAction(t, 1), "retry-me")
	recorder, pctx := x3Execute(t, x3ExecutionConfig{
		providers: []*model.Provider{provider}, selector: selector, transport: transport,
		rules: &x3RuleProvider{current: rules}, analyzer: x3AnalyzerSpyForTest(t), health: newX3Health(),
		stats: &x3RuleStats{}, backoff: x3Backoff{events: events}, globalMaxAttempts: 2,
	})
	if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), secondWire) {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.Bytes())
	}
	if transport.Count() != 2 || len(pctx.attempts) != 2 {
		t.Fatalf("fetch=%d attempts=%d", transport.Count(), len(pctx.attempts))
	}
	semantic := decodeSemanticAttemptEvidence(t, pctx.attempts[0].AttemptEvidenceJSON)
	if semantic.Response.BoundaryReason != responseanalysis.BoundarySemanticMatch ||
		semantic.Response.VisibleToClient || semantic.Response.ClientBodyBytesWritten != "0" ||
		semantic.Decision.Value != errorrule.DecisionRetrySame {
		t.Fatalf("semantic retry evidence=%#v", semantic)
	}
}
