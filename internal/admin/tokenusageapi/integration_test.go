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
	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/store"
	"github.com/doraemonkeys/switch-a/internal/tokenanalytics"
	tokenanalyticssqlite "github.com/doraemonkeys/switch-a/internal/tokenanalytics/sqlite"
	"go.uber.org/zap"
)

func TestHandlerIntegrationReadsCanonicalSQLiteSnapshot(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "token-usage-integration.db")
	writer, err := store.NewSQLiteStore(databasePath, internal.RealClock{}, nil)
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

func TestHandlerIntegrationRealWorldHeterogeneousTraffic(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "token-usage-heterogeneous.db")
	writer, err := store.NewSQLiteStore(databasePath, internal.RealClock{}, nil)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := writer.Close(); closeErr != nil {
			t.Errorf("writer.Close() error = %v", closeErr)
		}
	})

	asOf := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	logs := []*model.RequestLog{
		{
			ProviderID:       "openai-std",
			APIType:          string(apicontract.APITypeCodex),
			Model:            "gpt-4o",
			CreatedAt:        asOf.Add(-2 * time.Hour),
			PromptTokens:     int64Ptr(100),
			CompletionTokens: int64Ptr(50),
			TotalTokens:      int64Ptr(150),
		},
		{
			ProviderID:       "deepseek-provider",
			APIType:          string(apicontract.APITypeDeepSeekOpenAI),
			Model:            "deepseek-reasoner",
			CreatedAt:        asOf.Add(-90 * time.Minute),
			PromptTokens:     int64Ptr(200),
			CompletionTokens: int64Ptr(100),
			TotalTokens:      int64Ptr(300),
			ReasoningTokens:  int64Ptr(60),
		},
		{
			ProviderID:               "anthropic-provider",
			APIType:                  string(apicontract.APITypeClaude),
			Model:                    "claude-3-7-sonnet",
			CreatedAt:                asOf.Add(-60 * time.Minute),
			PromptTokens:             int64Ptr(100),
			CompletionTokens:         int64Ptr(50),
			TotalTokens:              int64Ptr(150),
			CacheReadInputTokens:     int64Ptr(20),
			CacheCreationInputTokens: int64Ptr(10),
		},
		{
			ProviderID:           "google-provider",
			APIType:              string(apicontract.APITypeGemini),
			Model:                "gemini-2.0-flash",
			CreatedAt:            asOf.Add(-30 * time.Minute),
			PromptTokens:         int64Ptr(80),
			CompletionTokens:     int64Ptr(30),
			TotalTokens:          int64Ptr(120),
			CacheReadInputTokens: int64Ptr(20),
		},
		{
			ProviderID:       "custom-provider",
			APIType:          apicontract.CustomAPITypePrefix + "self-hosted",
			Model:            "custom-llm",
			CreatedAt:        asOf.Add(-10 * time.Minute),
			PromptTokens:     int64Ptr(10),
			CompletionTokens: int64Ptr(5),
			TotalTokens:      int64Ptr(15),
		},
		{
			ProviderID: "openai-std",
			APIType:    string(apicontract.APITypeCodex),
			Model:      "gpt-4o",
			CreatedAt:  asOf.Add(-5 * time.Minute),
		},
	}

	for _, log := range logs {
		if err := writer.InsertLog(context.Background(), log); err != nil {
			t.Fatalf("InsertLog(%s) error = %v", log.ProviderID, err)
		}
	}

	reader, err := tokenanalyticssqlite.Open(databasePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
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
		OperationIDs:   &operationIDStub{id: "heterogeneous-op"},
		Logger:         zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	handler.GetTokenUsage(recorder, httptest.NewRequest(http.MethodGet,
		"/admin/api/token-usage?period=24h&granularity=1h&as_of=2026-08-21T12%3A00%3A00Z", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", recorder.Code, recorder.Body.String())
	}

	var response ResponseDTO
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Mixing semantic families in one response catches projections that accidentally
	// key off provider identity or let one protocol's component rules dominate.
	wantBreakdown := BreakdownDTO{
		TotalTokens:              "750",
		InputTokens:              "510",
		OutputTokens:             "240",
		FreshInputTokens:         "160",
		CacheReadInputTokens:     "40",
		CacheCreationInputTokens: "10",
		UnclassifiedInputTokens:  "300",
		StandardOutputTokens:     "70",
		ReasoningTokens:          "70",
		UnclassifiedOutputTokens: "100",
	}
	if response.Summary.BreakdownDTO != wantBreakdown {
		t.Errorf("Summary.BreakdownDTO = %+v, want %+v", response.Summary.BreakdownDTO, wantBreakdown)
	}
	if response.Coverage.TotalRequests != 6 || response.Coverage.ObservedRequests != 5 || response.Coverage.ComparableRequests != 4 {
		t.Errorf("Coverage = %+v, want Total: 6, Observed: 5, Comparable: 4", response.Coverage)
	}
	if response.DataQuality.UnknownSemanticsRequests != 1 {
		t.Errorf("DataQuality.UnknownSemanticsRequests = %d, want 1", response.DataQuality.UnknownSemanticsRequests)
	}
	if len(response.ByProvider) != 4 {
		t.Errorf("ByProvider count = %d, want 4", len(response.ByProvider))
	}
}

func int64Ptr(value int64) *int64 {
	return &value
}
