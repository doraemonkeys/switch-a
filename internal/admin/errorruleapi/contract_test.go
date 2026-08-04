package errorruleapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/errorrule"
	errorrulesqlite "github.com/doraemonkeys/switch-a/internal/errorrule/sqlite"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestRuleSetETagStrictness(t *testing.T) {
	t.Parallel()
	if got := FormatRuleSetETag(42); got != `"internal-error-rules/42"` {
		t.Fatalf("FormatRuleSetETag(42) = %q", got)
	}
	valid := []string{`"internal-error-rules/0"`, `"internal-error-rules/9223372036854775807"`}
	for _, value := range valid {
		if _, err := ParseRuleSetETag(value); err != nil {
			t.Errorf("ParseRuleSetETag(%q): %v", value, err)
		}
	}
	invalid := []string{
		"", "*", `W/"internal-error-rules/1"`, `"internal-error-rules/01"`,
		`"internal-error-rules/+1"`, `"internal-error-rules/-1"`, `"internal-error-rules/9223372036854775808"`,
		`"other/1"`, `"internal-error-rules/1", "internal-error-rules/2"`, ` "internal-error-rules/1"`,
	}
	for _, value := range invalid {
		if _, err := ParseRuleSetETag(value); err == nil {
			t.Errorf("ParseRuleSetETag(%q) succeeded", value)
		}
	}
}

func TestParseIfMatchRequiredOptionalAndMultiple(t *testing.T) {
	t.Parallel()
	if revision, apiErr := parseIfMatch(http.Header{}, false); revision != nil || apiErr != nil {
		t.Fatalf("optional missing = (%v, %v)", revision, apiErr)
	}
	_, apiErr := parseIfMatch(http.Header{}, true)
	if apiErr == nil || apiErr.Status != http.StatusPreconditionRequired || apiErr.Code != ErrorCodePreconditionRequired {
		t.Fatalf("required missing = %#v", apiErr)
	}
	header := http.Header{"If-Match": {`"internal-error-rules/3"`}}
	revision, apiErr := parseIfMatch(header, true)
	if apiErr != nil || revision == nil || *revision != 3 {
		t.Fatalf("valid If-Match = (%v, %v)", revision, apiErr)
	}
	for _, values := range [][]string{
		{`"internal-error-rules/3"`, `"internal-error-rules/3"`},
		{`"internal-error-rules/3", "internal-error-rules/4"`},
		{`W/"internal-error-rules/3"`},
	} {
		_, apiErr := parseIfMatch(http.Header{"If-Match": values}, true)
		if apiErr == nil || apiErr.Status != http.StatusBadRequest {
			t.Errorf("If-Match %q error = %#v", values, apiErr)
		}
	}
}

