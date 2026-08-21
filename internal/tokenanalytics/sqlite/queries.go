package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/doraemonkeys/switch-a/internal/tokenanalytics"
)

const aggregateBreakdownColumns = `
	CAST(SUM(canonical_total) AS TEXT) AS total_tokens,
	CAST(SUM(canonical_input) AS TEXT) AS input_tokens,
	CAST(SUM(canonical_output) AS TEXT) AS output_tokens,
	CAST(SUM(fresh_input) AS TEXT) AS fresh_input_tokens,
	CAST(SUM(cache_read_input) AS TEXT) AS cache_read_input_tokens,
	CAST(SUM(cache_creation_input) AS TEXT) AS cache_creation_input_tokens,
	CAST(SUM(unclassified_input) AS TEXT) AS unclassified_input_tokens,
	CAST(SUM(standard_output) AS TEXT) AS standard_output_tokens,
	CAST(SUM(reasoning_output) AS TEXT) AS reasoning_tokens,
	CAST(SUM(unclassified_output) AS TEXT) AS unclassified_output_tokens`

const conditionalAggregateBreakdownColumns = `
	CAST(COALESCE(SUM(CASE WHEN class = 'comparable' THEN canonical_total ELSE 0 END), 0) AS TEXT) AS total_tokens,
	CAST(COALESCE(SUM(CASE WHEN class = 'comparable' THEN canonical_input ELSE 0 END), 0) AS TEXT) AS input_tokens,
	CAST(COALESCE(SUM(CASE WHEN class = 'comparable' THEN canonical_output ELSE 0 END), 0) AS TEXT) AS output_tokens,
	CAST(COALESCE(SUM(CASE WHEN class = 'comparable' THEN fresh_input ELSE 0 END), 0) AS TEXT) AS fresh_input_tokens,
	CAST(COALESCE(SUM(CASE WHEN class = 'comparable' THEN cache_read_input ELSE 0 END), 0) AS TEXT) AS cache_read_input_tokens,
	CAST(COALESCE(SUM(CASE WHEN class = 'comparable' THEN cache_creation_input ELSE 0 END), 0) AS TEXT) AS cache_creation_input_tokens,
	CAST(COALESCE(SUM(CASE WHEN class = 'comparable' THEN unclassified_input ELSE 0 END), 0) AS TEXT) AS unclassified_input_tokens,
	CAST(COALESCE(SUM(CASE WHEN class = 'comparable' THEN standard_output ELSE 0 END), 0) AS TEXT) AS standard_output_tokens,
	CAST(COALESCE(SUM(CASE WHEN class = 'comparable' THEN reasoning_output ELSE 0 END), 0) AS TEXT) AS reasoning_tokens,
	CAST(COALESCE(SUM(CASE WHEN class = 'comparable' THEN unclassified_output ELSE 0 END), 0) AS TEXT) AS unclassified_output_tokens`

const summaryQuerySuffix = `
SELECT
	CAST(COALESCE(SUM(CASE WHEN class = 'comparable' THEN canonical_total ELSE 0 END), 0) AS TEXT),
	CAST(COALESCE(SUM(CASE WHEN class = 'comparable' THEN canonical_input ELSE 0 END), 0) AS TEXT),
	CAST(COALESCE(SUM(CASE WHEN class = 'comparable' THEN canonical_output ELSE 0 END), 0) AS TEXT),
	CAST(COALESCE(SUM(CASE WHEN class = 'comparable' THEN fresh_input ELSE 0 END), 0) AS TEXT),
	CAST(COALESCE(SUM(CASE WHEN class = 'comparable' THEN cache_read_input ELSE 0 END), 0) AS TEXT),
	CAST(COALESCE(SUM(CASE WHEN class = 'comparable' THEN cache_creation_input ELSE 0 END), 0) AS TEXT),
	CAST(COALESCE(SUM(CASE WHEN class = 'comparable' THEN unclassified_input ELSE 0 END), 0) AS TEXT),
	CAST(COALESCE(SUM(CASE WHEN class = 'comparable' THEN standard_output ELSE 0 END), 0) AS TEXT),
	CAST(COALESCE(SUM(CASE WHEN class = 'comparable' THEN reasoning_output ELSE 0 END), 0) AS TEXT),
	CAST(COALESCE(SUM(CASE WHEN class = 'comparable' THEN unclassified_output ELSE 0 END), 0) AS TEXT),
	COUNT(*),
	COALESCE(SUM(observed), 0),
	COALESCE(SUM(class = 'comparable'), 0),
	COALESCE(SUM(class = 'partial'), 0),
	COALESCE(SUM(class = 'invalid'), 0),
	COALESCE(SUM(class = 'unknown_semantics'), 0),
	MIN(created_at)
FROM classified`

