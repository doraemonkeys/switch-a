package sqlite

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/analyticswindow"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/tokenanalytics"
)

func TestRepositoryProjectsCanonicalUsageAndQualityClasses(t *testing.T) {
	database := newTestDatabase(t)
	start := time.Date(2026, time.August, 20, 0, 0, 0, 0, time.UTC)
	end := start.Add(4 * time.Hour)

	database.createProvider(t, "open", "Open Named")
	database.createProvider(t, "google", "")
	logs := []model.RequestLog{
		{
			ProviderID: "anthropic-deleted", APIType: "claude", Model: "claude-model", CreatedAt: start.Add(10 * time.Minute),
			PromptTokens: int64Pointer(100), CompletionTokens: int64Pointer(50), TotalTokens: int64Pointer(150),
			CacheReadInputTokens: int64Pointer(20), CacheCreationInputTokens: int64Pointer(10),
		},
		{
			ProviderID: "open", APIType: "codex", Model: "gpt", CreatedAt: start.Add(time.Hour + 10*time.Minute),
			PromptTokens: int64Pointer(100), CompletionTokens: int64Pointer(40), TotalTokens: int64Pointer(140),
			CacheReadInputTokens: int64Pointer(30), CacheCreationInputTokens: int64Pointer(10), ReasoningTokens: int64Pointer(15),
			SemanticsVersion: model.RequestSemanticsVersionLegacyPreAssessment,
		},
		{
			ProviderID: "open", APIType: "codex", Model: "gpt", CreatedAt: start.Add(time.Hour + 20*time.Minute),
			PromptTokens: int64Pointer(50), CompletionTokens: int64Pointer(20),
		},
		{
			ProviderID: "google", APIType: "gemini", Model: "gemini-model", CreatedAt: start.Add(2*time.Hour + 10*time.Minute),
			PromptTokens: int64Pointer(80), CompletionTokens: int64Pointer(30), TotalTokens: int64Pointer(120),
			CacheReadInputTokens: int64Pointer(20),
		},
		{
			ProviderID: "google", APIType: "gemini", Model: "gemini-model", CreatedAt: start.Add(2*time.Hour + 20*time.Minute),
			PromptTokens: int64Pointer(20), CompletionTokens: int64Pointer(10), TotalTokens: int64Pointer(0),
		},
		{
			ProviderID: "custom", APIType: "custom:opaque", Model: "custom-model", CreatedAt: start.Add(3*time.Hour + time.Minute),
			PromptTokens: int64Pointer(10), CompletionTokens: int64Pointer(5), TotalTokens: int64Pointer(15),
		},
		{
			ProviderID: "partial", APIType: "claude", Model: "partial", CreatedAt: start.Add(3*time.Hour + 2*time.Minute),
			PromptTokens: int64Pointer(10), CompletionTokens: int64Pointer(5), TotalTokens: int64Pointer(15),
		},
		{
			ProviderID: "invalid", APIType: "codex", Model: "invalid", CreatedAt: start.Add(3*time.Hour + 3*time.Minute),
			PromptTokens: int64Pointer(10), CompletionTokens: int64Pointer(5), TotalTokens: int64Pointer(16),
		},
		{
			ProviderID: "no-usage", APIType: "codex", Model: "none", CreatedAt: start.Add(3*time.Hour + 4*time.Minute),
			ReasoningTokens: int64Pointer(-1),
		},
	}
	for _, log := range logs {
		database.insertLog(t, log)
	}

	query := testQuery(start, end, time.Hour)
	snapshot, err := database.repository.OpenSnapshot(context.Background())
	if err != nil {
		t.Fatalf("OpenSnapshot() error = %v", err)
	}
	defer snapshot.Close()

	summary, err := snapshot.ReadSummary(context.Background(), query)
	if err != nil {
		t.Fatalf("ReadSummary() error = %v", err)
	}
	wantBreakdown := tokenanalytics.Breakdown{
		TotalTokens: 540, InputTokens: 380, OutputTokens: 160,
		FreshInputTokens: 220, CacheReadInputTokens: 70, CacheCreationInputTokens: 20,
		UnclassifiedInputTokens: 70, StandardOutputTokens: 65, ReasoningTokens: 25,
		UnclassifiedOutputTokens: 70,
	}
	if !reflect.DeepEqual(summary.Breakdown, wantBreakdown) {
		t.Fatalf("summary breakdown = %+v, want %+v", summary.Breakdown, wantBreakdown)
	}
	if summary.TotalRequests != 9 || summary.ObservedRequests != 8 || summary.ComparableRequests != 5 ||
		summary.PartialRequests != 1 || summary.InvalidRequests != 1 || summary.UnknownSemanticsRequests != 1 {
		t.Fatalf("summary quality counts = %+v", summary)
	}
	if summary.EarliestMatchingTime == nil || !summary.EarliestMatchingTime.Equal(start.Add(10*time.Minute)) {
		t.Fatalf("earliest = %v, want %v", summary.EarliestMatchingTime, start.Add(10*time.Minute))
	}

	buckets, err := snapshot.ReadBuckets(context.Background(), query)
	if err != nil {
		t.Fatalf("ReadBuckets() error = %v", err)
	}
	if got, want := len(buckets), 4; got != want {
		t.Fatalf("bucket count = %d, want %d", got, want)
	}
	for index, wantTotal := range []int64{180, 210, 150, 0} {
		if !buckets[index].AlignedStart.Equal(start.Add(time.Duration(index)*time.Hour)) || buckets[index].TotalTokens != wantTotal {
			t.Errorf("bucket[%d] = %+v, want aligned %v total %d", index, buckets[index], start.Add(time.Duration(index)*time.Hour), wantTotal)
		}
	}
	wantBucketCounts := [][3]int64{{1, 1, 1}, {2, 2, 2}, {2, 2, 2}, {4, 3, 0}}
	for index, want := range wantBucketCounts {
		got := [3]int64{buckets[index].TotalRequests, buckets[index].ObservedRequests, buckets[index].ComparableRequests}
		if got != want {
			t.Errorf("bucket[%d] counts = %v, want %v", index, got, want)
		}
	}

	providers, err := snapshot.ReadProviderRanks(context.Background(), query, tokenanalytics.TopRankLimit)
	if err != nil {
		t.Fatalf("ReadProviderRanks() error = %v", err)
	}
	wantProviders := []struct {
		id, label string
		total     int64
	}{
		{id: "open", label: "Open Named", total: 210},
		{id: "anthropic-deleted", label: "anthropic-deleted", total: 180},
		{id: "google", label: "google", total: 150},
	}
	if len(providers) != len(wantProviders) {
		t.Fatalf("provider rank count = %d, want %d", len(providers), len(wantProviders))
	}
	for index, want := range wantProviders {
		if providers[index].ProviderID != want.id || providers[index].ProviderLabel != want.label || providers[index].TotalTokens != want.total {
			t.Errorf("provider[%d] = %+v, want %+v", index, providers[index], want)
		}
	}

	models, err := snapshot.ReadModelRanks(context.Background(), query, tokenanalytics.TopRankLimit)
	if err != nil {
		t.Fatalf("ReadModelRanks() error = %v", err)
	}
	if got := []string{models[0].Model, models[1].Model, models[2].Model}; !reflect.DeepEqual(got, []string{"gpt", "claude-model", "gemini-model"}) {
		t.Fatalf("model order = %v", got)
	}
}

