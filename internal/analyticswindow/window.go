// Package analyticswindow resolves shared analytics time ranges without
// coupling HTTP handlers to wall-clock time or duplicating validation rules.
package analyticswindow

import (
	"net/url"
	"strings"
	"time"
)

const (
	Period24Hours = "24h"
	Period7Days   = "7d"
	Period30Days  = "30d"
	PeriodAll     = "all"

	Granularity5Minutes  = "5m"
	Granularity15Minutes = "15m"
	Granularity1Hour     = "1h"
	Granularity6Hours    = "6h"
	Granularity1Day      = "1d"

	DefaultPeriod = Period24Hours

	// MaxBucketCount bounds both result size and the lifetime of the SQLite
	// read snapshot that later analytics stages hold.
	MaxBucketCount = 512
)

const (
	period24HoursDuration = 24 * time.Hour
	period7DaysDuration   = 7 * period24HoursDuration
	period30DaysDuration  = 30 * period24HoursDuration
)

type validationReason string

// StartResolution distinguishes a data-dependent all-period lower bound from a
// concrete timestamp. Its zero value is resolved so ordinary fixed windows
// remain valid value literals.
type StartResolution uint8

const (
	StartResolved StartResolution = iota
	StartUnresolved
)

func (r StartResolution) String() string {
	switch r {
	case StartResolved:
		return "resolved"
	case StartUnresolved:
		return "unresolved"
	default:
		return "invalid"
	}
}

const (
	reasonBlank          validationReason = "blank"
	reasonDuplicate      validationReason = "duplicate"
	reasonMalformed      validationReason = "malformed"
	reasonUnsupported    validationReason = "unsupported"
	reasonIncompatible   validationReason = "incompatible"
	reasonInvalidRange   validationReason = "invalid_range"
	reasonTooManyBuckets validationReason = "too_many_buckets"
)

// Clock is defined at the point of use so tests and callers need expose only
// the one time operation the resolver requires.
type Clock interface {
	Now() time.Time
}

// Resolver applies one validation policy to both analytics endpoints.
type Resolver struct {
	clock Clock
}

// NewResolver constructs a resolver with an injected clock.
func NewResolver(clock Clock) Resolver {
	if clock == nil {
		panic("analyticswindow: nil clock")
	}
	return Resolver{clock: clock}
}

// Window is a value contract. Callers pass copies between layers so resolving
// an all-period start cannot mutate a concurrently executing query.
type Window struct {
	Period          string
	GranularityName string
	Granularity     time.Duration
	Start           time.Time
	End             time.Time
	StartResolution StartResolution
}

// HasResolvedStart reports whether Start represents a concrete timestamp
// rather than a lower bound that must be derived from matching data.
func (w Window) HasResolvedStart() bool {
	return w.StartResolution == StartResolved
}

// BucketBounds identifies both the epoch-aligned storage key and the visible,
// clipped interval represented by that key.
type BucketBounds struct {
	AlignedStart time.Time
	Start        time.Time
	End          time.Time
}

// ValidationError reports a safe, stable reason without echoing query values.
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	return "invalid analytics window"
}

func newValidationError(field string, reason validationReason) *ValidationError {
	return &ValidationError{Field: field, Reason: string(reason)}
}

// ResolveStats preserves omitted granularity as an explicit request for no
// time series while sharing all scalar validation with token analytics.
func (r Resolver) ResolveStats(values url.Values) (Window, error) {
	return r.resolve(values, false)
}

// ResolveTokenUsage selects a bounded default granularity because its response
// always includes a time-series section.
func (r Resolver) ResolveTokenUsage(values url.Values) (Window, error) {
	return r.resolve(values, true)
}

func (r Resolver) resolve(values url.Values, defaultGranularity bool) (Window, error) {
	period, present, err := scalarValue(values, "period")
	if err != nil {
		return Window{}, err
	}
	if !present {
		period = DefaultPeriod
	}
	periodDuration, ok := periodDurations[period]
	if !ok {
		return Window{}, newValidationError("period", reasonUnsupported)
	}

	granularityName, granularityPresent, err := scalarValue(values, "granularity")
	if err != nil {
		return Window{}, err
	}
	if !granularityPresent && defaultGranularity {
		granularityName = defaultGranularities[period]
		granularityPresent = true
	}

	var granularity time.Duration
	if granularityPresent {
		var known bool
		granularity, known = granularities[granularityName]
		if !known {
			return Window{}, newValidationError("granularity", reasonUnsupported)
		}
		if granularity < minimumGranularities[period] {
			return Window{}, newValidationError("granularity", reasonIncompatible)
		}
	}

	end, asOfPresent, err := scalarValue(values, "as_of")
	if err != nil {
		return Window{}, err
	}
	var endTime time.Time
	if asOfPresent {
		endTime, err = time.Parse(time.RFC3339Nano, end)
		if err != nil {
			return Window{}, newValidationError("as_of", reasonMalformed)
		}
	} else {
		endTime = r.clock.Now()
	}
	endTime = endTime.UTC()

	window := Window{
		Period:          period,
		GranularityName: granularityName,
		Granularity:     granularity,
		End:             endTime,
		StartResolution: StartResolved,
	}
	if period == PeriodAll {
		window.StartResolution = StartUnresolved
	} else {
		window.Start = endTime.Add(-periodDuration)
		if err := validateBucketCount(window); err != nil {
			return Window{}, err
		}
	}
	return window, nil
}

