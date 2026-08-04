package errorruleapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestHandlerRejectsEveryInvalidBoundaryBeforeCallingDependencies(t *testing.T) {
	ruleID := "11111111-1111-4111-8111-111111111111"
	rules := &fakeRuleService{snapshot: compileRules(t, 0), getErr: errTestFailure, mutErr: errTestFailure}
	stats := &statsReaderStub{revision: 0, err: errTestFailure}
	providers := &providerCatalogStub{items: map[string]*model.Provider{}, err: errTestFailure}
	handler, err := NewHandler(Config{
		Rules: rules, Stats: stats, StatsOverlay: &overlayStub{}, Providers: providers,
	})
	if err != nil {
		t.Fatal(err)
	}
	invalidRule := `{"schema_version":1,"rule":null}`
	invalidReorder := `{"schema_version":1,"ordered_rule_ids":null}`
	validTestWithProvider := `{"schema_version":1,"api_type":"codex","provider_id":"provider-failure","content_type":"application/json","content_encoding":"identity","body":{"encoding":"utf8","value":"{}"}}`
	tests := []struct {
		name   string
		invoke http.HandlerFunc
		body   string
		etag   string
		ruleID string
		status int
		method string
	}{
		{name: "get storage", invoke: handler.GetRule, ruleID: ruleID, status: 500, method: http.MethodGet},
		{name: "update id", invoke: handler.UpdateRule, ruleID: "bad", status: 400, method: http.MethodPut},
		{name: "update precondition", invoke: handler.UpdateRule, ruleID: ruleID, body: validPassthroughMutation("x"), status: 428, method: http.MethodPut},
		{name: "update malformed", invoke: handler.UpdateRule, ruleID: ruleID, body: `{`, etag: `"internal-error-rules/0"`, status: 400, method: http.MethodPut},
		{name: "update domain", invoke: handler.UpdateRule, ruleID: ruleID, body: invalidRule, etag: `"internal-error-rules/0"`, status: 400, method: http.MethodPut},
		{name: "update storage", invoke: handler.UpdateRule, ruleID: ruleID, body: validPassthroughMutation("x"), etag: `"internal-error-rules/0"`, status: 500, method: http.MethodPut},
		{name: "delete id", invoke: handler.DeleteRule, ruleID: "bad", etag: `"internal-error-rules/0"`, status: 400, method: http.MethodDelete},
		{name: "delete precondition", invoke: handler.DeleteRule, ruleID: ruleID, status: 428, method: http.MethodDelete},
		{name: "delete storage", invoke: handler.DeleteRule, ruleID: ruleID, etag: `"internal-error-rules/0"`, status: 500, method: http.MethodDelete},
		{name: "create header", invoke: handler.CreateRule, body: validPassthroughMutation("x"), etag: `W/"internal-error-rules/0"`, status: 400, method: http.MethodPost},
		{name: "create malformed", invoke: handler.CreateRule, body: `{`, etag: `"internal-error-rules/0"`, status: 400, method: http.MethodPost},
		{name: "create domain", invoke: handler.CreateRule, body: invalidRule, etag: `"internal-error-rules/0"`, status: 400, method: http.MethodPost},
		{name: "create storage", invoke: handler.CreateRule, body: validPassthroughMutation("x"), etag: `"internal-error-rules/0"`, status: 500, method: http.MethodPost},
		{name: "reorder precondition", invoke: handler.ReorderRules, body: invalidReorder, status: 428, method: http.MethodPost},
		{name: "reorder malformed", invoke: handler.ReorderRules, body: `{`, etag: `"internal-error-rules/0"`, status: 400, method: http.MethodPost},
		{name: "reorder domain", invoke: handler.ReorderRules, body: invalidReorder, etag: `"internal-error-rules/0"`, status: 400, method: http.MethodPost},
		{name: "reorder storage", invoke: handler.ReorderRules, body: `{"schema_version":1,"ordered_rule_ids":[]}`, etag: `"internal-error-rules/0"`, status: 500, method: http.MethodPost},
		{name: "stats storage", invoke: handler.GetStats, status: 500, method: http.MethodGet},
		{name: "test header", invoke: handler.TestMessage, body: validTestWithProvider, etag: `*`, status: 400, method: http.MethodPost},
		{name: "test malformed", invoke: handler.TestMessage, body: `{`, status: 400, method: http.MethodPost},
		{name: "test domain", invoke: handler.TestMessage, body: `{"schema_version":1}`, status: 400, method: http.MethodPost},
		{name: "test provider storage", invoke: handler.TestMessage, body: validTestWithProvider, status: 500, method: http.MethodPost},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := performRequest(t, test.invoke, test.method, "/", test.body, test.etag, test.ruleID)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.status, response.Body.String())
			}
		})
	}
}

