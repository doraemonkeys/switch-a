package redaction

// Former field limits are fixture sizes, never capture retention policy.
const (
	MaxRetainedIdentifierBytes         = 256
	MaxRetainedProviderIDBytes         = 256
	MaxRetainedProviderNameBytes       = 512
	MaxRetainedAPITypeBytes            = 128
	MaxRetainedMethodBytes             = 64
	MaxRetainedURLBytes                = 8 << 10
	MaxRetainedHostBytes               = 1 << 10
	MaxRetainedHeaderFields            = 128
	MaxRetainedHeaderValuesPerField    = 32
	MaxRetainedHeaderValueBytes        = 8 << 10
	MaxRetainedHeaderBytes             = 64 << 10
	MaxRetainedCredentialValueBytes    = 4 << 10
	MaxRetainedCredentialBytes         = 64 << 10
	MaxRetainedProviderErrorFieldBytes = 128
	MaxRetainedErrorBytes              = 2 << 10
	MaxRetainedCloseReasonBytes        = 1 << 10
)