const bucketQuerySuffix = `
,
bucket_config(granularity_seconds) AS (VALUES (?)),
bucketed AS (
	SELECT
		(
			(CAST(strftime('%s', created_at) AS INTEGER) / granularity_seconds)
			- CASE
				WHEN CAST(strftime('%s', created_at) AS INTEGER) < 0
					AND CAST(strftime('%s', created_at) AS INTEGER) % granularity_seconds != 0
				THEN 1
				ELSE 0
			END
		) * granularity_seconds AS bucket_start_unix,
		classified.*
	FROM classified
	CROSS JOIN bucket_config
)
SELECT
	bucket_start_unix,
	` + conditionalAggregateBreakdownColumns + `,
	COUNT(*) AS total_requests,
	COALESCE(SUM(observed), 0) AS observed_requests,
	COALESCE(SUM(class = 'comparable'), 0) AS comparable_requests
FROM bucketed
GROUP BY bucket_start_unix
ORDER BY bucket_start_unix ASC`

const providerRankQuerySuffix = `
,
provider_aggregates AS (
	SELECT
		provider_id,
		SUM(canonical_total) AS total_tokens,
		SUM(canonical_input) AS input_tokens,
		SUM(canonical_output) AS output_tokens,
		SUM(fresh_input) AS fresh_input_tokens,
		SUM(cache_read_input) AS cache_read_input_tokens,
		SUM(cache_creation_input) AS cache_creation_input_tokens,
		SUM(unclassified_input) AS unclassified_input_tokens,
		SUM(standard_output) AS standard_output_tokens,
		SUM(reasoning_output) AS reasoning_tokens,
		SUM(unclassified_output) AS unclassified_output_tokens,
		COUNT(*) AS comparable_requests
	FROM classified
	WHERE class = 'comparable'
	GROUP BY provider_id
	ORDER BY total_tokens DESC, comparable_requests DESC, provider_id ASC
	LIMIT ?
)
SELECT
	aggregates.provider_id,
	COALESCE(NULLIF(providers.name, ''), aggregates.provider_id) AS provider_label,
	CAST(aggregates.total_tokens AS TEXT),
	CAST(aggregates.input_tokens AS TEXT),
	CAST(aggregates.output_tokens AS TEXT),
	CAST(aggregates.fresh_input_tokens AS TEXT),
	CAST(aggregates.cache_read_input_tokens AS TEXT),
	CAST(aggregates.cache_creation_input_tokens AS TEXT),
	CAST(aggregates.unclassified_input_tokens AS TEXT),
	CAST(aggregates.standard_output_tokens AS TEXT),
	CAST(aggregates.reasoning_tokens AS TEXT),
	CAST(aggregates.unclassified_output_tokens AS TEXT),
	aggregates.comparable_requests
FROM provider_aggregates AS aggregates
LEFT JOIN providers ON providers.id = aggregates.provider_id
ORDER BY aggregates.total_tokens DESC, aggregates.comparable_requests DESC, aggregates.provider_id ASC`

const modelRankQuerySuffix = `
SELECT
	model,
	` + aggregateBreakdownColumns + `,
	COUNT(*) AS comparable_requests
FROM classified
WHERE class = 'comparable'
GROUP BY model
ORDER BY SUM(canonical_total) DESC, comparable_requests DESC, model ASC
LIMIT ?`

func (s *Snapshot) ReadSummary(ctx context.Context, query tokenanalytics.Query) (tokenanalytics.SummaryRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return tokenanalytics.SummaryRecord{}, errSnapshotClosed
	}

	projection, err := buildProjection(query)
	if err != nil {
		return tokenanalytics.SummaryRecord{}, err
	}

	var values breakdownText
	var record tokenanalytics.SummaryRecord
	var earliest nullableTime
	destinations := values.destinations()
	destinations = append(destinations,
		&record.TotalRequests,
		&record.ObservedRequests,
		&record.ComparableRequests,
		&record.PartialRequests,
		&record.InvalidRequests,
		&record.UnknownSemanticsRequests,
		&earliest,
	)
	if err := s.tx.QueryRowContext(ctx, projection.sql+summaryQuerySuffix, projection.args...).Scan(destinations...); err != nil {
		return tokenanalytics.SummaryRecord{}, fmt.Errorf("read token analytics summary: %w", err)
	}
	record.Breakdown, err = values.breakdown()
	if err != nil {
		return tokenanalytics.SummaryRecord{}, fmt.Errorf("read token analytics summary: %w", err)
	}
	if earliest.Valid {
		value := earliest.Time.UTC()
		record.EarliestMatchingTime = &value
	}
	return record, nil
}

