package tokenanalytics

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/analyticswindow"
)

func TestNewServiceRejectsNilReader(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("NewService(nil) did not panic")
		}
	}()
	NewService(nil)
}

func TestServiceAnalyzeBuildsConservingReport(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.August, 20, 0, 30, 0, 0, time.UTC)
	end := start.Add(2*time.Hour + 45*time.Minute)
	query := Query{
		Window: analyticswindow.Window{
			Period:          analyticswindow.Period24Hours,
			GranularityName: analyticswindow.Granularity1Hour,
			Granularity:     time.Hour,
			Start:           start,
			End:             end,
		},
		ProviderID: stringPointer("provider-filter"),
		Model:      stringPointer("model-filter"),
		APIType:    stringPointer("api-filter"),
	}
	summaryBreakdown := Breakdown{
		TotalTokens:              100,
		InputTokens:              60,
		OutputTokens:             40,
		FreshInputTokens:         20,
		CacheReadInputTokens:     20,
		CacheCreationInputTokens: 10,
		UnclassifiedInputTokens:  10,
		StandardOutputTokens:     20,
		ReasoningTokens:          10,
		UnclassifiedOutputTokens: 10,
	}
	firstBucket := simpleBreakdown(8, 2)
	thirdBucket := simpleBreakdown(14, 6)
	snapshot := &fakeSnapshot{
		summary: SummaryRecord{
			Breakdown:                summaryBreakdown,
			TotalRequests:            10,
			ObservedRequests:         8,
			ComparableRequests:       4,
			PartialRequests:          2,
			InvalidRequests:          1,
			UnknownSemanticsRequests: 1,
		},
		buckets: []BucketRecord{
			{AlignedStart: time.Date(2026, time.August, 20, 2, 0, 0, 0, time.UTC), Breakdown: thirdBucket, TotalRequests: 3, ObservedRequests: 2, ComparableRequests: 2},
			{AlignedStart: time.Date(2026, time.August, 20, 8, 0, 0, 0, time.FixedZone("equivalent", 8*60*60)), Breakdown: firstBucket, TotalRequests: 2, ObservedRequests: 2, ComparableRequests: 1},
		},
		providers: []ProviderRankRecord{
			{ProviderID: "a", ProviderLabel: "Alpha", Breakdown: simpleBreakdown(30, 10), ComparableRequests: 2},
			{ProviderID: "b", ProviderLabel: "Beta", Breakdown: simpleBreakdown(25, 10), ComparableRequests: 2},
			{ProviderID: "c", ProviderLabel: "Charlie", Breakdown: simpleBreakdown(15, 10), ComparableRequests: 3},
		},
		models: []ModelRankRecord{
			{Model: "", Breakdown: simpleBreakdown(30, 10), ComparableRequests: 2},
			{Model: "alpha", Breakdown: simpleBreakdown(25, 10), ComparableRequests: 2},
			{Model: "beta", Breakdown: simpleBreakdown(15, 10), ComparableRequests: 3},
		},
	}
	reader := &fakeReader{snapshot: snapshot}

	report, err := NewService(reader).Analyze(context.Background(), query)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	if reader.opens != 1 || snapshot.closed != 1 {
		t.Fatalf("snapshot lifecycle = opens %d, closes %d; want 1, 1", reader.opens, snapshot.closed)
	}
	if got, want := snapshot.calls, []string{"summary", "timeseries", "provider_rank", "model_rank", "close"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot calls = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(snapshot.summaryQueries[0], query) ||
		!reflect.DeepEqual(snapshot.bucketQueries[0], query) ||
		!reflect.DeepEqual(snapshot.providerQueries[0], query) ||
		!reflect.DeepEqual(snapshot.modelQueries[0], query) {
		t.Fatal("fixed-window query or exact filters changed between snapshot reads")
	}
	if snapshot.providerLimits[0] != TopRankLimit || snapshot.modelLimits[0] != TopRankLimit {
		t.Fatalf("rank limits = %d, %d; want %d", snapshot.providerLimits[0], snapshot.modelLimits[0], TopRankLimit)
	}

	if report.Summary.Breakdown != summaryBreakdown {
		t.Fatalf("summary breakdown = %+v, want %+v", report.Summary.Breakdown, summaryBreakdown)
	}
	if report.Summary.CacheHitRate != 1.0/3.0 || report.Summary.ReasoningRatio != 0.25 {
		t.Fatalf("summary ratios = %v, %v; want %v, %v", report.Summary.CacheHitRate, report.Summary.ReasoningRatio, 1.0/3.0, 0.25)
	}
	wantCoverage := Coverage{TotalRequests: 10, ObservedRequests: 8, ComparableRequests: 4, WithoutUsageRequests: 2, Rate: 0.4}
	if report.Coverage != wantCoverage {
		t.Fatalf("coverage = %+v, want %+v", report.Coverage, wantCoverage)
	}
	wantQuality := DataQuality{QualityRate: 0.5, PartialRequests: 2, InvalidRequests: 1, UnknownSemanticsRequests: 1}
	if report.DataQuality != wantQuality {
		t.Fatalf("data quality = %+v, want %+v", report.DataQuality, wantQuality)
	}
	if report.TimeRange != (TimeRange{Start: start, End: end}) {
		t.Fatalf("time range = %+v, want [%v, %v)", report.TimeRange, start, end)
	}

	if got, want := len(report.TimeSeries), 4; got != want {
		t.Fatalf("time series length = %d, want %d", got, want)
	}
	if !report.TimeSeries[0].Start.Equal(start) || !report.TimeSeries[0].End.Equal(time.Date(2026, time.August, 20, 1, 0, 0, 0, time.UTC)) {
		t.Fatalf("first bucket was not clipped: %+v", report.TimeSeries[0])
	}
	if report.TimeSeries[0].Breakdown != firstBucket || report.TimeSeries[0].TotalRequests != 2 || report.TimeSeries[0].ObservedRequests != 2 || report.TimeSeries[0].ComparableRequests != 1 {
		t.Fatalf("first bucket = %+v", report.TimeSeries[0])
	}
	if report.TimeSeries[1].Breakdown != (Breakdown{}) || report.TimeSeries[1].ComparableRequests != 0 {
		t.Fatalf("internal gap was not zero-filled: %+v", report.TimeSeries[1])
	}
	if report.TimeSeries[2].Breakdown != thirdBucket || report.TimeSeries[2].TotalRequests != 3 || report.TimeSeries[2].ObservedRequests != 2 || report.TimeSeries[2].ComparableRequests != 2 {
		t.Fatalf("third bucket = %+v", report.TimeSeries[2])
	}
	if !report.TimeSeries[3].Start.Equal(time.Date(2026, time.August, 20, 3, 0, 0, 0, time.UTC)) || !report.TimeSeries[3].End.Equal(end) {
		t.Fatalf("last bucket was not clipped: %+v", report.TimeSeries[3])
	}
	if got := []string{report.ByProvider[0].ProviderID, report.ByProvider[1].ProviderID, report.ByProvider[2].ProviderID}; !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("provider order = %v", got)
	}
	if report.ByProvider[0].ProviderLabel != "Alpha" {
		t.Fatalf("provider label = %q, want Alpha", report.ByProvider[0].ProviderLabel)
	}
	if report.ByProvider[0].Share != 0.4 || report.ByModel[0].Share != 0.4 {
		t.Fatalf("rank shares = %v, %v; want 0.4", report.ByProvider[0].Share, report.ByModel[0].Share)
	}
	if got := []string{report.ByModel[0].Model, report.ByModel[1].Model, report.ByModel[2].Model}; !reflect.DeepEqual(got, []string{"", "alpha", "beta"}) {
		t.Fatalf("model order = %v", got)
	}
}

