package errorruleapi

import (
	"context"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis"
)

func TestServiceTestMessageReturnsOrderedMatchesAndLatchesFirstWinner(t *testing.T) {
	t.Parallel()
	providerRule := fixtureRules(t)[0]
	codex := apicontract.APITypeCodex
	globalRule := newFixtureRule(t, errorrule.RuleSpec{
		Name: "global fallback", Enabled: true, Target: errorrule.NewGlobalTarget(), APIType: &codex,
		Keywords: []string{"server_is_overloaded"}, MatchMode: errorrule.MatchAny,
		Action: errorrule.NewPassthroughAction(),
	}, "44444444-4444-4444-8444-444444444444", "dddddddd-dddd-4ddd-8ddd-dddddddddddd", 1,
		time.Date(2026, 8, 3, 1, 5, 0, 0, time.UTC), time.Date(2026, 8, 3, 1, 5, 0, 0, time.UTC))
	analyzer := &analyzerStub{
		observed: []AnalyzedError{
			{FrameIndex: 3, Fields: responseanalysis.SemanticFields{Type: "error", Code: "server_is_overloaded", Message: "At capacity"}},
			{FrameIndex: 4, Fields: responseanalysis.SemanticFields{Type: "error", Code: "server_is_overloaded", Message: "later"}},
		},
	}
	protocol := apicontract.ProtocolOpenAIResponsesSSE
	analyzer.result.ProtocolID = &protocol
	providers := &providerCatalogStub{items: map[string]*model.Provider{"provider-codex": {ID: "provider-codex"}}}
	service := &service{
		rules:     &fakeRuleService{snapshot: compileRules(t, 10, providerRule, globalRule)},
		providers: providers, analyzer: analyzer,
	}
	providerID := "provider-codex"
	response, err := service.testMessage(context.Background(), nil, TestMessageInput{
		APIType: codex, ProviderID: &providerID, ContentType: "text/event-stream",
		ContentEncoding: "identity", Body: []byte("ignored by stub"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if analyzer.calls != 1 || analyzer.consumed != 1 || len(response.Errors) != 1 ||
		response.DecisiveErrorIndex == nil || *response.DecisiveErrorIndex != 0 || response.Winner == nil {
		t.Fatalf("response=%#v analyzer=%#v", response, analyzer)
	}
	if response.Errors[0].FrameIndex != 3 || len(response.Errors[0].Matches) != 2 ||
		response.Errors[0].Matches[0].RuleID != providerRule.ID || response.Errors[0].Matches[1].RuleID != globalRule.ID ||
		response.Winner.RuleID != providerRule.ID || response.Winner.ErrorIndex != 0 {
		t.Fatalf("ordered result = %#v", response)
	}
	if response.AnalysisStatus != "complete" || response.AnalysisReason != nil ||
		response.ResponseProtocolID == nil || *response.ResponseProtocolID != protocol {
		t.Fatalf("analysis envelope = %#v", response)
	}
}

func TestServiceTestMessageFailOpenRetainsBoundedErrorsWithoutInventingWinner(t *testing.T) {
	t.Parallel()
	rule := testRule(t, "11111111-1111-4111-8111-111111111111", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "unmatched", 0, errorrule.NewPassthroughAction())
	analyzer := &analyzerStub{
		observed: []AnalyzedError{{FrameIndex: 2, Fields: responseanalysis.SemanticFields{Message: "different"}}},
		result:   MessageAnalysisResult{Failure: responseanalysis.FailureMalformedFrame},
	}
	service := &service{
		rules:     &fakeRuleService{snapshot: compileRules(t, 4, rule)},
		providers: &providerCatalogStub{items: map[string]*model.Provider{}}, analyzer: analyzer,
	}
	response, err := service.testMessage(context.Background(), nil, TestMessageInput{APIType: apicontract.APITypeCodex})
	if err != nil {
		t.Fatal(err)
	}
	if response.AnalysisStatus != "fail_open" || response.AnalysisReason == nil ||
		*response.AnalysisReason != responseanalysis.FailureMalformedFrame || len(response.Errors) != 1 ||
		response.DecisiveErrorIndex != nil || response.Winner != nil || response.Errors[0].Matches == nil {
		t.Fatalf("fail-open response = %#v", response)
	}
}

func TestServiceTestMessageChecksPinnedRevisionAndProviderBeforeAnalysis(t *testing.T) {
	t.Parallel()
	analyzer := &analyzerStub{}
	providers := &providerCatalogStub{items: map[string]*model.Provider{}}
	service := &service{
		rules:     &fakeRuleService{snapshot: compileRules(t, 10)},
		providers: providers, analyzer: analyzer,
	}
	providerID := "missing-provider"
	stale := errorrule.Revision(9)
	if _, err := service.testMessage(context.Background(), &stale, TestMessageInput{
		APIType: apicontract.APITypeCodex, ProviderID: &providerID,
	}); serviceAPIError("", err).Status != 412 {
		t.Fatalf("stale error = %v", err)
	}
	if analyzer.calls != 0 || providers.getCall != 0 {
		t.Fatalf("stale request crossed boundary: analyzer=%d providers=%d", analyzer.calls, providers.getCall)
	}
	if _, err := service.testMessage(context.Background(), nil, TestMessageInput{
		APIType: apicontract.APITypeCodex, ProviderID: &providerID,
	}); serviceAPIError("", err).Status != 404 {
		t.Fatalf("missing provider error = %v", err)
	}
	if analyzer.calls != 0 || providers.getCall != 1 {
		t.Fatalf("missing provider crossed analyzer boundary: analyzer=%d providers=%d", analyzer.calls, providers.getCall)
	}
}

func TestServiceRejectsNilProviderCatalogSuccessAsInternalFailure(t *testing.T) {
	t.Parallel()
	service := &service{providers: nilProviderCatalog{}}
	err := service.ensureProvider(context.Background(), "provider-nil")
	if err == nil || serviceAPIError("", err).Status != 500 {
		t.Fatalf("nil provider result = %v", err)
	}
}

type nilProviderCatalog struct{}

func (nilProviderCatalog) GetProvider(context.Context, string) (*model.Provider, error) {
	return nil, nil
}
