package sqlite

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/analyticswindow"
	"github.com/doraemonkeys/switch-a/internal/clientaccess"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/tokenanalytics"
)

func TestClientAPIKeyFilterScopesEveryReportSection(t *testing.T) {
	database := newTestDatabase(t)
	start := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	keyA := clientaccess.IdentifyKey([]byte("prefix-client-a-suffix"))
	keyB := clientaccess.IdentifyKey([]byte("prefix-client-b-suffix"))
	for index, identity := range []clientaccess.UsageIdentity{keyA, keyB, {}, keyA} {
		log := model.RequestLog{
			CreatedAt: start.Add(time.Duration(index) * time.Hour), APIType: "codex",
			Model: "model-a", ProviderID: "provider-a",
			ClientAPIKeyFingerprint: identity.Fingerprint, ClientAPIKeyMasked: identity.MaskedKey,
			PromptTokens: int64Pointer(int64(10 * (index + 1))), CompletionTokens: int64Pointer(2),
		}
		if index == 1 {
			log.Model = "model-b"
			log.ProviderID = "provider-b"
		}
		if index == 3 {
			log.PromptTokens = nil
			log.CompletionTokens = nil
		}
		database.insertLog(t, log)
	}
	service := tokenanalytics.NewService(database.repository)
	query := testQuery(start, start.Add(5*time.Hour), time.Hour)
	for _, test := range []struct {
		name                        string
		fingerprint                 *string
		total, requests, comparable int64
	}{
		{"all", nil, 66, 4, 3},
		{"first", &keyA.Fingerprint, 12, 2, 1},
		{"second", &keyB.Fingerprint, 22, 1, 1},
		{"unattributed", stringPointer(""), 32, 1, 1},
		{"no matches", stringPointer("nonexistent"), 0, 0, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			selected := query
			selected.ClientAPIKeyFingerprint = test.fingerprint
			report, err := service.Analyze(context.Background(), selected)
			if err != nil {
				t.Fatal(err)
			}
			if report.Summary.TotalTokens != test.total || report.Coverage.TotalRequests != test.requests || report.Coverage.ComparableRequests != test.comparable {
				t.Fatalf("summary/coverage = %+v / %+v", report.Summary, report.Coverage)
			}
			var bucketTotal, providerTotal, modelTotal, bucketRequests int64
			for _, bucket := range report.TimeSeries {
				bucketTotal += bucket.TotalTokens
				bucketRequests += bucket.TotalRequests
			}
			for _, rank := range report.ByProvider {
				providerTotal += rank.TotalTokens
			}
			for _, rank := range report.ByModel {
				modelTotal += rank.TotalTokens
			}
			if bucketTotal != test.total || providerTotal != test.total || modelTotal != test.total || bucketRequests != test.requests {
				t.Fatalf("inconsistent sections: %d/%d/%d/%d", bucketTotal, providerTotal, modelTotal, bucketRequests)
			}
		})
	}
	query.ClientAPIKeyFingerprint = &keyB.Fingerprint
	query.Model = stringPointer("model-a")
	report, err := service.Analyze(context.Background(), query)
	if err != nil || report.Coverage.TotalRequests != 0 {
		t.Fatalf("combined filters = %+v, %v", report, err)
	}
	query.Model = nil
	query.Window.Period = analyticswindow.PeriodAll
	query.Window.StartResolution = analyticswindow.StartUnresolved
	report, err = service.Analyze(context.Background(), query)
	if err != nil || !report.TimeRange.Start.Equal(start.Add(time.Hour)) {
		t.Fatalf("all-period start = %+v, %v", report.TimeRange, err)
	}
	details := explainSummaryPlan(t, database.repository, testKeyQuery(start, keyA.Fingerprint))
	if !planUsesBoundedInstantIndex(details, clientAPIKeyCreatedAtIndex) {
		t.Fatalf("key query plan = %v", details)
	}
}

func testKeyQuery(start time.Time, fingerprint string) tokenanalytics.Query {
	query := testQuery(start, start.Add(5*time.Hour), time.Hour)
	query.ClientAPIKeyFingerprint = &fingerprint
	return query
}

func stringPointer(value string) *string { return &value }

func TestClientAPIKeyDirectoryPreservesObservedIdentityThroughRegistryChanges(t *testing.T) {
	database := newTestDatabase(t)
	ctx := context.Background()
	raw := "observed-before-registration"
	identity := clientaccess.IdentifyKey([]byte(raw))
	for range 2 {
		database.insertLog(t, model.RequestLog{CreatedAt: time.Now(), ClientAPIKeyFingerprint: identity.Fingerprint, ClientAPIKeyMasked: identity.MaskedKey})
	}
	database.insertLog(t, model.RequestLog{CreatedAt: time.Now()})
	service := tokenanalytics.NewService(database.repository)
	keys, err := service.ClientAPIKeys(ctx)
	want := []tokenanalytics.ClientAPIKey{{Fingerprint: identity.Fingerprint, MaskedKey: identity.MaskedKey}}
	if err != nil || !reflect.DeepEqual(keys, want) {
		t.Fatalf("observed keys = %+v, %v", keys, err)
	}
	registered := clientaccess.Key{ID: "registered-id", Name: "Laptop", Key: raw, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	repository := database.writer.ClientAPIKeyRepository()
	if err := repository.CreateKey(ctx, registered); err != nil {
		t.Fatal(err)
	}
	want[0].Name = "Laptop"
	keys, err = service.ClientAPIKeys(ctx)
	if err != nil || !reflect.DeepEqual(keys, want) {
		t.Fatalf("registered keys = %+v, %v", keys, err)
	}
	if _, err := repository.RenameKey(ctx, registered.ID, "Work laptop", time.Now()); err != nil {
		t.Fatal(err)
	}
	want[0].Name = "Work laptop"
	keys, err = service.ClientAPIKeys(ctx)
	if err != nil || !reflect.DeepEqual(keys, want) {
		t.Fatalf("renamed keys = %+v, %v", keys, err)
	}
	if err := repository.DeleteKey(ctx, registered.ID); err != nil {
		t.Fatal(err)
	}
	want[0].Name = ""
	keys, err = service.ClientAPIKeys(ctx)
	if err != nil || !reflect.DeepEqual(keys, want) {
		t.Fatalf("deleted keys = %+v, %v", keys, err)
	}
	registered.ID = "replacement-id"
	if err := repository.CreateKey(ctx, registered); err != nil {
		t.Fatal(err)
	}
	other := registered
	other.ID, other.Name, other.Key = "unused-id", "A unused", "unused-key"
	if err := repository.CreateKey(ctx, other); err != nil {
		t.Fatal(err)
	}
	keys, err = service.ClientAPIKeys(ctx)
	if err != nil || len(keys) != 2 || keys[0].Name != other.Name || keys[1].Fingerprint != identity.Fingerprint {
		t.Fatalf("re-registered and unused keys = %+v, %v", keys, err)
	}
}

func TestClientAPIKeyDirectoryErrors(t *testing.T) {
	if _, err := (&Snapshot{closed: true}).ReadClientAPIKeys(context.Background()); !errors.Is(err, errSnapshotClosed) {
		t.Fatal(err)
	}
	database := newTestDatabase(t)
	ctx, cancel := context.WithCancel(context.Background())
	snapshot, err := database.repository.OpenSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	cancel()
	if _, err := snapshot.ReadClientAPIKeys(ctx); err == nil {
		t.Fatal("canceled directory succeeded")
	}
}
