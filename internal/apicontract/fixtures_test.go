package apicontract_test

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/model"
)

// These values are frozen fixture assertions, not production domain
// authorities. Their owning runtime modules validate the same boundaries when
// they consume this corpus in later waves.
const (
	sharedSchemaVersion       = 1
	configFixtureVersion      = "4.0"
	evidenceFixtureWireLimit  = 32 << 10
	fixtureRuleCountLimit     = 256
	fixtureRuleNameByteLimit  = 128
	fixtureKeywordCountLimit  = 16
	fixtureKeywordByteLimit   = 128
	fixtureKeywordTotalLimit  = 2_048
	fixtureRuleRetryLimit     = 10
	fixtureGlobalAttemptLimit = 4
	fixtureActionPassthrough  = "passthrough"
)

var sharedFixtureManifest = []string{
	"api-catalog-internal.json",
	"api-catalog.json",
	"attempt-evidence-v2.json",
	"backoff-policy.json",
	"config-v4.json",
	"errors.json",
	"protocol-envelopes-negative.json",
	"protocol-envelopes-positive.json",
	"reorder.json",
	"rule-list.json",
	"rule-mutations.json",
	"rule-stats.json",
	"test-message.json",
}

func TestSharedFixtureManifestAndSchemas(t *testing.T) {
	t.Parallel()

	directory := filepath.Join("..", "..", "contracts", "internal-error", "v1")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	actual := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			actual[entry.Name()] = struct{}{}
		}
	}
	if len(actual) != len(sharedFixtureManifest) {
		t.Fatalf("shared fixture manifest has %d JSON files, want exactly %d: %v", len(actual), len(sharedFixtureManifest), actual)
	}
	for _, name := range sharedFixtureManifest {
		name := name
		t.Run(name, func(t *testing.T) {
			if _, ok := actual[name]; !ok {
				t.Fatalf("required shared fixture %q is missing", name)
			}
			data, err := os.ReadFile(filepath.Join(directory, name))
			if err != nil {
				t.Fatal(err)
			}
			validateFixtureSchema(t, name, data)
		})
	}
}

type schemaCasesFixture struct {
	SchemaVersion int               `json:"schema_version"`
	Cases         []json.RawMessage `json:"cases"`
}

type backoffFixtureEnvelope struct {
	SchemaVersion   int               `json:"schema_version"`
	ValidationCases []json.RawMessage `json:"validation_cases"`
}

type configFixtureEnvelope struct {
	Version            string            `json:"version"`
	ExportedAt         string            `json:"exported_at"`
	Providers          []json.RawMessage `json:"providers"`
	Groups             []json.RawMessage `json:"groups"`
	RoutingPolicies    []json.RawMessage `json:"routing_policies"`
	Settings           map[string]string `json:"settings"`
	InternalErrorRules []json.RawMessage `json:"internal_error_rules"`
}