func (s *Snapshot) ReadBuckets(ctx context.Context, query tokenanalytics.Query) ([]tokenanalytics.BucketRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errSnapshotClosed
	}
	if !query.Window.HasResolvedStart() || query.Window.Granularity <= 0 || query.Window.Granularity%time.Second != 0 {
		return nil, errInvalidWindow
	}

	projection, err := buildProjection(query)
	if err != nil {
		return nil, err
	}
	granularitySeconds := int64(query.Window.Granularity / time.Second)
	args := appendProjectionArgs(projection.args, granularitySeconds)
	rows, err := s.tx.QueryContext(ctx, projection.sql+bucketQuerySuffix, args...)
	if err != nil {
		return nil, fmt.Errorf("read token analytics buckets: %w", err)
	}
	defer rows.Close()

	records := make([]tokenanalytics.BucketRecord, 0)
	for rows.Next() {
		var alignedUnix int64
		var values breakdownText
		var record tokenanalytics.BucketRecord
		breakdownDestinations := values.destinations()
		destinations := make([]any, 0, 1+len(breakdownDestinations)+3)
		destinations = append(destinations, &alignedUnix)
		destinations = append(destinations, breakdownDestinations...)
		destinations = append(destinations,
			&record.TotalRequests,
			&record.ObservedRequests,
			&record.ComparableRequests,
		)
		if err := rows.Scan(destinations...); err != nil {
			return nil, fmt.Errorf("scan token analytics bucket: %w", err)
		}
		record.Breakdown, err = values.breakdown()
		if err != nil {
			return nil, fmt.Errorf("scan token analytics bucket: %w", err)
		}
		record.AlignedStart = time.Unix(alignedUnix, 0).UTC()
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read token analytics buckets: %w", err)
	}
	return records, nil
}

func (s *Snapshot) ReadProviderRanks(ctx context.Context, query tokenanalytics.Query, limit int) ([]tokenanalytics.ProviderRankRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errSnapshotClosed
	}
	if err := validateRankLimit(limit); err != nil {
		return nil, err
	}

	projection, err := buildProjection(query)
	if err != nil {
		return nil, err
	}
	args := appendProjectionArgs(projection.args, limit)
	rows, err := s.tx.QueryContext(ctx, projection.sql+providerRankQuerySuffix, args...)
	if err != nil {
		return nil, fmt.Errorf("read token analytics provider ranks: %w", err)
	}
	defer rows.Close()

	records := make([]tokenanalytics.ProviderRankRecord, 0, limit)
	for rows.Next() {
		var values breakdownText
		var record tokenanalytics.ProviderRankRecord
		breakdownDestinations := values.destinations()
		destinations := make([]any, 0, 2+len(breakdownDestinations)+1)
		destinations = append(destinations, &record.ProviderID, &record.ProviderLabel)
		destinations = append(destinations, breakdownDestinations...)
		destinations = append(destinations, &record.ComparableRequests)
		if err := rows.Scan(destinations...); err != nil {
			return nil, fmt.Errorf("scan token analytics provider rank: %w", err)
		}
		record.Breakdown, err = values.breakdown()
		if err != nil {
			return nil, fmt.Errorf("scan token analytics provider rank: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read token analytics provider ranks: %w", err)
	}
	return records, nil
}

