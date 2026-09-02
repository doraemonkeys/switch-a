package sqlite

import (
	"context"
	"database/sql"
	"slices"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/analyticswindow"
	"github.com/doraemonkeys/switch-a/internal/tokenanalytics"
)

func TestMixedOffsetWindowKeepsEveryAnalyticsResultInsideExactBounds(t *testing.T) {
	database := newTestDatabase(t)
	start := time.Date(2026, time.August, 20, 13, 5, 3, 250000400, time.UTC)
	end := start.Add(24 * time.Hour)
	offsetEast := time.FixedZone("incident-offset", 8*60*60)

	rows := []struct {
		requestID  string
		providerID string
		apiType    string
		model      string
		instant    time.Time
		storedIn   *time.Location
	}{
		// Its wall clock sorts after the UTC lower bound even though its instant is
		// almost eight hours too early. This is the production escape row.
		{requestID: "escaped-before", providerID: "target", apiType: "codex", model: "wanted", instant: start.Add(-7*time.Hour - 55*time.Minute), storedIn: offsetEast},
		{requestID: "exact-start", providerID: "target", apiType: "codex", model: "wanted", instant: start, storedIn: offsetEast},
		{requestID: "inside-utc", providerID: "target", apiType: "codex", model: "wanted", instant: start.Add(time.Hour), storedIn: time.UTC},
		// The offset representation sorts beyond the UTC upper bound despite being
		// inside it, reproducing the inverse omission from the incident trace.
		{requestID: "inside-offset", providerID: "target", apiType: "codex", model: "wanted", instant: end.Add(-time.Hour), storedIn: offsetEast},
		{requestID: "last-nanosecond", providerID: "target", apiType: "codex", model: "wanted", instant: end.Add(-time.Nanosecond), storedIn: offsetEast},
		{requestID: "exact-end", providerID: "target", apiType: "codex", model: "wanted", instant: end, storedIn: time.UTC},
		{requestID: "wrong-provider", providerID: "other", apiType: "codex", model: "wanted", instant: start.Add(2 * time.Hour), storedIn: offsetEast},
		{requestID: "wrong-model", providerID: "target", apiType: "codex", model: "other", instant: start.Add(3 * time.Hour), storedIn: time.UTC},
		{requestID: "wrong-api", providerID: "target", apiType: "grok", model: "wanted", instant: start.Add(4 * time.Hour), storedIn: offsetEast},
	}
	for _, row := range rows {
		insertMixedOffsetLog(t, database.path, row.requestID, row.providerID, row.apiType, row.model, row.instant.In(row.storedIn))
	}

	providerID, modelName, apiType := "target", "wanted", "codex"
	query := testQuery(start, end, time.Hour)
	query.ProviderID = &providerID
	query.Model = &modelName
	query.APIType = &apiType
	legacySelection := readLexicallySelectedRequestIDs(t, database, query)
	t.Logf("legacy lexical selection: %v", legacySelection)
	if !containsString(legacySelection, "escaped-before") || containsString(legacySelection, "inside-offset") {
		t.Fatalf("legacy text predicate selection = %v, want escaped-before admitted and inside-offset omitted", legacySelection)
	}

	snapshot, err := database.repository.OpenSnapshot(context.Background())
	if err != nil {
		t.Fatalf("OpenSnapshot() error = %v", err)
	}
	summary, err := snapshot.ReadSummary(context.Background(), query)
	if err != nil {
		t.Fatalf("ReadSummary() error = %v", err)
	}
	buckets, err := snapshot.ReadBuckets(context.Background(), query)
	if err != nil {
		t.Fatalf("ReadBuckets() error = %v", err)
	}
	providers, err := snapshot.ReadProviderRanks(context.Background(), query, tokenanalytics.TopRankLimit)
	if err != nil {
		t.Fatalf("ReadProviderRanks() error = %v", err)
	}
	models, err := snapshot.ReadModelRanks(context.Background(), query, tokenanalytics.TopRankLimit)
	if err != nil {
		t.Fatalf("ReadModelRanks() error = %v", err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatalf("Snapshot.Close() error = %v", err)
	}

	if summary.TotalRequests != 4 || summary.TotalTokens != 8 {
		t.Fatalf("mixed-offset summary = %+v, want four in-window rows and eight tokens", summary)
	}
	t.Logf("instant-key selection: requests=%d buckets=%d", summary.TotalRequests, len(buckets))
	if len(providers) != 1 || providers[0].ProviderID != providerID || providers[0].ComparableRequests != 4 {
		t.Fatalf("mixed-offset provider ranks = %+v", providers)
	}
	if len(models) != 1 || models[0].Model != modelName || models[0].ComparableRequests != 4 {
		t.Fatalf("mixed-offset model ranks = %+v", models)
	}
	assertBucketRecordsInsideWindow(t, buckets, start, end, time.Hour)

	report, err := tokenanalytics.NewService(database.repository).Analyze(context.Background(), query)
	if err != nil {
		t.Fatalf("Analyze() mixed-offset window error = %v", err)
	}
	if report.Coverage.TotalRequests != 4 || report.Summary.TotalTokens != 8 {
		t.Fatalf("mixed-offset report = %+v", report)
	}
	for _, bucket := range report.TimeSeries {
		if bucket.Start.Before(start) || bucket.End.After(end) || !bucket.Start.Before(bucket.End) {
			t.Errorf("report bucket [%v, %v) escapes [%v, %v)", bucket.Start, bucket.End, start, end)
		}
	}
}

func TestMixedOffsetAllPeriodResolvesFilteredEarliestInstant(t *testing.T) {
	database := newTestDatabase(t)
	end := time.Date(2026, time.August, 21, 13, 0, 0, 500, time.UTC)
	offsetEast := time.FixedZone("historical-east", 8*60*60)
	offsetWest := time.FixedZone("historical-west", -5*60*60)
	wantStart := end.Add(-20 * time.Hour)
	insertMixedOffsetLog(t, database.path, "other-earlier", "other", "codex", "model", end.Add(-30*time.Hour).In(offsetEast))
	insertMixedOffsetLog(t, database.path, "target-earliest", "target", "codex", "model", wantStart.In(offsetEast))
	insertMixedOffsetLog(t, database.path, "target-later", "target", "codex", "model", end.Add(-time.Hour).In(offsetWest))
	insertMixedOffsetLog(t, database.path, "at-end", "target", "codex", "model", end.In(offsetEast))

	providerID := "target"
	query := tokenanalytics.Query{
		Window: analyticswindow.Window{
			Period:          analyticswindow.PeriodAll,
			GranularityName: analyticswindow.Granularity1Day,
			Granularity:     24 * time.Hour,
			End:             end,
			StartResolution: analyticswindow.StartUnresolved,
		},
		ProviderID: &providerID,
	}
	report, err := tokenanalytics.NewService(database.repository).Analyze(context.Background(), query)
	if err != nil {
		t.Fatalf("Analyze() all-period mixed-offset error = %v", err)
	}
	if !report.TimeRange.Start.Equal(wantStart) || !report.TimeRange.End.Equal(end) {
		t.Fatalf("all-period range = [%v, %v), want [%v, %v)", report.TimeRange.Start, report.TimeRange.End, wantStart, end)
	}
	if report.Coverage.TotalRequests != 2 {
		t.Fatalf("all-period coverage = %+v, want two filtered rows", report.Coverage)
	}
}

func insertMixedOffsetLog(t *testing.T, databasePath, requestID, providerID, apiType, model string, createdAt time.Time) {
	t.Helper()
	db, err := sql.Open(sqliteDriverName, databasePath)
	if err != nil {
		t.Fatalf("open mixed-offset writer: %v", err)
	}
	defer db.Close()
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO request_logs (
			request_id, provider_id, api_type, model, created_at, created_at_unix_nano,
			prompt_tokens, completion_tokens, total_tokens
		) VALUES (?, ?, ?, ?, ?, ?, 1, 1, 2)`,
		requestID,
		providerID,
		apiType,
		model,
		createdAt,
		createdAt.UnixNano(),
	); err != nil {
		t.Fatalf("insert mixed-offset log %q: %v", requestID, err)
	}
}

func readLexicallySelectedRequestIDs(t *testing.T, database *testDatabase, query tokenanalytics.Query) []string {
	t.Helper()
	rows, err := database.repository.db.QueryContext(context.Background(), `
		SELECT request_id
		FROM request_logs
		WHERE created_at >= ? AND created_at < ?
		  AND provider_id = ? AND model = ? AND api_type = ?
		ORDER BY request_id`,
		query.Window.Start.UTC(),
		query.Window.End.UTC(),
		*query.ProviderID,
		*query.Model,
		*query.APIType,
	)
	if err != nil {
		t.Fatalf("read legacy lexical selection: %v", err)
	}
	defer rows.Close()
	var requestIDs []string
	for rows.Next() {
		var requestID string
		if err := rows.Scan(&requestID); err != nil {
			t.Fatalf("scan legacy lexical selection: %v", err)
		}
		requestIDs = append(requestIDs, requestID)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read legacy lexical selection rows: %v", err)
	}
	return requestIDs
}

func containsString(values []string, target string) bool {
	return slices.Contains(values, target)
}

func assertBucketRecordsInsideWindow(t *testing.T, records []tokenanalytics.BucketRecord, start, end time.Time, granularity time.Duration) {
	t.Helper()
	firstAligned := start.Truncate(granularity)
	lastAligned := end.Add(-time.Nanosecond).Truncate(granularity)
	for _, record := range records {
		if record.AlignedStart.Before(firstAligned) || record.AlignedStart.After(lastAligned) {
			t.Errorf("bucket %v escapes aligned window [%v, %v]", record.AlignedStart, firstAligned, lastAligned)
		}
	}
}
