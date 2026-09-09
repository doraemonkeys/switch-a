package attemptevidence

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"testing"
)

func TestIsConnectionReset(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil"},
		{name: "portable errno", err: syscall.ECONNRESET, want: true},
		{name: "wrapped syscall", err: fmt.Errorf("reader: %w", &net.OpError{
			Op: "read", Net: "tcp", Err: &os.SyscallError{Syscall: "recv", Err: syscall.ECONNRESET},
		}), want: true},
		{name: "same text is not an errno", err: errors.New(syscall.ECONNRESET.Error())},
		{name: "other socket failure", err: syscall.ECONNREFUSED},
		{name: "end of stream", err: io.EOF},
		{name: "canceled", err: context.Canceled},
		{name: "timeout", err: context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsConnectionReset(tc.err); got != tc.want {
				t.Fatalf("IsConnectionReset(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
