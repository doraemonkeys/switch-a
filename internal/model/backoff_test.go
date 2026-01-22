package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDuration_MarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		d       Duration
		want    string
		wantErr bool
	}{
		{"100ms", Duration(100 * time.Millisecond), `"100ms"`, false},
		{"5s", Duration(5 * time.Second), `"5s"`, false},
		{"1m30s", Duration(90 * time.Second), `"1m30s"`, false},
		{"zero", Duration(0), `"0s"`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.d)
			if (err != nil) != tt.wantErr {
				t.Errorf("MarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if string(got) != tt.want {
				t.Errorf("MarshalJSON() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestDuration_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Duration
		wantErr bool
	}{
		{"100ms", `"100ms"`, Duration(100 * time.Millisecond), false},
		{"5s", `"5s"`, Duration(5 * time.Second), false},
		{"1m30s", `"1m30s"`, Duration(90 * time.Second), false},
		{"invalid", `"invalid"`, 0, true},
		{"not_string", `123`, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Duration
			err := json.Unmarshal([]byte(tt.input), &got)
			if (err != nil) != tt.wantErr {
				t.Errorf("UnmarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("UnmarshalJSON() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBackoffPolicy_IsZero(t *testing.T) {
	tests := []struct {
		name   string
		policy BackoffPolicy
		want   bool
	}{
		{"zero_value", BackoffPolicy{}, true},
		{"zero_initial_delay", BackoffPolicy{InitialDelay: 0}, true},
		{"negative_initial_delay", BackoffPolicy{InitialDelay: -1}, true},
		{"positive_initial_delay", BackoffPolicy{InitialDelay: Duration(100 * time.Millisecond)}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.policy.IsZero(); got != tt.want {
				t.Errorf("IsZero() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBackoffPolicy_Validate(t *testing.T) {
	tests := []struct {
		name    string
		policy  BackoffPolicy
		wantErr bool
	}{
		{"valid_minimal", BackoffPolicy{InitialDelay: Duration(100 * time.Millisecond)}, false},
		{"valid_full", BackoffPolicy{
			InitialDelay: Duration(100 * time.Millisecond),
			MaxDelay:     Duration(5 * time.Second),
			Multiplier:   2.0,
			Jitter:       true,
		}, false},
		{"zero_is_valid", BackoffPolicy{}, false},
		{"negative_initial_delay", BackoffPolicy{InitialDelay: Duration(-1)}, true},
		{"initial_exceeds_max", BackoffPolicy{
			InitialDelay: Duration(10 * time.Second),
			MaxDelay:     Duration(5 * time.Second),
		}, true},
		{"multiplier_less_than_one", BackoffPolicy{
			InitialDelay: Duration(100 * time.Millisecond),
			Multiplier:   0.5,
		}, true},
		{"multiplier_zero_is_default", BackoffPolicy{
			InitialDelay: Duration(100 * time.Millisecond),
			Multiplier:   0,
		}, false},
		{"multiplier_one_is_valid", BackoffPolicy{
			InitialDelay: Duration(100 * time.Millisecond),
			Multiplier:   1.0,
		}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.policy.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestBackoffPolicy_DelayForRetry(t *testing.T) {
	tests := []struct {
		name       string
		policy     BackoffPolicy
		retryIndex int
		wantMin    time.Duration // minimum expected delay (for jitter)
		wantMax    time.Duration // maximum expected delay
	}{
		{
			name:       "zero_policy_returns_zero",
			policy:     BackoffPolicy{},
			retryIndex: 0,
			wantMin:    0,
			wantMax:    0,
		},
		{
			name:       "negative_index_returns_zero",
			policy:     BackoffPolicy{InitialDelay: Duration(100 * time.Millisecond)},
			retryIndex: -1,
			wantMin:    0,
			wantMax:    0,
		},
		{
			name:       "first_retry_returns_initial_delay",
			policy:     BackoffPolicy{InitialDelay: Duration(100 * time.Millisecond)},
			retryIndex: 0,
			wantMin:    100 * time.Millisecond,
			wantMax:    100 * time.Millisecond,
		},
		{
			name:       "second_retry_doubles_with_default_multiplier",
			policy:     BackoffPolicy{InitialDelay: Duration(100 * time.Millisecond)},
			retryIndex: 1,
			wantMin:    200 * time.Millisecond,
			wantMax:    200 * time.Millisecond,
		},
		{
			name:       "third_retry_quadruples",
			policy:     BackoffPolicy{InitialDelay: Duration(100 * time.Millisecond)},
			retryIndex: 2,
			wantMin:    400 * time.Millisecond,
			wantMax:    400 * time.Millisecond,
		},
		{
			name: "respects_max_delay",
			policy: BackoffPolicy{
				InitialDelay: Duration(100 * time.Millisecond),
				MaxDelay:     Duration(300 * time.Millisecond),
			},
			retryIndex: 5, // Would be 3200ms without cap
			wantMin:    300 * time.Millisecond,
			wantMax:    300 * time.Millisecond,
		},
		{
			name: "custom_multiplier",
			policy: BackoffPolicy{
				InitialDelay: Duration(100 * time.Millisecond),
				Multiplier:   3.0,
			},
			retryIndex: 2,
			wantMin:    900 * time.Millisecond, // 100ms * 3^2 = 900ms
			wantMax:    900 * time.Millisecond,
		},
		{
			name: "overflow_capped_at_max_delay",
			policy: BackoffPolicy{
				InitialDelay: Duration(100 * time.Millisecond),
				MaxDelay:     Duration(5 * time.Second),
				Multiplier:   2.0,
			},
			retryIndex: 1000, // math.Pow produces +Inf, must be capped at MaxDelay
			wantMin:    5 * time.Second,
			wantMax:    5 * time.Second,
		},
		{
			name: "multiplier_one_constant_delay",
			policy: BackoffPolicy{
				InitialDelay: Duration(100 * time.Millisecond),
				Multiplier:   1.0,
			},
			retryIndex: 5,
			wantMin:    100 * time.Millisecond, // 100ms * 1^5 = 100ms
			wantMax:    100 * time.Millisecond,
		},
		{
			name: "no_max_delay_large_retry_uses_maxint64_cap",
			policy: BackoffPolicy{
				InitialDelay: Duration(1 * time.Second),
				MaxDelay:     0, // unlimited
			},
			retryIndex: 100,
			wantMin:    1,                              // should not panic or return negative
			wantMax:    time.Duration(1<<63 - 1 - 512), // capped at MaxInt64-512 for float64 safety
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.policy.DelayForRetry(tt.retryIndex)
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("DelayForRetry(%d) = %v, want in range [%v, %v]",
					tt.retryIndex, got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestBackoffPolicy_DelayForRetry_Jitter(t *testing.T) {
	policy := BackoffPolicy{
		InitialDelay: Duration(100 * time.Millisecond),
		Jitter:       true,
	}

	// With Equal Jitter, delay should be in [base/2, base]
	// Run multiple times to verify randomness
	seen := make(map[time.Duration]bool)
	for i := 0; i < 100; i++ {
		delay := policy.DelayForRetry(0)
		if delay < 50*time.Millisecond || delay > 100*time.Millisecond {
			t.Errorf("DelayForRetry(0) with jitter = %v, want in [50ms, 100ms]", delay)
		}
		seen[delay] = true
	}

	// With 100 iterations, we should see multiple distinct values
	if len(seen) < 5 {
		t.Errorf("Jitter should produce varied delays, only got %d distinct values", len(seen))
	}
}

// TestBackoffPolicy_EqualJitter_Distribution demonstrates the actual distribution
// of Equal Jitter delays. Equal Jitter guarantees at least 50% of base delay.
// This test prints statistics to help understand jitter behavior.
func TestBackoffPolicy_EqualJitter_Distribution(t *testing.T) {
	policy := BackoffPolicy{
		InitialDelay: Duration(1 * time.Second),
		Multiplier:   3.0,
		Jitter:       true,
	}

	const iterations = 10000

	for retryIndex := 0; retryIndex < 3; retryIndex++ {
		var sum time.Duration
		var minDelay, maxDelay time.Duration

		// Calculate the base delay (without jitter)
		baseDelay := time.Second * time.Duration(1)
		for i := 0; i < retryIndex; i++ {
			baseDelay *= 3
		}

		minDelay = baseDelay // Initialize to max possible
		for i := 0; i < iterations; i++ {
			delay := policy.DelayForRetry(retryIndex)
			sum += delay

			if delay < minDelay {
				minDelay = delay
			}
			if delay > maxDelay {
				maxDelay = delay
			}
		}

		avgDelay := sum / iterations
		expectedAvg := baseDelay * 3 / 4 // Equal Jitter average is base * 3/4

		t.Logf("RetryIndex=%d (base=%v):", retryIndex, baseDelay)
		t.Logf("  Average delay: %v (expected ~%v)", avgDelay, expectedAvg)
		t.Logf("  Min delay: %v (expected >= %v)", minDelay, baseDelay/2)
		t.Logf("  Max delay: %v (expected <= %v)", maxDelay, baseDelay)

		// Verify minimum is at least base/2
		if minDelay < baseDelay/2 {
			t.Errorf("Min delay %v is less than base/2 (%v)", minDelay, baseDelay/2)
		}

		// Verify maximum does not exceed base
		if maxDelay > baseDelay {
			t.Errorf("Max delay %v exceeds base (%v)", maxDelay, baseDelay)
		}

		// Verify average is roughly 3/4 of base (within 10% tolerance)
		tolerance := baseDelay / 10
		if avgDelay < expectedAvg-tolerance || avgDelay > expectedAvg+tolerance {
			t.Errorf("Average delay %v is not close to expected %v", avgDelay, expectedAvg)
		}
	}
}

func TestBackoffPolicy_JSON_RoundTrip(t *testing.T) {
	original := BackoffPolicy{
		InitialDelay: Duration(100 * time.Millisecond),
		MaxDelay:     Duration(5 * time.Second),
		Multiplier:   2.0,
		Jitter:       true,
	}

	// Marshal
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Verify JSON structure
	expectedJSON := `{"initial_delay":"100ms","max_delay":"5s","multiplier":2,"jitter":true}`
	if string(data) != expectedJSON {
		t.Errorf("Marshal = %s, want %s", data, expectedJSON)
	}

	// Unmarshal
	var parsed BackoffPolicy
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Verify round-trip
	if parsed != original {
		t.Errorf("Round-trip failed: got %+v, want %+v", parsed, original)
	}
}