func TestServiceAnalyzeNormalizesEmptyCollectionsAndZeroDenominators(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.August, 20, 0, 0, 0, 0, time.UTC)
	snapshot := &fakeSnapshot{}
	report, err := NewService(&fakeReader{snapshot: snapshot}).Analyze(context.Background(), Query{Window: analyticswindow.Window{
		Period:          analyticswindow.Period24Hours,
		GranularityName: analyticswindow.Granularity1Hour,
		Granularity:     time.Hour,
		Start:           start,
		End:             start.Add(2 * time.Hour),
	}})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.TimeSeries == nil || report.ByProvider == nil || report.ByModel == nil {
		t.Fatalf("collections must be allocated: %+v", report)
	}
	if len(report.TimeSeries) != 2 || len(report.ByProvider) != 0 || len(report.ByModel) != 0 {
		t.Fatalf("empty collection lengths = %d, %d, %d", len(report.TimeSeries), len(report.ByProvider), len(report.ByModel))
	}
	if report.Summary.CacheHitRate != 0 || report.Summary.ReasoningRatio != 0 || report.Coverage.Rate != 0 || report.DataQuality.QualityRate != 0 {
		t.Fatalf("zero-denominator ratios must be zero: %+v", report)
	}
}

func TestServiceAnalyzeResolvesAllPeriodInsideSnapshot(t *testing.T) {
	t.Parallel()

	earliest := time.Date(2026, time.August, 18, 0, 30, 0, 0, time.FixedZone("source", -7*60*60))
	end := time.Date(2026, time.August, 20, 3, 15, 0, 0, time.UTC)
	snapshot := &fakeSnapshot{summary: SummaryRecord{
		TotalRequests:        1,
		ObservedRequests:     1,
		ComparableRequests:   1,
		Breakdown:            simpleBreakdown(0, 0),
		EarliestMatchingTime: &earliest,
	}}
	query := Query{Window: analyticswindow.Window{
		Period:          analyticswindow.PeriodAll,
		GranularityName: analyticswindow.Granularity1Day,
		Granularity:     24 * time.Hour,
		End:             end,
		StartResolution: analyticswindow.StartUnresolved,
	}}

	report, err := NewService(&fakeReader{snapshot: snapshot}).Analyze(context.Background(), query)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	wantStart := earliest.UTC()
	if !report.TimeRange.Start.Equal(wantStart) || !report.TimeRange.End.Equal(end) {
		t.Fatalf("time range = %+v, want [%v, %v)", report.TimeRange, wantStart, end)
	}
	if got := snapshot.summaryQueries[0].Window; got.HasResolvedStart() || !got.Start.IsZero() {
		t.Fatalf("summary query window was prematurely resolved: %+v", got)
	}
	for _, got := range []Query{snapshot.bucketQueries[0], snapshot.providerQueries[0], snapshot.modelQueries[0]} {
		if !got.Window.HasResolvedStart() || !got.Window.Start.Equal(wantStart) {
			t.Fatalf("post-summary query did not use resolved all window: %+v", got.Window)
		}
	}
	if len(report.TimeSeries) != 3 {
		t.Fatalf("time series length = %d, want 3 intersecting partial/full buckets", len(report.TimeSeries))
	}
}

