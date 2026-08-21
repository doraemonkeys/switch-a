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
		{name: "global", expectedIndex: "idx_request_logs_created_at"},
		{name: "provider", providerID: &providerID, expectedIndex: "idx_request_logs_provider_created_at"},
		{name: "model", model: &modelName, expectedIndex: "idx_request_logs_model_created_at"},
		{name: "api_type", apiType: &apiType, expectedIndex: "idx_request_logs_api_type_created_at"},
		{name: "provider_model", providerID: &providerID, model: &modelName, expectedIndex: "idx_request_logs_model_created_at"},
		{name: "provider_api_type", providerID: &providerID, apiType: &apiType, expectedIndex: "idx_request_logs_api_type_created_at"},
		{name: "model_api_type", model: &modelName, apiType: &apiType, expectedIndex: "idx_request_logs_api_type_created_at"},
		{name: "all_filters", providerID: &providerID, model: &modelName, apiType: &apiType, expectedIndex: "idx_request_logs_api_type_created_at"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			query := testQuery(start, end, time.Hour)
			query.ProviderID = test.providerID
			query.Model = test.model
			query.APIType = test.apiType
			details := explainSummaryPlan(t, database.repository, query)
			t.Logf("plan: %s", strings.Join(details, " | "))
			if !planUsesIndex(details, test.expectedIndex) {
				t.Errorf("plan does not use %s: %v", test.expectedIndex, details)
			}
		})
	}
}

func explainSummaryPlan(t *testing.T, repository *Repository, query tokenanalytics.Query) []string {
	t.Helper()
	projection, err := buildProjection(query)
	if err != nil {
		t.Fatalf("buildProjection() error = %v", err)
	}
	rows, err := repository.db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+projection.sql+summaryQuerySuffix, projection.args...)
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

func planUsesIndex(details []string, indexName string) bool {
	for _, detail := range details {
		if strings.Contains(detail, "request_logs") && strings.Contains(detail, indexName) {
			return true
		}
	}
	return false
}
