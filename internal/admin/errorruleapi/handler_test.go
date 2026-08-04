package errorruleapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/errorrule"
	errorrulesqlite "github.com/doraemonkeys/switch-a/internal/errorrule/sqlite"
	"github.com/doraemonkeys/switch-a/internal/errorrule/statistics"
	"github.com/doraemonkeys/switch-a/internal/model"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestHandlerCRUDReorderStatsAndNoOpContracts(t *testing.T) {
	_, repository, accumulator, handler := openRepositoryHandler(t,
		"11111111-1111-4111-8111-111111111111",
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
	)

	list := performRequest(t, handler.ListRules, http.MethodGet, "/", "", "", "")
	assertStatusHeader(t, list, http.StatusOK, `"internal-error-rules/0"`)
	var empty RuleListResponse
	decodeRecorder(t, list, &empty)
	if empty.RuleSetRevision != "0" || len(empty.Rules) != 0 {
		t.Fatalf("empty list = %#v", empty)
	}

	create := performRequest(t, handler.CreateRule, http.MethodPost, "/", validPassthroughMutation("Capacity"), `"internal-error-rules/0"`, "")
	assertStatusHeader(t, create, http.StatusCreated, `"internal-error-rules/1"`)
	var created RuleResponse
	decodeRecorder(t, create, &created)
	if created.Rule.ID != "11111111-1111-4111-8111-111111111111" ||
		create.Header().Get("Location") != createdRuleLocationPrefix+string(created.Rule.ID) {
		t.Fatalf("create response = %#v location=%q", created, create.Header().Get("Location"))
	}

	get := performRequest(t, handler.GetRule, http.MethodGet, "/", "", "", string(created.Rule.ID))
	assertStatusHeader(t, get, http.StatusOK, `"internal-error-rules/1"`)
	var got RuleResponse
	decodeRecorder(t, get, &got)
	if got.Rule.ID != created.Rule.ID {
		t.Fatalf("get = %#v", got)
	}

	published := repository.CurrentRuleSet()
	update := performRequest(t, handler.UpdateRule, http.MethodPut, "/", validPassthroughMutation("Capacity"), `"internal-error-rules/1"`, string(created.Rule.ID))
	assertStatusHeader(t, update, http.StatusOK, `"internal-error-rules/1"`)
	var unchanged RuleResponse
	decodeRecorder(t, update, &unchanged)
	if repository.CurrentRuleSet() != published || unchanged.Rule.UpdatedAt != created.Rule.UpdatedAt {
		t.Fatalf("no-op changed snapshot/timestamp: %#v", unchanged)
	}

	reorderBody := fmt.Sprintf(`{"schema_version":1,"ordered_rule_ids":[%q]}`, created.Rule.ID)
	reorder := performRequest(t, handler.ReorderRules, http.MethodPost, "/", reorderBody, `"internal-error-rules/1"`, "")
	assertStatusHeader(t, reorder, http.StatusOK, `"internal-error-rules/1"`)
	if repository.CurrentRuleSet() != published {
		t.Fatal("no-op reorder changed snapshot pointer")
	}

	rule := repository.CurrentRuleSet().Rules()[0]
	hitAt := time.Date(2026, 8, 3, 3, 4, 5, 0, time.UTC)
	if err := accumulator.Hit(statistics.HandleFor(rule), hitAt); err != nil {
		t.Fatal(err)
	}
	stats := performRequest(t, handler.GetStats, http.MethodGet, "/", "", "", "")
	if stats.Code != http.StatusOK || stats.Header().Get("ETag") != "" {
		t.Fatalf("stats status/header = %d %v", stats.Code, stats.Header())
	}
	var statsResponse RuleStatsResponse
	decodeRecorder(t, stats, &statsResponse)
	if statsResponse.RuleSetRevision != "1" || len(statsResponse.Stats) != 1 ||
		statsResponse.Stats[0].HitCount != "1" || statsResponse.Stats[0].LastHitAt == nil ||
		!statsResponse.Stats[0].LastHitAt.Equal(hitAt) {
		t.Fatalf("stats = %#v", statsResponse)
	}

	deleted := performRequest(t, handler.DeleteRule, http.MethodDelete, "/", "", `"internal-error-rules/1"`, string(created.Rule.ID))
	assertStatusHeader(t, deleted, http.StatusNoContent, `"internal-error-rules/2"`)
	if deleted.Body.Len() != 0 || deleted.Header().Get("Content-Type") != "" {
		t.Fatalf("DELETE body/header = %q %v", deleted.Body.String(), deleted.Header())
	}
	missing := performRequest(t, handler.GetRule, http.MethodGet, "/", "", "", string(created.Rule.ID))
	assertAPIError(t, missing, http.StatusNotFound, ErrorCodeNotFound, "rule_id", string(created.Rule.ID))
}