func TestServiceAnalyzeReturnsEmptyAllPeriodRange(t *testing.T) {
	t.Parallel()

	end := time.Date(2026, time.August, 20, 3, 15, 0, 0, time.UTC)
	snapshot := &fakeSnapshot{}
	report, err := NewService(&fakeReader{snapshot: snapshot}).Analyze(context.Background(), Query{Window: analyticswindow.Window{
		Period:          analyticswindow.PeriodAll,
		GranularityName: analyticswindow.Granularity1Day,
		Granularity:     24 * time.Hour,
		End:             end,
		StartResolution: analyticswindow.StartUnresolved,
	}})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if !report.TimeRange.Start.Equal(end) || !report.TimeRange.End.Equal(end) {
		t.Fatalf("empty all-period range = %+v, want [%v, %v)", report.TimeRange, end, end)
	}
	if report.TimeSeries == nil || report.ByProvider == nil || report.ByModel == nil {
		t.Fatal("empty all-period collections must be allocated")
	}
	if len(report.TimeSeries) != 0 {
		t.Fatalf("empty all-period buckets = %d, want 0", len(report.TimeSeries))
	}
	if got, want := snapshot.calls, []string{"summary", "timeseries", "provider_rank", "model_rank", "close"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot calls = %v, want %v", got, want)
	}
}

func TestServiceAnalyzeRejectsAllPeriodBucketOverflow(t *testing.T) {
	t.Parallel()

	end := time.Date(2026, time.August, 20, 0, 0, 0, 0, time.UTC)
	earliest := end.Add(-(analyticswindow.MaxBucketCount + 1) * 24 * time.Hour)
	snapshot := &fakeSnapshot{summary: SummaryRecord{TotalRequests: 1, EarliestMatchingTime: &earliest}}
	_, err := NewService(&fakeReader{snapshot: snapshot}).Analyze(context.Background(), Query{Window: analyticswindow.Window{
		Period:          analyticswindow.PeriodAll,
		GranularityName: analyticswindow.Granularity1Day,
		Granularity:     24 * time.Hour,
		End:             end,
		StartResolution: analyticswindow.StartUnresolved,
	}})
	if !IsFailureAt(err, FailureStageResponseMap) || FailureCodeOf(err) != FailureCodeWindowResolution {
		t.Fatalf("Analyze() error = %v, code %q; want response_map/%q", err, FailureCodeOf(err), FailureCodeWindowResolution)
	}
	var validationErr *analyticswindow.ValidationError
	if !errors.As(err, &validationErr) || validationErr.Reason != "too_many_buckets" {
		t.Fatalf("Analyze() error = %v, want detectable bucket validation error", err)
	}
	failureWindow, ok := ResolvedFailureWindow(err)
	if !ok || !failureWindow.Start.Equal(earliest) || !failureWindow.End.Equal(end) {
		t.Fatalf("failure window = %+v/%v, want [%s,%s)", failureWindow, ok, earliest, end)
	}
	if got, want := snapshot.calls, []string{"summary", "close"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot calls = %v, want %v", got, want)
	}
}

