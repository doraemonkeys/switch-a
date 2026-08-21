package analyticswindow

import (
	"errors"
	"net/url"
	"testing"
	"time"
)

type fakeClock struct {
	now time.Time
}

func (c fakeClock) Now() time.Time {
	return c.now
}

func TestStartResolutionStatesAreStable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		resolution StartResolution
		want       string
		resolved   bool
	}{
		{resolution: StartResolved, want: "resolved", resolved: true},
		{resolution: StartUnresolved, want: "unresolved"},
		{resolution: StartResolution(255), want: "invalid"},
	}
	for _, test := range tests {
		window := Window{StartResolution: test.resolution}
		if got := test.resolution.String(); got != test.want || window.HasResolvedStart() != test.resolved {
			t.Errorf("resolution %d = %q/%v, want %q/%v", test.resolution, got, window.HasResolvedStart(), test.want, test.resolved)
		}
	}
}

func TestResolversApplyEndpointDefaults(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 21, 9, 10, 11, 12, time.FixedZone("SGT", 8*60*60))
	resolver := NewResolver(fakeClock{now: now})

	stats, err := resolver.ResolveStats(nil)
	if err != nil {
		t.Fatal(err)
	}
	assertWindow(t, stats, Window{
		Period: Period24Hours,
		Start:  now.UTC().Add(-24 * time.Hour),
		End:    now.UTC(),
	})

	tokenUsage, err := resolver.ResolveTokenUsage(nil)
	if err != nil {
		t.Fatal(err)
	}
	assertWindow(t, tokenUsage, Window{
		Period:          Period24Hours,
		GranularityName: Granularity1Hour,
		Granularity:     time.Hour,
		Start:           now.UTC().Add(-24 * time.Hour),
		End:             now.UTC(),
	})
}

func TestTokenUsageDefaultGranularityByPeriod(t *testing.T) {
	t.Parallel()

	resolver := NewResolver(fakeClock{now: time.Date(2026, time.August, 21, 0, 0, 0, 0, time.UTC)})
	tests := []struct {
		period      string
		granularity string
		duration    time.Duration
		pending     bool
	}{
		{period: Period24Hours, granularity: Granularity1Hour, duration: time.Hour},
		{period: Period7Days, granularity: Granularity6Hours, duration: 6 * time.Hour},
		{period: Period30Days, granularity: Granularity1Day, duration: 24 * time.Hour},
		{period: PeriodAll, granularity: Granularity1Day, duration: 24 * time.Hour, pending: true},
	}
	for _, test := range tests {
		t.Run(test.period, func(t *testing.T) {
			window, err := resolver.ResolveTokenUsage(url.Values{"period": {test.period}})
			if err != nil {
				t.Fatal(err)
			}
			if window.GranularityName != test.granularity || window.Granularity != test.duration {
				t.Fatalf("granularity = %q/%s, want %q/%s", window.GranularityName, window.Granularity, test.granularity, test.duration)
			}
			if window.HasResolvedStart() == test.pending {
				t.Fatalf("HasResolvedStart() = %v, want %v", window.HasResolvedStart(), !test.pending)
			}
		})
	}
}

func TestResolveAsOfPreservesExactInstantInUTC(t *testing.T) {
	t.Parallel()

	resolver := NewResolver(fakeClock{now: time.Time{}})
	window, err := resolver.ResolveTokenUsage(url.Values{
		"as_of": {"2026-08-21T09:10:11.123456789+08:00"},
	})
	if err != nil {
		t.Fatal(err)
	}

	wantEnd := time.Date(2026, time.August, 21, 1, 10, 11, 123456789, time.UTC)
	if !window.End.Equal(wantEnd) || window.End.Location() != time.UTC {
		t.Fatalf("End = %s (%s), want %s UTC", window.End, window.End.Location(), wantEnd)
	}
	if got := window.End.Sub(window.Start); got != 24*time.Hour {
		t.Fatalf("window duration = %s, want 24h", got)
	}
}

func TestResolveRejectsInvalidScalarWindowValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		values url.Values
		field  string
		reason validationReason
	}{
		{name: "blank period", values: url.Values{"period": {""}}, field: "period", reason: reasonBlank},
		{name: "whitespace period", values: url.Values{"period": {"  "}}, field: "period", reason: reasonBlank},
		{name: "empty period values", values: url.Values{"period": {}}, field: "period", reason: reasonBlank},
		{name: "duplicate period", values: url.Values{"period": {Period24Hours, Period7Days}}, field: "period", reason: reasonDuplicate},
		{name: "unsupported period", values: url.Values{"period": {"1h"}}, field: "period", reason: reasonUnsupported},
		{name: "blank granularity", values: url.Values{"granularity": {""}}, field: "granularity", reason: reasonBlank},
		{name: "duplicate granularity", values: url.Values{"granularity": {Granularity1Hour, Granularity6Hours}}, field: "granularity", reason: reasonDuplicate},
		{name: "unsupported granularity", values: url.Values{"granularity": {"2h"}}, field: "granularity", reason: reasonUnsupported},
		{name: "blank as of", values: url.Values{"as_of": {""}}, field: "as_of", reason: reasonBlank},
		{name: "duplicate as of", values: url.Values{"as_of": {"2026-08-21T00:00:00Z", "2026-08-22T00:00:00Z"}}, field: "as_of", reason: reasonDuplicate},
		{name: "malformed as of", values: url.Values{"as_of": {"2026-08-21"}}, field: "as_of", reason: reasonMalformed},
	}
	resolver := NewResolver(fakeClock{now: time.Now()})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := resolver.ResolveStats(test.values)
			assertValidationError(t, err, test.field, test.reason)
		})
	}
}

