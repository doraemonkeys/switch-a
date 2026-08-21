package tokenanalytics

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/doraemonkeys/switch-a/internal/analyticswindow"
)

var (
	errNilSnapshot             = errors.New("token analytics snapshot is nil")
	errSnapshotClose           = errors.New("token analytics snapshot close failed")
	errInvalidQualityPartition = errors.New("token analytics quality partition is invalid")
	errInvalidBreakdown        = errors.New("token analytics breakdown does not conserve tokens")
	errInvalidBucketSet        = errors.New("token analytics bucket set is invalid")
	errBucketKeyRejected       = errors.New("token analytics bucket key is outside the response window")
	errInvalidProviderRanks    = errors.New("token analytics provider ranking is invalid")
	errInvalidModelRanks       = errors.New("token analytics model ranking is invalid")
)

// Service coordinates one report without depending on the concrete storage
// implementation or leaking transport representation into the domain.
type Service struct {
	reader SnapshotReader
}

// NewService constructs the token analytics orchestration boundary.
func NewService(reader SnapshotReader) *Service {
	if reader == nil {
		panic("tokenanalytics: nil snapshot reader")
	}
	return &Service{reader: reader}
}

// Analyze reads and validates one complete report from a single snapshot.
func (s *Service) Analyze(ctx context.Context, query Query) (report Report, err error) {
	snapshot, openErr := s.reader.OpenSnapshot(ctx)
	if openErr != nil {
		return Report{}, NewFailure(FailureStageSnapshotOpen, FailureCodeRepository, openErr)
	}
	if snapshot == nil {
		return Report{}, NewFailure(FailureStageSnapshotOpen, FailureCodeSnapshotUnavailable, errNilSnapshot)
	}
	defer func() {
		closeErr := snapshot.Close()
		if closeErr == nil || err != nil {
			return
		}

		// A report is not successful until its read snapshot has been released.
		report = Report{}
		err = NewFailureForWindow(FailureStageResponseMap, FailureCodeSnapshotClose, errors.Join(errSnapshotClose, closeErr), query.Window)
	}()

	summary, readErr := snapshot.ReadSummary(ctx, query)
	if readErr != nil {
		return Report{}, NewFailure(FailureStageSummary, FailureCodeRepository, readErr)
	}
	if validateErr := validateSummary(summary, !query.Window.HasResolvedStart()); validateErr != nil {
		return Report{}, NewFailure(FailureStageResponseMap, FailureCodeSummaryValidation, validateErr)
	}

	resolvedWindow, resolveErr := analyticswindow.ResolveAll(query.Window, summary.EarliestMatchingTime)
	if resolveErr != nil {
		return Report{}, NewFailureForWindow(FailureStageResponseMap, FailureCodeWindowResolution, resolveErr, resolvedWindow)
	}
	query.Window = resolvedWindow

	bucketRecords, readErr := snapshot.ReadBuckets(ctx, query)
	if readErr != nil {
		return Report{}, NewFailureForWindow(FailureStageTimeSeries, FailureCodeRepository, readErr, query.Window)
	}
	providerRecords, readErr := snapshot.ReadProviderRanks(ctx, query, TopRankLimit)
	if readErr != nil {
		return Report{}, NewFailureForWindow(FailureStageProviderRank, FailureCodeRepository, readErr, query.Window)
	}
	modelRecords, readErr := snapshot.ReadModelRanks(ctx, query, TopRankLimit)
	if readErr != nil {
		return Report{}, NewFailureForWindow(FailureStageModelRank, FailureCodeRepository, readErr, query.Window)
	}

	timeSeries, mapErr := mapBuckets(resolvedWindow, bucketRecords)
	if mapErr != nil {
		return Report{}, NewFailureForWindow(FailureStageResponseMap, bucketFailureCode(mapErr), mapErr, query.Window)
	}
	byProvider, mapErr := mapProviderRanks(providerRecords, summary.TotalTokens)
	if mapErr != nil {
		return Report{}, NewFailureForWindow(FailureStageResponseMap, FailureCodeProviderRankValidation, mapErr, query.Window)
	}
	byModel, mapErr := mapModelRanks(modelRecords, summary.TotalTokens)
	if mapErr != nil {
		return Report{}, NewFailureForWindow(FailureStageResponseMap, FailureCodeModelRankValidation, mapErr, query.Window)
	}

	return Report{
		Summary:    aggregate(summary.Breakdown),
		TimeSeries: timeSeries,
		ByProvider: byProvider,
		ByModel:    byModel,
		TimeRange: TimeRange{
			Start: resolvedWindow.Start,
			End:   resolvedWindow.End,
		},
		Coverage: Coverage{
			TotalRequests:        summary.TotalRequests,
			ObservedRequests:     summary.ObservedRequests,
			ComparableRequests:   summary.ComparableRequests,
			WithoutUsageRequests: summary.TotalRequests - summary.ObservedRequests,
			Rate:                 ratio(summary.ComparableRequests, summary.TotalRequests),
		},
		DataQuality: DataQuality{
			QualityRate:              ratio(summary.ComparableRequests, summary.ObservedRequests),
			PartialRequests:          summary.PartialRequests,
			InvalidRequests:          summary.InvalidRequests,
			UnknownSemanticsRequests: summary.UnknownSemanticsRequests,
		},
	}, nil
}

