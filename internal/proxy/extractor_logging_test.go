package proxy

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/proxy/requestbody"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type headerEchoSemanticDecoder struct{}

func (headerEchoSemanticDecoder) Decode(_ []byte, values []string, _ int64) ([]byte, error) {
	raw := strings.Join(values, ",")
	return nil, &requestbody.DecodeError{
		Failure: requestbody.FailureUnsupportedEncoding,
		Coding:  raw,
		Cause:   errors.New("decoder rejected " + raw),
	}
}

func TestDecodeSemanticRequestBodyKeepsContentEncodingOutOfLogs(t *testing.T) {
	t.Parallel()
	core, observed := observer.New(zap.WarnLevel)
	handler := &Handler{logger: zap.New(core), requestSemanticDecoder: headerEchoSemanticDecoder{}}
	request := httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader("wire-body"))
	request.Header.Add("Content-Encoding", "codeql-secret-coding-one")
	request.Header.Add("Content-Encoding", "codeql-secret-coding-two")

	if decoded := handler.decodeSemanticRequestBody("request-safe-id", APITypeCodex, request, []byte("wire-body"), 1024); decoded != nil {
		t.Fatalf("decoded body = %q, want nil after observational failure", decoded)
	}
	entries := observed.All()
	if len(entries) != 1 {
		t.Fatalf("log entries = %d, want 1", len(entries))
	}
	context := entries[0].ContextMap()
	encoded, err := json.Marshal(context)
	if err != nil {
		t.Fatal(err)
	}
	logged := entries[0].Message + string(encoded)
	for _, secret := range []string{"codeql-secret-coding-one", "codeql-secret-coding-two"} {
		if strings.Contains(logged, secret) {
			t.Fatalf("log contains raw Content-Encoding token %q: %s", secret, logged)
		}
	}
	for _, forbidden := range []string{"content_encoding", "content_coding", "error"} {
		if _, exists := context[forbidden]; exists {
			t.Fatalf("log contains raw-derived field %q: %#v", forbidden, context)
		}
	}
	if context["request_id"] != "request-safe-id" || context["api_type"] != APITypeCodex ||
		context["content_encoding_value_count"] != int64(2) ||
		context["decode_failure"] != string(requestbody.FailureUnsupportedEncoding) {
		t.Fatalf("diagnostic context = %#v", context)
	}
}

func TestSemanticDecodeFailureClampsUnknownClassification(t *testing.T) {
	t.Parallel()
	err := &requestbody.DecodeError{Failure: requestbody.Failure("codeql-secret-failure"), Cause: errors.New("cause")}
	if got := semanticDecodeFailure(err); got != requestbody.FailureInternal {
		t.Fatalf("failure = %q, want %q", got, requestbody.FailureInternal)
	}
}