func TestStrictMutationRequestRejectsMissingUnknownDuplicateTrailingAndUnionFields(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		body      string
		wantField string
		wantText  string
	}{
		{name: "schema", body: `{"rule":{}}`, wantField: "schema_version"},
		{name: "unsupported schema", body: `{"schema_version":2,"rule":{}}`, wantField: "schema_version"},
		{name: "missing enabled", body: `{"schema_version":1,"rule":{"name":"x","target":{"kind":"global"},"api_type":null,"keywords":["x"],"match_mode":"any","action":{"type":"passthrough"}}}`, wantField: "rule.enabled"},
		{name: "missing nullable api", body: `{"schema_version":1,"rule":{"name":"x","enabled":true,"target":{"kind":"global"},"keywords":["x"],"match_mode":"any","action":{"type":"passthrough"}}}`, wantField: "rule.api_type"},
		{name: "global provider", body: `{"schema_version":1,"rule":{"name":"x","enabled":true,"target":{"kind":"global","provider_id":"p"},"api_type":null,"keywords":["x"],"match_mode":"any","action":{"type":"passthrough"}}}`, wantField: "rule.target.provider_id"},
		{name: "global null provider", body: `{"schema_version":1,"rule":{"name":"x","enabled":true,"target":{"kind":"global","provider_id":null},"api_type":null,"keywords":["x"],"match_mode":"any","action":{"type":"passthrough"}}}`, wantField: "rule.target.provider_id"},
		{name: "passthrough retry", body: `{"schema_version":1,"rule":{"name":"x","enabled":true,"target":{"kind":"global"},"api_type":null,"keywords":["x"],"match_mode":"any","action":{"type":"passthrough","max_retries":1}}}`, wantField: "rule.action.max_retries", wantText: "rule.action contains fields outside its discriminator"},
		{name: "passthrough null retry", body: `{"schema_version":1,"rule":{"name":"x","enabled":true,"target":{"kind":"global"},"api_type":null,"keywords":["x"],"match_mode":"any","action":{"type":"passthrough","max_retries":null}}}`, wantField: "rule.action.max_retries", wantText: "rule.action contains fields outside its discriminator"},
		{name: "passthrough null backoff", body: `{"schema_version":1,"rule":{"name":"x","enabled":true,"target":{"kind":"global"},"api_type":null,"keywords":["x"],"match_mode":"any","action":{"type":"passthrough","backoff":null}}}`, wantField: "rule.action.backoff", wantText: "rule.action contains fields outside its discriminator"},
		{name: "retry null max", body: `{"schema_version":1,"rule":{"name":"x","enabled":true,"target":{"kind":"global"},"api_type":null,"keywords":["x"],"match_mode":"any","action":{"type":"retry_only","max_retries":null,"backoff":{"initial_delay":"0s","max_delay":"0s","multiplier":0,"jitter":false}}}}`, wantField: "rule.action.max_retries"},
		{name: "retry null backoff", body: `{"schema_version":1,"rule":{"name":"x","enabled":true,"target":{"kind":"global"},"api_type":null,"keywords":["x"],"match_mode":"any","action":{"type":"retry_only","max_retries":1,"backoff":null}}}`, wantField: "rule.action.backoff"},
		{name: "retry missing jitter", body: `{"schema_version":1,"rule":{"name":"x","enabled":true,"target":{"kind":"global"},"api_type":null,"keywords":["x"],"match_mode":"any","action":{"type":"retry_only","max_retries":1,"backoff":{"initial_delay":"0s","max_delay":"0s","multiplier":0}}}}`, wantField: "rule.action.backoff.jitter"},
		{name: "backoff unknown", body: `{"schema_version":1,"rule":{"name":"x","enabled":true,"target":{"kind":"global"},"api_type":null,"keywords":["x"],"match_mode":"any","action":{"type":"retry_only","max_retries":1,"backoff":{"initial_delay":"0s","max_delay":"0s","multiplier":0,"jitter":false,"extra":true}}}}`, wantField: "rule.action.backoff.extra"},
		{name: "unknown", body: validPassthroughMutation("x")[:len(validPassthroughMutation("x"))-1] + `,"extra":true}`, wantField: "extra"},
		{name: "case alias", body: strings.Replace(validPassthroughMutation("x"), `"schema_version"`, `"SCHEMA_VERSION"`, 1), wantField: "SCHEMA_VERSION"},
		{name: "case collision", body: strings.Replace(validPassthroughMutation("x"), `"schema_version":1`, `"schema_version":2,"SCHEMA_VERSION":1`, 1), wantField: "SCHEMA_VERSION"},
		{name: "nested case alias", body: strings.Replace(validPassthroughMutation("x"), `"enabled"`, `"Enabled"`, 1), wantField: "rule.Enabled"},
		{name: "duplicate", body: `{"schema_version":1,"schema_version":1,"rule":{}}`, wantField: "schema_version", wantText: "request contains a duplicate field"},
		{name: "trailing", body: validPassthroughMutation("x") + `{}`, wantField: "request"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
			var decoded mutationRequest
			apiErr := decodeRequest(httptest.NewRecorder(), request, MaxRuleMutationRequestBytes, &decoded)
			if apiErr == nil {
				_, apiErr = decoded.domainRule()
			}
			if apiErr == nil || apiErr.Status != http.StatusBadRequest || apiErr.Details["field"] != test.wantField {
				t.Fatalf("error = %#v, want field %q", apiErr, test.wantField)
			}
			if test.wantText != "" && apiErr.Message != test.wantText {
				t.Fatalf("message = %q, want %q", apiErr.Message, test.wantText)
			}
		})
	}
}

