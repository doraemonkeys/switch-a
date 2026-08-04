package proxy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/attemptevidence"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestE4SemanticRetryPersistsAttemptEvidenceAndExplicitAxes(t *testing.T) {
	events := &x3EventLog{}
	provider := x3Provider("p1")
	lease := x3NewLease(provider, events)
	selector := &x3Selector{initial: provider, initialLease: lease, events: events}
	semanticWire := []byte("event: error\r\ndata: {\"type\":\"error\",\"code\":\"busy\",\"message\":\"retry-me\"}\r\n\r\n")
	successWire := []byte(`{"id":"success"}`)
	transport := &x3ScriptedTransport{events: events, steps: []x3TransportStep{
		x3HTTPResponseStep(http.StatusOK, "text/event-stream", "", x3NewBlockingBody(semanticWire, "close:semantic", events), len(semanticWire)),
		x3HTTPResponseStep(http.StatusOK, "application/json", "", x3NewTrackedBody(successWire, "close:success", events), len(successWire)),
	}}
	rules := x3CompiledRuleSet(t, 71, x3RetryOnlyAction(t, 1), "retry-me")
	_, pctx := x3Execute(t, x3ExecutionConfig{
		providers: []*model.Provider{provider}, selector: selector, transport: transport,
		rules: &x3RuleProvider{current: rules}, analyzer: x3AnalyzerSpyForTest(t), health: newX3Health(),
		stats: &x3RuleStats{}, globalMaxAttempts: 2,
	})
	if len(pctx.attempts) != 2 {
		t.Fatalf("attempts = %d", len(pctx.attempts))
	}
	absorbed := pctx.attempts[0]
	assertAttemptAxes(t, absorbed, model.RequestAttemptOutcomeUpstreamSemanticError, false, nil,
		model.RequestAttemptHealthNeutral, model.RequestAttemptHealthCauseSemanticNeutral)
	semantic := decodeSemanticAttemptEvidence(t, absorbed.AttemptEvidenceJSON)
	if semantic.Identity.LogicalAttempt != "1" || semantic.Identity.ProviderAttempt != "1" ||
		semantic.Identity.CredentialPhase != attemptevidence.CredentialPhasePrimary {
		t.Fatalf("identity = %#v", semantic.Identity)
	}
	if semantic.Response.State != attemptevidence.ResponseStateDiscarded ||
		semantic.Response.MatchTiming != attemptevidence.MatchTimingProbing ||
		semantic.Response.BoundaryReason != "semantic_match" ||
		semantic.Response.HeadersCommitted || semantic.Response.VisibleToClient ||
		semantic.Response.ClientBodyBytesWritten != "0" {
		t.Fatalf("response = %#v", semantic.Response)
	}
	if mustDecimal(t, semantic.Response.UpstreamBytesRead) == 0 ||
		mustDecimal(t, semantic.Response.DecodedProbeBytes) == 0 ||
		mustDecimal(t, semantic.Response.PeakProbeBytes) == 0 {
		t.Fatalf("response metrics = %#v", semantic.Response)
	}
	if semantic.Rule.Revision != "71" || semantic.Decision.Value != errorrule.DecisionRetrySame ||
		semantic.Decision.Reason != errorrule.ReasonRetryBudgetAvailable ||
		semantic.Retry.GlobalAttemptsStarted != "1" || semantic.Retry.GlobalAttemptsRemaining == nil ||
		*semantic.Retry.GlobalAttemptsRemaining != "1" || semantic.Retry.RuleRetriesScheduled != "0" ||
		semantic.Retry.RuleRetryLimit != 1 || semantic.Alternate.Outcome != attemptevidence.AlternateNotRequested {
		t.Fatalf("decision/retry = decision:%#v retry:%#v alternate:%#v", semantic.Decision, semantic.Retry, semantic.Alternate)
	}

	completed := pctx.attempts[1]
	clientStatus := http.StatusOK
	assertAttemptAxes(t, completed, model.RequestAttemptOutcomeUpstreamCompleted, true, &clientStatus,
		model.RequestAttemptHealthSuccess, model.RequestAttemptHealthCauseNormalCompletion)
}

