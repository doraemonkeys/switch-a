package errorruleapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	errorrulesqlite "github.com/doraemonkeys/switch-a/internal/errorrule/sqlite"
	"github.com/doraemonkeys/switch-a/internal/model"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestSharedFixtureRuleListMutationsAndStats(t *testing.T) {
	rules := fixtureRules(t)
	assertGoldenValue(t, readFixture(t, "rule-list.json"), newRuleListResponse(7, rules))

	var mutations struct {
		Create struct {
			Response json.RawMessage `json:"response"`
		} `json:"create"`
		Update struct {
			Response json.RawMessage `json:"response"`
		} `json:"update"`
	}
	decodeJSON(t, readFixture(t, "rule-mutations.json"), &mutations)
	createdAt := time.Date(2026, 8, 3, 1, 4, 0, 0, time.UTC)
	created := fixtureMutationRule(t, false, errorrule.ActionRetryOnly, 1, model.BackoffPolicy{}, createdAt)
	assertGoldenValue(t, mutations.Create.Response, newRuleResponse(8, created))

	updatedAt := time.Date(2026, 8, 3, 2, 0, 0, 0, time.UTC)
	updated := fixtureMutationRule(t, true, errorrule.ActionRetryThenSwitch, 2, fixtureBackoff(), updatedAt)
	assertGoldenValue(t, mutations.Update.Response, newRuleResponse(9, updated))

	lastHit := time.Date(2026, 8, 3, 11, 4, 5, 0, time.FixedZone("fixture+8", 8*60*60))
	stats := []errorrule.RuleStats{
		{RuleID: rules[0].ID, HitCount: 42, LastHitAt: &lastHit},
		{RuleID: rules[1].ID, HitCount: 0},
	}
	assertGoldenValue(t, readFixture(t, "rule-stats.json"), newStatsResponse(10, stats))
}