func TestRepositoryUsesExactFiltersBoundariesAndStableTies(t *testing.T) {
	database := newTestDatabase(t)
	start := time.Date(2026, time.August, 20, 1, 30, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)

	rows := []model.RequestLog{
		{ProviderID: "b", APIType: "codex", Model: "", CreatedAt: start, PromptTokens: int64Pointer(5), CompletionTokens: int64Pointer(5)},
		{ProviderID: "a", APIType: "codex", Model: "a", CreatedAt: start.Add(time.Hour), PromptTokens: int64Pointer(5), CompletionTokens: int64Pointer(5)},
		{ProviderID: "a", APIType: "grok", Model: "a", CreatedAt: start.Add(time.Hour), PromptTokens: int64Pointer(100), CompletionTokens: int64Pointer(100)},
		{ProviderID: "b", APIType: "codex", Model: "", CreatedAt: end, PromptTokens: int64Pointer(1000), CompletionTokens: int64Pointer(1000)},
		{ProviderID: "b", APIType: "codex", Model: "", CreatedAt: start.Add(-time.Nanosecond), PromptTokens: int64Pointer(1000), CompletionTokens: int64Pointer(1000)},
	}
	for _, row := range rows {
		database.insertLog(t, row)
	}

	query := testQuery(start, end, time.Hour)
	summary := readSummary(t, database.repository, query)
	if summary.TotalRequests != 3 || summary.ComparableRequests != 3 || summary.TotalTokens != 220 {
		t.Fatalf("boundary summary = %+v", summary)
	}

	providerID, modelName, apiType := "a", "a", "codex"
	query.ProviderID = &providerID
	query.Model = &modelName
	query.APIType = &apiType
	filtered := readSummary(t, database.repository, query)
	if filtered.TotalRequests != 1 || filtered.TotalTokens != 10 {
		t.Fatalf("combined-filter summary = %+v", filtered)
	}
	windowSnapshot, err := database.repository.OpenSnapshot(context.Background())
	if err != nil {
		t.Fatalf("OpenSnapshot(bucket alignment) error = %v", err)
	}
	buckets, err := windowSnapshot.ReadBuckets(context.Background(), testQuery(start, end, time.Hour))
	if closeErr := windowSnapshot.Close(); closeErr != nil {
		t.Fatalf("Close(bucket alignment) error = %v", closeErr)
	}
	if err != nil {
		t.Fatalf("ReadBuckets() error = %v", err)
	}
	if len(buckets) != 2 || !buckets[0].AlignedStart.Equal(start.Truncate(time.Hour)) || !buckets[1].AlignedStart.Equal(start.Truncate(time.Hour).Add(time.Hour)) {
		t.Fatalf("partial-window aligned buckets = %+v", buckets)
	}

	tieQuery := testQuery(start, start.Add(30*time.Minute), time.Hour)
	tieDatabase := newTestDatabase(t)
	for _, provider := range []string{"b", "a"} {
		tieDatabase.insertLog(t, model.RequestLog{
			ProviderID: provider, APIType: "codex", Model: map[string]string{"a": "a", "b": ""}[provider], CreatedAt: start,
			PromptTokens: int64Pointer(5), CompletionTokens: int64Pointer(5),
		})
	}
	snapshot, err := tieDatabase.repository.OpenSnapshot(context.Background())
	if err != nil {
		t.Fatalf("OpenSnapshot() error = %v", err)
	}
	defer snapshot.Close()
	providers, err := snapshot.ReadProviderRanks(context.Background(), tieQuery, tokenanalytics.TopRankLimit)
	if err != nil {
		t.Fatalf("ReadProviderRanks() error = %v", err)
	}
	models, err := snapshot.ReadModelRanks(context.Background(), tieQuery, tokenanalytics.TopRankLimit)
	if err != nil {
		t.Fatalf("ReadModelRanks() error = %v", err)
	}
	if got := []string{providers[0].ProviderID, providers[1].ProviderID}; !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("provider tie order = %v", got)
	}
	if got := []string{models[0].Model, models[1].Model}; !reflect.DeepEqual(got, []string{"", "a"}) {
		t.Fatalf("model tie order = %v", got)
	}
}