func TestServiceAnalyzeAllPeriodFailureWindowTracksResolutionStage(t *testing.T) {
	t.Parallel()

	end := time.Date(2026, time.August, 20, 0, 0, 0, 0, time.UTC)
	allQuery := Query{Window: analyticswindow.Window{
		Period:          analyticswindow.PeriodAll,
		GranularityName: analyticswindow.Granularity1Day,
		Granularity:     24 * time.Hour,
		End:             end,
		StartResolution: analyticswindow.StartUnresolved,
	}}

	t.Run("summary remains unresolved", func(t *testing.T) {
		t.Parallel()
		snapshot := &fakeSnapshot{summaryErr: context.Canceled}
		_, err := NewService(&fakeReader{snapshot: snapshot}).Analyze(context.Background(), allQuery)
		if _, ok := ResolvedFailureWindow(err); ok {
			t.Fatalf("summary failure exposed a fabricated resolved window: %v", err)
		}
	})

	t.Run("post-summary storage failure retains earliest", func(t *testing.T) {
		t.Parallel()
		earliest := end.Add(-48 * time.Hour)
		snapshot := &fakeSnapshot{
			summary:   SummaryRecord{TotalRequests: 1, EarliestMatchingTime: &earliest},
			bucketErr: context.Canceled,
		}
		_, err := NewService(&fakeReader{snapshot: snapshot}).Analyze(context.Background(), allQuery)
		window, ok := ResolvedFailureWindow(err)
		if !ok || !window.Start.Equal(earliest) || !window.End.Equal(end) {
			t.Fatalf("post-summary failure window = %+v/%v, want [%s,%s)", window, ok, earliest, end)
		}
	})
}

func TestServiceAnalyzeWrapsRepositoryFailuresOnceAndCloses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		openError error
		configure func(*fakeSnapshot)
		stage     FailureStage
		closed    int
		calls     []string
	}{
		{name: "open", openError: context.Canceled, stage: FailureStageSnapshotOpen},
		{name: "summary", configure: func(snapshot *fakeSnapshot) { snapshot.summaryErr = context.Canceled }, stage: FailureStageSummary, closed: 1, calls: []string{"summary", "close"}},
		{name: "timeseries", configure: func(snapshot *fakeSnapshot) { snapshot.bucketErr = context.Canceled }, stage: FailureStageTimeSeries, closed: 1, calls: []string{"summary", "timeseries", "close"}},
		{name: "provider", configure: func(snapshot *fakeSnapshot) { snapshot.providerErr = context.Canceled }, stage: FailureStageProviderRank, closed: 1, calls: []string{"summary", "timeseries", "provider_rank", "close"}},
		{name: "model", configure: func(snapshot *fakeSnapshot) { snapshot.modelErr = context.Canceled }, stage: FailureStageModelRank, closed: 1, calls: []string{"summary", "timeseries", "provider_rank", "model_rank", "close"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			snapshot := &fakeSnapshot{}
			if test.configure != nil {
				test.configure(snapshot)
			}
			reader := &fakeReader{snapshot: snapshot, err: test.openError}
			_, err := NewService(reader).Analyze(context.Background(), fixedQuery())
			if !IsFailureAt(err, test.stage) || FailureCodeOf(err) != FailureCodeRepository {
				t.Fatalf("Analyze() error = %v, code %q; want stage/code %q/%q", err, FailureCodeOf(err), test.stage, FailureCodeRepository)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Analyze() error = %v, want errors.Is(context.Canceled)", err)
			}
			if err.Error() != failureMessage {
				t.Fatalf("safe error text = %q, want %q", err.Error(), failureMessage)
			}
			var failure *Failure
			if !errors.As(err, &failure) || errors.As(failure.Unwrap(), &failure) {
				t.Fatalf("failure was nested instead of wrapped once: %v", err)
			}
			if snapshot.closed != test.closed || !reflect.DeepEqual(snapshot.calls, test.calls) {
				t.Fatalf("snapshot lifecycle = closes %d calls %v, want %d %v", snapshot.closed, snapshot.calls, test.closed, test.calls)
			}
		})
	}
}

func TestServiceAnalyzeHandlesSnapshotLifecycleFailures(t *testing.T) {
	t.Parallel()

	t.Run("nil snapshot", func(t *testing.T) {
		t.Parallel()
		_, err := NewService(&fakeReader{}).Analyze(context.Background(), fixedQuery())
		if !IsFailureAt(err, FailureStageSnapshotOpen) || !errors.Is(err, errNilSnapshot) {
			t.Fatalf("Analyze() error = %v, want nil snapshot failure", err)
		}
	})

	t.Run("close", func(t *testing.T) {
		t.Parallel()
		closeCause := errors.New("private close detail")
		snapshot := &fakeSnapshot{closeErr: closeCause}
		report, err := NewService(&fakeReader{snapshot: snapshot}).Analyze(context.Background(), fixedQuery())
		if !IsFailureAt(err, FailureStageResponseMap) || FailureCodeOf(err) != FailureCodeSnapshotClose ||
			!errors.Is(err, closeCause) || !errors.Is(err, errSnapshotClose) {
			t.Fatalf("Analyze() error = %v, code %q; want response_map/%q close failure", err, FailureCodeOf(err), FailureCodeSnapshotClose)
		}
		if err.Error() != failureMessage {
			t.Fatalf("safe error text leaked close detail: %q", err.Error())
		}
		if !reflect.DeepEqual(report, Report{}) || snapshot.closed != 1 {
			t.Fatalf("close failure returned report %+v or close count %d", report, snapshot.closed)
		}
	})
}

