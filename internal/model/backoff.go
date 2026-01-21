package model

import (
	"encoding/json"
	"errors"
	"math"
	"math/rand/v2"
	"time"

	"switch-a/internal/defaults"
)

// Duration wraps time.Duration with human-readable JSON marshaling.
// JSON format uses Go duration strings (e.g., "100ms", "5s", "1m30s").
type Duration time.Duration

// MarshalJSON converts the duration to a human-readable string.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON parses a human-readable duration string.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var v string
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(v)
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

// BackoffPolicy defines exponential backoff behavior for same-provider retries.
// When a provider fails and has retries remaining, this policy controls the
// delay before each retry attempt.
type BackoffPolicy struct {
	// InitialDelay is the base delay for the first retry (e.g., 100ms).
	// If zero or negative, no backoff is applied.
	InitialDelay Duration `gorm:"column:backoff_initial_delay" json:"initial_delay"`
	// MaxDelay caps the maximum delay regardless of retry count (e.g., 5s).
	// If zero, no maximum is enforced.
	MaxDelay Duration `gorm:"column:backoff_max_delay" json:"max_delay"`
	// Multiplier controls exponential growth. Default is 2.0 if zero.
	// Must be >= 1.0 to ensure delays don't shrink.
	Multiplier float64 `gorm:"column:backoff_multiplier" json:"multiplier,omitempty"`
	// Jitter enables Full Jitter mode: delay becomes random in [0, calculated_delay].
	// Recommended for distributed systems to prevent thundering herd.
	Jitter bool `gorm:"column:backoff_jitter" json:"jitter,omitempty"`
}

// IsZero reports whether the policy is unconfigured.
// A zero InitialDelay means no backoff should be applied.
func (b BackoffPolicy) IsZero() bool {
	return b.InitialDelay <= 0
}

// Validate ensures the configuration is logically consistent.
// Note: InitialDelay == 0 is treated as unconfigured (see IsZero), not an error.
func (b BackoffPolicy) Validate() error {
	if b.InitialDelay < 0 {
		return errors.New("initial_delay must be non-negative")
	}
	if b.MaxDelay > 0 && b.InitialDelay > b.MaxDelay {
		return errors.New("initial_delay cannot exceed max_delay")
	}
	if b.Multiplier < 1.0 && b.Multiplier != 0 {
		return errors.New("multiplier must be >= 1.0 (or 0 for default)")
	}
	return nil
}

// DelayForRetry calculates the delay for a given retry attempt.
// retryIndex: 0 = first retry, 1 = second retry, etc.
// Uses math/rand/v2 global functions (thread-safe since Go 1.22).
func (b BackoffPolicy) DelayForRetry(retryIndex int) time.Duration {
	if b.IsZero() || retryIndex < 0 {
		return 0
	}
	multiplier := b.Multiplier
	if multiplier <= 0 {
		multiplier = defaults.BackoffMultiplier
	}

	delay := float64(b.InitialDelay) * math.Pow(multiplier, float64(retryIndex))

	maxDelay := float64(b.MaxDelay)
	if maxDelay <= 0 {
		// Cap at a value safely convertible to int64 to avoid overflow.
		// float64 cannot exactly represent MaxInt64, so we use a slightly smaller value.
		maxDelay = float64(math.MaxInt64 - 512)
	}
	// Handle +Inf from math.Pow overflow
	if math.IsInf(delay, 1) || delay > maxDelay {
		delay = maxDelay
	}

	if b.Jitter {
		delay = rand.Float64() * delay // Full Jitter: [0, delay]
	}

	return time.Duration(delay)
}
