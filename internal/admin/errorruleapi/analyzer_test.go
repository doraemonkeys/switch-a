package errorruleapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/framing"
)

func TestRegistryAnalyzerSelectsProtocolDecodesGzipAndPreservesFrameIndex(t *testing.T) {
	t.Parallel()
	body := []byte("event: response.created\ndata: {\"type\":\"response.created\"}\n\n" +
		"event: error\ndata: {\"type\":\"error\",\"code\":\"busy\",\"message\":\"At Capacity\"}\n\n")
	compressed := gzipBytes(t, body)
	var observed []AnalyzedError
	result := NewRegistryAnalyzer().Analyze(context.Background(), MessageAnalysisInput{
		APIType: apicontract.APITypeCodex, ContentType: "text/event-stream; charset=utf-8",
		ContentEncoding: "gzip", Body: compressed,
	}, func(current AnalyzedError) bool {
		observed = append(observed, current)
		return true
	})
	if result.Failure != "" || result.ProtocolID == nil || *result.ProtocolID != apicontract.ProtocolOpenAIResponsesSSE {
		t.Fatalf("analysis result = %#v", result)
	}
	if len(observed) != 1 || observed[0].FrameIndex != 1 || observed[0].Fields.Code != "busy" || observed[0].Fields.Message != "At Capacity" {
		t.Fatalf("observations = %#v", observed)
	}
}

func TestRegistryAnalyzerFirstDecisionStopsBeforeLaterMalformedOutput(t *testing.T) {
	t.Parallel()
	body := []byte("event: error\ndata: {\"type\":\"error\",\"code\":\"first\",\"message\":\"First\"}\n\n" +
		"event: error\ndata: not-json\n\n")
	calls := 0
	result := NewRegistryAnalyzer().Analyze(context.Background(), MessageAnalysisInput{
		APIType: apicontract.APITypeCodex, ContentType: "text/event-stream", ContentEncoding: "identity", Body: body,
	}, func(AnalyzedError) bool {
		calls++
		return false
	})
	if calls != 1 || result.Failure != "" {
		t.Fatalf("calls=%d result=%#v", calls, result)
	}
}

func TestRegistryAnalyzerMatchesSharedBoundedRuntimeProtocolFacts(t *testing.T) {
	t.Parallel()
	body := []byte("event: response.created\ndata: {\"type\":\"response.created\"}\n\n" +
		"event: error\ndata: {\"type\":\"error\",\"code\":\"Busy\",\"message\":\"At Capacity\",\"param\":\"load\"}\n\n")
	shared := responseanalysis.NewRegistry().AnalyzeBounded(
		string(apicontract.APITypeCodex), "text/event-stream", "identity",
		bytes.NewReader(body), allocation.NoopReserver{}, responseanalysis.DefaultTestMessageAnalysisLimits(),
	)
	defer responseanalysis.ReleaseObservations(shared)
	var sharedError *responseanalysis.Observation
	for index := range shared {
		if shared[index].Class == responseanalysis.EventError {
			sharedError = &shared[index]
			break
		}
	}
	if sharedError == nil || sharedError.Fields == nil {
		t.Fatalf("shared observations = %#v", shared)
	}
	var admin AnalyzedError
	adminResult := NewRegistryAnalyzer().Analyze(context.Background(), MessageAnalysisInput{
		APIType: apicontract.APITypeCodex, ContentType: "text/event-stream",
		ContentEncoding: "identity", Body: body,
	}, func(observed AnalyzedError) bool {
		admin = observed
		return true
	})
	if adminResult.Failure != "" || adminResult.ProtocolID == nil || *adminResult.ProtocolID != sharedError.ProtocolID ||
		admin.FrameIndex != 1 || admin.Fields.Type != sharedError.Fields.Type || admin.Fields.Code != sharedError.Fields.Code ||
		admin.Fields.Message != sharedError.Fields.Message || admin.Fields.Reason != sharedError.Fields.Reason ||
		admin.Fields.HasType() != sharedError.Fields.HasType() || admin.Fields.HasCode() != sharedError.Fields.HasCode() ||
		admin.Fields.HasReason() != sharedError.Fields.HasReason() {
		t.Fatalf("admin=%#v result=%#v shared=%#v", admin, adminResult, *sharedError)
	}
}