func TestResolvePeriodGranularityCompatibility(t *testing.T) {
	t.Parallel()

	resolver := NewResolver(fakeClock{now: time.Date(2026, time.August, 21, 0, 0, 0, 0, time.UTC)})
	granularityOrder := []string{
		Granularity5Minutes,
		Granularity15Minutes,
		Granularity1Hour,
		Granularity6Hours,
		Granularity1Day,
	}
	minimumIndex := map[string]int{
		Period24Hours: 0,
		Period7Days:   2,
		Period30Days:  3,
		PeriodAll:     4,
	}
	for period, minimum := range minimumIndex {
		for index, granularity := range granularityOrder {
			name := period + "/" + granularity
			t.Run(name, func(t *testing.T) {
				window, err := resolver.ResolveTokenUsage(url.Values{
					"period":      {period},
					"granularity": {granularity},
				})
				if index < minimum {
					assertValidationError(t, err, "granularity", reasonIncompatible)
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if window.GranularityName != granularity {
					t.Fatalf("GranularityName = %q, want %q", window.GranularityName, granularity)
				}
			})
		}
	}
}

func TestBucketsAreEpochAlignedAndClippedToHalfOpenWindow(t *testing.T) {
	t.Parallel()

	resolver := NewResolver(fakeClock{now: time.Time{}})
	window, err := resolver.ResolveTokenUsage(url.Values{
		"period":      {Period24Hours},
		"granularity": {Granularity1Hour},
		"as_of":       {"2026-08-21T00:07:30.25Z"},
	})
	if err != nil {
		t.Fatal(err)
	}
	buckets, err := window.Buckets()
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 25 {
		t.Fatalf("len(Buckets) = %d, want 25 intersecting buckets", len(buckets))
	}

	first := buckets[0]
	wantAlignedFirst := time.Date(2026, time.August, 20, 0, 0, 0, 0, time.UTC)
	if !first.AlignedStart.Equal(wantAlignedFirst) || !first.Start.Equal(window.Start) {
		t.Fatalf("first bucket = %+v, want aligned %s clipped to %s", first, wantAlignedFirst, window.Start)
	}
	if !first.End.Equal(time.Date(2026, time.August, 20, 1, 0, 0, 0, time.UTC)) {
		t.Fatalf("first bucket end = %s", first.End)
	}

	last := buckets[len(buckets)-1]
	if !last.AlignedStart.Equal(time.Date(2026, time.August, 21, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("last aligned start = %s", last.AlignedStart)
	}
	if !last.End.Equal(window.End) {
		t.Fatalf("last bucket end = %s, want exclusive end %s", last.End, window.End)
	}
	for index, bucket := range buckets {
		if bucket.Start.Before(window.Start) || bucket.End.After(window.End) || !bucket.Start.Before(bucket.End) {
			t.Errorf("bucket %d escapes [start,end): %+v", index, bucket)
		}
		if index > 0 && !bucket.Start.Equal(buckets[index-1].End) {
			t.Errorf("bucket %d does not continue bucket %d", index, index-1)
		}
	}
}

func TestBucketsAlignBeforeUnixEpoch(t *testing.T) {
	t.Parallel()

	window := Window{
		Period:          Period24Hours,
		GranularityName: Granularity1Hour,
		Granularity:     time.Hour,
		Start:           time.Date(1969, time.December, 31, 23, 30, 0, 0, time.UTC),
		End:             time.Date(1970, time.January, 1, 0, 30, 0, 0, time.UTC),
	}
	buckets, err := window.Buckets()
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 2 {
		t.Fatalf("len(Buckets) = %d, want 2", len(buckets))
	}
	if got := buckets[0].AlignedStart; !got.Equal(time.Date(1969, time.December, 31, 23, 0, 0, 0, time.UTC)) {
		t.Fatalf("first aligned start = %s", got)
	}
}

func TestResolveAllCompletesBoundaryAndEmptyRanges(t *testing.T) {
	t.Parallel()

	end := time.Date(2026, time.August, 21, 0, 0, 0, 0, time.UTC)
	unresolved := Window{
		Period:          PeriodAll,
		GranularityName: Granularity1Hour,
		Granularity:     time.Hour,
		End:             end,
		StartResolution: StartUnresolved,
	}

	earliestAtBoundary := end.Add(-MaxBucketCount * time.Hour)
	resolved, err := ResolveAll(unresolved, &earliestAtBoundary)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.HasResolvedStart() || !resolved.Start.Equal(earliestAtBoundary) {
		t.Fatalf("resolved window = %+v", resolved)
	}
	buckets, err := resolved.Buckets()
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != MaxBucketCount {
		t.Fatalf("len(Buckets) = %d, want %d", len(buckets), MaxBucketCount)
	}

	empty, err := ResolveAll(unresolved, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !empty.HasResolvedStart() || !empty.Start.Equal(empty.End) {
		t.Fatalf("empty all-period window = %+v", empty)
	}
	emptyBuckets, err := empty.Buckets()
	if err != nil {
		t.Fatal(err)
	}
	if emptyBuckets == nil || len(emptyBuckets) != 0 {
		t.Fatalf("empty buckets = %#v, want allocated empty slice", emptyBuckets)
	}
}

func TestResolveAllRejectsOverflowAndImpossibleEarliest(t *testing.T) {
	t.Parallel()

	end := time.Date(2026, time.August, 21, 0, 0, 0, 0, time.UTC)
	unresolved := Window{
		Period:          PeriodAll,
		GranularityName: Granularity1Hour,
		Granularity:     time.Hour,
		End:             end,
		StartResolution: StartUnresolved,
	}

	overflow := end.Add(-(MaxBucketCount + 1) * time.Hour)
	resolved, err := ResolveAll(unresolved, &overflow)
	assertValidationError(t, err, "granularity", reasonTooManyBuckets)
	if !resolved.HasResolvedStart() || !resolved.Start.Equal(overflow) {
		t.Fatalf("overflow window = %+v, want resolved start %s", resolved, overflow)
	}

	for _, earliest := range []time.Time{end, end.Add(time.Nanosecond)} {
		resolved, err := ResolveAll(unresolved, &earliest)
		assertValidationError(t, err, "period", reasonInvalidRange)
		if !resolved.HasResolvedStart() || !resolved.Start.Equal(earliest) {
			t.Fatalf("invalid-range window = %+v, want resolved start %s", resolved, earliest)
		}
	}
}

func TestResolveAllIsIdempotentForCompletedWindows(t *testing.T) {
	t.Parallel()

	window := Window{
		Period:          Period24Hours,
		GranularityName: Granularity1Hour,
		Granularity:     time.Hour,
		Start:           time.Date(2026, time.August, 20, 0, 0, 0, 0, time.UTC),
		End:             time.Date(2026, time.August, 21, 0, 0, 0, 0, time.UTC),
	}
	unused := time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
	got, err := ResolveAll(window, &unused)
	if err != nil {
		t.Fatal(err)
	}
	assertWindow(t, got, window)
}

func TestBucketsRejectUnresolvedAllPeriod(t *testing.T) {
	t.Parallel()

	_, err := (Window{
		Period:          PeriodAll,
		GranularityName: Granularity1Day,
		Granularity:     24 * time.Hour,
		End:             time.Now().UTC(),
		StartResolution: StartUnresolved,
	}).Buckets()
	assertValidationError(t, err, "period", reasonInvalidRange)
}

func TestNewResolverRejectsNilClock(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("NewResolver(nil) did not panic")
		}
	}()
	NewResolver(nil)
}

func assertWindow(t *testing.T, got, want Window) {
	t.Helper()
	if got.Period != want.Period ||
		got.GranularityName != want.GranularityName ||
		got.Granularity != want.Granularity ||
		!got.Start.Equal(want.Start) ||
		!got.End.Equal(want.End) ||
		got.StartResolution != want.StartResolution {
		t.Fatalf("window = %+v, want %+v", got, want)
	}
}

func assertValidationError(t *testing.T, err error, field string, reason validationReason) {
	t.Helper()
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error = %v, want *ValidationError", err)
	}
	if validationErr.Error() != "invalid analytics window" {
		t.Fatalf("Error() = %q", validationErr.Error())
	}
	if validationErr.Field != field || validationErr.Reason != string(reason) {
		t.Fatalf("validation error = %+v, want field=%q reason=%q", validationErr, field, reason)
	}
}