func TestValidateSummaryEnforcesQualityPartition(t *testing.T) {
	t.Parallel()

	valid := SummaryRecord{
		TotalRequests:            9,
		ObservedRequests:         7,
		ComparableRequests:       4,
		PartialRequests:          1,
		InvalidRequests:          1,
		UnknownSemanticsRequests: 1,
	}
	tests := []struct {
		name         string
		mutate       func(*SummaryRecord)
		resolvingAll bool
	}{
		{name: "negative total", mutate: func(record *SummaryRecord) { record.TotalRequests = -1 }},
		{name: "negative observed", mutate: func(record *SummaryRecord) { record.ObservedRequests = -1 }},
		{name: "negative comparable", mutate: func(record *SummaryRecord) { record.ComparableRequests = -1 }},
		{name: "negative partial", mutate: func(record *SummaryRecord) { record.PartialRequests = -1 }},
		{name: "negative invalid", mutate: func(record *SummaryRecord) { record.InvalidRequests = -1 }},
		{name: "negative unknown", mutate: func(record *SummaryRecord) { record.UnknownSemanticsRequests = -1 }},
		{name: "observed exceeds total", mutate: func(record *SummaryRecord) { record.TotalRequests = 6 }},
		{name: "partition mismatch", mutate: func(record *SummaryRecord) { record.PartialRequests = 2 }},
		{name: "partition overflow", mutate: func(record *SummaryRecord) {
			record.ObservedRequests = math.MaxInt64
			record.ComparableRequests = math.MaxInt64
			record.PartialRequests = 1
			record.TotalRequests = math.MaxInt64
		}},
		{name: "all rows without earliest", resolvingAll: true, mutate: func(record *SummaryRecord) {
			record.TotalRequests = 1
			record.ObservedRequests = 0
			record.ComparableRequests = 0
			record.PartialRequests = 0
			record.InvalidRequests = 0
			record.UnknownSemanticsRequests = 0
		}},
		{name: "all earliest without rows", resolvingAll: true, mutate: func(record *SummaryRecord) {
			instant := time.Now()
			record.TotalRequests = 0
			record.ObservedRequests = 0
			record.ComparableRequests = 0
			record.PartialRequests = 0
			record.InvalidRequests = 0
			record.UnknownSemanticsRequests = 0
			record.EarliestMatchingTime = &instant
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			record := valid
			test.mutate(&record)
			if err := validateSummary(record, test.resolvingAll); !errors.Is(err, errInvalidQualityPartition) {
				t.Fatalf("validateSummary() error = %v, want quality partition failure", err)
			}
		})
	}
	if err := validateSummary(valid, false); err != nil {
		t.Fatalf("validateSummary(valid) error = %v", err)
	}
}

func TestValidateBreakdownEnforcesCanonicalConservation(t *testing.T) {
	t.Parallel()

	valid := Breakdown{
		TotalTokens:              100,
		InputTokens:              60,
		OutputTokens:             40,
		FreshInputTokens:         20,
		CacheReadInputTokens:     20,
		CacheCreationInputTokens: 10,
		UnclassifiedInputTokens:  10,
		StandardOutputTokens:     20,
		ReasoningTokens:          10,
		UnclassifiedOutputTokens: 10,
	}
	tests := []struct {
		name   string
		mutate func(*Breakdown)
	}{
		{name: "negative total", mutate: func(value *Breakdown) { value.TotalTokens = -1 }},
		{name: "negative input", mutate: func(value *Breakdown) { value.InputTokens = -1 }},
		{name: "negative output", mutate: func(value *Breakdown) { value.OutputTokens = -1 }},
		{name: "negative fresh", mutate: func(value *Breakdown) { value.FreshInputTokens = -1 }},
		{name: "negative cache read", mutate: func(value *Breakdown) { value.CacheReadInputTokens = -1 }},
		{name: "negative cache creation", mutate: func(value *Breakdown) { value.CacheCreationInputTokens = -1 }},
		{name: "negative unclassified input", mutate: func(value *Breakdown) { value.UnclassifiedInputTokens = -1 }},
		{name: "negative standard output", mutate: func(value *Breakdown) { value.StandardOutputTokens = -1 }},
		{name: "negative reasoning", mutate: func(value *Breakdown) { value.ReasoningTokens = -1 }},
		{name: "negative unclassified output", mutate: func(value *Breakdown) { value.UnclassifiedOutputTokens = -1 }},
		{name: "canonical total mismatch", mutate: func(value *Breakdown) { value.TotalTokens++ }},
		{name: "input segment mismatch", mutate: func(value *Breakdown) { value.FreshInputTokens++ }},
		{name: "output segment mismatch", mutate: func(value *Breakdown) { value.ReasoningTokens++ }},
		{name: "canonical overflow", mutate: func(value *Breakdown) {
			*value = Breakdown{TotalTokens: math.MaxInt64, InputTokens: math.MaxInt64, OutputTokens: 1, FreshInputTokens: math.MaxInt64, StandardOutputTokens: 1}
		}},
		{name: "input segment overflow", mutate: func(value *Breakdown) {
			*value = Breakdown{TotalTokens: math.MaxInt64, InputTokens: math.MaxInt64, FreshInputTokens: math.MaxInt64, CacheReadInputTokens: 1}
		}},
		{name: "output segment overflow", mutate: func(value *Breakdown) {
			*value = Breakdown{TotalTokens: math.MaxInt64, OutputTokens: math.MaxInt64, StandardOutputTokens: math.MaxInt64, ReasoningTokens: 1}
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value := valid
			test.mutate(&value)
			if err := validateBreakdown(value); !errors.Is(err, errInvalidBreakdown) {
				t.Fatalf("validateBreakdown() error = %v, want conservation failure", err)
			}
		})
	}
	if err := validateBreakdown(valid); err != nil {
		t.Fatalf("validateBreakdown(valid) error = %v", err)
	}
}