func validateSummary(summary SummaryRecord, resolvingAll bool) error {
	if err := validateBreakdown(summary.Breakdown); err != nil {
		return err
	}

	counts := []int64{
		summary.TotalRequests,
		summary.ObservedRequests,
		summary.ComparableRequests,
		summary.PartialRequests,
		summary.InvalidRequests,
		summary.UnknownSemanticsRequests,
	}
	for _, count := range counts {
		if count < 0 {
			return errInvalidQualityPartition
		}
	}
	if summary.ObservedRequests > summary.TotalRequests {
		return errInvalidQualityPartition
	}

	partition, ok := sumNonNegative(
		summary.ComparableRequests,
		summary.PartialRequests,
		summary.InvalidRequests,
		summary.UnknownSemanticsRequests,
	)
	if !ok || partition != summary.ObservedRequests {
		return errInvalidQualityPartition
	}

	// An all-period start must be derived from the same filtered population as
	// the counts; otherwise later reads would describe a different time range.
	if resolvingAll && (summary.TotalRequests == 0) != (summary.EarliestMatchingTime == nil) {
		return errInvalidQualityPartition
	}
	return nil
}

func validateBreakdown(breakdown Breakdown) error {
	values := []int64{
		breakdown.TotalTokens,
		breakdown.InputTokens,
		breakdown.OutputTokens,
		breakdown.FreshInputTokens,
		breakdown.CacheReadInputTokens,
		breakdown.CacheCreationInputTokens,
		breakdown.UnclassifiedInputTokens,
		breakdown.StandardOutputTokens,
		breakdown.ReasoningTokens,
		breakdown.UnclassifiedOutputTokens,
	}
	for _, value := range values {
		if value < 0 {
			return errInvalidBreakdown
		}
	}

	canonicalTotal, ok := sumNonNegative(breakdown.InputTokens, breakdown.OutputTokens)
	if !ok || canonicalTotal != breakdown.TotalTokens {
		return errInvalidBreakdown
	}
	inputTotal, ok := sumNonNegative(
		breakdown.FreshInputTokens,
		breakdown.CacheReadInputTokens,
		breakdown.CacheCreationInputTokens,
		breakdown.UnclassifiedInputTokens,
	)
	if !ok || inputTotal != breakdown.InputTokens {
		return errInvalidBreakdown
	}
	outputTotal, ok := sumNonNegative(
		breakdown.StandardOutputTokens,
		breakdown.ReasoningTokens,
		breakdown.UnclassifiedOutputTokens,
	)
	if !ok || outputTotal != breakdown.OutputTokens {
		return errInvalidBreakdown
	}
	segmentTotal, ok := sumNonNegative(
		breakdown.FreshInputTokens,
		breakdown.CacheReadInputTokens,
		breakdown.CacheCreationInputTokens,
		breakdown.UnclassifiedInputTokens,
		breakdown.StandardOutputTokens,
		breakdown.ReasoningTokens,
		breakdown.UnclassifiedOutputTokens,
	)
	if !ok || segmentTotal != breakdown.TotalTokens {
		return errInvalidBreakdown
	}
	return nil
}

func mapBuckets(window analyticswindow.Window, records []BucketRecord) ([]Bucket, error) {
	bounds, err := window.Buckets()
	if err != nil {
		return nil, err
	}

	result := make([]Bucket, len(bounds))
	positions := make(map[time.Time]int, len(bounds))
	for index, bound := range bounds {
		key := normalizedTime(bound.AlignedStart)
		positions[key] = index
		result[index] = Bucket{Start: bound.Start, End: bound.End}
	}

	seen := make(map[time.Time]struct{}, len(records))
	for _, record := range records {
		if !validBucketCounts(record) || validateBreakdown(record.Breakdown) != nil {
			return nil, errInvalidBucketSet
		}
		key := normalizedTime(record.AlignedStart)
		index, exists := positions[key]
		if !exists {
			return nil, errors.Join(errInvalidBucketSet, errBucketKeyRejected)
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, errInvalidBucketSet
		}
		seen[key] = struct{}{}
		result[index].Breakdown = record.Breakdown
		result[index].TotalRequests = record.TotalRequests
		result[index].ObservedRequests = record.ObservedRequests
		result[index].ComparableRequests = record.ComparableRequests
	}
	return result, nil
}