func TestHandlerTwoWritersSharingETagHaveExactlyOneWinner(t *testing.T) {
	_, repository, _, handler := openRepositoryHandler(t,
		"11111111-1111-4111-8111-111111111111",
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
	)
	start := make(chan struct{})
	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for _, name := range []string{"first", "second"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			<-start
			recorder := performRequest(t, handler.CreateRule, http.MethodPost, "/", validPassthroughMutation(name), `"internal-error-rules/0"`, "")
			codes <- recorder.Code
		}(name)
	}
	close(start)
	wg.Wait()
	close(codes)
	got := make([]int, 0, 2)
	for code := range codes {
		got = append(got, code)
	}
	sort.Ints(got)
	if fmt.Sprint(got) != fmt.Sprint([]int{http.StatusCreated, http.StatusPreconditionFailed}) {
		t.Fatalf("statuses = %v", got)
	}
	if revision, rules := repository.ListRules(); revision != 1 || len(rules) != 1 {
		t.Fatalf("repository revision=%s rules=%d", revision, len(rules))
	}
}

func TestHandlerTransactionTimeProviderDeletionMapsToTyped404(t *testing.T) {
	db, repository, _, handler := openRepositoryHandler(t,
		"11111111-1111-4111-8111-111111111111",
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
	)
	if err := db.Exec("INSERT INTO providers (id) VALUES ('provider-race')").Error; err != nil {
		t.Fatal(err)
	}
	providers := &providerCatalogStub{items: map[string]*model.Provider{
		"provider-race": {ID: "provider-race"},
	}}
	providers.onGet = func(string) {
		if err := db.Exec("DELETE FROM providers WHERE id = 'provider-race'").Error; err != nil {
			t.Errorf("delete provider: %v", err)
		}
	}
	handler.service.providers = providers
	body := `{"schema_version":1,"rule":{"name":"race","enabled":true,"target":{"kind":"provider","provider_id":"provider-race"},"api_type":"codex","keywords":["capacity"],"match_mode":"any","action":{"type":"passthrough"}}}`
	response := performRequest(t, handler.CreateRule, http.MethodPost, "/", body, `"internal-error-rules/0"`, "")
	assertAPIError(t, response, http.StatusNotFound, ErrorCodeNotFound, "provider_id", "provider-race")
	if revision, rules := repository.ListRules(); revision != 0 || len(rules) != 0 {
		t.Fatalf("failed mutation changed repository: revision=%s rules=%d", revision, len(rules))
	}
}