type errorsFixtureEnvelope struct {
	Cases []struct {
		Name   string `json:"name"`
		Status int    `json:"status"`
		Body   struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"body"`
	} `json:"cases"`
}

type ruleListFixtureEnvelope struct {
	SchemaVersion   int               `json:"schema_version"`
	RuleSetRevision string            `json:"rule_set_revision"`
	Rules           []json.RawMessage `json:"rules"`
}

type ruleStatsFixtureEnvelope struct {
	SchemaVersion   int               `json:"schema_version"`
	RuleSetRevision string            `json:"rule_set_revision"`
	Stats           []json.RawMessage `json:"stats"`
}

type ruleMutationRequestFixture struct {
	SchemaVersion int             `json:"schema_version"`
	Rule          json.RawMessage `json:"rule"`
}

type singleRuleResponseFixture struct {
	SchemaVersion   int             `json:"schema_version"`
	RuleSetRevision string          `json:"rule_set_revision"`
	Rule            json.RawMessage `json:"rule"`
}

type ruleMutationsFixtureEnvelope struct {
	Create struct {
		IfMatch  string                     `json:"if_match"`
		Request  ruleMutationRequestFixture `json:"request"`
		Status   int                        `json:"status"`
		Location string                     `json:"location"`
		ETag     string                     `json:"etag"`
		Response singleRuleResponseFixture  `json:"response"`
	} `json:"create"`
	Update struct {
		RuleID   string                     `json:"rule_id"`
		IfMatch  string                     `json:"if_match"`
		Request  ruleMutationRequestFixture `json:"request"`
		Status   int                        `json:"status"`
		ETag     string                     `json:"etag"`
		Response singleRuleResponseFixture  `json:"response"`
	} `json:"update"`
	Delete struct {
		RuleID       string          `json:"rule_id"`
		IfMatch      string          `json:"if_match"`
		Status       int             `json:"status"`
		ETag         string          `json:"etag"`
		ResponseBody json.RawMessage `json:"response_body"`
	} `json:"delete"`
}

type reorderFixtureEnvelope struct {
	IfMatch string `json:"if_match"`
	Request struct {
		SchemaVersion  int      `json:"schema_version"`
		OrderedRuleIDs []string `json:"ordered_rule_ids"`
	} `json:"request"`
	Status   int    `json:"status"`
	ETag     string `json:"etag"`
	Response struct {
		SchemaVersion   int               `json:"schema_version"`
		RuleSetRevision string            `json:"rule_set_revision"`
		Rules           []json.RawMessage `json:"rules"`
	} `json:"response"`
}

type testMessageRequestFixture struct {
	SchemaVersion   int     `json:"schema_version"`
	APIType         string  `json:"api_type"`
	ProviderID      *string `json:"provider_id"`
	ContentType     string  `json:"content_type"`
	ContentEncoding string  `json:"content_encoding"`
	Body            struct {
		Encoding string `json:"encoding"`
		Value    string `json:"value"`
	} `json:"body"`
}

type testMessageResponseFixture struct {
	SchemaVersion      int               `json:"schema_version"`
	RuleSetRevision    string            `json:"rule_set_revision"`
	ResponseProtocolID json.RawMessage   `json:"response_protocol_id"`
	AnalysisStatus     string            `json:"analysis_status"`
	AnalysisReason     json.RawMessage   `json:"analysis_reason"`
	Errors             []json.RawMessage `json:"errors"`
	DecisiveErrorIndex json.RawMessage   `json:"decisive_error_index"`
	Winner             json.RawMessage   `json:"winner"`
}

type testMessageFixtureEnvelope struct {
	Complete struct {
		IfMatch  string                     `json:"if_match"`
		Request  testMessageRequestFixture  `json:"request"`
		Status   int                        `json:"status"`
		Response testMessageResponseFixture `json:"response"`
	} `json:"complete"`
	FailOpen struct {
		Request  testMessageRequestFixture  `json:"request"`
		Status   int                        `json:"status"`
		Response testMessageResponseFixture `json:"response"`
	} `json:"fail_open"`
}

func validateFixtureSchema(t *testing.T, name string, data []byte) {
	t.Helper()

	switch name {
	case "api-catalog.json":
		fixture := decodeStrictFixture[apicontract.CatalogResponse](t, data)
		assertFixtureSchemaVersion(t, fixture.SchemaVersion)
		if len(fixture.APITypes) == 0 {
			t.Fatal("catalog fixture contains no API types")
		}
	case "api-catalog-internal.json":
		fixture := decodeStrictFixture[[]apicontract.Definition](t, data)
		if len(fixture) != 6 {
			t.Fatalf("internal catalog contains %d definitions, want 6", len(fixture))
		}
	case "attempt-evidence-v2.json", "protocol-envelopes-negative.json", "protocol-envelopes-positive.json":
		fixture := decodeStrictFixture[schemaCasesFixture](t, data)
		assertFixtureSchemaVersion(t, fixture.SchemaVersion)
		if len(fixture.Cases) == 0 {
			t.Fatal("case fixture is empty")
		}
	case "backoff-policy.json":
		fixture := decodeStrictFixture[backoffFixtureEnvelope](t, data)
		assertFixtureSchemaVersion(t, fixture.SchemaVersion)
		if len(fixture.ValidationCases) == 0 {
			t.Fatal("backoff fixture contains no validation cases")
		}
	case "config-v4.json":
		fixture := decodeStrictFixture[configFixtureEnvelope](t, data)
		if fixture.Version != configFixtureVersion {
			t.Fatalf("config fixture version = %q, want %q", fixture.Version, configFixtureVersion)
		}
	case "errors.json":
		fixture := decodeStrictFixture[errorsFixtureEnvelope](t, data)
		wantCodes := map[int]string{
			400: "VALIDATION_ERROR",
			404: "NOT_FOUND",
			409: "CONFLICT",
			412: "REVISION_MISMATCH",
			413: "REQUEST_TOO_LARGE",
			428: "PRECONDITION_REQUIRED",
			500: "INTERNAL_ERROR",
		}
		if len(fixture.Cases) != len(wantCodes) {
			t.Fatalf("error fixture contains %d cases, want %d", len(fixture.Cases), len(wantCodes))
		}
		for _, testCase := range fixture.Cases {
			if wantCode, ok := wantCodes[testCase.Status]; !ok || testCase.Body.Code != wantCode {
				t.Errorf("error status %d has code %q, want %q", testCase.Status, testCase.Body.Code, wantCode)
			}
			delete(wantCodes, testCase.Status)
		}
		if len(wantCodes) != 0 {
			t.Fatalf("error fixture omits status/code contracts: %v", wantCodes)
		}
	case "reorder.json":
		fixture := decodeStrictFixture[reorderFixtureEnvelope](t, data)
		assertFixtureSchemaVersion(t, fixture.Request.SchemaVersion)
		assertFixtureSchemaVersion(t, fixture.Response.SchemaVersion)
		if fixture.Status != 200 || fixture.ETag == "" {
			t.Fatal("reorder fixture must expose HTTP 200 and its resulting ETag")
		}
	case "rule-list.json":
		fixture := decodeStrictFixture[ruleListFixtureEnvelope](t, data)
		assertFixtureSchemaVersion(t, fixture.SchemaVersion)
	case "rule-mutations.json":
		fixture := decodeStrictFixture[ruleMutationsFixtureEnvelope](t, data)
		assertFixtureSchemaVersion(t, fixture.Create.Request.SchemaVersion)
		assertFixtureSchemaVersion(t, fixture.Create.Response.SchemaVersion)
		assertFixtureSchemaVersion(t, fixture.Update.Request.SchemaVersion)
		assertFixtureSchemaVersion(t, fixture.Update.Response.SchemaVersion)
		if fixture.Create.Status != 201 || fixture.Create.Location == "" || fixture.Create.ETag == "" {
			t.Fatal("create fixture must expose HTTP 201, Location, and its resulting ETag")
		}
		if fixture.Update.Status != 200 || fixture.Update.ETag == "" {
			t.Fatal("update fixture must expose HTTP 200 and its resulting ETag")
		}
		if fixture.Delete.Status != 204 || fixture.Delete.ETag == "" || string(fixture.Delete.ResponseBody) != "null" {
			t.Fatal("DELETE fixture must expose its resulting ETag and a null response body")
		}
	case "rule-stats.json":
		fixture := decodeStrictFixture[ruleStatsFixtureEnvelope](t, data)
		assertFixtureSchemaVersion(t, fixture.SchemaVersion)
	case "test-message.json":
		fixture := decodeStrictFixture[testMessageFixtureEnvelope](t, data)
		assertFixtureSchemaVersion(t, fixture.Complete.Request.SchemaVersion)
		assertFixtureSchemaVersion(t, fixture.Complete.Response.SchemaVersion)
		assertFixtureSchemaVersion(t, fixture.FailOpen.Request.SchemaVersion)
		assertFixtureSchemaVersion(t, fixture.FailOpen.Response.SchemaVersion)
		if fixture.Complete.Status != 200 || fixture.FailOpen.Status != 200 {
			t.Fatal("valid Test Message cases must return HTTP 200, including fail-open analysis")
		}
	default:
		t.Fatalf("fixture %q has no strict schema harness", name)
	}
}

func decodeStrictFixture[T any](t *testing.T, data []byte) T {
	t.Helper()

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var fixture T
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatalf("strict fixture decode: %v", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("fixture must contain exactly one JSON value, got trailing decode error %v", err)
	}
	return fixture
}

func assertFixtureSchemaVersion(t *testing.T, got int) {
	t.Helper()
	if got != sharedSchemaVersion {
		t.Fatalf("schema_version = %d, want %d", got, sharedSchemaVersion)
	}
}

func TestWorstCaseEvidenceFixtureFitsWireLimit(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "internal-error", "v1", "attempt-evidence-v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name     string         `json:"name"`
			Evidence map[string]any `json:"evidence"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range fixture.Cases {
		if testCase.Name != "worst_case_bounds" {
			continue
		}
		encoded, err := json.Marshal(testCase.Evidence)
		if err != nil {
			t.Fatal(err)
		}
		if len(encoded) > evidenceFixtureWireLimit {
			t.Fatalf("worst-case evidence uses %d bytes, limit is %d", len(encoded), evidenceFixtureWireLimit)
		}
		semantic := testCase.Evidence["semantic_error"].(map[string]any)
		rule := semantic["rule"].(map[string]any)
		if got := len(rule["matching_rule_ids"].([]any)); got != fixtureRuleCountLimit {
			t.Fatalf("worst-case matching IDs = %d, want %d", got, fixtureRuleCountLimit)
		}
		snapshot := rule["normalized_snapshot"].(map[string]any)
		if got := len([]byte(snapshot["name"].(string))); got != fixtureRuleNameByteLimit {
			t.Fatalf("worst-case rule name = %d bytes, want %d", got, fixtureRuleNameByteLimit)
		}
		if got := len(snapshot["keywords"].([]any)); got != fixtureKeywordCountLimit {
			t.Fatalf("worst-case keywords = %d, want %d", got, fixtureKeywordCountLimit)
		}
		totalKeywordBytes := 0
		for _, keywordValue := range snapshot["keywords"].([]any) {
			keywordBytes := len([]byte(keywordValue.(string)))
			if keywordBytes != fixtureKeywordByteLimit {
				t.Fatalf("worst-case keyword = %d bytes, want %d", keywordBytes, fixtureKeywordByteLimit)
			}
			totalKeywordBytes += keywordBytes
		}
		if totalKeywordBytes != fixtureKeywordTotalLimit {
			t.Fatalf("worst-case keywords total = %d bytes, want %d", totalKeywordBytes, fixtureKeywordTotalLimit)
		}
		return
	}
	t.Fatal("worst_case_bounds evidence case missing")
}

func TestEvidenceFixtureKeepsRuleAndRetryAxesConsistent(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "internal-error", "v1", "attempt-evidence-v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name     string         `json:"name"`
			Evidence map[string]any `json:"evidence"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range fixture.Cases {
		t.Run(testCase.Name, func(t *testing.T) {
			semantic := testCase.Evidence["semantic_error"].(map[string]any)
			identity := semantic["identity"].(map[string]any)
			rule := semantic["rule"].(map[string]any)
			retry := semantic["retry"].(map[string]any)
			decision := semantic["decision"].(map[string]any)
			snapshot := rule["normalized_snapshot"].(map[string]any)
			action := snapshot["action"].(map[string]any)
			if action["type"] != retry["action"] {
				t.Fatalf("snapshot action %q differs from retry action %q", action["type"], retry["action"])
			}
			if action["type"] == fixtureActionPassthrough {
				if retry["rule_retry_limit"] != float64(0) {
					t.Fatal("passthrough evidence has a non-zero retry limit")
				}
			} else if action["max_retries"] != retry["rule_retry_limit"] {
				t.Fatalf("snapshot max retries %v differs from retry limit %v", action["max_retries"], retry["rule_retry_limit"])
			}
			winnerID := rule["winner_id"]
			foundWinner := false
			for _, matchingID := range rule["matching_rule_ids"].([]any) {
				foundWinner = foundWinner || matchingID == winnerID
			}
			if !foundWinner {
				t.Fatal("winner is absent from ordered matching rule IDs")
			}

			logicalAttempt := fixtureDecimalString(t, identity["logical_attempt"])
			providerAttempt := fixtureDecimalString(t, identity["provider_attempt"])
			globalStarted := fixtureDecimalString(t, retry["global_attempts_started"])
			if logicalAttempt != globalStarted {
				t.Fatalf("logical attempt %d differs from global attempts started %d", logicalAttempt, globalStarted)
			}
			if providerAttempt == 0 || providerAttempt > logicalAttempt {
				t.Fatalf("provider attempt %d is invalid for logical attempt %d", providerAttempt, logicalAttempt)
			}
			if unlimited := retry["global_attempts_unlimited"].(bool); unlimited {
				if retry["global_attempts_remaining"] != nil {
					t.Fatal("unlimited global attempts must use a null remaining count")
				}
			} else {
				remaining := fixtureDecimalString(t, retry["global_attempts_remaining"])
				if globalStarted+remaining != fixtureGlobalAttemptLimit {
					t.Fatalf("finite ledger started %d + remaining %d, want %d", globalStarted, remaining, fixtureGlobalAttemptLimit)
				}
				scheduled := fixtureDecimalString(t, retry["rule_retries_scheduled"])
				limit := uint64(retry["rule_retry_limit"].(float64))
				switch decision["reason"] {
				case "retry_budget_available":
					if scheduled >= limit || remaining <= 1 {
						t.Fatalf("retry decision has scheduled=%d limit=%d remaining=%d", scheduled, limit, remaining)
					}
				case "rule_retry_budget_exhausted":
					if scheduled < limit {
						t.Fatalf("exhausted decision has scheduled=%d below limit=%d", scheduled, limit)
					}
				case "reserved_switch_attempt":
					if remaining != 1 {
						t.Fatalf("reserved switch decision has %d global attempts remaining, want 1", remaining)
					}
				}
			}
		})
	}
}

func fixtureDecimalString(t *testing.T, value any) uint64 {
	t.Helper()
	text, ok := value.(string)
	if !ok {
		t.Fatalf("decimal fixture value has type %T, want string", value)
	}
	parsed, err := strconv.ParseUint(text, 10, 64)
	if err != nil || strconv.FormatUint(parsed, 10) != text {
		t.Fatalf("fixture value %q is not a canonical unsigned decimal", text)
	}
	return parsed
}

func TestRuleMutationFixturesFormExecutableRevisionBranches(t *testing.T) {
	t.Parallel()

	directory := filepath.Join("..", "..", "contracts", "internal-error", "v1")
	listData, err := os.ReadFile(filepath.Join(directory, "rule-list.json"))
	if err != nil {
		t.Fatal(err)
	}
	list := decodeStrictFixture[ruleListFixtureEnvelope](t, listData)
	mutationsData, err := os.ReadFile(filepath.Join(directory, "rule-mutations.json"))
	if err != nil {
		t.Fatal(err)
	}
	mutations := decodeStrictFixture[ruleMutationsFixtureEnvelope](t, mutationsData)
	reorderData, err := os.ReadFile(filepath.Join(directory, "reorder.json"))
	if err != nil {
		t.Fatal(err)
	}
	reorder := decodeStrictFixture[reorderFixtureEnvelope](t, reorderData)

	if mutations.Create.IfMatch != fixtureRuleETag(list.RuleSetRevision) {
		t.Fatalf("create If-Match %q does not continue base revision %q", mutations.Create.IfMatch, list.RuleSetRevision)
	}
	created := decodeFixtureRuleIdentity(t, mutations.Create.Response.Rule)
	for _, rawRule := range list.Rules {
		if base := decodeFixtureRuleIdentity(t, rawRule); base.ID == created.ID {
			t.Fatalf("create rule %q already exists in the base list", created.ID)
		}
	}
	if created.Position != len(list.Rules) || mutations.Create.ETag != fixtureRuleETag(mutations.Create.Response.RuleSetRevision) {
		t.Fatal("create response does not append the rule and expose its resulting revision")
	}
	if mutations.Update.RuleID != created.ID || mutations.Update.IfMatch != mutations.Create.ETag {
		t.Fatal("update does not continue the created rule revision")
	}
	if mutations.Update.ETag != fixtureRuleETag(mutations.Update.Response.RuleSetRevision) {
		t.Fatal("update ETag does not describe its response revision")
	}
	if mutations.Delete.RuleID != created.ID || mutations.Delete.IfMatch != mutations.Update.ETag {
		t.Fatal("delete does not branch from the updated rule revision")
	}
	if reorder.IfMatch != mutations.Update.ETag {
		t.Fatal("reorder does not form the alternate executable branch from the updated revision")
	}
	if reorder.ETag != fixtureRuleETag(reorder.Response.RuleSetRevision) || mutations.Delete.ETag != reorder.ETag {
		t.Fatal("delete and reorder branches do not expose the same single-step resulting revision")
	}
}

func TestConfigFixtureProviderRuleReferencesResolve(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "internal-error", "v1", "config-v4.json"))
	if err != nil {
		t.Fatal(err)
	}
	fixture := decodeStrictFixture[configFixtureEnvelope](t, data)
	providerAPITypes := make(map[string]map[string]struct{}, len(fixture.Providers))
	for _, rawProvider := range fixture.Providers {
		var providerShape map[string]json.RawMessage
		if err := json.Unmarshal(rawProvider, &providerShape); err != nil {
			t.Fatal(err)
		}
		if _, present := providerShape["backoff"]; present {
			t.Fatal("zero provider backoff must follow ExportedProvider omitzero encoding")
		}
		var provider struct {
			ID       string `json:"id"`
			APITypes []struct {
				APIType string `json:"api_type"`
			} `json:"api_types"`
		}
		if err := json.Unmarshal(rawProvider, &provider); err != nil {
			t.Fatal(err)
		}
		configured := make(map[string]struct{}, len(provider.APITypes))
		for _, apiType := range provider.APITypes {
			configured[apiType.APIType] = struct{}{}
		}
		providerAPITypes[provider.ID] = configured
	}
	for _, rawRule := range fixture.InternalErrorRules {
		var rule struct {
			Target struct {
				Kind       string `json:"kind"`
				ProviderID string `json:"provider_id"`
			} `json:"target"`
			APIType *string `json:"api_type"`
		}
		if err := json.Unmarshal(rawRule, &rule); err != nil {
			t.Fatal(err)
		}
		if rule.Target.Kind != "provider" {
			continue
		}
		configured, ok := providerAPITypes[rule.Target.ProviderID]
		if !ok {
			t.Fatalf("provider-scoped rule references missing provider %q", rule.Target.ProviderID)
		}
		if rule.APIType != nil {
			if _, ok := configured[*rule.APIType]; !ok {
				t.Fatalf("provider %q does not configure rule API type %q", rule.Target.ProviderID, *rule.APIType)
			}
		}
	}
}

func TestScenarioFixturesShareThePostReorderRevision(t *testing.T) {
	t.Parallel()

	directory := filepath.Join("..", "..", "contracts", "internal-error", "v1")
	read := func(name string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	reorder := decodeStrictFixture[reorderFixtureEnvelope](t, read("reorder.json"))
	wantRevision := reorder.Response.RuleSetRevision
	stats := decodeStrictFixture[ruleStatsFixtureEnvelope](t, read("rule-stats.json"))
	testMessage := decodeStrictFixture[testMessageFixtureEnvelope](t, read("test-message.json"))
	if stats.RuleSetRevision != wantRevision ||
		testMessage.Complete.Response.RuleSetRevision != wantRevision ||
		testMessage.FailOpen.Response.RuleSetRevision != wantRevision {
		t.Fatalf("post-reorder fixtures do not share revision %q", wantRevision)
	}

	errorsFixture := decodeStrictFixture[errorsFixtureEnvelope](t, read("errors.json"))
	foundRevisionMismatch := false
	for _, testCase := range errorsFixture.Cases {
		if testCase.Status != 412 {
			continue
		}
		foundRevisionMismatch = true
		if got, ok := testCase.Body.Details["current_revision"].(string); !ok || got != wantRevision {
			t.Fatalf("revision-mismatch details use %v, want string %q", testCase.Body.Details["current_revision"], wantRevision)
		}
	}
	if !foundRevisionMismatch {
		t.Fatal("revision-mismatch error fixture is missing")
	}

	evidence := decodeStrictFixture[schemaCasesFixture](t, read("attempt-evidence-v2.json"))
	for index, rawCase := range evidence.Cases {
		var testCase struct {
			Evidence struct {
				SemanticError struct {
					Rule struct {
						Revision string `json:"revision"`
					} `json:"rule"`
				} `json:"semantic_error"`
			} `json:"evidence"`
		}
		if err := json.Unmarshal(rawCase, &testCase); err != nil {
			t.Fatal(err)
		}
		if got := testCase.Evidence.SemanticError.Rule.Revision; got != wantRevision {
			t.Errorf("evidence case %d revision = %q, want %q", index, got, wantRevision)
		}
	}
}

type fixtureRuleIdentity struct {
	ID       string `json:"id"`
	Position int    `json:"position"`
}

func decodeFixtureRuleIdentity(t *testing.T, raw json.RawMessage) fixtureRuleIdentity {
	t.Helper()
	var identity fixtureRuleIdentity
	if err := json.Unmarshal(raw, &identity); err != nil {
		t.Fatal(err)
	}
	return identity
}

func fixtureRuleETag(revision string) string {
	return `"internal-error-rules/` + revision + `"`
}

func TestBackoffFixtureMatchesGoBehavior(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "internal-error", "v1", "backoff-policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		ValidationCases []struct {
			Name         string              `json:"name"`
			MaxRetries   int                 `json:"max_retries"`
			Backoff      model.BackoffPolicy `json:"backoff"`
			Valid        bool                `json:"valid"`
			Error        string              `json:"error"`
			BaseDelaysMS []int64             `json:"base_delays_ms"`
		} `json:"validation_cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range fixture.ValidationCases {
		t.Run(testCase.Name, func(t *testing.T) {
			validationErr := testCase.Backoff.Validate()
			if testCase.MaxRetries < 0 || testCase.MaxRetries > fixtureRuleRetryLimit {
				validationErr = errRetryRange
			}
			if testCase.Valid {
				if validationErr != nil {
					t.Fatalf("unexpected validation error: %v", validationErr)
				}
				withoutJitter := testCase.Backoff
				withoutJitter.Jitter = false
				for retryIndex, wantMS := range testCase.BaseDelaysMS {
					if got := withoutJitter.DelayForRetry(retryIndex) / time.Millisecond; got != time.Duration(wantMS) {
						t.Errorf("DelayForRetry(%d) = %dms, want %dms", retryIndex, got, wantMS)
					}
				}
				return
			}
			if validationErr == nil {
				t.Fatal("expected validation failure")
			}
			if validationErr.Error() != testCase.Error {
				t.Fatalf("validation error = %q, want %q", validationErr, testCase.Error)
			}
		})
	}
}