func TestRegistryAnalyzerFailsOpenForSelectionDecodingFramingAndBounds(t *testing.T) {
	t.Parallel()
	controlFrame := "event: response.created\ndata: {\"type\":\"response.created\"}\n\n"
	oversizedDecoded := gzipBytes(t, []byte(strings.Repeat(
		controlFrame,
		responseanalysis.MaxTestMessageDecodedBodyBytes/len(controlFrame)+1,
	)))
	tooManyErrors := []byte(strings.Repeat("event: error\ndata: {\"type\":\"error\",\"code\":\"busy\",\"message\":\"busy\"}\n\n", responseanalysis.MaxTestMessageErrors+1))
	tests := []struct {
		name  string
		input MessageAnalysisInput
		want  responseanalysis.AnalysisFailureReason
	}{
		{
			name:  "unsupported protocol",
			input: MessageAnalysisInput{APIType: "custom:unsupported", ContentType: "application/json", ContentEncoding: "identity", Body: []byte(`{}`)},
			want:  responseanalysis.FailureUnsupportedProtocol,
		},
		{
			name:  "unsupported coding token",
			input: MessageAnalysisInput{APIType: apicontract.APITypeCodex, ContentType: "application/json", ContentEncoding: "compress", Body: []byte(`{}`)},
			want:  responseanalysis.FailureUnsupportedEncoding,
		},
		{
			name:  "bounded brotli unavailable",
			input: MessageAnalysisInput{APIType: apicontract.APITypeCodex, ContentType: "application/json", ContentEncoding: "br", Body: []byte{0x1b, 0x00}},
			want:  responseanalysis.FailureUnsupportedEncoding,
		},
		{
			name:  "corrupt gzip",
			input: MessageAnalysisInput{APIType: apicontract.APITypeCodex, ContentType: "application/json", ContentEncoding: "gzip", Body: []byte("not-gzip")},
			want:  responseanalysis.FailureContentDecoding,
		},
		{
			name:  "malformed JSON",
			input: MessageAnalysisInput{APIType: apicontract.APITypeCodex, ContentType: "application/json", ContentEncoding: "identity", Body: []byte(`{"type":`)},
			want:  responseanalysis.FailureMalformedFrame,
		},
		{
			name:  "decoded body overflow",
			input: MessageAnalysisInput{APIType: apicontract.APITypeCodex, ContentType: "text/event-stream", ContentEncoding: "gzip", Body: oversizedDecoded},
			want:  responseanalysis.FailureRequestMemoryExhausted,
		},
		{
			name:  "extracted error overflow",
			input: MessageAnalysisInput{APIType: apicontract.APITypeCodex, ContentType: "text/event-stream", ContentEncoding: "identity", Body: tooManyErrors},
			want:  responseanalysis.FailureRequestMemoryExhausted,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			count := 0
			result := NewRegistryAnalyzer().Analyze(context.Background(), test.input, func(AnalyzedError) bool {
				count++
				return true
			})
			if result.Failure != test.want {
				t.Fatalf("failure = %q, want %q (errors=%d)", result.Failure, test.want, count)
			}
			if test.name == "extracted error overflow" && count != responseanalysis.MaxTestMessageErrors {
				t.Fatalf("retained errors = %d, want %d", count, responseanalysis.MaxTestMessageErrors)
			}
		})
	}
}

