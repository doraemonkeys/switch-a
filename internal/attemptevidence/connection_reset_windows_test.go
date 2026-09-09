package attemptevidence

import (
	"fmt"
	"net"
	"os"
	"syscall"
	"testing"
)

func TestIsConnectionResetWinsock(t *testing.T) {
	t.Parallel()
	for _, err := range []error{
		syscall.WSAECONNRESET,
		fmt.Errorf("frame header: %w", &net.OpError{
			Op: "read", Net: "tcp", Err: &os.SyscallError{Syscall: "wsarecv", Err: syscall.WSAECONNRESET},
		}),
	} {
		if !IsConnectionReset(err) {
			t.Fatalf("Winsock reset was not recognized: %v", err)
		}
	}
	if IsConnectionReset(syscall.WSAECONNABORTED) {
		t.Fatal("an aborted connection must not be relabeled as a peer reset")
	}
}