func TestHandlerPreconditionsValidationAndCapacityErrors(t *testing.T) {
	_, _, _, handler := openRepositoryHandler(t,
		"11111111-1111-4111-8111-111111111111",
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
	)
	missing := performRequest(t, handler.CreateRule, http.MethodPost, "/", validPassthroughMutation("x"), "", "")
	assertAPIError(t, missing, http.StatusPreconditionRequired, ErrorCodePreconditionRequired, "", nil)
	malformed := performRequest(t, handler.CreateRule, http.MethodPost, "/", validPassthroughMutation("x"), "*", "")
	assertAPIError(t, malformed, http.StatusBadRequest, ErrorCodeValidation, "field", "If-Match")
	invalidID := performRequest(t, handler.GetRule, http.MethodGet, "/", "", "", "bad")
	assertAPIError(t, invalidID, http.StatusBadRequest, ErrorCodeValidation, "field", "rule_id")

	created := performRequest(t, handler.CreateRule, http.MethodPost, "/", validPassthroughMutation("x"), `"internal-error-rules/0"`, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	stale := performRequest(t, handler.CreateRule, http.MethodPost, "/", validPassthroughMutation("y"), `"internal-error-rules/0"`, "")
	assertAPIError(t, stale, http.StatusPreconditionFailed, ErrorCodeRevisionMismatch, "current_revision", "1")

	// Capacity is mapped independently of repository implementation details.
	capacity := serviceAPIError("", fmt.Errorf("wrapped: %w", errorrulesqlite.ErrRuleCapacity))
	if capacity.Status != http.StatusConflict || capacity.Details["limit"] != errorrule.MaxRuleCount {
		t.Fatalf("capacity mapping = %#v", capacity)
	}
}

func TestHandlerEndpointBodyAndWireLimits(t *testing.T) {
	_, _, _, handler := openRepositoryHandler(t,
		"11111111-1111-4111-8111-111111111111",
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
	)
	mutation := validPassthroughMutation("limit")
	mutation = padJSON(t, mutation, MaxRuleMutationRequestBytes)
	exactMutation := performRequest(t, handler.CreateRule, http.MethodPost, "/", mutation, `"internal-error-rules/0"`, "")
	if exactMutation.Code != http.StatusCreated {
		t.Fatalf("exact mutation = %d %s", exactMutation.Code, exactMutation.Body.String())
	}
	overMutation := performRequest(t, handler.CreateRule, http.MethodPost, "/", mutation+" ", `"internal-error-rules/1"`, "")
	assertAPIError(t, overMutation, http.StatusRequestEntityTooLarge, ErrorCodeRequestTooLarge, "limit_bytes", int64(MaxRuleMutationRequestBytes))

	var created RuleResponse
	decodeRecorder(t, exactMutation, &created)
	reorderJSON := fmt.Sprintf(`{"schema_version":1,"ordered_rule_ids":[%q]}`, created.Rule.ID)
	reorderJSON = padJSON(t, reorderJSON, MaxRuleReorderRequestBytes)
	exactReorder := performRequest(t, handler.ReorderRules, http.MethodPost, "/", reorderJSON, `"internal-error-rules/1"`, "")
	if exactReorder.Code != http.StatusOK {
		t.Fatalf("exact reorder = %d %s", exactReorder.Code, exactReorder.Body.String())
	}
	overReorder := performRequest(t, handler.ReorderRules, http.MethodPost, "/", reorderJSON+" ", `"internal-error-rules/1"`, "")
	assertAPIError(t, overReorder, http.StatusRequestEntityTooLarge, ErrorCodeRequestTooLarge, "limit_bytes", int64(MaxRuleReorderRequestBytes))

	testJSON := `{"schema_version":1,"api_type":"codex","provider_id":null,"content_type":"application/json","content_encoding":"identity","body":{"encoding":"utf8","value":"{}"}}`
	testJSON = padJSON(t, testJSON, MaxTestMessageRequestBytes)
	exactTest := performRequest(t, handler.TestMessage, http.MethodPost, "/", testJSON, "", "")
	if exactTest.Code != http.StatusOK {
		t.Fatalf("exact test request = %d %s", exactTest.Code, exactTest.Body.String())
	}
	overTest := performRequest(t, handler.TestMessage, http.MethodPost, "/", testJSON+" ", "", "")
	assertAPIError(t, overTest, http.StatusRequestEntityTooLarge, ErrorCodeRequestTooLarge, "limit_bytes", int64(MaxTestMessageRequestBytes))

	exactWireValue := strings.Repeat("x", MaxTestMessageWireBodyBytes)
	exactWireJSON := `{"schema_version":1,"api_type":"codex","provider_id":null,"content_type":"text/plain","content_encoding":"identity","body":{"encoding":"utf8","value":"` + exactWireValue + `"}}`
	exactWire := performRequest(t, handler.TestMessage, http.MethodPost, "/", exactWireJSON, "", "")
	if exactWire.Code != http.StatusOK {
		t.Fatalf("exact wire body = %d %s", exactWire.Code, exactWire.Body.String())
	}
	overWireJSON := strings.Replace(exactWireJSON, exactWireValue, exactWireValue+"x", 1)
	overWire := performRequest(t, handler.TestMessage, http.MethodPost, "/", overWireJSON, "", "")
	assertAPIError(t, overWire, http.StatusRequestEntityTooLarge, ErrorCodeRequestTooLarge, "limit_bytes", int64(MaxTestMessageWireBodyBytes))

	exactBase64Value := base64.StdEncoding.EncodeToString([]byte(exactWireValue))
	exactBase64JSON := `{"schema_version":1,"api_type":"codex","provider_id":null,"content_type":"text/plain","content_encoding":"identity","body":{"encoding":"base64","value":"` + exactBase64Value + `"}}`
	exactBase64 := performRequest(t, handler.TestMessage, http.MethodPost, "/", exactBase64JSON, "", "")
	if exactBase64.Code != http.StatusOK {
		t.Fatalf("exact base64 wire body = %d %s", exactBase64.Code, exactBase64.Body.String())
	}
	overBase64Value := base64.StdEncoding.EncodeToString([]byte(exactWireValue + "x"))
	overBase64JSON := strings.Replace(exactBase64JSON, exactBase64Value, overBase64Value, 1)
	overBase64 := performRequest(t, handler.TestMessage, http.MethodPost, "/", overBase64JSON, "", "")
	assertAPIError(t, overBase64, http.StatusRequestEntityTooLarge, ErrorCodeRequestTooLarge, "limit_bytes", int64(MaxTestMessageWireBodyBytes))
}

func TestStatsReadRetriesAcrossConcurrentSnapshotMutation(t *testing.T) {
	action := errorrule.NewPassthroughAction()
	firstRule := testRule(t, "11111111-1111-4111-8111-111111111111", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "first", 0, action)
	secondRule := testRule(t, "22222222-2222-4222-8222-222222222222", "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "second", 0, action)
	first := compileRules(t, 1, firstRule)
	second := compileRules(t, 2, secondRule)
	rules := &fakeRuleService{snapshot: first}
	reader := &statsReaderStub{revision: 2, values: []errorrule.RuleStats{{RuleID: secondRule.ID}}}
	reader.onList = func() { rules.setSnapshot(second) }
	overlay := &overlayStub{}
	s := &service{rules: rules, stats: reader, overlay: overlay}
	revision, stats, err := s.listStats(context.Background())
	if err != nil || revision != 2 || len(stats) != 1 || reader.calls != 2 || overlay.calls != 1 {
		t.Fatalf("stats revision=%s values=%#v err=%v calls=%d overlay=%d", revision, stats, err, reader.calls, overlay.calls)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := s.listStats(cancelled); err == nil {
		t.Fatal("cancelled stats read succeeded")
	}
	reader.err = errTestFailure
	if _, _, err := s.listStats(context.Background()); err == nil {
		t.Fatal("stats storage failure succeeded")
	}
	reader.err = nil
	reader.revision = 99
	reader.calls = 0
	if _, _, err := s.listStats(context.Background()); !errors.Is(err, errStatsSnapshotDidNotConverge) || reader.calls != maxStatsSnapshotReadAttempts {
		t.Fatalf("non-convergent stats error=%v calls=%d", err, reader.calls)
	}
}

func TestNewHandlerDependenciesAndUnavailableReceiver(t *testing.T) {
	snapshot := compileRules(t, 0)
	valid := Config{
		Rules: &fakeRuleService{snapshot: snapshot}, Stats: &statsReaderStub{},
		StatsOverlay: &overlayStub{}, Providers: &providerCatalogStub{items: map[string]*model.Provider{}},
	}
	checks := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "rules", mutate: func(c *Config) { c.Rules = nil }},
		{name: "stats", mutate: func(c *Config) { c.Stats = nil }},
		{name: "overlay", mutate: func(c *Config) { c.StatsOverlay = nil }},
		{name: "providers", mutate: func(c *Config) { c.Providers = nil }},
	}
	for _, check := range checks {
		config := valid
		check.mutate(&config)
		if _, err := NewHandler(config); err == nil {
			t.Errorf("missing %s accepted", check.name)
		}
	}
	handler, err := NewHandler(valid)
	if err != nil || handler.logger == nil || handler.operationIDs == nil || handler.service.analyzer == nil {
		t.Fatalf("defaulted handler = %#v err=%v", handler, err)
	}
	var unavailable *Handler
	recorder := httptest.NewRecorder()
	unavailable.ListRules(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	assertAPIError(t, recorder, http.StatusInternalServerError, ErrorCodeInternal, "", nil)
}

func TestConditionalErrorPrecedenceRejectsBeforeDependencySideEffects(t *testing.T) {
	t.Parallel()
	validMutation := validPassthroughMutation("precedence")
	validReorder := `{"schema_version":1,"ordered_rule_ids":[]}`
	validTest := `{"schema_version":1,"api_type":"codex","provider_id":null,"content_type":"application/json","content_encoding":"identity","body":{"encoding":"utf8","value":"{}"}}`
	endpoints := []struct {
		name     string
		method   string
		ruleID   string
		limit    int
		required bool
		valid    string
		selectFn func(*Handler) http.HandlerFunc
	}{
		{name: "create", method: http.MethodPost, limit: MaxRuleMutationRequestBytes, required: true, valid: validMutation, selectFn: func(h *Handler) http.HandlerFunc { return h.CreateRule }},
		{name: "update", method: http.MethodPut, ruleID: "11111111-1111-4111-8111-111111111111", limit: MaxRuleMutationRequestBytes, required: true, valid: validMutation, selectFn: func(h *Handler) http.HandlerFunc { return h.UpdateRule }},
		{name: "reorder", method: http.MethodPost, limit: MaxRuleReorderRequestBytes, required: true, valid: validReorder, selectFn: func(h *Handler) http.HandlerFunc { return h.ReorderRules }},
		{name: "test message", method: http.MethodPost, limit: MaxTestMessageRequestBytes, valid: validTest, selectFn: func(h *Handler) http.HandlerFunc { return h.TestMessage }},
	}
	for _, endpoint := range endpoints {
		cases := []struct {
			name   string
			etag   string
			body   string
			status int
		}{
			{name: "malformed header beats oversized body", etag: "*", body: strings.Repeat(" ", endpoint.limit+1), status: http.StatusBadRequest},
			{name: "stale allows malformed body validation", etag: `"internal-error-rules/0"`, body: `{`, status: http.StatusBadRequest},
			{name: "stale allows body limit validation", etag: `"internal-error-rules/0"`, body: strings.Repeat(" ", endpoint.limit+1), status: http.StatusRequestEntityTooLarge},
			{name: "stale valid request", etag: `"internal-error-rules/0"`, body: endpoint.valid, status: http.StatusPreconditionFailed},
		}
		if endpoint.required {
			cases = append(cases, struct {
				name   string
				etag   string
				body   string
				status int
			}{name: "missing header beats malformed body", body: `{`, status: http.StatusPreconditionRequired})
		}
		for _, testCase := range cases {
			t.Run(endpoint.name+"/"+testCase.name, func(t *testing.T) {
				rules := &fakeRuleService{snapshot: compileRules(t, 1)}
				providers := &providerCatalogStub{items: map[string]*model.Provider{}}
				analyzer := &analyzerStub{}
				handler, err := NewHandler(Config{
					Rules: rules, Stats: &statsReaderStub{revision: 1}, StatsOverlay: &overlayStub{},
					Providers: providers, Analyzer: analyzer,
				})
				if err != nil {
					t.Fatal(err)
				}
				response := performRequest(t, endpoint.selectFn(handler), endpoint.method, "/", testCase.body, testCase.etag, endpoint.ruleID)
				if response.Code != testCase.status {
					t.Fatalf("status = %d, want %d: %s", response.Code, testCase.status, response.Body.String())
				}
				providers.mu.Lock()
				providerCalls := providers.getCall
				providers.mu.Unlock()
				if rules.mutationCalls() != 0 || providerCalls != 0 || analyzer.calls != 0 {
					t.Fatalf("rejected request crossed dependency boundary: mutations=%d providers=%d analyzer=%d", rules.mutationCalls(), providerCalls, analyzer.calls)
				}
			})
		}
	}
}

func TestUnexpectedReadFailureAllocatesStableCorrelationIDLazily(t *testing.T) {
	t.Parallel()
	rules := &fakeRuleService{snapshot: compileRules(t, 1), getErr: errTestFailure}
	operationIDs := &sequenceIDs{values: []string{"read-failure-correlation"}}
	logCore, observedLogs := observer.New(zap.ErrorLevel)
	handler, err := NewHandler(Config{
		Rules: rules, Stats: &statsReaderStub{revision: 1}, StatsOverlay: &overlayStub{},
		Providers:    &providerCatalogStub{items: map[string]*model.Provider{}},
		OperationIDs: operationIDs, Logger: zap.New(logCore),
	})
	if err != nil {
		t.Fatal(err)
	}
	response := performRequest(
		t, handler.GetRule, http.MethodGet, "/", "", "", "11111111-1111-4111-8111-111111111111",
	)
	if response.Code != http.StatusInternalServerError || observedLogs.Len() != 1 {
		t.Fatalf("status=%d logs=%d body=%s", response.Code, observedLogs.Len(), response.Body.String())
	}
	fields := observedLogs.All()[0].ContextMap()
	if fields["operation_id"] != "read-failure-correlation" || fields["operation"] != "get" {
		t.Fatalf("log context = %#v", fields)
	}
}

func performRequest(
	t *testing.T,
	handler http.HandlerFunc,
	method, path, body, ifMatch, ruleID string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if ifMatch != "" {
		request.Header.Set("If-Match", ifMatch)
	}
	if ruleID != "" {
		request.SetPathValue("id", ruleID)
	}
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	return recorder
}

func assertStatusHeader(t *testing.T, recorder *httptest.ResponseRecorder, status int, etag string) {
	t.Helper()
	if recorder.Code != status || recorder.Header().Get("ETag") != etag {
		t.Fatalf("status/ETag = %d %q, want %d %q; body=%s", recorder.Code, recorder.Header().Get("ETag"), status, etag, recorder.Body.String())
	}
}

func assertAPIError(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
	status int,
	code, detailKey string,
	detailValue any,
) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, status, recorder.Body.String())
	}
	var response errorResponse
	decodeRecorder(t, recorder, &response)
	if response.Code != code || response.Details == nil {
		t.Fatalf("error = %#v, want code %s", response, code)
	}
	if detailKey != "" {
		if fmt.Sprint(response.Details[detailKey]) != fmt.Sprint(detailValue) {
			t.Fatalf("detail %s = %#v, want %#v", detailKey, response.Details[detailKey], detailValue)
		}
	}
}

func decodeRecorder(t *testing.T, recorder *httptest.ResponseRecorder, target any) {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(recorder.Body.String()))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		t.Fatalf("decode %s: %v", recorder.Body.String(), err)
	}
}

func padJSON(t *testing.T, body string, size int) string {
	t.Helper()
	if len(body) > size {
		t.Fatalf("body size %d exceeds target %d", len(body), size)
	}
	return body + strings.Repeat(" ", size-len(body))
}