func TestE4SwitchEvidenceAndTraceRepeatBoundedContext(t *testing.T) {
	core, observed := observer.New(zap.DebugLevel)
	logger := zap.New(core)
	events := &x3EventLog{}
	primary := x3Provider("p1")
	alternate := x3Provider("p2")
	health := newX3Health()
	health.openOnFailure = true
	selector := &x3Selector{
		initial: primary, initialLease: x3NewLease(primary, events), alternate: alternate, events: events,
	}
	semanticWire := []byte("event: error\r\ndata: {\"type\":\"error\",\"code\":\"busy\",\"message\":\"switch-secret-value\"}\r\n\r\n")
	successWire := []byte(`{"id":"alternate"}`)
	transport := &x3ScriptedTransport{events: events, steps: []x3TransportStep{
		x3HTTPResponseStep(http.StatusOK, "text/event-stream", "", x3NewBlockingBody(semanticWire, "close:primary", events), len(semanticWire)),
		x3HTTPResponseStep(http.StatusOK, "application/json", "", x3NewTrackedBody(successWire, "close:alternate", events), len(successWire)),
	}}
	rules := x3CompiledRuleSet(t, 72, x3RetryThenSwitchAction(t, 3), "switch-secret-value")
	_, pctx := x3Execute(t, x3ExecutionConfig{
		providers: []*model.Provider{primary, alternate}, selector: selector, transport: transport,
		rules: &x3RuleProvider{current: rules}, analyzer: x3AnalyzerSpyForTest(t), health: health,
		stats: &x3RuleStats{}, globalMaxAttempts: 2, logger: logger,
	})
	semantic := decodeSemanticAttemptEvidence(t, pctx.attempts[0].AttemptEvidenceJSON)
	if semantic.Decision.Value != errorrule.DecisionSwitchProvider ||
		semantic.Alternate.Outcome != attemptevidence.AlternateActivated ||
		semantic.Alternate.ProviderID == nil || *semantic.Alternate.ProviderID != "p2" ||
		semantic.Alternate.SwitchMode == nil || *semantic.Alternate.SwitchMode != attemptevidence.SwitchModeReplacement ||
		semantic.Alternate.SwitchReason == nil || *semantic.Alternate.SwitchReason != errorrule.SwitchReasonRuleExhausted ||
		!semantic.Health.CircuitOpened {
		t.Fatalf("semantic switch evidence = %#v", semantic)
	}
	assertAttemptAxes(t, pctx.attempts[0], model.RequestAttemptOutcomeUpstreamSemanticError, false, nil,
		model.RequestAttemptHealthFailure, model.RequestAttemptHealthCauseSemanticRetryThenSwitch)

	wantNames := []string{
		string(attemptevidence.MilestoneProbeReleased), string(attemptevidence.MilestoneRuleMatched),
		string(attemptevidence.MilestoneDecision), string(attemptevidence.MilestoneResponseFinalized),
		string(attemptevidence.MilestoneHealthVerdict),
	}
	entries := observed.All()
	filtered := entries[:0]
	for _, entry := range entries {
		if strings.HasPrefix(entry.Message, "internal_error.") {
			filtered = append(filtered, entry)
		}
	}
	entries = filtered
	if len(entries) != len(wantNames) {
		t.Fatalf("internal-error trace count = %d, entries %#v", len(entries), entries)
	}
	required := []string{
		"request_id", "operation_id", "provider_id", "logical_attempt", "provider_attempt",
		"rule_revision", "protocol_id", "rule_id", "decision", "decision_reason",
		"response_state", "boundary_reason", "elapsed_ms", "peak_probe_bytes",
		"peak_process_bytes",
		"raw_probe_bytes", "decoded_probe_bytes", "upstream_bytes_read",
		"client_body_bytes_written", "headers_committed", "visible_to_client",
		"global_attempts_started", "global_attempts_remaining", "global_attempts_unlimited",
		"rule_retries_scheduled", "rule_retry_limit", "health_verdict", "health_cause", "circuit_opened",
	}
	for index, entry := range entries {
		if entry.Message != wantNames[index] {
			t.Fatalf("trace[%d] = %q, want %q", index, entry.Message, wantNames[index])
		}
		context := entry.ContextMap()
		for _, key := range required {
			if _, ok := context[key]; !ok {
				t.Fatalf("trace %q lacks %q: %#v", entry.Message, key, context)
			}
		}
		encoded, _ := json.Marshal(context)
		if bytes.Contains(encoded, []byte("switch-secret-value")) || bytes.Contains(encoded, []byte("event: error")) {
			t.Fatalf("trace leaked upstream values: %s", encoded)
		}
	}
}

