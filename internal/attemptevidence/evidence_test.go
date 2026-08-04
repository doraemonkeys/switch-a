package attemptevidence

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestEncodeMatchesSharedGoldenFixture(t *testing.T) {
	fixtureBytes, err := os.ReadFile(filepath.Join("..", "..", "contracts", "internal-error", "v1", "attempt-evidence-v2.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fixture struct {
		Cases []struct {
			Name     string          `json:"name"`
			Evidence json.RawMessage `json:"evidence"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(fixtureBytes, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if len(fixture.Cases) != 6 {
		t.Fatalf("fixture cases = %d, want 6", len(fixture.Cases))
	}
	for _, testCase := range fixture.Cases {
		t.Run(testCase.Name, func(t *testing.T) {
			var envelope map[string]json.RawMessage
			if err := json.Unmarshal(testCase.Evidence, &envelope); err != nil {
				t.Fatalf("decode envelope: %v", err)
			}
			var semantic SemanticError
			if err := json.Unmarshal(envelope["semantic_error"], &semantic); err != nil {
				t.Fatalf("decode semantic evidence: %v", err)
			}
			delete(envelope, "semantic_error")
			existing, err := json.Marshal(envelope)
			if err != nil {
				t.Fatalf("encode existing siblings: %v", err)
			}
			got, err := Encode(existing, &semantic)
			if err != nil {
				t.Fatalf("Encode() error = %v", err)
			}
			assertJSONEqual(t, got, testCase.Evidence)
			if len(got) > MaxAttemptEvidenceBytes {
				t.Fatalf("encoded evidence = %d bytes, limit %d", len(got), MaxAttemptEvidenceBytes)
			}
		})
	}
}

func TestEncodeEnvelopePolicies(t *testing.T) {
	semantic, err := NewSemanticError(validFacts(t))
	if err != nil {
		t.Fatalf("NewSemanticError() error = %v", err)
	}
	t.Run("unknown siblings and HTML remain intact", func(t *testing.T) {
		existing := []byte(`{"v":2,"transport":{"raw_error_snippet":"safe <tag>"},"future":{"count":7}}`)
		got, err := Encode(existing, &semantic)
		if err != nil {
			t.Fatalf("Encode() error = %v", err)
		}
		if strings.Contains(string(got), `\u003c`) || !strings.Contains(string(got), `<tag>`) {
			t.Fatalf("HTML escaping changed evidence: %s", got)
		}
		var envelope map[string]any
		if err := json.Unmarshal(got, &envelope); err != nil {
			t.Fatalf("decode result: %v", err)
		}
		if _, ok := envelope["future"]; !ok {
			t.Fatal("unknown sibling was removed")
		}
		for _, key := range append(envelopeSiblingKeys[:], "semantic_error") {
			if _, ok := envelope[key]; !ok {
				t.Fatalf("required sibling %q is absent", key)
			}
		}
	})

	tests := []struct {
		name     string
		existing []byte
		wantText string
	}{
		{name: "malformed", existing: []byte(`{`), wantText: "decode existing"},
		{name: "wrong version", existing: []byte(`{"v":1}`), wantText: "must use v2"},
		{name: "missing version", existing: []byte(`{"transport":null}`), wantText: "must use v2"},
		{name: "multiple values", existing: []byte(`{"v":2} {"v":2}`), wantText: "multiple JSON values"},
		{name: "trailing malformed", existing: []byte(`{"v":2} x`), wantText: "decode trailing"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := Encode(testCase.existing, &semantic)
			if err == nil || !strings.Contains(err.Error(), testCase.wantText) {
				t.Fatalf("Encode() error = %v, want containing %q", err, testCase.wantText)
			}
		})
	}

	t.Run("empty", func(t *testing.T) {
		got, err := Encode(nil, nil)
		if err != nil || got != nil {
			t.Fatalf("Encode(nil, nil) = (%q, %v)", got, err)
		}
		gotString, err := EncodeString(nil, nil)
		if err != nil || gotString != nil {
			t.Fatalf("EncodeString(nil, nil) = (%v, %v)", gotString, err)
		}
	})

	t.Run("limit", func(t *testing.T) {
		existing := []byte(`{"v":2,"future":"` + strings.Repeat("x", MaxAttemptEvidenceBytes) + `"}`)
		_, err := Encode(existing, nil)
		if !errors.Is(err, ErrEvidenceTooLarge) {
			t.Fatalf("Encode() error = %v, want ErrEvidenceTooLarge", err)
		}
	})
}

func TestSanitizeSnippet(t *testing.T) {
	input := "  Authorization: Bearer secret-token; x-api-key=another Cookie: session=abc  "
	got := SanitizeSnippet(input)
	if strings.Contains(got, "secret-token") || strings.Contains(got, "another") || strings.Contains(got, "session=abc") {
		t.Fatalf("secret survived redaction: %q", got)
	}
	if !strings.Contains(got, RedactedPlaceholder) {
		t.Fatalf("redaction marker absent: %q", got)
	}
	if got := SanitizeSnippet(" \t\n "); got != "" {
		t.Fatalf("blank sanitize = %q", got)
	}
	unicode := strings.Repeat("界", SnippetLimitBytes)
	got = SanitizeSnippet(unicode)
	if len(got) > SnippetLimitBytes || !json.Valid([]byte(`"`+got+`"`)) {
		t.Fatalf("UTF-8 truncation produced %d invalid bytes", len(got))
	}
	if got := truncateUTF8("value", 0); got != "" {
		t.Fatalf("truncate zero = %q", got)
	}
}

func TestNewSemanticErrorBuildsBoundedSanitizedValue(t *testing.T) {
	facts := validFacts(t)
	facts.Rule.Winner.Rule.Name = "Authorization: secret"
	facts.Rule.Winner.Rule.Keywords[0] = "Bearer raw-token"
	facts.Rule.Winner.MatchedKeywords[0] = "Bearer raw-token"
	facts.Rule.Matches[0] = facts.Rule.Winner
	semantic, err := NewSemanticError(facts)
	if err != nil {
		t.Fatalf("NewSemanticError() error = %v", err)
	}
	encoded, err := Encode(nil, &semantic)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "raw-token") {
		t.Fatalf("semantic evidence leaked sanitized values: %s", encoded)
	}
	if semantic.Retry.GlobalAttemptsRemaining == nil || *semantic.Retry.GlobalAttemptsRemaining != "3" {
		t.Fatalf("remaining attempts = %v", semantic.Retry.GlobalAttemptsRemaining)
	}
	if events := TraceEvents(semantic); len(events) != 5 || events[0].Name != MilestoneProbeReleased || events[4].Name != MilestoneHealthVerdict {
		t.Fatalf("trace events = %#v", events)
	}
}

func TestNewSemanticErrorRejectsInvalidFacts(t *testing.T) {
	invalid := []struct {
		name   string
		mutate func(*Facts)
	}{
		{name: "request ID absent", mutate: func(f *Facts) { f.Identity.RequestID = "" }},
		{name: "operation ID too long", mutate: func(f *Facts) { f.Identity.OperationID = strings.Repeat("x", MaxIdentityBytes+1) }},
		{name: "attempt zero", mutate: func(f *Facts) { f.Identity.LogicalAttempt = 0 }},
		{name: "credential phase", mutate: func(f *Facts) { f.Identity.CredentialPhase = "rotated" }},
		{name: "protocol absent", mutate: func(f *Facts) { f.Response.ProtocolID = "" }},
		{name: "protocol unknown", mutate: func(f *Facts) { f.Response.ProtocolID = "future.protocol" }},
		{name: "response state", mutate: func(f *Facts) { f.Response.State = "held" }},
		{name: "match timing", mutate: func(f *Facts) { f.Response.MatchTiming = "late" }},
		{name: "boundary", mutate: func(f *Facts) { f.Response.BoundaryReason = "invented" }},
		{name: "visible without headers", mutate: func(f *Facts) { f.Response.HeadersCommitted = false }},
		{name: "discarded visible", mutate: func(f *Facts) { f.Response.State = ResponseStateDiscarded }},
		{name: "revision", mutate: func(f *Facts) { f.Rule.Revision = -1 }},
		{name: "winner ID", mutate: func(f *Facts) { f.Rule.Winner.Rule.ID = "invalid" }},
		{name: "winner action", mutate: func(f *Facts) { f.Rule.Winner.Rule.Action = errorrule.Action{} }},
		{name: "matches absent", mutate: func(f *Facts) { f.Rule.Matches = nil }},
		{name: "matches over bound", mutate: func(f *Facts) { f.Rule.Matches = make([]errorrule.RuleMatch, errorrule.MaxRuleCount+1) }},
		{name: "winner not first", mutate: func(f *Facts) { f.Rule.Matches[0].Rule.ID = "22222222-2222-4222-8222-222222222222" }},
		{name: "invalid matching ID", mutate: func(f *Facts) {
			f.Rule.Matches = append(f.Rule.Matches, errorrule.RuleMatch{Rule: errorrule.Rule{ID: "bad"}})
		}},
		{name: "matched keywords absent", mutate: func(f *Facts) { f.Rule.Winner.MatchedKeywords = nil }},
		{name: "matched keywords over bound", mutate: func(f *Facts) { f.Rule.Winner.MatchedKeywords = make([]string, errorrule.MaxKeywordsPerRule+1) }},
		{name: "indexes misaligned", mutate: func(f *Facts) { f.Rule.Winner.MatchedKeywordIndexes = nil }},
		{name: "indexes duplicate", mutate: func(f *Facts) {
			f.Rule.Winner.MatchedKeywords = []string{"a", "b"}
			f.Rule.Winner.MatchedKeywordIndexes = []int{0, 0}
		}},
		{name: "index out of range", mutate: func(f *Facts) { f.Rule.Winner.MatchedKeywordIndexes[0] = 9 }},
		{name: "fields order", mutate: func(f *Facts) {
			f.Rule.Winner.MatchedFields = []errorrule.SemanticField{errorrule.FieldMessage, errorrule.FieldCode}
		}},
		{name: "fields absent", mutate: func(f *Facts) { f.Rule.Winner.MatchedFields = nil }},
		{name: "attempts started", mutate: func(f *Facts) { f.Retry.GlobalAttemptsStarted = 0 }},
		{name: "rule limit negative", mutate: func(f *Facts) { f.Retry.RuleRetryLimit = -1 }},
		{name: "rule limit over bound", mutate: func(f *Facts) { f.Retry.RuleRetryLimit = errorrule.MaxRuleRetries + 1 }},
		{name: "rule limit mismatches action", mutate: func(f *Facts) { f.Retry.RuleRetryLimit = 1 }},
		{name: "rule retries over limit", mutate: func(f *Facts) { f.Retry.RuleRetriesScheduled = 1 }},
		{name: "decision value", mutate: func(f *Facts) { f.Decision.Value = "retry_elsewhere" }},
		{name: "decision reason", mutate: func(f *Facts) { f.Decision.Reason = "parsed_from_switch_reason" }},
		{name: "decision pair", mutate: func(f *Facts) { f.Decision.Reason = errorrule.ReasonReservedSwitchAttempt }},
		{name: "switch without alternate", mutate: func(f *Facts) {
			f.Decision = errorrule.Decision{Value: errorrule.DecisionSwitchProvider, Reason: errorrule.ReasonReservedSwitchAttempt}
		}},
		{name: "health verdict", mutate: func(f *Facts) { f.Health.Assessment.Verdict = "bad" }},
		{name: "health cause", mutate: func(f *Facts) { f.Health.Assessment.Cause = "bad" }},
		{name: "health pair", mutate: func(f *Facts) { f.Health.Assessment.Cause = errorrule.HealthCauseNormalCompletion }},
		{name: "alternate outcome", mutate: func(f *Facts) { f.Alternate.Outcome = "maybe" }},
		{name: "not requested facts", mutate: func(f *Facts) { value := "p2"; f.Alternate.ProviderID = &value }},
		{name: "activated incomplete", mutate: func(f *Facts) { f.Alternate.Outcome = AlternateActivated }},
		{name: "reserved incomplete", mutate: func(f *Facts) { f.Alternate.Outcome = AlternateReserved }},
		{name: "unavailable switch mode", mutate: func(f *Facts) {
			mode := SwitchModeFailover
			f.Alternate.Outcome = AlternateUnavailable
			f.Alternate.SwitchMode = &mode
		}},
		{name: "unknown switch mode", mutate: func(f *Facts) {
			provider := "p2"
			mode := SwitchMode("other")
			reason := errorrule.SwitchReasonRuleExhausted
			f.Alternate = AlternateFacts{Outcome: AlternateActivated, ProviderID: &provider, SwitchMode: &mode, SwitchReason: &reason}
		}},
		{name: "unknown switch reason", mutate: func(f *Facts) {
			provider := "p2"
			mode := SwitchModeFailover
			reason := errorrule.SwitchReason("other")
			f.Alternate = AlternateFacts{Outcome: AlternateActivated, ProviderID: &provider, SwitchMode: &mode, SwitchReason: &reason}
		}},
	}
	for _, testCase := range invalid {
		t.Run(testCase.name, func(t *testing.T) {
			facts := validFacts(t)
			testCase.mutate(&facts)
			if _, err := NewSemanticError(facts); err == nil {
				t.Fatal("NewSemanticError() unexpectedly succeeded")
			}
		})
	}
}

func TestNewSemanticErrorAlternateAndUnlimitedBranches(t *testing.T) {
	facts := validFacts(t)
	action, err := errorrule.NewRetryThenSwitchAction(3, model.BackoffPolicy{})
	if err != nil {
		t.Fatalf("retry action: %v", err)
	}
	facts.Rule.Winner.Rule.Action = action
	facts.Rule.Matches[0] = facts.Rule.Winner
	facts.Retry.RuleRetryLimit = 3
	facts.Decision = errorrule.Decision{Value: errorrule.DecisionSwitchProvider, Reason: errorrule.ReasonReservedSwitchAttempt}
	facts.Health.Assessment = errorrule.HealthAssessment{Verdict: errorrule.HealthFailure, Cause: errorrule.HealthCauseSemanticRetryThenSwitch}
	provider := "alternate"
	mode := SwitchModeReplacement
	reason := errorrule.SwitchReasonRuleExhausted
	facts.Alternate = AlternateFacts{
		Outcome: AlternateActivated, ProviderID: &provider, SwitchMode: &mode, SwitchReason: &reason,
	}
	facts.Retry.GlobalAttemptsUnlimited = true
	semantic, err := NewSemanticError(facts)
	if err != nil {
		t.Fatalf("activated alternate: %v", err)
	}
	if semantic.Retry.GlobalAttemptsRemaining != nil || semantic.Alternate.ProviderID == nil || *semantic.Alternate.ProviderID != provider {
		t.Fatalf("semantic = %#v", semantic)
	}
	facts.Decision.Reason = errorrule.ReasonProviderDeleted
	if _, err := NewSemanticError(facts); err == nil {
		t.Fatal("provider-unavailable decision accepted a rule-exhausted switch reason")
	}
	providerReason := errorrule.SwitchReasonProviderUnavailable
	facts.Alternate.SwitchReason = &providerReason
	if _, err := NewSemanticError(facts); err != nil {
		t.Fatalf("provider-unavailable switch: %v", err)
	}

	for _, alternate := range []AlternateFacts{
		{Outcome: AlternateReserved, ProviderID: &provider},
		{Outcome: AlternateUnavailable},
		{Outcome: AlternateFailed},
		{Outcome: AlternateReleased},
	} {
		facts := validFacts(t)
		facts.Alternate = alternate
		if _, err := NewSemanticError(facts); err != nil {
			t.Fatalf("alternate %q: %v", alternate.Outcome, err)
		}
	}
}

func validFacts(t *testing.T) Facts {
	t.Helper()
	target, err := errorrule.NewProviderTarget("provider-codex")
	if err != nil {
		t.Fatalf("target: %v", err)
	}
	apiType := apicontract.APITypeCodex
	rule := errorrule.NewRule(errorrule.RuleSpec{
		Name: "Codex capacity", Enabled: true, Target: target, APIType: &apiType,
		Keywords: []string{"server_is_overloaded", "at capacity"}, MatchMode: errorrule.MatchAny,
		Action: errorrule.NewPassthroughAction(),
	}, errorrule.RuleMetadata{ID: "11111111-1111-4111-8111-111111111111"})
	winner := errorrule.RuleMatch{
		Rule: rule, MatchedKeywords: []string{"server_is_overloaded"},
		MatchedKeywordIndexes: []int{0}, MatchedFields: []errorrule.SemanticField{errorrule.FieldCode},
	}
	return Facts{
		Identity: IdentityFacts{
			RequestID: "request-01", OperationID: "operation-01", ProviderID: "provider-codex",
			LogicalAttempt: 1, ProviderAttempt: 1, CredentialPhase: CredentialPhasePrimary,
		},
		Response: ResponseFacts{
			ProtocolID: apicontract.ProtocolOpenAIResponsesSSE,
			State:      ResponseStateForwarding, MatchTiming: MatchTimingForwarding,
			BoundaryReason: "passthrough_only", ElapsedMilliseconds: 37,
			PeakProbeBytes: 128, RawProbeBytes: 128, DecodedProbeBytes: 120,
			UpstreamBytesRead: 128, ClientBodyBytesWritten: 128,
			HeadersCommitted: true, VisibleToClient: true,
		},
		Rule:      RuleFacts{Revision: 10, Winner: winner, Matches: []errorrule.RuleMatch{winner}},
		Retry:     RetryFacts{GlobalAttemptsStarted: 1, GlobalAttemptsRemaining: 3},
		Alternate: AlternateFacts{Outcome: AlternateNotRequested},
		Decision:  errorrule.Decision{Value: errorrule.DecisionPassthrough, Reason: errorrule.ReasonActionPassthrough},
		Health: HealthFacts{Assessment: errorrule.HealthAssessment{
			Verdict: errorrule.HealthNeutral, Cause: errorrule.HealthCauseSemanticNeutral,
		}},
	}
}

func assertJSONEqual(t *testing.T, got, want []byte) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("decode got JSON: %v", err)
	}
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("decode want JSON: %v", err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("JSON mismatch\ngot:  %s\nwant: %s", got, want)
	}
}
