package proxy

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

func TestX3UpstreamEOFRetriesCurrentProviderWithoutReplacement(t *testing.T) {
	events := &x3EventLog{}
	primary := x3Provider("eof-primary")
	primary.MaxRetries = 1
	alternate := x3Provider("eof-alternate")
	lease := x3NewLease(primary, events)
	selection := &x3Selector{
		initial: primary, initialLease: lease, alternate: alternate, events: events,
	}
	transport := &x3ScriptedTransport{events: events, steps: []x3TransportStep{
		{disclosure: upstreamtransport.RequestDisclosurePossible, err: io.EOF},
		{disclosure: upstreamtransport.RequestDisclosurePossible, err: io.EOF},
	}}
	rules, err := errorrule.CompileRuleSet(63, nil)
	if err != nil {
		t.Fatal(err)
	}
	health := newX3Health()

	recorder, pctx := x3Execute(t, x3ExecutionConfig{
		providers: []*model.Provider{primary, alternate}, selector: selection, transport: transport,
		rules: &x3RuleProvider{current: rules}, analyzer: x3AnalyzerSpyForTest(t), health: health,
		stats: &x3RuleStats{}, globalMaxAttempts: 3,
	})

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status=%d, want %d", recorder.Code, http.StatusBadGateway)
	}
	if body := recorder.Body.String(); !strings.Contains(body, `"code":"UPSTREAM_NO_RESPONSE"`) {
		t.Fatalf("body=%q, want upstream no-response error", body)
	}
	if transport.Count() != 2 || len(pctx.attempts) != 2 {
		t.Fatalf("fetches=%d attempts=%d, want same-provider initial plus retry", transport.Count(), len(pctx.attempts))
	}
	if selection.LastAlternateRequest() != nil {
		t.Fatal("EOF exhaustion selected an alternate provider")
	}
	for index, attempt := range pctx.attempts {
		if attempt.ProviderID != primary.ID || attempt.ProviderAttempt != index+1 {
			t.Fatalf(
				"attempt[%d]=provider:%q provider_attempt:%d, want provider:%q provider_attempt:%d",
				index, attempt.ProviderID, attempt.ProviderAttempt, primary.ID, index+1,
			)
		}
	}
	if health.Failures(primary.ID) != 2 || health.Failures(alternate.ID) != 0 {
		t.Fatalf(
			"health failures primary=%d alternate=%d, want 2/0",
			health.Failures(primary.ID), health.Failures(alternate.ID),
		)
	}
	if lease.ReleaseCount() != 1 {
		t.Fatalf("primary lease releases=%d, want 1", lease.ReleaseCount())
	}
}