func TestE4CaptureDistinguishesAbsorbedAndCommittedSemanticResponses(t *testing.T) {
	t.Run("absorbed", func(t *testing.T) {
		manager, session := startCaptureTestManager(t, []requestcapture.ProviderIdentity{{ID: "p1", Name: "provider-p1"}})
		defer manager.Close()
		events := &x3EventLog{}
		provider := x3Provider("p1")
		selector := &x3Selector{initial: provider, initialLease: x3NewLease(provider, events), events: events}
		semanticWire := []byte("event: error\r\ndata: {\"type\":\"error\",\"code\":\"busy\",\"message\":\"retry-me\"}\r\n\r\n")
		successWire := []byte(`{"id":"success"}`)
		transport := &x3ScriptedTransport{events: events, steps: []x3TransportStep{
			x3HTTPResponseStep(http.StatusOK, "text/event-stream", "", x3NewBlockingBody(semanticWire, "close:absorbed", events), len(semanticWire)),
			x3HTTPResponseStep(http.StatusOK, "application/json", "", x3NewTrackedBody(successWire, "close:success", events), len(successWire)),
		}}
		x3Execute(t, x3ExecutionConfig{
			providers: []*model.Provider{provider}, selector: selector, transport: transport,
			rules:    &x3RuleProvider{current: x3CompiledRuleSet(t, 73, x3RetryOnlyAction(t, 1), "retry-me")},
			analyzer: x3AnalyzerSpyForTest(t), health: newX3Health(), stats: &x3RuleStats{},
			globalMaxAttempts: 2, capture: manager,
		})
		page, err := readCaptureTestPage(manager, session, requestcapture.ListQuery{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Records) != 2 {
			t.Fatalf("records = %#v", page.Records)
		}
		sort.Slice(page.Records, func(i, j int) bool { return page.Records[i].ExchangeIndex < page.Records[j].ExchangeIndex })
		absorbed := page.Records[0]
		if absorbed.TerminationReason != requestcapture.TerminationReasonInternalErrorAbsorbed ||
			absorbed.UpstreamObservedBytes != int64(len(semanticWire)) || absorbed.ApplicationWriteConfirmedBytes != 0 ||
			!absorbed.HasFailure || absorbed.Failure.Primary.Class != requestcapture.FailureClassUpstreamSemantic ||
			absorbed.Failure.Primary.Code != requestcapture.FailureCodeProviderSemantic {
			t.Fatalf("absorbed capture = %#v", absorbed)
		}
	})

	t.Run("committed", func(t *testing.T) {
		manager, session := startCaptureTestManager(t, []requestcapture.ProviderIdentity{{ID: "p1", Name: "provider-p1"}})
		defer manager.Close()
		events := &x3EventLog{}
		provider := x3Provider("p1")
		wire := []byte(`{"type":"error","error":{"message":"pass-me"}}`)
		transport := &x3ScriptedTransport{events: events, steps: []x3TransportStep{
			x3HTTPResponseStep(http.StatusOK, "application/json", "", x3NewTrackedBody(wire, "close:committed", events), len(wire)),
		}}
		_, pctx := x3Execute(t, x3ExecutionConfig{
			providers: []*model.Provider{provider}, selector: &x3Selector{initial: provider, initialLease: x3NewLease(provider, events), events: events},
			transport: transport,
			rules:     &x3RuleProvider{current: x3CompiledRuleSet(t, 74, errorrule.NewPassthroughAction(), "pass-me")},
			analyzer:  x3AnalyzerSpyForTest(t), health: newX3Health(), stats: &x3RuleStats{},
			globalMaxAttempts: 1, capture: manager,
		})
		semantic := decodeSemanticAttemptEvidence(t, pctx.attempts[0].AttemptEvidenceJSON)
		if semantic.Decision.Value != errorrule.DecisionPassthrough || semantic.Decision.Reason != errorrule.ReasonActionPassthrough {
			t.Fatalf("decision = %#v", semantic.Decision)
		}
		page, err := readCaptureTestPage(manager, session, requestcapture.ListQuery{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Records) != 1 {
			t.Fatalf("records = %#v", page.Records)
		}
		committed := page.Records[0]
		if committed.TerminationReason != requestcapture.TerminationReasonInternalErrorCommitted ||
			committed.UpstreamObservedBytes != int64(len(wire)) || committed.ApplicationWriteConfirmedBytes != int64(len(wire)) ||
			!committed.HasFailure || committed.Failure.Primary.Class != requestcapture.FailureClassUpstreamSemantic ||
			committed.Failure.Primary.Code != requestcapture.FailureCodeProviderSemantic {
			t.Fatalf("committed capture = %#v", committed)
		}
	})
}

func TestE4HTTPCaptureFinalizesBeforeLeaseRelease(t *testing.T) {
	manager, session := startCaptureTestManager(t, []requestcapture.ProviderIdentity{{ID: "p1", Name: "provider-p1"}})
	defer manager.Close()
	events := &x3EventLog{}
	provider := x3Provider("p1")
	lease := x3NewLease(provider, events)
	releaseObserved := false
	releaseObservation := "lease release was not observed"
	lease.releaseObserver = func() {
		releaseObserved = true
		page, err := readCaptureTestPage(manager, session, requestcapture.ListQuery{Limit: 10})
		if err != nil {
			releaseObservation = "capture query failed at lease release: " + err.Error()
			return
		}
		if len(page.Records) != 1 {
			releaseObservation = "capture record was not published before lease release"
			return
		}
		record := page.Records[0]
		if record.LifecycleState != requestcapture.LifecycleStateCompleted || record.CompletedAt == nil {
			releaseObservation = "capture record was not finalized before lease release"
			return
		}
		releaseObservation = ""
	}
	wire := []byte(`{"id":"success"}`)
	transport := &x3ScriptedTransport{events: events, steps: []x3TransportStep{
		x3HTTPResponseStep(http.StatusOK, "application/json", "", x3NewTrackedBody(wire, "close:success", events), len(wire)),
	}}
	x3Execute(t, x3ExecutionConfig{
		providers:         []*model.Provider{provider},
		selector:          &x3Selector{initial: provider, initialLease: lease, events: events},
		transport:         transport,
		rules:             &x3RuleProvider{current: x3CompiledRuleSet(t, 75, errorrule.NewPassthroughAction(), "never-match")},
		analyzer:          x3AnalyzerSpyForTest(t),
		health:            newX3Health(),
		stats:             &x3RuleStats{},
		globalMaxAttempts: 1,
		capture:           manager,
	})
	if !releaseObserved || releaseObservation != "" {
		t.Fatalf("release observation = %q, observed = %t", releaseObservation, releaseObserved)
	}
}

func TestE4AttemptAxisPrecedenceAndHealthFallback(t *testing.T) {
	semantic := &semanticAttemptFacts{}
	if got := classifyHTTPAttemptOutcome(forwardResult{
		semantic: semantic, failureKind: attemptFailureRead,
	}); got != model.RequestAttemptOutcomeUpstreamTransportError {
		t.Fatalf("late semantic transport outcome = %q", got)
	}
	attempt := model.RequestAttempt{}
	applyHTTPAttemptAxes(&attempt, forwardResult{})
	if attempt.HealthVerdict == nil || *attempt.HealthVerdict != model.RequestAttemptHealthNeutral ||
		attempt.HealthCause == nil || *attempt.HealthCause != model.RequestAttemptHealthCauseIncomplete {
		t.Fatalf("fallback health axes = %#v", attempt)
	}
}

func decodeSemanticAttemptEvidence(t *testing.T, encoded *string) attemptevidence.SemanticError {
	t.Helper()
	if encoded == nil {
		t.Fatal("attempt evidence is nil")
	}
	if len(*encoded) > attemptevidence.MaxAttemptEvidenceBytes {
		t.Fatalf("attempt evidence = %d bytes", len(*encoded))
	}
	var envelope struct {
		Version       int                            `json:"v"`
		SemanticError *attemptevidence.SemanticError `json:"semantic_error"`
	}
	if err := json.Unmarshal([]byte(*encoded), &envelope); err != nil {
		t.Fatalf("decode attempt evidence: %v", err)
	}
	if envelope.Version != attemptevidence.EnvelopeVersion || envelope.SemanticError == nil {
		t.Fatalf("envelope = %#v", envelope)
	}
	return *envelope.SemanticError
}

func assertAttemptAxes(
	t *testing.T,
	attempt model.RequestAttempt,
	wantOutcome model.RequestAttemptOutcome,
	wantVisible bool,
	wantClientStatus *int,
	wantVerdict model.RequestAttemptHealthVerdict,
	wantCause model.RequestAttemptHealthCause,
) {
	t.Helper()
	if attempt.Outcome == nil || *attempt.Outcome != wantOutcome ||
		attempt.ResultVisibleToClient == nil || *attempt.ResultVisibleToClient != wantVisible ||
		attempt.HealthVerdict == nil || *attempt.HealthVerdict != wantVerdict ||
		attempt.HealthCause == nil || *attempt.HealthCause != wantCause {
		t.Fatalf("attempt axes = %#v", attempt)
	}
	if wantClientStatus == nil {
		if attempt.ClientTransportStatusCode != nil {
			t.Fatalf("client status = %v, want nil", attempt.ClientTransportStatusCode)
		}
	} else if attempt.ClientTransportStatusCode == nil || *attempt.ClientTransportStatusCode != *wantClientStatus {
		t.Fatalf("client status = %v, want %d", attempt.ClientTransportStatusCode, *wantClientStatus)
	}
}

func mustDecimal(t *testing.T, value string) uint64 {
	t.Helper()
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		t.Fatalf("parse decimal %q: %v", value, err)
	}
	return parsed
}
