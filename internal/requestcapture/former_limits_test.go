package requestcapture

// Former field limits are fixture sizes, never capture retention policy.
const (
	maxRetainedAPITypeBytes         = 128
	maxRetainedURLBytes             = 8 << 10
	maxRetainedHeaderBytes          = 64 << 10
	maxRetainedCredentialValueBytes = 4 << 10
	maxRetainedErrorBytes           = 2 << 10
	maxRetainedCloseReasonBytes     = 1 << 10
)