func TestServiceAnalyzeRejectsInvalidBreakdownAtEveryLevel(t *testing.T) {
	t.Parallel()

	invalid := simpleBreakdown(1, 1)
	invalid.TotalTokens++
	tests := []struct {
		name      string
		code      FailureCode
		configure func(*fakeSnapshot)
	}{
		{name: "summary", code: FailureCodeSummaryValidation, configure: func(snapshot *fakeSnapshot) {
			snapshot.summary.Breakdown = invalid
		}},
		{name: "bucket", code: FailureCodeBucketSetValidation, configure: func(snapshot *fakeSnapshot) {
			snapshot.buckets = []BucketRecord{{AlignedStart: fixedQuery().Window.Start, Breakdown: invalid}}
		}},
		{name: "provider", code: FailureCodeProviderRankValidation, configure: func(snapshot *fakeSnapshot) {
			snapshot.providers = []ProviderRankRecord{{ProviderID: "provider", Breakdown: invalid}}
		}},
		{name: "model", code: FailureCodeModelRankValidation, configure: func(snapshot *fakeSnapshot) {
			snapshot.models = []ModelRankRecord{{Model: "model", Breakdown: invalid}}
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			snapshot := &fakeSnapshot{}
			test.configure(snapshot)
			_, err := NewService(&fakeReader{snapshot: snapshot}).Analyze(context.Background(), fixedQuery())
			if !IsFailureAt(err, FailureStageResponseMap) || FailureCodeOf(err) != test.code {
				t.Fatalf("Analyze() error = %v, code %q; want response_map/%q", err, FailureCodeOf(err), test.code)
			}
			if snapshot.closed != 1 {
				t.Fatalf("snapshot close count = %d, want 1", snapshot.closed)
			}
		})
	}
}

func TestServiceAnalyzeClassifiesRejectedBucketKey(t *testing.T) {
	t.Parallel()

	query := fixedQuery()
	snapshot := &fakeSnapshot{buckets: []BucketRecord{{
		AlignedStart: query.Window.Start.Add(-time.Hour),
	}}}
	_, err := NewService(&fakeReader{snapshot: snapshot}).Analyze(context.Background(), query)
	if !IsFailureAt(err, FailureStageResponseMap) || FailureCodeOf(err) != FailureCodeBucketKeyRejected {
		t.Fatalf("Analyze() error = %v, code %q; want response_map/%q", err, FailureCodeOf(err), FailureCodeBucketKeyRejected)
	}
	if !errors.Is(err, errInvalidBucketSet) || !errors.Is(err, errBucketKeyRejected) {
		t.Fatalf("Analyze() error = %v, want preserved bucket-set and bucket-key causes", err)
	}
}

