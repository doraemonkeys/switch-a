// Package instant defines the exact timestamp domain shared by persistence and
// analytics. Keeping the conversion here prevents different boundaries from
// silently accepting times that time.Time cannot represent as int64 nanoseconds.
package instant

import (
	"errors"
	"math"
	"time"
)

// ErrOutOfRange means an instant cannot be encoded as signed Unix nanoseconds.
var ErrOutOfRange = errors.New("instant is outside the signed nanosecond range")

var (
	minimum = time.Unix(math.MinInt64/int64(time.Second), math.MinInt64%int64(time.Second)).UTC()
	maximum = time.Unix(math.MaxInt64/int64(time.Second), math.MaxInt64%int64(time.Second)).UTC()
)

// UnixNano returns an exact storage/query key only when Go defines the
// conversion. Comparing first avoids relying on UnixNano's undefined overflow
// behavior for otherwise valid time.Time values.
func UnixNano(value time.Time) (int64, error) {
	value = value.UTC()
	if value.Before(minimum) || value.After(maximum) {
		return 0, ErrOutOfRange
	}
	return value.UnixNano(), nil
}