func TestStrictRuleRequestNormalizesAndKeepsRequiredBackoffZeroes(t *testing.T) {
	t.Parallel()
	body := `{"schema_version":1,"rule":{"name":"  Retry  ","enabled":false,"target":{"kind":"provider","provider_id":"p"},"api_type":"custom:tool","keywords":[" CAPACITY ","capacity"],"match_mode":"all","action":{"type":"retry_then_switch","max_retries":0,"backoff":{"initial_delay":"0s","max_delay":"0s","multiplier":0,"jitter":false}}}}`
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	var decoded mutationRequest
	if apiErr := decodeRequest(httptest.NewRecorder(), request, MaxRuleMutationRequestBytes, &decoded); apiErr != nil {
		t.Fatal(apiErr)
	}
	spec, apiErr := decoded.domainRule()
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	retry, ok := spec.Action.RetryPolicy()
	if spec.Name != "Retry" || !reflect.DeepEqual(spec.Keywords, []string{"capacity"}) || !ok ||
		retry.MaxRetries != 0 || retry.Backoff.Multiplier != 0 || retry.Backoff.Jitter {
		t.Fatalf("normalized spec = %#v, retry=%#v", spec, retry)
	}
}

func TestReorderAndMessageWireValidation(t *testing.T) {
	t.Parallel()
	invalidReorders := []string{
		`{"schema_version":1}`,
		`{"schema_version":1,"ordered_rule_ids":["bad"]}`,
		`{"schema_version":1,"ordered_rule_ids":["11111111-1111-4111-8111-111111111111","11111111-1111-4111-8111-111111111111"]}`,
	}
	for _, body := range invalidReorders {
		var request reorderRequest
		apiErr := decodeRequest(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)), MaxRuleReorderRequestBytes, &request)
		if apiErr == nil {
			_, apiErr = request.ruleIDs()
		}
		if apiErr == nil || apiErr.Status != http.StatusBadRequest {
			t.Errorf("reorder %s error = %#v", body, apiErr)
		}
	}

	messageTests := []struct {
		body  string
		field string
	}{
		{body: `{"schema_version":1,"api_type":"custom:x","provider_id":null,"content_type":"application/json","content_encoding":"identity","body":{"encoding":"utf8","value":"{}"}}`, field: "api_type"},
		{body: `{"schema_version":1,"api_type":"codex","content_type":"application/json","content_encoding":"identity","body":{"encoding":"utf8","value":"{}"}}`, field: "provider_id"},
		{body: `{"schema_version":1,"api_type":"codex","provider_id":" ","content_type":"application/json","content_encoding":"identity","body":{"encoding":"utf8","value":"{}"}}`, field: "provider_id"},
		{body: `{"schema_version":1,"api_type":"codex","provider_id":null,"content_type":"application/json","content_encoding":"identity","body":{"encoding":"base64","value":"e30"}}`, field: "body.value"},
		{body: `{"schema_version":1,"api_type":"codex","provider_id":null,"content_type":"application/json","content_encoding":"identity","body":{"encoding":"hex","value":"00"}}`, field: "body.encoding"},
	}
	for _, test := range messageTests {
		var request testMessageRequest
		apiErr := decodeRequest(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body)), MaxTestMessageRequestBytes, &request)
		if apiErr == nil {
			_, apiErr = request.input()
		}
		if apiErr == nil || apiErr.Details["field"] != test.field {
			t.Errorf("message error = %#v, want field %s", apiErr, test.field)
		}
	}
}

