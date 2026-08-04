package responseanalysis

import (
	"mime"
	"strconv"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis/framing"
)

const (
	mediaTypeJSON        = "application/json"
	mediaTypeEventStream = "text/event-stream"
)

// ResponseMediaSource identifies the HTTP evidence used to classify the
// upstream representation. Keeping the source explicit makes the missing-header
// recovery path observable without weakening the authority of a declared type.
type ResponseMediaSource string

const (
	ResponseMediaUnknown           ResponseMediaSource = "unknown"
	ResponseMediaFromContentType   ResponseMediaSource = "response_content_type"
	ResponseMediaFromRequestAccept ResponseMediaSource = "request_accept"
)

// ResponseMedia is the resolved representation shared by protocol analysis,
// streaming behavior, timeout selection, and request-log semantics.
type ResponseMedia struct {
	contentType string
	kind        framing.Kind
	source      ResponseMediaSource
	supported   bool
}

func (m ResponseMedia) ContentType() string {
	return m.contentType
}

func (m ResponseMedia) Source() ResponseMediaSource {
	if m.source == "" {
		return ResponseMediaUnknown
	}
	return m.source
}

func (m ResponseMedia) Supported() bool {
	return m.supported
}

func (m ResponseMedia) IsEventStream() bool {
	return m.supported && m.kind == framing.KindSSE
}

// ResolveResponseMedia treats the response Content-Type as authoritative. Some
// Responses-compatible upstreams omit it even after negotiating one concrete
// representation; only that unambiguous Accept case is safe to recover.
func ResolveResponseMedia(responseContentType string, requestAccept []string) ResponseMedia {
	if declared := strings.TrimSpace(responseContentType); declared != "" {
		kind, supported := parseMediaKind(declared)
		return ResponseMedia{
			contentType: declared,
			kind:        kind,
			source:      ResponseMediaFromContentType,
			supported:   supported,
		}
	}

	kind, supported := uniqueAcceptedMediaKind(requestAccept)
	if !supported {
		return ResponseMedia{source: ResponseMediaUnknown}
	}
	contentType := mediaTypeJSON
	if kind == framing.KindSSE {
		contentType = mediaTypeEventStream
	}
	return ResponseMedia{
		contentType: contentType,
		kind:        kind,
		source:      ResponseMediaFromRequestAccept,
		supported:   true,
	}
}

func uniqueAcceptedMediaKind(headerValues []string) (framing.Kind, bool) {
	var resolved framing.Kind
	hasResolved := false
	for _, headerValue := range headerValues {
		mediaRanges, valid := splitHTTPList(headerValue)
		if !valid {
			return 0, false
		}
		for _, mediaRange := range mediaRanges {
			mediaType, parameters, err := mime.ParseMediaType(mediaRange)
			if err != nil {
				return 0, false
			}
			accepted, valid := positiveQuality(parameters["q"])
			if !valid {
				return 0, false
			}
			if !accepted {
				continue
			}
			kind, supported := parseMediaKind(mediaType)
			if !supported {
				// A missing response Content-Type cannot prove which one of several
				// acceptable representations the upstream selected.
				return 0, false
			}
			if hasResolved && resolved != kind {
				return 0, false
			}
			resolved = kind
			hasResolved = true
		}
	}
	return resolved, hasResolved
}

func positiveQuality(raw string) (bool, bool) {
	if raw == "" {
		return true, true
	}
	quality, err := strconv.ParseFloat(raw, 64)
	if err != nil || quality < 0 || quality > 1 {
		return false, false
	}
	return quality > 0, true
}

func splitHTTPList(value string) ([]string, bool) {
	var result []string
	start := 0
	quoted := false
	escaped := false
	for index, current := range value {
		switch {
		case escaped:
			escaped = false
		case quoted && current == '\\':
			escaped = true
		case current == '"':
			quoted = !quoted
		case current == ',' && !quoted:
			part := strings.TrimSpace(value[start:index])
			if part == "" {
				return nil, false
			}
			result = append(result, part)
			start = index + 1
		}
	}
	if quoted || escaped {
		return nil, false
	}
	part := strings.TrimSpace(value[start:])
	if part == "" {
		return nil, len(result) == 0
	}
	return append(result, part), true
}

func parseMediaKind(contentType string) (framing.Kind, bool) {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return 0, false
	}
	mediaType = strings.ToLower(mediaType)
	if mediaType == mediaTypeEventStream {
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