func TestRegistryAnalyzerHandlesNilConsumerAndCancellation(t *testing.T) {
	t.Parallel()
	input := MessageAnalysisInput{
		APIType: apicontract.APITypeCodex, ContentType: "application/json",
		ContentEncoding: "identity", Body: []byte(`{}`),
	}
	withoutConsumer := NewRegistryAnalyzer().Analyze(context.Background(), input, nil)
	if withoutConsumer.Failure != responseanalysis.FailureAnalysisInternal || withoutConsumer.ProtocolID == nil {
		t.Fatalf("nil consumer result = %#v", withoutConsumer)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	result := NewRegistryAnalyzer().Analyze(canceled, input, func(AnalyzedError) bool { return true })
	if result.Failure != responseanalysis.FailureAnalysisInternal {
		t.Fatalf("canceled result = %#v", result)
	}
}

func TestRegistryAnalyzerAcceptsExactDecodedAndExtractedErrorCaps(t *testing.T) {
	t.Parallel()
	controlFrame := "event: response.created\ndata: {\"type\":\"response.created\"}\n\n"
	exactDecoded := strings.Repeat(
		controlFrame,
		responseanalysis.MaxTestMessageDecodedBodyBytes/len(controlFrame),
	)
	exactDecoded += strings.Repeat("\n", responseanalysis.MaxTestMessageDecodedBodyBytes-len(exactDecoded))
	input := MessageAnalysisInput{
		APIType: apicontract.APITypeCodex, ContentType: "text/event-stream",
		ContentEncoding: "gzip", Body: gzipBytes(t, []byte(exactDecoded)),
	}
	result := NewRegistryAnalyzer().Analyze(context.Background(), input, func(AnalyzedError) bool { return true })
	if result.Failure != "" {
		t.Fatalf("exact decoded limit failed open: %#v", result)
	}
	input.Body = gzipBytes(t, []byte(exactDecoded+"\n"))
	result = NewRegistryAnalyzer().Analyze(context.Background(), input, func(AnalyzedError) bool { return true })
	if result.Failure != responseanalysis.FailureRequestMemoryExhausted {
		t.Fatalf("decoded limit+1 = %#v", result)
	}

	errorEvent := "event: error\ndata: {\"type\":\"error\",\"code\":\"busy\",\"message\":\"busy\"}\n\n"
	count := 0
	result = NewRegistryAnalyzer().Analyze(context.Background(), MessageAnalysisInput{
		APIType: apicontract.APITypeCodex, ContentType: "text/event-stream",
		ContentEncoding: "identity", Body: []byte(strings.Repeat(errorEvent, responseanalysis.MaxTestMessageErrors)),
	}, func(AnalyzedError) bool {
		count++
		return true
	})
	if result.Failure != "" || count != responseanalysis.MaxTestMessageErrors {
		t.Fatalf("exact error cap result=%#v count=%d", result, count)
	}
}

func TestTestMessageWireDistinguishesPresentEmptyOptionalFieldsFromAbsent(t *testing.T) {
	t.Parallel()
	explicitEmpty := "event: error\ndata: {\"type\":\"error\",\"code\":\"\",\"message\":\"\",\"param\":\"\"}\n\n"
	absent := "event: error\ndata: {\"type\":\"error\",\"message\":\"present\",\"error\":{\"message\":\"present\"}}\n\n"

	var explicitFields responseanalysis.SemanticFields
	result := NewRegistryAnalyzer().Analyze(context.Background(), MessageAnalysisInput{
		APIType: apicontract.APITypeCodex, ContentType: "text/event-stream",
		ContentEncoding: "identity", Body: []byte(explicitEmpty),
	}, func(observed AnalyzedError) bool {
		explicitFields = observed.Fields
		return true
	})
	if result.Failure != "" || !explicitFields.HasType() || !explicitFields.HasCode() ||
		!explicitFields.HasMessage() || !explicitFields.HasReason() || explicitFields.Code != "" ||
		explicitFields.Message != "" || explicitFields.Reason != "" {
		t.Fatalf("explicit-empty fields = %#v result=%#v", explicitFields, result)
	}

	handler, _, _ := newReadHandler(t, compileRules(t, 3), nil)
	for _, testCase := range []struct {
		name        string
		contentType string
		body        string
		wantCode    bool
		wantMessage bool
		wantReason  bool
	}{
		{name: "explicit empty", contentType: "text/event-stream", body: explicitEmpty, wantCode: true, wantMessage: true, wantReason: true},
		{name: "code and reason absent", contentType: "text/event-stream", body: absent, wantMessage: true},
		{name: "message absent", contentType: "application/json", body: `{"type":"error","error":{}}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			requestBody := `{"schema_version":1,"api_type":"codex","provider_id":null,"content_type":` + strconv.Quote(testCase.contentType) + `,"content_encoding":"identity","body":{"encoding":"utf8","value":` + strconv.Quote(testCase.body) + `}}`
			recorder := performRequest(t, handler.TestMessage, http.MethodPost, "/", requestBody, "", "")
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var response map[string]any
			decodeRecorder(t, recorder, &response)
			errorsWire := response["errors"].([]any)
			wire := errorsWire[0].(map[string]any)
			_, hasCode := wire["code"]
			_, hasMessage := wire["message"]
			_, hasReason := wire["reason"]
			if hasCode != testCase.wantCode || hasMessage != testCase.wantMessage || hasReason != testCase.wantReason {
				t.Fatalf("wire fields = %#v", wire)
			}
		})
	}
}

func TestAnalysisFailureUsesTypedReasonsOnly(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err  error
		want responseanalysis.AnalysisFailureReason
	}{
		{err: &allocation.Denial{Reason: allocation.DenialRequestMemoryExhausted}, want: responseanalysis.FailureRequestMemoryExhausted},
		{err: &allocation.Denial{Reason: allocation.DenialProcessMemoryExhausted}, want: responseanalysis.FailureProcessMemoryExhausted},
		{err: &framing.Error{Reason: framing.FailureUnsupportedEncoding}, want: responseanalysis.FailureUnsupportedEncoding},
		{err: &framing.Error{Reason: framing.FailureContentDecoding}, want: responseanalysis.FailureContentDecoding},
		{err: &framing.Error{Reason: framing.FailureMalformedFrame}, want: responseanalysis.FailureMalformedFrame},
		{err: &framing.Error{Reason: framing.FailureDecodedEventTooLarge}, want: responseanalysis.FailureDecodedEventTooLarge},
		{err: &framing.Error{Reason: framing.FailureSemanticFieldTooLarge}, want: responseanalysis.FailureSemanticFieldTooLarge},
		{err: errors.New("request_probe_memory_exhausted"), want: responseanalysis.FailureAnalysisInternal},
		{err: nil, want: responseanalysis.FailureAnalysisInternal},
	}
	for _, test := range tests {
		if got := analysisFailure(test.err); got != test.want {
			t.Errorf("analysisFailure(%v) = %q, want %q", test.err, got, test.want)
		}
	}
}

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := gzip.NewWriter(&output)
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
