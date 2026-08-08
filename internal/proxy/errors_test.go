package proxy

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestIsUpstreamReadError(t *testing.T) {
	t.Run("returns true for UpstreamReadError", func(t *testing.T) {
		err := NewUpstreamReadError(errors.New("connection reset"))
		if !IsUpstreamReadError(err) {
			t.Error("expected true for UpstreamReadError")
		}
	})

	t.Run("returns true for wrapped UpstreamReadError", func(t *testing.T) {
		err := fmt.Errorf("outer: %w", NewUpstreamReadError(errors.New("inner")))
		if !IsUpstreamReadError(err) {
			t.Error("expected true for wrapped UpstreamReadError")
		}
	})

	t.Run("returns false for regular error", func(t *testing.T) {
		err := errors.New("regular error")
		if IsUpstreamReadError(err) {
			t.Error("expected false for regular error")
		}
	})

	t.Run("returns false for nil", func(t *testing.T) {
		if IsUpstreamReadError(nil) {
			t.Error("expected false for nil")
		}
	})
}

func TestClassifyClientTermination_UsesRequestContext(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	timedOut, cancelTimeout := context.WithTimeout(context.Background(), 0)
	defer cancelTimeout()

	tests := []struct {
		name string
		ctx  context.Context
		want clientTermination
	}{
		{name: "nil context", want: clientTerminationNone},
		{name: "active request", ctx: context.Background(), want: clientTerminationNone},
		{name: "request canceled", ctx: canceled, want: clientTerminationDisconnect},
		{name: "request deadline exceeded", ctx: timedOut, want: clientTerminationTimeout},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyClientTermination(test.ctx); got != test.want {
				t.Fatalf("classifyClientTermination() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestMergeClientTermination_PreservesFirstObservedCause(t *testing.T) {
	if got := mergeClientTermination(clientTerminationNone, clientTerminationTimeout); got != clientTerminationTimeout {
		t.Fatalf("merge none/timeout = %d, want timeout", got)
	}
	if got := mergeClientTermination(clientTerminationDisconnect, clientTerminationTimeout); got != clientTerminationDisconnect {
		t.Fatalf("merge disconnect/timeout = %d, want disconnect", got)
	}
}

func TestUpstreamReadError(t *testing.T) {
	t.Run("Error method formats correctly", func(t *testing.T) {
		inner := errors.New("connection reset by peer")
		err := NewUpstreamReadError(inner)
		expected := "upstream read error: connection reset by peer"
		if err.Error() != expected {
			t.Errorf("got %q, want %q", err.Error(), expected)
		}
	})

	t.Run("Unwrap returns inner error", func(t *testing.T) {
		inner := errors.New("inner error")
		err := NewUpstreamReadError(inner)
		var upstreamErr *UpstreamReadError
		if !errors.As(err, &upstreamErr) {
			t.Fatal("expected to find UpstreamReadError")
		}
		if upstreamErr.Unwrap() != inner {
			t.Error("Unwrap should return inner error")
		}
	})

	t.Run("NewUpstreamReadError returns nil for nil input", func(t *testing.T) {
		err := NewUpstreamReadError(nil)
		if err != nil {
			t.Errorf("expected nil, got %v", err)
		}
	})
}
