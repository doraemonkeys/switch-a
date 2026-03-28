package model

import (
	"testing"
	"time"
)

func TestDurationValueAndScan(t *testing.T) {
	t.Parallel()

	original := Duration(1500 * time.Millisecond)
	value, err := original.Value()
	if err != nil {
		t.Fatalf("Value returned error: %v", err)
	}
	if got, want := value, int64(original); got != want {
		t.Fatalf("Value = %v, want %v", got, want)
	}

	var scanned Duration
	if err := scanned.Scan(value); err != nil {
		t.Fatalf("Scan(int64) returned error: %v", err)
	}
	if scanned != original {
		t.Fatalf("Scan(int64) = %v, want %v", scanned, original)
	}

	if err := scanned.Scan(nil); err != nil {
		t.Fatalf("Scan(nil) returned error: %v", err)
	}
	if scanned != 0 {
		t.Fatalf("Scan(nil) = %v, want 0", scanned)
	}

	if err := scanned.Scan("bad-type"); err == nil {
		t.Fatal("Scan(string) error = nil, want error")
	}
}

func TestHasAPIKey(t *testing.T) {
	t.Parallel()

	if !HasAPIKey("  token  ") {
		t.Fatal("HasAPIKey returned false for non-empty key")
	}
	if HasAPIKey(" \n\t ") {
		t.Fatal("HasAPIKey returned true for whitespace-only key")
	}
}

func TestLogFilterHasWebSocketLifecycleFilter(t *testing.T) {
	t.Parallel()

	trueValue := true

	testCases := []struct {
		name   string
		filter LogFilter
		want   bool
	}{
		{
			name:   "empty",
			filter: LogFilter{},
			want:   false,
		},
		{
			name: "sticky written",
			filter: LogFilter{
				StickyWritten: &trueValue,
			},
			want: true,
		},
		{
			name: "session committed",
			filter: LogFilter{
				SessionCommitted: &trueValue,
			},
			want: true,
		},
		{
			name: "terminal cause",
			filter: LogFilter{
				TerminalCause: TerminalInternalError,
			},
			want: true,
		},
		{
			name: "probe outcome",
			filter: LogFilter{
				ProbeOutcome: WebSocketProbeOutcomeUnsupported,
			},
			want: true,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.filter.HasWebSocketLifecycleFilter(); got != tc.want {
				t.Fatalf("HasWebSocketLifecycleFilter() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsValidTerminalCause(t *testing.T) {
	t.Parallel()

	if !IsValidTerminalCause(TerminalUnknown) {
		t.Fatal("IsValidTerminalCause(TerminalUnknown) = false, want true")
	}
	if !IsValidTerminalCause(TerminalUpstreamHandshakeRejected) {
		t.Fatal("IsValidTerminalCause(TerminalUpstreamHandshakeRejected) = false, want true")
	}
	if IsValidTerminalCause(TerminalCause("bogus")) {
		t.Fatal("IsValidTerminalCause(invalid) = true, want false")
	}
}

func TestIsValidWebSocketProbeOutcome(t *testing.T) {
	t.Parallel()

	if !IsValidWebSocketProbeOutcome(WebSocketProbeOutcomeUnknown) {
		t.Fatal("IsValidWebSocketProbeOutcome(unknown) = false, want true")
	}
	if !IsValidWebSocketProbeOutcome(WebSocketProbeOutcomeDemandResolutionFailed) {
		t.Fatal("IsValidWebSocketProbeOutcome(demand_resolution_failed) = false, want true")
	}
	if !IsValidWebSocketProbeOutcome(WebSocketProbeOutcomeTransportFailed) {
		t.Fatal("IsValidWebSocketProbeOutcome(transport_failed) = false, want true")
	}
	if IsValidWebSocketProbeOutcome(WebSocketProbeOutcome("bogus")) {
		t.Fatal("IsValidWebSocketProbeOutcome(invalid) = true, want false")
	}
}
