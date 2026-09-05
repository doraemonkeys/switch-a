package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/requestingress/semantic"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestDecodeSemanticRequestBodyLogsNormalizedEncodingWithoutSensitiveHeaders(t *testing.T) {
	t.Parallel()
	core, observed := observer.New(zap.WarnLevel)
	handler := &Handler{logger: zap.New(core)}
	request := httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader("wire-body"))
	request.Header.Add("Content-Encoding", "GZip")
	request.Header.Add("Content-Encoding", " BR ")
	request.Header.Set("Authorization", "Bearer codeql-secret-authorization")
	request.Header.Set("Cookie", "session=codeql-secret-cookie")
	request.Header.Set("X-Codex-Turn-State", "codeql-secret-turn-state")
	request.Header.Set("X-Codex-Turn-Metadata", "codeql-secret-turn-metadata")
	request.Header.Set("X-Oai-Attestation", "codeql-secret-attestation")

	result := semantic.Project(context.Background(), strings.NewReader("wire-body"), semantic.Options{ContentEncodingValues: request.Header.Values("Content-Encoding"), MaxDecodedBytes: 1024})
	handler.recordRequestProjection("request-safe-id", APITypeCodex, request, result)
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
	for _, secret := range []string{
		"codeql-secret-authorization",
		"codeql-secret-cookie",
		"codeql-secret-turn-state",
		"codeql-secret-turn-metadata",
		"codeql-secret-attestation",
	} {
		if strings.Contains(logged, secret) {
			t.Fatalf("log contains sensitive header value %q: %s", secret, logged)
		}
	}
	for _, forbidden := range []string{"content_coding", "error"} {
		if _, exists := context[forbidden]; exists {
			t.Fatalf("log contains raw-derived field %q: %#v", forbidden, context)
		}
	}
	if context["request_id"] != "request-safe-id" || context["operation_id"] != "request-safe-id" ||
		context["api_type"] != APITypeCodex || context["content_encoding"] != "gzip,br" ||
		context["content_encoding_value_count"] != int64(2) ||
		context["decode_failure"] != "unsupported_content_encoding" {
		t.Fatalf("diagnostic context = %#v", context)
	}
}
