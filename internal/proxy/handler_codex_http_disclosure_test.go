package proxy

import (
	"bytes"
	"errors"
	"net/http"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

func TestCodexHTTPTransportDisclosureControlsCrossAuthorityReplacement(t *testing.T) {
	rules, err := errorrule.CompileRuleSet(70, nil)
	if err != nil {
		t.Fatal(err)
	}
	requestHeaders := http.Header{"Thread-Id": []string{"new-thread"}}

	t.Run("definitely undisclosed failure releases provisional authority", func(t *testing.T) {
		events := &x3EventLog{}
		primary := x3Provider("pre-disclosure-primary")
		alternate := x3Provider("pre-disclosure-alternate")
		selection := &x3Selector{
			initial: primary, initialLease: x3NewLease(primary, events), alternate: alternate, events: events,
		}
		accepted := []byte(`{"id":"accepted-after-replacement"}`)
		acceptedBody := x3NewTrackedBody(accepted, "close:accepted-replacement", events)
		transport := &x3ScriptedTransport{events: events, steps: []x3TransportStep{
			{disclosure: upstreamtransport.RequestDisclosureNone, err: errors.New("dial refused before request write")},
			x3HTTPResponseStep(http.StatusOK, "application/json", "", acceptedBody, len(accepted)),
		}}

		recorder, pctx := x3Execute(t, x3ExecutionConfig{
			providers: []*model.Provider{primary, alternate}, selector: selection, transport: transport,
			requestHeaders: requestHeaders, rules: &x3RuleProvider{current: rules},
			analyzer: x3AnalyzerSpyForTest(t), health: newX3Health(), stats: &x3RuleStats{}, globalMaxAttempts: 2,
		})

		if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), accepted) {
			t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.Bytes())
		}
		alternateRequest := selection.LastAlternateRequest()
		if alternateRequest == nil || alternateRequest.RequiredAuthority != nil {
			t.Fatalf("pre-disclosure alternate request authority = %v, want nil", alternateRequest)
		}
		required, _ := pctx.codex.RequiredAuthority()
		alternateCandidate, _ := selection.Reservation().lease.CandidateSnapshot()
		if required == nil || !required.Equal(alternateCandidate.Authority()) {
			t.Fatalf("committed authority = %v, want alternate authority", required)
		}
		if transport.Count() != 2 || len(pctx.attempts) != 2 {
			t.Fatalf("fetches=%d attempts=%d, want 2/2", transport.Count(), len(pctx.attempts))
		}
	})

	t.Run("possible disclosure preserves authority", func(t *testing.T) {
		events := &x3EventLog{}
		primary := x3Provider("possible-primary")
		alternate := x3Provider("possible-alternate")
		selection := &x3Selector{
			initial: primary, initialLease: x3NewLease(primary, events), alternate: alternate, events: events,
		}
		transport := &x3ScriptedTransport{events: events, steps: []x3TransportStep{
			{disclosure: upstreamtransport.RequestDisclosurePossible, err: errors.New("request write may have been partial")},
		}}

		recorder, pctx := x3Execute(t, x3ExecutionConfig{
			providers: []*model.Provider{primary, alternate}, selector: selection, transport: transport,
			requestHeaders: requestHeaders, rules: &x3RuleProvider{current: rules},
			analyzer: x3AnalyzerSpyForTest(t), health: newX3Health(), stats: &x3RuleStats{}, globalMaxAttempts: 2,
		})

		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d, want %d", recorder.Code, http.StatusServiceUnavailable)
		}
		alternateRequest := selection.LastAlternateRequest()
		primaryCandidate, _ := selection.initialLease.CandidateSnapshot()
		if alternateRequest == nil || alternateRequest.RequiredAuthority == nil ||
			!alternateRequest.RequiredAuthority.Equal(primaryCandidate.Authority()) {
			t.Fatalf("possible-disclosure alternate request authority = %v, want primary", alternateRequest)
		}
		required, _ := pctx.codex.RequiredAuthority()
		if required == nil || !required.Equal(primaryCandidate.Authority()) {
			t.Fatalf("operation authority = %v, want primary", required)
		}
		if transport.Count() != 1 {
			t.Fatalf("transport count = %d, cross-authority request reached transport", transport.Count())
		}
	})
}