func TestSharedFixturesExecuteThroughHTTPHandlers(t *testing.T) {
	baseRules := fixtureRules(t)
	createdAt := time.Date(2026, 8, 3, 1, 4, 0, 0, time.UTC)
	createdRule := fixtureMutationRule(t, false, errorrule.ActionRetryOnly, 1, model.BackoffPolicy{}, createdAt)
	updatedRule := fixtureMutationRule(
		t, true, errorrule.ActionRetryThenSwitch, 2, fixtureBackoff(),
		time.Date(2026, 8, 3, 2, 0, 0, 0, time.UTC),
	)
	createdRules := append(append([]errorrule.Rule(nil), baseRules...), createdRule)
	updatedRules := append(append([]errorrule.Rule(nil), baseRules...), updatedRule)

	var mutations struct {
		Create fixtureMutationCase `json:"create"`
		Update fixtureMutationCase `json:"update"`
		Delete struct {
			RuleID       string          `json:"rule_id"`
			IfMatch      string          `json:"if_match"`
			Status       int             `json:"status"`
			ETag         string          `json:"etag"`
			ResponseBody json.RawMessage `json:"response_body"`
		} `json:"delete"`
	}
	decodeJSON(t, readFixture(t, "rule-mutations.json"), &mutations)
	service := &fixtureRuleService{
		snapshot: compileRules(t, 7, baseRules...),
		createResult: errorrulesqlite.MutationResult{
			Revision: 8, Rules: createdRules, Changed: true,
		},
		updateResult: errorrulesqlite.MutationResult{
			Revision: 9, Rules: updatedRules, Changed: true,
		},
		deleteResult: errorrulesqlite.MutationResult{
			Revision: 10, Rules: baseRules, Changed: true,
		},
	}
	handler := newFixtureHTTPHandler(t, service, &statsReaderStub{revision: 7})

	list := performRequest(t, handler.ListRules, http.MethodGet, "/", "", "", "")
	assertFixtureHTTP(t, list, http.StatusOK, `"internal-error-rules/7"`, "", readFixture(t, "rule-list.json"))
	created := performRequest(t, handler.CreateRule, http.MethodPost, "/", string(mutations.Create.Request), mutations.Create.IfMatch, "")
	assertFixtureHTTP(t, created, mutations.Create.Status, mutations.Create.ETag, mutations.Create.Location, mutations.Create.Response)
	getCreated := performRequest(t, handler.GetRule, http.MethodGet, "/", "", "", string(createdRule.ID))
	assertFixtureHTTP(t, getCreated, http.StatusOK, mutations.Create.ETag, "", mutations.Create.Response)
	updated := performRequest(t, handler.UpdateRule, http.MethodPut, "/", string(mutations.Update.Request), mutations.Update.IfMatch, mutations.Update.RuleID)
	assertFixtureHTTP(t, updated, mutations.Update.Status, mutations.Update.ETag, "", mutations.Update.Response)
	deleted := performRequest(t, handler.DeleteRule, http.MethodDelete, "/", "", mutations.Delete.IfMatch, mutations.Delete.RuleID)
	if deleted.Code != mutations.Delete.Status || deleted.Header().Get("ETag") != mutations.Delete.ETag ||
		deleted.Body.Len() != 0 || deleted.Header().Get("Content-Type") != "" || string(mutations.Delete.ResponseBody) != "null" {
		t.Fatalf("DELETE fixture status=%d headers=%v body=%q", deleted.Code, deleted.Header(), deleted.Body.String())
	}
	if !reflect.DeepEqual(service.createSpec, createdRule.RuleSpec) ||
		!reflect.DeepEqual(service.updateSpec, updatedRule.RuleSpec) || service.deletedID != createdRule.ID {
		t.Fatalf("decoded mutation inputs create=%#v update=%#v delete=%q", service.createSpec, service.updateSpec, service.deletedID)
	}

	var reorder struct {
		IfMatch  string          `json:"if_match"`
		Request  json.RawMessage `json:"request"`
		Status   int             `json:"status"`
		ETag     string          `json:"etag"`
		Response json.RawMessage `json:"response"`
	}
	decodeJSON(t, readFixture(t, "reorder.json"), &reorder)
	reorderedRules := []errorrule.Rule{baseRules[1], baseRules[0], updatedRule}
	for index := range reorderedRules {
		reorderedRules[index].Position = int64(index)
	}
	reorderService := &fixtureRuleService{
		snapshot: compileRules(t, 9, updatedRules...),
		reorderResult: errorrulesqlite.MutationResult{
			Revision: 10, Rules: reorderedRules, Changed: true,
		},
	}
	reorderHandler := newFixtureHTTPHandler(t, reorderService, &statsReaderStub{revision: 9})
	reordered := performRequest(t, reorderHandler.ReorderRules, http.MethodPost, "/", string(reorder.Request), reorder.IfMatch, "")
	assertFixtureHTTP(t, reordered, reorder.Status, reorder.ETag, "", reorder.Response)
	if got := fmt.Sprint(reorderService.reorderedIDs); got != fmt.Sprint([]errorrule.RuleID{baseRules[1].ID, baseRules[0].ID, updatedRule.ID}) {
		t.Fatalf("decoded reorder IDs = %s", got)
	}

	lastHit := time.Date(2026, 8, 3, 3, 4, 5, 0, time.UTC)
	statsReader := &statsReaderStub{revision: 10, values: []errorrule.RuleStats{
		{RuleID: baseRules[0].ID, HitCount: 42, LastHitAt: &lastHit},
		{RuleID: baseRules[1].ID},
	}}
	statsHandler := newFixtureHTTPHandler(t, &fixtureRuleService{snapshot: compileRules(t, 10, baseRules...)}, statsReader)
	stats := performRequest(t, statsHandler.GetStats, http.MethodGet, "/", "", "", "")
	assertFixtureHTTP(t, stats, http.StatusOK, "", "", readFixture(t, "rule-stats.json"))
}

type fixtureMutationCase struct {
	RuleID   string          `json:"rule_id"`
	IfMatch  string          `json:"if_match"`
	Request  json.RawMessage `json:"request"`
	Status   int             `json:"status"`
	Location string          `json:"location"`
	ETag     string          `json:"etag"`
	Response json.RawMessage `json:"response"`
}

type fixtureRuleService struct {
	mu sync.RWMutex

	snapshot      *errorrule.CompiledRuleSet
	createResult  errorrulesqlite.MutationResult
	updateResult  errorrulesqlite.MutationResult
	deleteResult  errorrulesqlite.MutationResult
	reorderResult errorrulesqlite.MutationResult

	createSpec   errorrule.RuleSpec
	updateSpec   errorrule.RuleSpec
	deletedID    errorrule.RuleID
	reorderedIDs []errorrule.RuleID
}

func (s *fixtureRuleService) CurrentRuleSet() *errorrule.CompiledRuleSet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot
}

func (s *fixtureRuleService) ListRules() (errorrule.Revision, []errorrule.Rule) {
	snapshot := s.CurrentRuleSet()
	return snapshot.Revision(), snapshot.Rules()
}

