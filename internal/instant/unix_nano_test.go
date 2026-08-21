package instant

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestUnixNanoAcceptsExactRepresentableDomain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value time.Time
		want  int64
	}{
		{name: "minimum", value: minimum, want: math.MinInt64},
		{name: "maximum", value: maximum, want: math.MaxInt64},
		{name: "before_epoch", value: time.Unix(-1, 999999999), want: -1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := UnixNano(test.value)
			if err != nil {
				t.Fatalf("UnixNano() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("UnixNano() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestUnixNanoRejectsUnrepresentableTimes(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		value time.Time
	}{
		{name: "before_minimum", value: minimum.Add(-time.Nanosecond)},
		{name: "after_maximum", value: maximum.Add(time.Nanosecond)},
		{name: "zero_time", value: time.Time{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := UnixNano(test.value)
			if !errors.Is(err, ErrOutOfRange) {
				t.Fatalf("UnixNano() error = %v, want %v", err, ErrOutOfRange)
			}
		})
	}
}