func TestDecodeRequestLimitsUTF8AndReadFailures(t *testing.T) {
	t.Parallel()
	target := map[string]any{}
	exact := `{"x":1}` + strings.Repeat(" ", 32-len(`{"x":1}`))
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(exact))
	if apiErr := decodeRequest(httptest.NewRecorder(), request, 32, &target); apiErr != nil {
		t.Fatalf("exact limit: %v", apiErr)
	}
	request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(exact+" "))
	if apiErr := decodeRequest(httptest.NewRecorder(), request, 32, &target); apiErr == nil || apiErr.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("limit+1 error = %#v", apiErr)
	}
	request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}))
	request.ContentLength = -1
	if apiErr := decodeRequest(httptest.NewRecorder(), request, 32, &target); apiErr == nil || !strings.Contains(apiErr.Message, "UTF-8") {
		t.Fatalf("invalid UTF-8 error = %#v", apiErr)
	}
	request = httptest.NewRequest(http.MethodPost, "/", &failingReader{})
	if apiErr := decodeRequest(httptest.NewRecorder(), request, 32, &target); apiErr == nil || !errors.Is(apiErr, errTestFailure) {
		t.Fatalf("read error = %#v", apiErr)
	}
	request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	if apiErr := decodeRequest(httptest.NewRecorder(), request, 32, &target); apiErr == nil {
		t.Fatal("empty body accepted")
	}
	if apiErr := decodeRequest(httptest.NewRecorder(), nil, 32, &target); apiErr == nil {
		t.Fatal("nil request accepted")
	}
}

type failingReader struct{}

func (*failingReader) Read([]byte) (int, error) { return 0, errTestFailure }

func TestErrorResponseAlwaysHasTypedDetailsAndEncodingFallback(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	writeAPIError(recorder, &apiError{Status: 418, Code: ErrorCodeInternal, Message: "teapot"})
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["details"] == nil || recorder.Header().Get("Content-Type") != jsonContentType {
		t.Fatalf("response = %#v headers=%v", response, recorder.Header())
	}
	recorder = httptest.NewRecorder()
	writeJSON(recorder, http.StatusOK, make(chan int))
	if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), ErrorCodeInternal) {
		t.Fatalf("encoding fallback = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestServiceAPIErrorMappings(t *testing.T) {
	t.Parallel()
	ruleID := errorrule.RuleID("11111111-1111-4111-8111-111111111111")
	tests := []struct {
		err    error
		status int
		code   string
	}{
		{err: errorrulesqlite.ErrRuleNotFound, status: 404, code: ErrorCodeNotFound},
		{err: errorrulesqlite.ErrRuleCapacity, status: 409, code: ErrorCodeConflict},
		{err: errorrulesqlite.ErrRevisionOverflow, status: 409, code: ErrorCodeConflict},
		{err: &errorrulesqlite.RevisionMismatchError{Expected: 1, Current: 2}, status: 412, code: ErrorCodeRevisionMismatch},
		{err: &providerNotFoundError{providerID: "p"}, status: 404, code: ErrorCodeNotFound},
		{err: &errorrulesqlite.ProviderNotFoundError{ProviderID: "p"}, status: 404, code: ErrorCodeNotFound},
		{err: errTestFailure, status: 500, code: ErrorCodeInternal},
	}
	for _, test := range tests {
		apiErr := serviceAPIError(ruleID, test.err)
		if apiErr.Status != test.status || apiErr.Code != test.code {
			t.Errorf("serviceAPIError(%v) = %#v", test.err, apiErr)
		}
	}
	direct := validationError("x", "bad", nil)
	if serviceAPIError(ruleID, direct) != direct {
		t.Fatal("direct API error identity was not preserved")
	}
	if got := requestTooLargeError(12).Details["limit_bytes"]; got != int64(12) {
		t.Fatalf("limit detail = %#v", got)
	}
	if got := serviceAPIError(ruleID, errorrulesqlite.ErrRevisionOverflow).Details; len(got) != 0 {
		t.Fatalf("revision overflow details = %#v", got)
	}
}

func TestRuleWireAlwaysEmitsZeroAndFalseBackoffFields(t *testing.T) {
	t.Parallel()
	action, err := errorrule.NewRetryOnlyAction(1, model.BackoffPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	rule := testRule(t,
		"11111111-1111-4111-8111-111111111111",
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "retry", 0, action,
	)
	payload, err := json.Marshal(newRuleResponse(7, rule))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"multiplier":0`, `"jitter":false`} {
		if !bytes.Contains(payload, []byte(field)) {
			t.Errorf("wire response omits %s: %s", field, payload)
		}
	}
}