func (s *fixtureRuleService) GetRule(id errorrule.RuleID) (errorrule.Revision, errorrule.Rule, error) {
	snapshot := s.CurrentRuleSet()
	rule, found := snapshot.Rule(id)
	if !found {
		return snapshot.Revision(), errorrule.Rule{}, errorrulesqlite.ErrRuleNotFound
	}
	return snapshot.Revision(), rule, nil
}

func (s *fixtureRuleService) CreateRule(
	_ context.Context,
	expected errorrule.Revision,
	spec errorrule.RuleSpec,
) (errorrulesqlite.MutationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if expected != s.snapshot.Revision() {
		return errorrulesqlite.MutationResult{}, &errorrulesqlite.RevisionMismatchError{Expected: expected, Current: s.snapshot.Revision()}
	}
	s.createSpec = spec
	return s.installLocked(s.createResult)
}

func (s *fixtureRuleService) UpdateRule(
	_ context.Context,
	expected errorrule.Revision,
	_ errorrule.RuleID,
	spec errorrule.RuleSpec,
) (errorrulesqlite.MutationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if expected != s.snapshot.Revision() {
		return errorrulesqlite.MutationResult{}, &errorrulesqlite.RevisionMismatchError{Expected: expected, Current: s.snapshot.Revision()}
	}
	s.updateSpec = spec
	return s.installLocked(s.updateResult)
}

func (s *fixtureRuleService) DeleteRule(
	_ context.Context,
	expected errorrule.Revision,
	id errorrule.RuleID,
) (errorrulesqlite.MutationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if expected != s.snapshot.Revision() {
		return errorrulesqlite.MutationResult{}, &errorrulesqlite.RevisionMismatchError{Expected: expected, Current: s.snapshot.Revision()}
	}
	s.deletedID = id
	return s.installLocked(s.deleteResult)
}

func (s *fixtureRuleService) ReorderRules(
	_ context.Context,
	expected errorrule.Revision,
	ordered []errorrule.RuleID,
) (errorrulesqlite.MutationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if expected != s.snapshot.Revision() {
		return errorrulesqlite.MutationResult{}, &errorrulesqlite.RevisionMismatchError{Expected: expected, Current: s.snapshot.Revision()}
	}
	s.reorderedIDs = append([]errorrule.RuleID(nil), ordered...)
	return s.installLocked(s.reorderResult)
}

func (s *fixtureRuleService) installLocked(
	result errorrulesqlite.MutationResult,
) (errorrulesqlite.MutationResult, error) {
	snapshot, err := errorrule.CompileRuleSet(result.Revision, result.Rules)
	if err != nil {
		return errorrulesqlite.MutationResult{}, err
	}
	s.snapshot = snapshot
	return result, nil
}