func bucketFailureCode(err error) FailureCode {
	if errors.Is(err, errBucketKeyRejected) {
		return FailureCodeBucketKeyRejected
	}
	return FailureCodeBucketSetValidation
}

func mapProviderRanks(records []ProviderRankRecord, totalTokens int64) ([]ProviderRank, error) {
	if len(records) > TopRankLimit {
		return nil, errInvalidProviderRanks
	}

	result := make([]ProviderRank, len(records))
	seen := make(map[string]struct{}, len(records))
	var rankedTokens int64
	for index, record := range records {
		var validSum bool
		rankedTokens, validSum = sumNonNegative(rankedTokens, record.TotalTokens)
		if record.ComparableRequests < 0 || !validSum || rankedTokens > totalTokens || validateBreakdown(record.Breakdown) != nil {
			return nil, errInvalidProviderRanks
		}
		if _, duplicate := seen[record.ProviderID]; duplicate {
			return nil, errInvalidProviderRanks
		}
		seen[record.ProviderID] = struct{}{}
		if index > 0 && !providerRecordBefore(records[index-1], record) {
			return nil, errInvalidProviderRanks
		}
		result[index] = ProviderRank{
			ProviderID:         record.ProviderID,
			ProviderLabel:      record.ProviderLabel,
			Breakdown:          record.Breakdown,
			ComparableRequests: record.ComparableRequests,
			Share:              ratio(record.TotalTokens, totalTokens),
		}
	}
	return result, nil
}

func mapModelRanks(records []ModelRankRecord, totalTokens int64) ([]ModelRank, error) {
	if len(records) > TopRankLimit {
		return nil, errInvalidModelRanks
	}

	result := make([]ModelRank, len(records))
	seen := make(map[string]struct{}, len(records))
	var rankedTokens int64
	for index, record := range records {
		var validSum bool
		rankedTokens, validSum = sumNonNegative(rankedTokens, record.TotalTokens)
		if record.ComparableRequests < 0 || !validSum || rankedTokens > totalTokens || validateBreakdown(record.Breakdown) != nil {
			return nil, errInvalidModelRanks
		}
		if _, duplicate := seen[record.Model]; duplicate {
			return nil, errInvalidModelRanks
		}
		seen[record.Model] = struct{}{}
		if index > 0 && !modelRecordBefore(records[index-1], record) {
			return nil, errInvalidModelRanks
		}
		result[index] = ModelRank{
			Model:              record.Model,
			Breakdown:          record.Breakdown,
			ComparableRequests: record.ComparableRequests,
			Share:              ratio(record.TotalTokens, totalTokens),
		}
	}
	return result, nil
}

func validBucketCounts(record BucketRecord) bool {
	return record.TotalRequests >= 0 &&
		record.ObservedRequests >= 0 &&
		record.ComparableRequests >= 0 &&
		record.ObservedRequests <= record.TotalRequests &&
		record.ComparableRequests <= record.ObservedRequests
}

func providerRecordBefore(left, right ProviderRankRecord) bool {
	if left.TotalTokens != right.TotalTokens {
		return left.TotalTokens > right.TotalTokens
	}
	if left.ComparableRequests != right.ComparableRequests {
		return left.ComparableRequests > right.ComparableRequests
	}
	return left.ProviderID < right.ProviderID
}

func modelRecordBefore(left, right ModelRankRecord) bool {
	if left.TotalTokens != right.TotalTokens {
		return left.TotalTokens > right.TotalTokens
	}
	if left.ComparableRequests != right.ComparableRequests {
		return left.ComparableRequests > right.ComparableRequests
	}
	return left.Model < right.Model
}

func aggregate(breakdown Breakdown) Aggregate {
	return Aggregate{
		Breakdown:      breakdown,
		CacheHitRate:   ratio(breakdown.CacheReadInputTokens, breakdown.InputTokens),
		ReasoningRatio: ratio(breakdown.ReasoningTokens, breakdown.OutputTokens),
	}
}

func ratio(numerator, denominator int64) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func sumNonNegative(values ...int64) (int64, bool) {
	var total int64
	for _, value := range values {
		if value < 0 || total > math.MaxInt64-value {
			return 0, false
		}
		total += value
	}
	return total, true
}

func normalizedTime(value time.Time) time.Time {
	return value.UTC().Round(0)
}
