//go:build !windows

package attemptevidence

import (
	"errors"
	"syscall"
)

// IsConnectionReset identifies a transport fact without inferring its peer or
// business outcome. Error identity survives wrapping and localized messages.
func IsConnectionReset(err error) bool {
	return errors.Is(err, syscall.ECONNRESET)
}