func TestContractErrorClassificationAndFieldRouting(t *testing.T) {
	t.Parallel()
	typeError := &json.UnmarshalTypeError{Value: "string", Type: reflect.TypeOf(0), Field: "schema_version"}
	wrappedTypeError := fmt.Errorf("wrapped: %w", typeError)
	tests := []struct {
		err       error
		wantField string
	}{
		{err: typeError, wantField: "schema_version"},
		{err: wrappedTypeError, wantField: "schema_version"},
		{err: &json.UnmarshalTypeError{Value: "number", Type: reflect.TypeOf("")}, wantField: "request"},
		{err: errors.New("syntax"), wantField: "request"},
	}
	for _, test := range tests {
		classified := decodeValidationError(test.err)
		if classified.Details["field"] != test.wantField {
			t.Errorf("decodeValidationError(%v) = %#v", test.err, classified)
		}
	}
	var typed *json.UnmarshalTypeError
	if errors.As(io.EOF, &typed) || !errors.As(wrappedTypeError, &typed) || typed != typeError {
		t.Fatal("typed JSON error traversal is not exact")
	}

	ruleFields := map[string]string{
		"rule name is invalid": "rule.name", "provider_id is invalid": "rule.target",
		"target is invalid": "rule.target", "API type is invalid": "rule.api_type",
		"keyword is invalid": "rule.keywords", "match_mode is invalid": "rule.match_mode",
		"action is invalid": "rule.action", "other": "rule",
	}
	for message, want := range ruleFields {
		if got := domainRuleErrorField("rule", errors.New(message)); got != want {
			t.Errorf("domainRuleErrorField(%q) = %q, want %q", message, got, want)
		}
	}
	actionFields := map[string]string{
		"max_retries invalid":   "rule.action.max_retries",
		"initial_delay invalid": "rule.action.backoff.initial_delay",
		"max_delay invalid":     "rule.action.backoff.max_delay",
		"multiplier invalid":    "rule.action.backoff.multiplier",
		"other":                 "rule.action",
	}
	for message, want := range actionFields {
		if got := domainActionErrorField("rule.action", errors.New(message)); got != want {
			t.Errorf("domainActionErrorField(%q) = %q, want %q", message, got, want)
		}
	}

	apiErr := validationError("field", "message", errTestFailure)
	if apiErr.Error() != "message" || !errors.Is(apiErr, errTestFailure) {
		t.Fatalf("API error contract = %#v", apiErr)
	}
	var nilError *apiError
	if nilError.Error() == "" || nilError.Unwrap() != nil {
		t.Fatal("nil API error methods are not defensive")
	}
	if (&duplicateFieldError{field: "x"}).Error() == "" || (&unknownFieldError{field: "x"}).Error() == "" {
		t.Fatal("strict JSON field errors lost diagnostics")
	}
	recorder := httptest.NewRecorder()
	writeAPIError(recorder, nil)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("nil API error status = %d", recorder.Code)
	}
}

func TestContractRequiredFieldsAndRawUnionValues(t *testing.T) {
	t.Parallel()
	validAPI := json.RawMessage(`"codex"`)
	nullValue := json.RawMessage(`null`)
	tests := []struct {
		name  string
		input testMessageRequest
		field string
	}{
		{name: "api", input: testMessageRequest{SchemaVersion: intPointer(1)}, field: "api_type"},
		{name: "provider", input: testMessageRequest{SchemaVersion: intPointer(1), APIType: stringPointer("codex")}, field: "provider_id"},
		{name: "content type", input: testMessageRequest{SchemaVersion: intPointer(1), APIType: stringPointer("codex"), ProviderID: nullValue}, field: "content_type"},
		{name: "encoding", input: testMessageRequest{SchemaVersion: intPointer(1), APIType: stringPointer("codex"), ProviderID: nullValue, ContentType: stringPointer("application/json")}, field: "content_encoding"},
		{name: "body", input: testMessageRequest{SchemaVersion: intPointer(1), APIType: stringPointer("codex"), ProviderID: nullValue, ContentType: stringPointer("application/json"), ContentEncoding: stringPointer("identity")}, field: "body"},
	}
	for _, test := range tests {
		_, apiErr := test.input.input()
		if apiErr == nil || apiErr.Details["field"] != test.field {
			t.Errorf("%s error = %#v", test.name, apiErr)
		}
	}
	if _, apiErr := parseNullableAPIType(validAPI, "api_type"); apiErr != nil {
		t.Fatal(apiErr)
	}
	if value, apiErr := parseNullableAPIType(nullValue, "api_type"); apiErr != nil || value != nil {
		t.Fatalf("null API type = (%v, %v)", value, apiErr)
	}
	if _, apiErr := parseNullableAPIType(json.RawMessage(`1`), "api_type"); apiErr == nil {
		t.Fatal("numeric API type accepted")
	}

	for _, body := range []messageBodyWire{
		{},
		{Encoding: stringPointer("utf8")},
	} {
		if _, apiErr := body.decode(); apiErr == nil {
			t.Fatalf("incomplete body accepted: %#v", body)
		}
	}
	var value int
	if apiErr := decodeUnionField(json.RawMessage(`1 2`), "field", &value); apiErr == nil {
		t.Fatal("trailing union value accepted")
	}
	if apiErr := decodeUnionField(json.RawMessage(`"x"`), "field", &value); apiErr == nil {
		t.Fatal("wrong union value type accepted")
	}

	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"SCHEMA_VERSION":1}`))
	var reorder reorderRequest
	if apiErr := decodeRequest(httptest.NewRecorder(), request, MaxRuleReorderRequestBytes, &reorder); apiErr == nil {
		t.Fatal("non-canonical reorder key accepted")
	}
}

func intPointer(value int) *int          { return &value }
func stringPointer(value string) *string { return &value }