func (s *Snapshot) ReadModelRanks(ctx context.Context, query tokenanalytics.Query, limit int) ([]tokenanalytics.ModelRankRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errSnapshotClosed
	}
	if err := validateRankLimit(limit); err != nil {
		return nil, err
	}

	projection, err := buildProjection(query)
	if err != nil {
		return nil, err
	}
	args := appendProjectionArgs(projection.args, limit)
	rows, err := s.tx.QueryContext(ctx, projection.sql+modelRankQuerySuffix, args...)
	if err != nil {
		return nil, fmt.Errorf("read token analytics model ranks: %w", err)
	}
	defer rows.Close()

	records := make([]tokenanalytics.ModelRankRecord, 0, limit)
	for rows.Next() {
		var values breakdownText
		var record tokenanalytics.ModelRankRecord
		breakdownDestinations := values.destinations()
		destinations := make([]any, 0, 1+len(breakdownDestinations)+1)
		destinations = append(destinations, &record.Model)
		destinations = append(destinations, breakdownDestinations...)
		destinations = append(destinations, &record.ComparableRequests)
		if err := rows.Scan(destinations...); err != nil {
			return nil, fmt.Errorf("scan token analytics model rank: %w", err)
		}
		record.Breakdown, err = values.breakdown()
		if err != nil {
			return nil, fmt.Errorf("scan token analytics model rank: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read token analytics model ranks: %w", err)
	}
	return records, nil
}

func validateRankLimit(limit int) error {
	if limit <= 0 || limit > tokenanalytics.TopRankLimit {
		return errInvalidRankLimit
	}
	return nil
}

func appendProjectionArgs(base []any, values ...any) []any {
	args := make([]any, 0, len(base)+len(values))
	args = append(args, base...)
	return append(args, values...)
}

type breakdownText struct {
	total              string
	input              string
	output             string
	fresh              string
	cacheRead          string
	cacheCreation      string
	unclassifiedInput  string
	standardOutput     string
	reasoning          string
	unclassifiedOutput string
}

func (v *breakdownText) destinations() []any {
	return []any{
		&v.total,
		&v.input,
		&v.output,
		&v.fresh,
		&v.cacheRead,
		&v.cacheCreation,
		&v.unclassifiedInput,
		&v.standardOutput,
		&v.reasoning,
		&v.unclassifiedOutput,
	}
}

func (v breakdownText) breakdown() (tokenanalytics.Breakdown, error) {
	values := []string{
		v.total,
		v.input,
		v.output,
		v.fresh,
		v.cacheRead,
		v.cacheCreation,
		v.unclassifiedInput,
		v.standardOutput,
		v.reasoning,
		v.unclassifiedOutput,
	}
	parsed := make([]int64, len(values))
	for index, value := range values {
		number, err := parseExactInt64(value)
		if err != nil {
			return tokenanalytics.Breakdown{}, err
		}
		parsed[index] = number
	}
	return tokenanalytics.Breakdown{
		TotalTokens:              parsed[0],
		InputTokens:              parsed[1],
		OutputTokens:             parsed[2],
		FreshInputTokens:         parsed[3],
		CacheReadInputTokens:     parsed[4],
		CacheCreationInputTokens: parsed[5],
		UnclassifiedInputTokens:  parsed[6],
		StandardOutputTokens:     parsed[7],
		ReasoningTokens:          parsed[8],
		UnclassifiedOutputTokens: parsed[9],
	}, nil
}

func parseExactInt64(value string) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || strconv.FormatInt(parsed, 10) != value {
		return 0, fmt.Errorf("non-integer token aggregate")
	}
	return parsed, nil
}

type nullableTime struct {
	Time  time.Time
	Valid bool
}

func (value *nullableTime) Scan(source any) error {
	if source == nil {
		value.Valid = false
		value.Time = time.Time{}
		return nil
	}
	switch typed := source.(type) {
	case time.Time:
		value.Time = typed
		value.Valid = true
		return nil
	case string:
		return value.parse(typed)
	case []byte:
		return value.parse(string(typed))
	default:
		return fmt.Errorf("unsupported SQLite time type %T", source)
	}
}

func (value *nullableTime) parse(raw string) error {
	formats := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05",
	}
	for _, format := range formats {
		parsed, err := time.Parse(format, raw)
		if err == nil {
			value.Time = parsed
			value.Valid = true
			return nil
		}
	}
	return fmt.Errorf("unsupported SQLite time value")
}

var _ sql.Scanner = (*nullableTime)(nil)

func combineCloseErrors(errorsToJoin ...error) error {
	nonNil := make([]error, 0, len(errorsToJoin))
	for _, err := range errorsToJoin {
		if err != nil && !errors.Is(err, sql.ErrTxDone) {
			nonNil = append(nonNil, err)
		}
	}
	return errors.Join(nonNil...)
}