// ResolveAll completes an unresolved all-period window from the earliest row
// selected by the same query filters. A nil earliest value produces [end,end)
// so an empty dataset never pretends to cover an arbitrary historical range.
func ResolveAll(window Window, earliest *time.Time) (Window, error) {
	if window.HasResolvedStart() {
		return window, nil
	}

	window.StartResolution = StartResolved
	if earliest == nil {
		window.Start = window.End
		return window, nil
	}

	window.Start = earliest.UTC()
	if !window.Start.Before(window.End) {
		return window, newValidationError("period", reasonInvalidRange)
	}
	if err := validateBucketCount(window); err != nil {
		return window, err
	}
	return window, nil
}

// Buckets returns every epoch-aligned bucket intersecting [start,end), with
// visible endpoints clipped to the exact requested range.
func (w Window) Buckets() ([]BucketBounds, error) {
	if !w.HasResolvedStart() {
		return nil, newValidationError("period", reasonInvalidRange)
	}
	if w.Granularity == 0 || !w.Start.Before(w.End) {
		return []BucketBounds{}, nil
	}
	if err := validateBucketCount(w); err != nil {
		return nil, err
	}

	buckets := make([]BucketBounds, 0, MaxBucketCount)
	for aligned := alignUTC(w.Start, w.Granularity); aligned.Before(w.End); aligned = aligned.Add(w.Granularity) {
		start := aligned
		if start.Before(w.Start) {
			start = w.Start
		}
		end := aligned.Add(w.Granularity)
		if end.After(w.End) {
			end = w.End
		}
		buckets = append(buckets, BucketBounds{
			AlignedStart: aligned,
			Start:        start,
			End:          end,
		})
	}
	return buckets, nil
}

func validateBucketCount(window Window) error {
	if window.Granularity == 0 || !window.Start.Before(window.End) {
		return nil
	}

	count := 0
	for aligned := alignUTC(window.Start, window.Granularity); aligned.Before(window.End); aligned = aligned.Add(window.Granularity) {
		count++
		if count > MaxBucketCount {
			return newValidationError("granularity", reasonTooManyBuckets)
		}
	}
	return nil
}

func alignUTC(value time.Time, granularity time.Duration) time.Time {
	stepSeconds := int64(granularity / time.Second)
	unixSeconds := value.UTC().Unix()
	remainder := unixSeconds % stepSeconds
	if remainder < 0 {
		remainder += stepSeconds
	}
	return time.Unix(unixSeconds-remainder, 0).UTC()
}

func scalarValue(values url.Values, field string) (string, bool, error) {
	raw, present := values[field]
	if !present {
		return "", false, nil
	}
	if len(raw) == 0 {
		return "", false, newValidationError(field, reasonBlank)
	}
	if len(raw) > 1 {
		return "", false, newValidationError(field, reasonDuplicate)
	}
	if strings.TrimSpace(raw[0]) == "" {
		return "", false, newValidationError(field, reasonBlank)
	}
	return raw[0], true, nil
}

var periodDurations = map[string]time.Duration{
	Period24Hours: period24HoursDuration,
	Period7Days:   period7DaysDuration,
	Period30Days:  period30DaysDuration,
	PeriodAll:     0,
}

var granularities = map[string]time.Duration{
	Granularity5Minutes:  5 * time.Minute,
	Granularity15Minutes: 15 * time.Minute,
	Granularity1Hour:     time.Hour,
	Granularity6Hours:    6 * time.Hour,
	Granularity1Day:      period24HoursDuration,
}

var minimumGranularities = map[string]time.Duration{
	Period24Hours: 5 * time.Minute,
	Period7Days:   time.Hour,
	Period30Days:  6 * time.Hour,
	PeriodAll:     period24HoursDuration,
}

var defaultGranularities = map[string]string{
	Period24Hours: Granularity1Hour,
	Period7Days:   Granularity6Hours,
	Period30Days:  Granularity1Day,
	PeriodAll:     Granularity1Day,
}
