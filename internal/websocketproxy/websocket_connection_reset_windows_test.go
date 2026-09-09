package websocketproxy

import (
	"syscall"
	"testing"
)

func TestWebSocketWindowsConnectionResetEvidence(t *testing.T) {
	t.Parallel()
	// Winsock returns a different errno from Go's portable ECONNRESET on Windows.
	assertWebSocketConnectionResetEvidence(t, syscall.WSAECONNRESET)
}