func TestRepositoryClassifiesProtocolEdgeCasesWithoutRepairingFacts(t *testing.T) {
	database := newTestDatabase(t)
	start := time.Date(2026, time.August, 20, 0, 0, 0, 0, time.UTC)
	testCases := []struct {
		name       string
		log        model.RequestLog
		comparable int64
		invalid    int64
		want       tokenanalytics.Breakdown
	}{
		{
			name: "openai explicit zero details",
			log: model.RequestLog{ProviderID: "open-zero", APIType: "codex", PromptTokens: int64Pointer(10), CompletionTokens: int64Pointer(4), TotalTokens: int64Pointer(14),
				CacheReadInputTokens: int64Pointer(0), CacheCreationInputTokens: int64Pointer(0), ReasoningTokens: int64Pointer(0)},
			comparable: 1,
			want:       tokenanalytics.Breakdown{TotalTokens: 14, InputTokens: 10, OutputTokens: 4, FreshInputTokens: 10, StandardOutputTokens: 4},
		},
		{
			name: "openai incomplete cache detail is unclassified",
			log: model.RequestLog{ProviderID: "open-unclassified", APIType: "codex", PromptTokens: int64Pointer(10), CompletionTokens: int64Pointer(4), TotalTokens: int64Pointer(14),
				CacheReadInputTokens: int64Pointer(3)},
			comparable: 1,
			want:       tokenanalytics.Breakdown{TotalTokens: 14, InputTokens: 10, OutputTokens: 4, UnclassifiedInputTokens: 10, UnclassifiedOutputTokens: 4},
		},
		{
			name: "openai cache children exceed input",
			log: model.RequestLog{ProviderID: "open-cache-invalid", APIType: "codex", PromptTokens: int64Pointer(10), CompletionTokens: int64Pointer(4), TotalTokens: int64Pointer(14),
				CacheReadInputTokens: int64Pointer(8), CacheCreationInputTokens: int64Pointer(3)},
			invalid: 1,
		},
		{
			name: "openai reasoning exceeds output",
			log: model.RequestLog{ProviderID: "open-reasoning-invalid", APIType: "codex", PromptTokens: int64Pointer(10), CompletionTokens: int64Pointer(4), TotalTokens: int64Pointer(14),
				ReasoningTokens: int64Pointer(5)},
			invalid: 1,
		},
		{
			name: "anthropic raw total mismatch",
			log: model.RequestLog{ProviderID: "anthropic-invalid", APIType: "claude", PromptTokens: int64Pointer(10), CompletionTokens: int64Pointer(4), TotalTokens: int64Pointer(15),
				CacheReadInputTokens: int64Pointer(2), CacheCreationInputTokens: int64Pointer(1)},
			invalid: 1,
		},
		{
			name:    "negative observed core",
			log:     model.RequestLog{ProviderID: "negative", APIType: "codex", PromptTokens: int64Pointer(-1), CompletionTokens: int64Pointer(0), TotalTokens: int64Pointer(-1)},
			invalid: 1,
		},
		{
			name:       "google known output without completion",
			log:        model.RequestLog{ProviderID: "google-unclassified", APIType: "gemini", PromptTokens: int64Pointer(10), TotalTokens: int64Pointer(15)},
			comparable: 1,
			want:       tokenanalytics.Breakdown{TotalTokens: 15, InputTokens: 10, OutputTokens: 5, UnclassifiedInputTokens: 10, UnclassifiedOutputTokens: 5},
		},
		{
			name:    "google negative residual",
			log:     model.RequestLog{ProviderID: "google-invalid", APIType: "gemini", PromptTokens: int64Pointer(10), CompletionTokens: int64Pointer(6), TotalTokens: int64Pointer(15)},
			invalid: 1,
		},
		{
			name:    "google cache child exceeds input",
			log:     model.RequestLog{ProviderID: "google-cache-invalid", APIType: "gemini", PromptTokens: int64Pointer(10), CompletionTokens: int64Pointer(4), TotalTokens: int64Pointer(14), CacheReadInputTokens: int64Pointer(11)},
			invalid: 1,
		},
	}

	for index := range testCases {
		testCases[index].log.CreatedAt = start.Add(time.Duration(index) * time.Minute)
		database.insertLog(t, testCases[index].log)
	}
	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			providerID := test.log.ProviderID
			query := testQuery(start, start.Add(time.Hour), time.Hour)
			query.ProviderID = &providerID
			record := readSummary(t, database.repository, query)
			if record.ComparableRequests != test.comparable || record.InvalidRequests != test.invalid || !reflect.DeepEqual(record.Breakdown, test.want) {
				t.Fatalf("summary = %+v, want comparable=%d invalid=%d breakdown=%+v", record, test.comparable, test.invalid, test.want)
			}
		})
	}
}

