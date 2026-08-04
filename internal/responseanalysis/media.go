package responseanalysis

import (
	"mime"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis/framing"
)

func parseMediaKind(contentType string) (framing.Kind, bool) {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return 0, false
	}
	mediaType = strings.ToLower(mediaType)
	if mediaType == "text/event-stream" {
		return framing.KindSSE, true
	}
	major, subtype, found := strings.Cut(mediaType, "/")
	if !found || major != "application" {
		return 0, false
	}
	if subtype == "json" || strings.HasSuffix(subtype, "+json") && len(subtype) > len("+json") {
		return framing.KindJSON, true
	}
	return 0, false
}

// ParseContentCoding is shared by protocol selection and transitional runtime
// consumers so unsupported encodings cannot accidentally take an identity path.
func ParseContentCoding(contentEncoding string) (framing.ContentCoding, bool) {
	encoding := strings.TrimSpace(strings.ToLower(contentEncoding))
	if encoding == "" || encoding == "identity" {
		return framing.CodingIdentity, true
	}
	// One coding token is accepted; commas cannot be reinterpreted after a
	// partially successful decode because raw passthrough must remain exact.
	if strings.ContainsRune(encoding, ',') {
		return 0, false
	}
	switch encoding {
	case "gzip":
		return framing.CodingGzip, true
	case "br":
		return framing.CodingBrotli, true
	default:
		return 0, false
	}
}
