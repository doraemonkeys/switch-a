package attemptevidence

import (
	"errors"
	"syscall"
)

// IsConnectionReset identifies a transport fact without inferring its peer or
// business outcome. Winsock's errno differs from Go's portable ECONNRESET.
func IsConnectionReset(err error) bool {
	return errors.Is(err, syscall.WSAECONNRESET) || errors.Is(err, syscall.ECONNRESET)
}
