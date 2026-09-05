package redaction

import (
	"net/http"
)

type IngressHead struct {
	Protocol         string
	ContentLength    int64
	TransferEncoding []string
	TrailerKeys      []string
}

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
)

// CredentialEvidence is fixed-capacity attempt-scoped evidence containing only
// the provider credential switch-a injected: a static API key or OAuth access
// token. It may be copied between proxy phases and retained for one record.
type CredentialEvidence struct {
	values   [MaxRetainedCredentialValues]string
	count    uint8
	bytes    uint32
	overflow bool
	sealed   bool
}

// SensitiveHeaderEvidence remains an explicit fail-closed contract between
// capture producers and the sanitizer. Runtime producers seal it empty because
// header names no longer determine whether user-owned values are hidden.
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
