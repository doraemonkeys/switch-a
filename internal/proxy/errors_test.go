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

func TestIsClientCancellation(t *testing.T) {
	t.Run("returns true for context.Canceled", func(t *testing.T) {
		if !isClientCancellation(context.Canceled) {
			t.Error("expected true for context.Canceled")
		}
	})

	t.Run("returns true for context.DeadlineExceeded", func(t *testing.T) {
		if !isClientCancellation(context.DeadlineExceeded) {
			t.Error("expected true for context.DeadlineExceeded")
		}
	})

	t.Run("returns true for wrapped context.Canceled", func(t *testing.T) {
		err := fmt.Errorf("request failed: %w", context.Canceled)
		if !isClientCancellation(err) {
			t.Error("expected true for wrapped context.Canceled")
		}
	})

	t.Run("returns true for wrapped context.DeadlineExceeded", func(t *testing.T) {
		err := fmt.Errorf("timeout: %w", context.DeadlineExceeded)
		if !isClientCancellation(err) {
			t.Error("expected true for wrapped context.DeadlineExceeded")
		}
	})

	t.Run("returns false for regular error", func(t *testing.T) {
		err := errors.New("connection refused")
		if isClientCancellation(err) {
			t.Error("expected false for regular error")
		}
	})

	t.Run("returns false for nil", func(t *testing.T) {
		if isClientCancellation(nil) {
			t.Error("expected false for nil")
		}
	})

	t.Run("returns false for upstream errors", func(t *testing.T) {
		err := NewUpstreamReadError(errors.New("upstream failed"))
		if isClientCancellation(err) {
			t.Error("expected false for upstream read error")
		}
	})
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