func newFixtureHTTPHandler(t *testing.T, rules RuleService, stats RuleStatsReader) *Handler {
	t.Helper()
	handler, err := NewHandler(Config{
		Rules: rules, Stats: stats, StatsOverlay: &overlayStub{},
		Providers: &providerCatalogStub{items: map[string]*model.Provider{"provider-codex": {ID: "provider-codex"}}},
		Logger:    zap.NewNop(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func assertFixtureHTTP(
	t *testing.T,
	response *httptest.ResponseRecorder,
	status int,
	etag, location string,
	expectedBody []byte,
) {
	t.Helper()
	if response.Code != status || response.Header().Get("Content-Type") != jsonContentType ||
		response.Header().Get("ETag") != etag || response.Header().Get("Location") != location {
		t.Fatalf("status/headers = %d %v", response.Code, response.Header())
	}
	assertGoldenJSON(t, expectedBody, response.Body.Bytes())
}

func TestSharedFixtureErrorEnvelopes(t *testing.T) {
	var fixture struct {
		Cases []struct {
			Name   string          `json:"name"`
			Status int             `json:"status"`
			Body   json.RawMessage `json:"body"`
		} `json:"cases"`
	}
	decodeJSON(t, readFixture(t, "errors.json"), &fixture)

	var invalid mutationRequest
	decodeJSON(t, []byte(`{"schema_version":1,"rule":{"name":"invalid","enabled":true,"target":{"kind":"global"},"api_type":"codex","keywords":["x"],"match_mode":"any","action":{"type":"passthrough","max_retries":1}}}`), &invalid)
	_, validation := invalid.domainRule()
	_, precondition := parseIfMatch(http.Header{}, true)
	errorsByName := map[string]*apiError{
		"validation":            validation,
		"not found":             serviceAPIError("99999999-9999-4999-8999-999999999999", errorrulesqlite.ErrRuleNotFound),
		"conflict":              serviceAPIError("", errorrulesqlite.ErrRuleCapacity),
		"revision mismatch":     serviceAPIError("", &errorrulesqlite.RevisionMismatchError{Expected: 9, Current: 10}),
		"request too large":     requestTooLargeError(MaxRuleMutationRequestBytes),
		"precondition required": precondition,
		"internal":              serviceAPIError("", errors.New("storage unavailable")),
	}

	if len(fixture.Cases) != len(errorsByName) {
		t.Fatalf("fixture cases = %d, want %d", len(fixture.Cases), len(errorsByName))
	}
	for _, testCase := range fixture.Cases {
		t.Run(testCase.Name, func(t *testing.T) {
			apiErr := errorsByName[testCase.Name]
			if apiErr == nil {
				t.Fatalf("no implementation case for fixture %q", testCase.Name)
			}
			recorder := httptest.NewRecorder()
			writeAPIError(recorder, apiErr)
			if recorder.Code != testCase.Status {
				t.Fatalf("status = %d, want %d", recorder.Code, testCase.Status)
			}
			assertGoldenJSON(t, testCase.Body, recorder.Body.Bytes())
		})
	}
}

func TestSharedFixtureTestMessageUsesRuntimeRegistryAndResolverWithoutSideEffects(t *testing.T) {
	var fixture struct {
		Complete struct {
			IfMatch  string          `json:"if_match"`
			Request  json.RawMessage `json:"request"`
			Status   int             `json:"status"`
			Response json.RawMessage `json:"response"`
		} `json:"complete"`
		FailOpen struct {
			Request  json.RawMessage `json:"request"`
			Status   int             `json:"status"`
			Response json.RawMessage `json:"response"`
		} `json:"fail_open"`
	}
	decodeJSON(t, readFixture(t, "test-message.json"), &fixture)

	rules := &fakeRuleService{snapshot: compileRules(t, 10, fixtureRules(t)...)}
	stats := &statsReaderStub{}
	overlay := &overlayStub{}
	providers := &providerCatalogStub{items: map[string]*model.Provider{
		"provider-codex": {ID: "provider-codex"},
	}}
	operationIDs := &sequenceIDs{values: []string{"must-not-be-consumed"}}
	logCore, observedLogs := observer.New(zap.InfoLevel)
	handler, err := NewHandler(Config{
		Rules: rules, Stats: stats, StatsOverlay: overlay, Providers: providers,
		OperationIDs: operationIDs,
		Logger:       zap.New(logCore),
	})
	if err != nil {
		t.Fatal(err)
	}

	complete := performRequest(t, handler.TestMessage, http.MethodPost, "/", string(fixture.Complete.Request), fixture.Complete.IfMatch, "")
	if complete.Code != fixture.Complete.Status {
		t.Fatalf("complete status = %d, want %d: %s", complete.Code, fixture.Complete.Status, complete.Body.String())
	}
	assertGoldenJSON(t, fixture.Complete.Response, complete.Body.Bytes())

	failOpen := performRequest(t, handler.TestMessage, http.MethodPost, "/", string(fixture.FailOpen.Request), "", "")
	if failOpen.Code != fixture.FailOpen.Status {
		t.Fatalf("fail-open status = %d, want %d: %s", failOpen.Code, fixture.FailOpen.Status, failOpen.Body.String())
	}
	assertGoldenJSON(t, fixture.FailOpen.Response, failOpen.Body.Bytes())

	if stats.calls != 0 || overlay.calls != 0 || rules.mutationCalls() != 0 {
		t.Fatalf("Test Message side effects: stats=%d overlay=%d mutations=%d", stats.calls, overlay.calls, rules.mutationCalls())
	}
	providers.mu.Lock()
	providerCalls := providers.getCall
	providers.mu.Unlock()
	if providerCalls != 1 {
		t.Fatalf("provider lookups = %d, want exactly one for the scoped fixture", providerCalls)
	}
	operationIDs.mu.Lock()
	remainingOperationIDs := len(operationIDs.values)
	operationIDs.mu.Unlock()
	if remainingOperationIDs != 1 {
		t.Fatal("Test Message consumed a mutation operation ID")
	}
	if observedLogs.Len() != 0 {
		t.Fatalf("Test Message emitted %d trace/log entries", observedLogs.Len())
	}
}

func fixtureRules(t *testing.T) []errorrule.Rule {
	t.Helper()
	target, err := errorrule.NewProviderTarget("provider-codex")
	if err != nil {
		t.Fatal(err)
	}
	retry, err := errorrule.NewRetryThenSwitchAction(2, fixtureBackoff())
	if err != nil {
		t.Fatal(err)
	}
	codex := apicontract.APITypeCodex
	firstTime := time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC)
	secondTime := time.Date(2026, 8, 3, 1, 3, 0, 0, time.UTC)
	return []errorrule.Rule{
		newFixtureRule(t, errorrule.RuleSpec{
			Name: "Codex capacity", Enabled: true, Target: target, APIType: &codex,
			Keywords:  []string{"server_is_overloaded", "our servers are currently overloaded at capacity"},
			MatchMode: errorrule.MatchAny, Action: retry,
		}, "11111111-1111-4111-8111-111111111111", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", 0, firstTime, firstTime),
		newFixtureRule(t, errorrule.RuleSpec{
			Name: "Observe upstream maintenance", Enabled: true, Target: errorrule.NewGlobalTarget(), APIType: nil,
			Keywords: []string{"maintenance window"}, MatchMode: errorrule.MatchAll,
			Action: errorrule.NewPassthroughAction(),
		}, "22222222-2222-4222-8222-222222222222", "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", 1, secondTime, secondTime),
	}
}

func fixtureMutationRule(
	t *testing.T,
	enabled bool,
	actionType errorrule.ActionType,
	maxRetries int,
	backoff model.BackoffPolicy,
	updatedAt time.Time,
) errorrule.Rule {
	t.Helper()
	var (
		action errorrule.Action
		err    error
	)
	if actionType == errorrule.ActionRetryOnly {
		action, err = errorrule.NewRetryOnlyAction(maxRetries, backoff)
	} else {
		action, err = errorrule.NewRetryThenSwitchAction(maxRetries, backoff)
	}
	if err != nil {
		t.Fatal(err)
	}
	claude := apicontract.APITypeClaude
	createdAt := time.Date(2026, 8, 3, 1, 4, 0, 0, time.UTC)
	return newFixtureRule(t, errorrule.RuleSpec{
		Name: "Anthropic overload", Enabled: enabled, Target: errorrule.NewGlobalTarget(), APIType: &claude,
		Keywords: []string{"overloaded_error"}, MatchMode: errorrule.MatchAny, Action: action,
	}, "33333333-3333-4333-8333-333333333333", "cccccccc-cccc-4ccc-8ccc-cccccccccccc", 2, createdAt, updatedAt)
}

func newFixtureRule(
	t *testing.T,
	spec errorrule.RuleSpec,
	id, generation string,
	position int64,
	createdAt, updatedAt time.Time,
) errorrule.Rule {
	t.Helper()
	parsedGeneration, err := errorrule.ParseRuleGeneration(generation)
	if err != nil {
		t.Fatal(err)
	}
	return errorrule.NewRule(spec, errorrule.RuleMetadata{
		ID: errorrule.RuleID(id), Generation: parsedGeneration, Position: position,
		CreatedAt: createdAt, UpdatedAt: updatedAt,
	})
}

func fixtureBackoff() model.BackoffPolicy {
	return model.BackoffPolicy{
		InitialDelay: model.Duration(250 * time.Millisecond),
		MaxDelay:     model.Duration(2 * time.Second),
		Multiplier:   2,
		Jitter:       true,
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve fixture test source path")
	}
	path := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "contracts", "internal-error", "v1", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return data
}

func assertGoldenValue(t *testing.T, expected []byte, actual any) {
	t.Helper()
	encoded, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	assertGoldenJSON(t, expected, encoded)
}

func assertGoldenJSON(t *testing.T, expected, actual []byte) {
	t.Helper()
	var expectedValue, actualValue any
	decodeJSON(t, expected, &expectedValue)
	decodeJSON(t, actual, &actualValue)
	if !reflect.DeepEqual(actualValue, expectedValue) {
		actualNormalized, _ := json.MarshalIndent(actualValue, "", "  ")
		expectedNormalized, _ := json.MarshalIndent(expectedValue, "", "  ")
		t.Fatalf("JSON mismatch\nactual:   %s\nexpected: %s", actualNormalized, expectedNormalized)
	}
}

func decodeJSON(t *testing.T, data []byte, target any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		t.Fatal(err)
	}
}