func TestProtocolFixtureBodiesAreExecutable(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"protocol-envelopes-positive.json", "protocol-envelopes-negative.json"} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "internal-error", "v1", name))
			if err != nil {
				t.Fatal(err)
			}
			var fixture struct {
				Cases []struct {
					Name            string `json:"name"`
					APIType         string `json:"api_type"`
					ContentEncoding string `json:"content_encoding"`
					Body            struct {
						Encoding string `json:"encoding"`
						Value    string `json:"value"`
					} `json:"body"`
					Expected struct {
						ResponseProtocolID apicontract.ResponseProtocolID `json:"response_protocol_id"`
					} `json:"expected"`
				} `json:"cases"`
			}
			if err := json.Unmarshal(data, &fixture); err != nil {
				t.Fatal(err)
			}
			seenProtocols := make(map[apicontract.ResponseProtocolID]struct{})
			for _, testCase := range fixture.Cases {
				t.Run(testCase.Name, func(t *testing.T) {
					if !apicontract.IsValidProviderAPIType(testCase.APIType) {
						t.Fatalf("fixture uses unknown API type %q", testCase.APIType)
					}
					if testCase.Expected.ResponseProtocolID != "" {
						definition, ok := apicontract.Lookup(testCase.APIType)
						if !ok || !containsProtocol(definition.ResponseProtocolIDs, testCase.Expected.ResponseProtocolID) {
							t.Fatalf("protocol %q does not belong to API type %q", testCase.Expected.ResponseProtocolID, testCase.APIType)
						}
						seenProtocols[testCase.Expected.ResponseProtocolID] = struct{}{}
					}
					wireBytes := []byte(testCase.Body.Value)
					if testCase.Body.Encoding == "base64" {
						decoded, err := base64.StdEncoding.DecodeString(testCase.Body.Value)
						if err != nil {
							t.Fatalf("decode base64 body: %v", err)
						}
						wireBytes = decoded
					} else if testCase.Body.Encoding != "utf8" {
						t.Fatalf("unsupported fixture body encoding %q", testCase.Body.Encoding)
					}
					if testCase.ContentEncoding == "br" {
						if len(wireBytes) == 0 {
							t.Fatal("unsupported Brotli fixture must retain non-empty raw bytes")
						}
						return
					}
					decodedBytes := decodeFixtureBody(t, testCase.ContentEncoding, wireBytes)
					if !utf8.Valid(decodedBytes) {
						t.Fatal("decoded fixture body is not UTF-8")
					}
					if testCase.Name == "openai responses direct sse multiline eof tail" {
						const want = "event: error\ndata: {\"type\":\"error\",\"code\":\"server_is_overloaded\",\ndata: \"message\":\"At capacity\"}"
						if string(decodedBytes) != want {
							t.Fatalf("multiline EOF-tail fixture decoded to %q, want %q", decodedBytes, want)
						}
						if bytes.Count(decodedBytes, []byte("data: ")) != 2 || bytes.HasSuffix(decodedBytes, []byte("\n")) {
							t.Fatal("multiline EOF-tail fixture must contain two data lines and no terminal newline")
						}
					}
				})
			}
			if name == "protocol-envelopes-positive.json" {
				for _, definition := range apicontract.All() {
					for _, protocolID := range definition.ResponseProtocolIDs {
						if _, ok := seenProtocols[protocolID]; !ok {
							t.Errorf("positive fixture omits canonical protocol %q", protocolID)
						}
					}
				}
			}
		})
	}
}

func containsProtocol(protocols []apicontract.ResponseProtocolID, want apicontract.ResponseProtocolID) bool {
	for _, protocolID := range protocols {
		if protocolID == want {
			return true
		}
	}
	return false
}

func decodeFixtureBody(t *testing.T, contentEncoding string, wireBytes []byte) []byte {
	t.Helper()

	var reader io.Reader = bytes.NewReader(wireBytes)
	switch contentEncoding {
	case "identity":
	case "gzip":
		gzipReader, err := gzip.NewReader(reader)
		if err != nil {
			t.Fatalf("open gzip fixture: %v", err)
		}
		defer gzipReader.Close()
		reader = gzipReader
	default:
		t.Fatalf("unsupported content encoding %q", contentEncoding)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("decode fixture body: %v", err)
	}
	return decoded
}

type fixtureError string

func (e fixtureError) Error() string { return string(e) }

const errRetryRange fixtureError = "max_retries must be between 0 and 10"
