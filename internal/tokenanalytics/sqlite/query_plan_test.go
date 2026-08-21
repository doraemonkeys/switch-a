package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/tokenanalytics"
)

func TestSummaryQueryPlansUseBoundedRequestLogIndexes(t *testing.T) {
	database := newTestDatabase(t)
	start := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	providerID, modelName, apiType := "provider", "model", "codex"

	tests := []struct {
		name          string
		providerID    *string
		model         *string
		apiType       *string
		expectedIndex string
	}{
		{name: "global", expectedIndex: createdAtUnixNanoIndex},
		{name: "provider", providerID: &providerID, expectedIndex: providerCreatedAtUnixNanoIndex},
		{name: "model", model: &modelName, expectedIndex: modelCreatedAtUnixNanoIndex},
		{name: "api_type", apiType: &apiType, expectedIndex: apiTypeCreatedAtUnixNanoIndex},
		{name: "provider_model", providerID: &providerID, model: &modelName, expectedIndex: modelCreatedAtUnixNanoIndex},
		{name: "provider_api_type", providerID: &providerID, apiType: &apiType, expectedIndex: apiTypeCreatedAtUnixNanoIndex},
		{name: "model_api_type", model: &modelName, apiType: &apiType, expectedIndex: apiTypeCreatedAtUnixNanoIndex},
		{name: "all_filters", providerID: &providerID, model: &modelName, apiType: &apiType, expectedIndex: apiTypeCreatedAtUnixNanoIndex},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			query := testQuery(start, end, time.Hour)
			query.ProviderID = test.providerID
			query.Model = test.model
			query.APIType = test.apiType
			details := explainSummaryPlan(t, database.repository, query)
			t.Logf("plan: %s", strings.Join(details, " | "))
			if !planUsesBoundedInstantIndex(details, test.expectedIndex) {
				t.Errorf("plan does not use bounded instant range through %s: %v", test.expectedIndex, details)
			}
		})
	}
}

func TestEveryAnalyticsReadPlanUsesBoundedInstantIndex(t *testing.T) {
	database := newTestDatabase(t)
	start := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	query := testQuery(start, start.Add(24*time.Hour), time.Hour)
	tests := []struct {
		name      string
		suffix    string
		extraArgs []any
	}{
		{name: "summary", suffix: summaryQuerySuffix},
		{name: "buckets", suffix: bucketQuerySuffix, extraArgs: []any{time.Hour.Nanoseconds(), int64(time.Hour / time.Second)}},
		{name: "provider_ranks", suffix: providerRankQuerySuffix, extraArgs: []any{tokenanalytics.TopRankLimit}},
		{name: "model_ranks", suffix: modelRankQuerySuffix, extraArgs: []any{tokenanalytics.TopRankLimit}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			details := explainPlan(t, database.repository, query, test.suffix, test.extraArgs...)
			t.Logf("plan: %s", strings.Join(details, " | "))
			if !planUsesBoundedInstantIndex(details, createdAtUnixNanoIndex) {
				t.Errorf("plan does not use bounded instant range: %v", details)
			}
		})
	}
}

func explainSummaryPlan(t *testing.T, repository *Repository, query tokenanalytics.Query) []string {
	t.Helper()
	return explainPlan(t, repository, query, summaryQuerySuffix)
}

func explainPlan(t *testing.T, repository *Repository, query tokenanalytics.Query, suffix string, extraArgs ...any) []string {
	t.Helper()
	projection, err := buildProjection(query)
	if err != nil {
		t.Fatalf("buildProjection() error = %v", err)
	}
	args := appendProjectionArgs(projection.args, extraArgs...)
	rows, err := repository.db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+projection.sql+suffix, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN error = %v", err)
	}
	defer rows.Close()
	details := make([]string, 0)
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan EXPLAIN QUERY PLAN error = %v", err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN rows error = %v", err)
	}
	return details
}

func planUsesBoundedInstantIndex(details []string, indexName string) bool {
	for _, detail := range details {
		if strings.Contains(detail, "SEARCH") &&
			strings.Contains(detail, "request_logs") &&
			strings.Contains(detail, indexName) &&
			strings.Contains(detail, "created_at_unix_nano>?") &&
			strings.Contains(detail, "created_at_unix_nano<?") {
			return true
		}
	}
	return false
}
