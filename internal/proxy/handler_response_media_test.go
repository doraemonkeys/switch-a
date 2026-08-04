package proxy

import (
	"net/http"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
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