func TestRepositoryUnresolvedAllWindowUsesFilteredEarliestRow(t *testing.T) {
	database := newTestDatabase(t)
	end := time.Date(2026, time.August, 20, 3, 0, 0, 0, time.UTC)
	for index, providerID := range []string{"other", "target", "target"} {
		database.insertLog(t, model.RequestLog{
			ProviderID: providerID, APIType: "codex", CreatedAt: end.Add(time.Duration(index-3) * time.Hour),
			PromptTokens: int64Pointer(1), CompletionTokens: int64Pointer(1),
		})
	}
	providerID := "target"
	query := tokenanalytics.Query{
		Window: analyticswindow.Window{
			Period: analyticswindow.PeriodAll, Granularity: 24 * time.Hour,
			Start: end.Add(24 * time.Hour), End: end, StartResolution: analyticswindow.StartUnresolved,
		},
		ProviderID: &providerID,
	}
	record := readSummary(t, database.repository, query)
	wantEarliest := end.Add(-2 * time.Hour)
	if record.TotalRequests != 2 || record.EarliestMatchingTime == nil || !record.EarliestMatchingTime.Equal(wantEarliest) {
		t.Fatalf("all-period summary = %+v, want earliest %v", record, wantEarliest)
	}
}

func TestRepositoryRejectsInvalidConstructionAndArguments(t *testing.T) {
	for _, databasePath := range []string{"", ":memory:", "file::memory:?cache=shared", "file:test?mode=memory"} {
		if _, err := Open(databasePath); !errors.Is(err, errMemoryDatabase) {
			t.Errorf("Open(%q) error = %v, want errMemoryDatabase", databasePath, err)
		}
	}

	database := newTestDatabase(t)
	snapshot, err := database.repository.OpenSnapshot(context.Background())
	if err != nil {
		t.Fatalf("OpenSnapshot() error = %v", err)
	}
	query := tokenanalytics.Query{Window: analyticswindow.Window{End: time.Now().UTC(), StartResolution: analyticswindow.StartUnresolved}}
	if _, err := snapshot.ReadBuckets(context.Background(), query); !errors.Is(err, errInvalidWindow) {
		t.Errorf("ReadBuckets(unresolved) error = %v", err)
	}
	resolved := testQuery(time.Now().Add(-time.Hour), time.Now(), time.Hour)
	if _, err := snapshot.ReadProviderRanks(context.Background(), resolved, 0); !errors.Is(err, errInvalidRankLimit) {
		t.Errorf("ReadProviderRanks(limit=0) error = %v", err)
	}
	if _, err := snapshot.ReadModelRanks(context.Background(), resolved, tokenanalytics.TopRankLimit+1); !errors.Is(err, errInvalidRankLimit) {
		t.Errorf("ReadModelRanks(limit=too-large) error = %v", err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if _, err := snapshot.ReadSummary(context.Background(), resolved); !errors.Is(err, errSnapshotClosed) {
		t.Errorf("ReadSummary(closed) error = %v", err)
	}
}

func TestRepositorySurfacesIntegerOverflow(t *testing.T) {
	database := newTestDatabase(t)
	start := time.Date(2026, time.August, 20, 0, 0, 0, 0, time.UTC)
	for index := 0; index < 2; index++ {
		database.insertLog(t, model.RequestLog{
			ProviderID: "overflow", APIType: "codex", Model: "overflow", CreatedAt: start.Add(time.Duration(index) * time.Minute),
			PromptTokens: int64Pointer(1 << 62), CompletionTokens: int64Pointer(0), TotalTokens: int64Pointer(1 << 62),
		})
	}

	snapshot, err := database.repository.OpenSnapshot(context.Background())
	if err != nil {
		t.Fatalf("OpenSnapshot() error = %v", err)
	}
	defer snapshot.Close()
	if _, err := snapshot.ReadSummary(context.Background(), testQuery(start, start.Add(time.Hour), time.Hour)); err == nil {
		t.Fatal("ReadSummary() error = nil, want integer overflow")
	}
}
