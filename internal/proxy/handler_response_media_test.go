package proxy

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestResolveHTTPResponseMediaNormalizesOnlyNegotiatedMissingType(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                string
		responseContentType string
		accept              string
		wantContentType     string
		wantSource          responseanalysis.ResponseMediaSource
		wantSSE             bool
	}{
		{
			name: "missing upstream type uses the sole accepted representation", accept: "text/event-stream",
			wantContentType: "text/event-stream", wantSource: responseanalysis.ResponseMediaFromRequestAccept, wantSSE: true,
		},
		{
			name: "declared upstream type remains authoritative", responseContentType: "application/json", accept: "text/event-stream",
			wantContentType: "application/json", wantSource: responseanalysis.ResponseMediaFromContentType,
		},
		{
			name: "ambiguous negotiation remains unresolved", accept: "application/json, text/event-stream",
			wantSource: responseanalysis.ResponseMediaUnknown,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			sourceHeader := make(http.Header)
			clientHeader := make(http.Header)
			if test.responseContentType != "" {
				sourceHeader.Set("Content-Type", test.responseContentType)
				clientHeader.Set("Content-Type", test.responseContentType)
			}
			head := upstreamtransport.ResponseHead{SourceHeader: sourceHeader, Header: clientHeader}

			media := resolveHTTPResponseMedia(&head, []string{test.accept})

			if media.ContentType() != test.wantContentType || media.Source() != test.wantSource || media.IsEventStream() != test.wantSSE {
				t.Fatalf("media = %#v", media)
			}
			if got := head.Header.Get("Content-Type"); got != test.wantContentType {
				t.Fatalf("downstream Content-Type = %q, want %q", got, test.wantContentType)
			}
		})
	}
}

func TestResolveHTTPResponseMediaHandlesAbsentExchangeMetadata(t *testing.T) {
	t.Parallel()
	if media := resolveHTTPResponseMedia(nil, nil); media.Supported() || media.Source() != responseanalysis.ResponseMediaUnknown {
		t.Fatalf("media = %#v", media)
	}
}

func TestLogHTTPResponseMediaDecisionKeepsHeaderValuesOutOfLogs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                string
		responseContentType string
		accept              []string
		secrets             []string
		wantReason          responseanalysis.ResponseMediaReason
	}{
		{
			name: "inferred representation", accept: []string{
				`application/json; profile="codeql-accept-secret-one"`,
				`application/problem+json; profile="codeql-accept-secret-two"`,
			},
			secrets:    []string{"codeql-accept-secret-one", "codeql-accept-secret-two"},
			wantReason: responseanalysis.ResponseMediaAcceptInferredJSON,
		},
		{
			name: "unresolved accept", accept: []string{`text/plain; token="codeql-unsupported-accept"`},
			secrets: []string{"codeql-unsupported-accept"}, wantReason: responseanalysis.ResponseMediaAcceptUnsupported,
		},
		{
			name: "unsupported declared type", responseContentType: `text/plain; token="codeql-content-type-secret"`,
			accept:     []string{`application/json; token="codeql-ignored-accept"`},
			secrets:    []string{"codeql-content-type-secret", "codeql-ignored-accept"},
			wantReason: responseanalysis.ResponseMediaDeclaredUnsupported,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			core, observed := observer.New(zap.DebugLevel)
			handler := &Handler{logger: zap.New(core)}
			sourceHeader := make(http.Header)
			if test.responseContentType != "" {
				sourceHeader.Set("Content-Type", test.responseContentType)
			}
			media := responseanalysis.ResolveResponseMedia(sourceHeader.Get("Content-Type"), test.accept)
			handler.logHTTPResponseMediaDecision(media, httpResponseMediaLogContext{
				requestID: "request-safe-id", operationID: "operation-safe-id", providerID: "provider-safe-id",
				apiType: "codex", logicalAttempt: 2, providerAttempt: 3,
				acceptValueCount: len(test.accept), contentTypeValueCount: len(sourceHeader.Values("Content-Type")),
			})

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
			for _, secret := range test.secrets {
				if strings.Contains(logged, secret) {
					t.Fatalf("log contains raw header token %q: %s", secret, logged)
				}
			}
			if _, exists := context["request_accept"]; exists {
				t.Fatalf("log contains raw Accept field: %#v", context)
			}
			if _, exists := context["response_content_type"]; exists {
				t.Fatalf("log contains raw Content-Type field: %#v", context)
			}
			if context["request_id"] != "request-safe-id" || context["operation_id"] != "operation-safe-id" ||
				context["logical_attempt"] != int64(2) || context["provider_attempt"] != int64(3) ||
				context["accept_value_count"] != int64(len(test.accept)) ||
				context["response_content_type_value_count"] != int64(len(sourceHeader.Values("Content-Type"))) ||
				context["media_reason"] != string(test.wantReason) {
				t.Fatalf("diagnostic context = %#v", context)
			}
		})
	}
}
