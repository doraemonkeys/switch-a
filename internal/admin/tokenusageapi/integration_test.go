package tokenusageapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/analyticswindow"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/store"
	"github.com/doraemonkeys/switch-a/internal/tokenanalytics"
	tokenanalyticssqlite "github.com/doraemonkeys/switch-a/internal/tokenanalytics/sqlite"
	"go.uber.org/zap"
)

func TestHandlerIntegrationReadsCanonicalSQLiteSnapshot(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "token-usage-integration.db")
	writer, err := store.NewSQLiteStore(databasePath, internal.RealClock{})
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := writer.Close(); closeErr != nil {
			t.Errorf("writer.Close() error = %v", closeErr)
		}
	})

	asOf := time.Date(2026, time.August, 21, 2, 0, 0, 0, time.UTC)
	logTime := asOf.Add(-30 * time.Minute)
	prompt, completion, total := int64(10), int64(4), int64(14)
	cacheRead, cacheCreation, reasoning := int64(0), int64(0), int64(0)
	if err := writer.InsertLog(context.Background(), &model.RequestLog{
		ProviderID: "deleted-provider",
		APIType:    "codex",
		Model:      "",
		CreatedAt:  logTime,

		PromptTokens:             &prompt,
		CompletionTokens:         &completion,
		TotalTokens:              &total,
		CacheReadInputTokens:     &cacheRead,
		CacheCreationInputTokens: &cacheCreation,
		ReasoningTokens:          &reasoning,
	}); err != nil {
		t.Fatalf("InsertLog() error = %v", err)
	}

	reader, err := tokenanalyticssqlite.Open(databasePath)
	if err != nil {
		t.Fatalf("token analytics Open() error = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := reader.Close(); closeErr != nil {
			t.Errorf("reader.Close() error = %v", closeErr)
		}
	})
	clock := fixedClock{now: asOf}
	resolver := analyticswindow.NewResolver(clock)
	handler, err := NewHandler(Config{
		Analyzer:       tokenanalytics.NewService(reader),
		WindowResolver: &resolver,
		Clock:          clock,
		OperationIDs:   &operationIDStub{id: "integration-operation"},
		Logger:         zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	handler.GetTokenUsage(recorder, httptest.NewRequest(http.MethodGet,
		"/admin/api/token-usage?period=24h&granularity=1h&as_of=2026-08-21T02%3A00%3A00Z", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", recorder.Code, recorder.Body.String())
	}
	var response ResponseDTO
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Summary.TotalTokens != "14" || response.Coverage.TotalRequests != 1 || response.Coverage.ComparableRequests != 1 {
		t.Fatalf("summary/coverage = %+v/%+v", response.Summary, response.Coverage)
	}
	if len(response.TimeSeries) != 24 {
		t.Fatalf("timeseries length = %d, want 24", len(response.TimeSeries))
	}
	var populated *BucketDTO
	for index := range response.TimeSeries {
		if response.TimeSeries[index].TotalRequests > 0 {
			populated = &response.TimeSeries[index]
			break
		}
	}
	if populated == nil || populated.TotalTokens != "14" || populated.ObservedRequests != 1 || populated.ComparableRequests != 1 {
		t.Fatalf("populated bucket = %+v", populated)
	}
	if len(response.ByProvider) != 1 || response.ByProvider[0].ProviderName != "deleted-provider" || response.ByProvider[0].Share != 1 {
		t.Fatalf("provider rank = %+v", response.ByProvider)
	}
	if len(response.ByModel) != 1 || response.ByModel[0].Model != "" || response.ByModel[0].Share != 1 {
		t.Fatalf("model rank = %+v", response.ByModel)
	}
}
