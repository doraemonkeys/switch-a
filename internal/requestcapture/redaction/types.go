package redaction

import (
	"net/http"
)

const (
	RedactedValue       = "[REDACTED]"
	InvalidURLRedaction = "[REDACTED_INVALID_URL]"

	MaxRetainedIdentifierBytes         = 256
	MaxRetainedProviderIDBytes         = 256
	MaxRetainedProviderNameBytes       = 512
	MaxRetainedAPITypeBytes            = 128
	MaxRetainedMethodBytes             = 64
	MaxRetainedURLBytes                = 8 << 10
	MaxRetainedHostBytes               = 1 << 10
	MaxRetainedHeaderFields            = 128
	MaxRetainedHeaderValuesPerField    = 32
	MaxRetainedHeaderNameBytes         = 256
	MaxRetainedHeaderValueBytes        = 8 << 10
	MaxRetainedHeaderBytes             = 64 << 10
	MaxRetainedSensitiveHeaderNames    = 64
	MaxRetainedCredentialValues        = 64
	MaxRetainedCredentialValueBytes    = 4 << 10
	MaxRetainedCredentialBytes         = 64 << 10
	MaxRetainedProviderErrorFieldBytes = 128
	MaxRetainedErrorBytes              = 2 << 10
	MaxRetainedCloseReasonBytes        = 1 << 10
	maximumCredentialJSONDepth         = 32
)

// CredentialEvidence is a borrowed, fixed-capacity redaction input. It may be
// copied by value between proxy phases, but request capture never retains it.
type CredentialEvidence struct {
	values   [MaxRetainedCredentialValues]string
	count    uint8
	bytes    uint32
	overflow bool
	sealed   bool
}

// SensitiveHeaderEvidence carries the configured, nonstandard credential
// header names inspected by a producer. Common authentication headers are
// always discovered directly from the borrowed HTTP header maps.
type SensitiveHeaderEvidence struct {
	names    [MaxRetainedSensitiveHeaderNames]string
	count    uint8
	bytes    uint32
	overflow bool
	sealed   bool
}

type RequestMetadata struct {
	Method             string
	Headers            http.Header
	ContentLength      int64
	Trailers           http.Header
	SensitiveHeaders   SensitiveHeaderEvidence
	CredentialEvidence CredentialEvidence
}

type HTTPResponseMetadata struct {
	StatusCode         int
	Protocol           string
	Headers            http.Header
	ContentLength      int64
	DeclaredTrailers   http.Header
	SensitiveHeaders   SensitiveHeaderEvidence
	CredentialEvidence CredentialEvidence
}

type WebSocketHandshakeMetadata struct {
	StatusCode         int
	Protocol           string
	Headers            http.Header
	SensitiveHeaders   SensitiveHeaderEvidence
	CredentialEvidence CredentialEvidence
}