func TestMapBucketsRejectsMalformedRepositorySets(t *testing.T) {
	t.Parallel()

	query := fixedQuery()
	validRecord := BucketRecord{AlignedStart: query.Window.Start, Breakdown: simpleBreakdown(1, 1), TotalRequests: 1, ObservedRequests: 1, ComparableRequests: 1}
	tests := []struct {
		name    string
		records []BucketRecord
	}{
		{name: "outside range", records: []BucketRecord{{AlignedStart: query.Window.Start.Add(-time.Hour)}}},
		{name: "misaligned", records: []BucketRecord{{AlignedStart: query.Window.Start.Add(time.Minute)}}},
		{name: "duplicate", records: []BucketRecord{validRecord, validRecord}},
		{name: "negative requests", records: []BucketRecord{{AlignedStart: query.Window.Start, ComparableRequests: -1}}},
		{name: "observed exceeds total", records: []BucketRecord{{AlignedStart: query.Window.Start, TotalRequests: 1, ObservedRequests: 2}}},
		{name: "comparable exceeds observed", records: []BucketRecord{{AlignedStart: query.Window.Start, TotalRequests: 1, ComparableRequests: 1}}},
		{name: "invalid breakdown", records: []BucketRecord{{AlignedStart: query.Window.Start, Breakdown: Breakdown{TotalTokens: 1}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := mapBuckets(query.Window, test.records); !errors.Is(err, errInvalidBucketSet) {
				t.Fatalf("mapBuckets() error = %v, want invalid bucket set", err)
			}
		})
	}
}

func TestMapRanksEnforcesStableTopNContracts(t *testing.T) {
	t.Parallel()

	providerBase := []ProviderRankRecord{
		{ProviderID: "a", Breakdown: simpleBreakdown(6, 4), ComparableRequests: 2},
		{ProviderID: "b", Breakdown: simpleBreakdown(6, 4), ComparableRequests: 2},
		{ProviderID: "c", Breakdown: simpleBreakdown(5, 4), ComparableRequests: 3},
	}
	modelBase := []ModelRankRecord{
		{Model: "", Breakdown: simpleBreakdown(6, 4), ComparableRequests: 2},
		{Model: "b", Breakdown: simpleBreakdown(6, 4), ComparableRequests: 2},
		{Model: "c", Breakdown: simpleBreakdown(5, 4), ComparableRequests: 3},
	}

	providerTests := []struct {
		name    string
		records []ProviderRankRecord
	}{
		{name: "total order", records: []ProviderRankRecord{providerBase[2], providerBase[0]}},
		{name: "request order", records: []ProviderRankRecord{{ProviderID: "a", Breakdown: simpleBreakdown(6, 4), ComparableRequests: 1}, {ProviderID: "b", Breakdown: simpleBreakdown(6, 4), ComparableRequests: 2}}},
		{name: "identifier order", records: []ProviderRankRecord{providerBase[1], providerBase[0]}},
		{name: "duplicate", records: []ProviderRankRecord{providerBase[0], {ProviderID: "a", Breakdown: simpleBreakdown(5, 4), ComparableRequests: 1}}},
		{name: "negative requests", records: []ProviderRankRecord{{ProviderID: "a", ComparableRequests: -1}}},
		{name: "invalid breakdown", records: []ProviderRankRecord{{ProviderID: "a", Breakdown: Breakdown{TotalTokens: 1}}}},
		{name: "over limit", records: repeatProviderRecords(TopRankLimit + 1)},
	}
	for _, test := range providerTests {
		t.Run("provider "+test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := mapProviderRanks(test.records, 100); !errors.Is(err, errInvalidProviderRanks) {
				t.Fatalf("mapProviderRanks() error = %v, want invalid ranking", err)
			}
		})
	}

	modelTests := []struct {
		name    string
		records []ModelRankRecord
	}{
		{name: "total order", records: []ModelRankRecord{modelBase[2], modelBase[0]}},
		{name: "request order", records: []ModelRankRecord{{Model: "a", Breakdown: simpleBreakdown(6, 4), ComparableRequests: 1}, {Model: "b", Breakdown: simpleBreakdown(6, 4), ComparableRequests: 2}}},
		{name: "identifier order", records: []ModelRankRecord{modelBase[1], modelBase[0]}},
		{name: "duplicate", records: []ModelRankRecord{modelBase[0], {Model: "", Breakdown: simpleBreakdown(5, 4), ComparableRequests: 1}}},
		{name: "negative requests", records: []ModelRankRecord{{Model: "a", ComparableRequests: -1}}},
		{name: "invalid breakdown", records: []ModelRankRecord{{Model: "a", Breakdown: Breakdown{TotalTokens: 1}}}},
		{name: "over limit", records: repeatModelRecords(TopRankLimit + 1)},
	}
	for _, test := range modelTests {
		t.Run("model "+test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := mapModelRanks(test.records, 100); !errors.Is(err, errInvalidModelRanks) {
				t.Fatalf("mapModelRanks() error = %v, want invalid ranking", err)
			}
		})
	}

	providers, err := mapProviderRanks(providerBase, 100)
	if err != nil || len(providers) != len(providerBase) || providers[0].ProviderID != "a" {
		t.Fatalf("mapProviderRanks(valid) = %+v, %v", providers, err)
	}
	models, err := mapModelRanks(modelBase, 100)
	if err != nil || len(models) != len(modelBase) || models[0].Model != "" {
		t.Fatalf("mapModelRanks(valid) = %+v, %v", models, err)
	}
	if providers[0].Share != 0.1 || models[0].Share != 0.1 {
		t.Fatalf("rank shares = %v, %v; want 0.1", providers[0].Share, models[0].Share)
	}
}

