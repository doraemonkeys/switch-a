package errorruleapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis"
)

func TestSharedFixtureRuntimeAnalyzerAndTestMessageStayInParity(t *testing.T) {
	var fixture struct {
		Complete struct {
			IfMatch string          `json:"if_match"`
			Request json.RawMessage `json:"request"`
		} `json:"complete"`
	}
	decodeJSON(t, readFixture(t, "test-message.json"), &fixture)

	var request testMessageRequest
	decodeJSON(t, fixture.Complete.Request, &request)
	input, apiErr := request.input()
	if apiErr != nil {
		t.Fatalf("decode shared request: %v", apiErr)
	}

	snapshot := compileRules(t, 10, fixtureRules(t)...)
	rules := &fakeRuleService{snapshot: snapshot}
	handler, err := NewHandler(Config{
		Rules: rules, Stats: &statsReaderStub{}, StatsOverlay: &overlayStub{},
		Providers: &providerCatalogStub{items: map[string]*model.Provider{
			"provider-codex": {ID: "provider-codex"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := performRequest(
		t, handler.TestMessage, http.MethodPost, "/", string(fixture.Complete.Request), fixture.Complete.IfMatch, "",
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("Test Message status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var admin TestMessageResponse
	decodeRecorder(t, recorder, &admin)

	runtime := analyzeSharedFixtureThroughRuntime(t, snapshot, input)
	if admin.RuleSetRevision != runtime.revision || admin.ResponseProtocolID == nil ||
		*admin.ResponseProtocolID != runtime.protocolID {
		t.Fatalf("protocol/revision parity: admin=%#v runtime=%#v", admin, runtime)
	}
	if admin.AnalysisStatus != "complete" || admin.AnalysisReason != nil {
		t.Fatalf("admin analysis state=%#v", admin)
	}
	if len(admin.Errors) != len(runtime.errors) {
		t.Fatalf("extracted error count: admin=%d runtime=%d", len(admin.Errors), len(runtime.errors))
	}
	for index := range admin.Errors {
		adminFact := admin.Errors[index]
		adminFact.FrameIndex = 0 // Runtime's decision callback intentionally exposes semantic facts, not framing coordinates.
		if !reflect.DeepEqual(adminFact, runtime.errors[index]) {
			t.Fatalf("error %d parity: admin=%#v runtime=%#v", index, adminFact, runtime.errors[index])
		}
	}
	if !reflect.DeepEqual(admin.Winner, runtime.winner) {
		t.Fatalf("winner parity: admin=%#v runtime=%#v", admin.Winner, runtime.winner)
	}
}

type runtimeParityResult struct {
	revision   string
	protocolID apicontract.ResponseProtocolID
	errors     []TestMessageError
	winner     *TestMessageWinner
}

func analyzeSharedFixtureThroughRuntime(
	t *testing.T,
	snapshot *errorrule.CompiledRuleSet,
	input TestMessageInput,
) runtimeParityResult {
	t.Helper()
	budget, err := responseanalysis.NewProcessMemoryBudget(responseanalysis.ResponseProbeMemoryBudget)
	if err != nil {
		t.Fatal(err)
	}
	analyzer, err := responseanalysis.NewAnalyzer(
		responseanalysis.NewRegistry(), budget, responseanalysis.AnalyzerOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}

	result := runtimeParityResult{revision: snapshot.Revision().String()}
	scope := errorrule.RequestScope{APIType: input.APIType}
	if input.ProviderID != nil {
		scope.ProviderID = errorrule.ProviderID(*input.ProviderID)
	}
	pending := analyzer.Start(context.Background(), responseanalysis.StartInput{
		OperationID: "b2-runtime-parity", Mode: responseanalysis.ProbeMode(),
		APIType: string(input.APIType), ContentType: input.ContentType, ContentEncoding: input.ContentEncoding,
		StatusCode: http.StatusOK, Header: make(http.Header), Trailer: make(http.Header),
		Body: io.NopCloser(bytes.NewReader(input.Body)), Writer: httptest.NewRecorder(),
		Match: func(fields responseanalysis.SemanticFields) bool {
			matches := snapshot.Match(scope, errorrule.SemanticFields{
				Type: fields.Type, Code: fields.Code, Message: fields.Message, Reason: fields.Reason,
			})
			errorIndex := len(result.errors)
			wireError := TestMessageError{
				Type:    testSemanticString(fields.Type, fields.HasType()),
				Code:    testSemanticString(fields.Code, fields.HasCode()),
				Message: testSemanticString(fields.Message, fields.HasMessage()),
				Reason:  testSemanticString(fields.Reason, fields.HasReason()),
				Matches: make([]TestMessageMatch, len(matches.All)),
			}
			for index, match := range matches.All {
				wireError.Matches[index] = newMessageMatch(match)
			}
			result.errors = append(result.errors, wireError)
			if matches.Winner == nil {
				return false
			}
			result.winner = &TestMessageWinner{
				ErrorIndex: errorIndex, TestMessageMatch: newMessageMatch(*matches.Winner),
			}
			return true
		},
	})
	boundary := pending.AwaitBoundary()
	if boundary.State != responseanalysis.StateProbing ||
		boundary.Reason != responseanalysis.BoundarySemanticMatch || !boundary.HasObservation {
		t.Fatalf("runtime boundary=%#v", boundary)
	}
	result.protocolID = boundary.Observation.ProtocolID
	boundary.Observation.Release()
	receipt, err := pending.Discard(responseanalysis.TransitionSemanticDecision)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.BodyClosed || budget.Used() != 0 {
		t.Fatalf("runtime cleanup: receipt=%#v budget=%d", receipt, budget.Used())
	}
	return result
}

func testSemanticString(value string, present bool) *string {
	if !present {
		return nil
	}
	clone := value
	return &clone
}
