package providerauth

import (
	"fmt"
	"math"
	"time"
)

// JSON itself permits larger integers, but the admin client consumes them as
// JavaScript numbers. Keeping source integers in this range prevents a previewed
// value from silently changing before the commit request returns to Go.
const (
	maximumJSONSafeInteger int64 = 1<<53 - 1
	minimumJSONSafeInteger int64 = -maximumJSONSafeInteger
)

const (
	minimumJSONTimeYear = 0
	maximumJSONTimeYear = 9999
)

var (
	minimumJSONTime = time.Date(minimumJSONTimeYear, time.January, 1, 0, 0, 0, 0, time.UTC)
	maximumJSONTime = time.Date(maximumJSONTimeYear, time.December, 31, 23, 59, 59, int(time.Second-time.Nanosecond), time.UTC)
)

// jsonRepresentableUnixTime rejects externally supplied timestamps that Go can
// construct but encoding/json cannot emit. Keeping this invariant at ingestion
// prevents one malformed account from breaking the entire batch response later.
func jsonRepresentableUnixTime(unixSeconds int64) (time.Time, error) {
	return jsonRepresentableTime(time.Unix(unixSeconds, 0))
}

func jsonRepresentableTime(value time.Time) (time.Time, error) {
	value = value.UTC()
	if year := value.Year(); year < minimumJSONTimeYear || year > maximumJSONTimeYear {
		return time.Time{}, fmt.Errorf(
			"timestamp year must be between %04d and %04d",
			minimumJSONTimeYear,
			maximumJSONTimeYear,
		)
	}
	return value, nil
}

func jsonRepresentableUnixFloat(unixSeconds float64) (time.Time, error) {
	if math.IsNaN(unixSeconds) || math.IsInf(unixSeconds, 0) || math.Trunc(unixSeconds) != unixSeconds {
		return time.Time{}, fmt.Errorf("timestamp must be a finite integer")
	}
	if unixSeconds < float64(minimumJSONTime.Unix()) ||
		unixSeconds > float64(maximumJSONTime.Unix()) {
		return time.Time{}, fmt.Errorf(
			"timestamp year must be between %04d and %04d",
			minimumJSONTimeYear,
			maximumJSONTimeYear,
		)
	}
	return jsonRepresentableUnixTime(int64(unixSeconds))
}