func TestRepositoryFailureContract(t *testing.T) {
	t.Parallel()

	cause := errors.New("sensitive storage detail")
	err := NewFailure(FailureStageSummary, FailureCodeRepository, cause)
	if err.Error() != failureMessage || err.Unwrap() != cause {
		t.Fatalf("failure contract = error %q unwrap %v", err.Error(), err.Unwrap())
	}
	if !IsFailureAt(err, FailureStageSummary) || IsFailureAt(err, FailureStageTimeSeries) || IsFailureAt(cause, FailureStageSummary) {
		t.Fatal("failure stage detection did not preserve the stable stage")
	}
	if FailureCodeOf(err) != FailureCodeRepository {
		t.Fatalf("FailureCodeOf() = %q, want %q", FailureCodeOf(err), FailureCodeRepository)
	}
	if _, ok := ResolvedFailureWindow(err); ok {
		t.Fatal("plain failure unexpectedly exposed window context")
	}

	window := fixedQuery().Window
	windowed := NewFailureForWindow(FailureStageTimeSeries, FailureCodeRepository, cause, window)
	got, ok := ResolvedFailureWindow(windowed)
	if !ok || !got.Start.Equal(window.Start) || !got.End.Equal(window.End) {
		t.Fatalf("resolved failure window = %+v/%v, want %+v", got, ok, window)
	}
	window.StartResolution = analyticswindow.StartUnresolved
	if _, ok := ResolvedFailureWindow(NewFailureForWindow(FailureStageSummary, FailureCodeRepository, cause, window)); ok {
		t.Fatal("unresolved failure exposed a timestamp")
	}
	if code := FailureCodeOf(errors.New("outside domain")); code != FailureCodeUnexpectedAnalyzerError {
		t.Fatalf("naked error code = %q, want %q", code, FailureCodeUnexpectedAnalyzerError)
	}
	if code := FailureCodeOf(NewFailure(FailureStageResponseMap, FailureCode("unregistered"), cause)); code != FailureCodeUnexpectedAnalyzerError {
		t.Fatalf("unregistered code = %q, want %q", code, FailureCodeUnexpectedAnalyzerError)
	}
}

type fakeReader struct {
	snapshot Snapshot
	err      error
	opens    int
	ctx      context.Context
}

func (reader *fakeReader) OpenSnapshot(ctx context.Context) (Snapshot, error) {
	reader.opens++
	reader.ctx = ctx
	return reader.snapshot, reader.err
}

type fakeSnapshot struct {
	summary   SummaryRecord
	buckets   []BucketRecord
	providers []ProviderRankRecord
	models    []ModelRankRecord

	summaryErr  error
	bucketErr   error
	providerErr error
	modelErr    error
	closeErr    error

	summaryQueries  []Query
	bucketQueries   []Query
	providerQueries []Query
	modelQueries    []Query
	providerLimits  []int
	modelLimits     []int
	calls           []string
	closed          int
}

func (snapshot *fakeSnapshot) ReadSummary(_ context.Context, query Query) (SummaryRecord, error) {
	snapshot.calls = append(snapshot.calls, "summary")
	snapshot.summaryQueries = append(snapshot.summaryQueries, query)
	return snapshot.summary, snapshot.summaryErr
}

func (snapshot *fakeSnapshot) ReadBuckets(_ context.Context, query Query) ([]BucketRecord, error) {
	snapshot.calls = append(snapshot.calls, "timeseries")
	snapshot.bucketQueries = append(snapshot.bucketQueries, query)
	return snapshot.buckets, snapshot.bucketErr
}

func (snapshot *fakeSnapshot) ReadProviderRanks(_ context.Context, query Query, limit int) ([]ProviderRankRecord, error) {
	snapshot.calls = append(snapshot.calls, "provider_rank")
	snapshot.providerQueries = append(snapshot.providerQueries, query)
	snapshot.providerLimits = append(snapshot.providerLimits, limit)
	return snapshot.providers, snapshot.providerErr
}

func (snapshot *fakeSnapshot) ReadModelRanks(_ context.Context, query Query, limit int) ([]ModelRankRecord, error) {
	snapshot.calls = append(snapshot.calls, "model_rank")
	snapshot.modelQueries = append(snapshot.modelQueries, query)
	snapshot.modelLimits = append(snapshot.modelLimits, limit)
	return snapshot.models, snapshot.modelErr
}

func (snapshot *fakeSnapshot) Close() error {
	snapshot.calls = append(snapshot.calls, "close")
	snapshot.closed++
	return snapshot.closeErr
}

func fixedQuery() Query {
	start := time.Date(2026, time.August, 20, 0, 0, 0, 0, time.UTC)
	return Query{Window: analyticswindow.Window{
		Period:          analyticswindow.Period24Hours,
		GranularityName: analyticswindow.Granularity1Hour,
		Granularity:     time.Hour,
		Start:           start,
		End:             start.Add(time.Hour),
	}}
}

func simpleBreakdown(input, output int64) Breakdown {
	return Breakdown{
		TotalTokens:          input + output,
		InputTokens:          input,
		OutputTokens:         output,
		FreshInputTokens:     input,
		StandardOutputTokens: output,
	}
}

func repeatProviderRecords(count int) []ProviderRankRecord {
	records := make([]ProviderRankRecord, count)
	for index := range records {
		records[index].ProviderID = string(rune('a' + index))
	}
	return records
}

func repeatModelRecords(count int) []ModelRankRecord {
	records := make([]ModelRankRecord, count)
	for index := range records {
		records[index].Model = string(rune('a' + index))
	}
	return records
}

func stringPointer(value string) *string {
	return &value
}
