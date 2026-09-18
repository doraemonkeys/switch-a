package clientdisguise

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestClientActivityPrecedesReferenceSelectionAndKeepsNewestIngress(t *testing.T) {
	ctx := context.Background()
	repo := testRepository(t)
	at := time.Date(2026, 9, 18, 18, 30, 0, 0, time.FixedZone("client", 8*60*60))
	desktop := http.Header{"User-Agent": {"Codex Desktop/0.153.4 (Windows 10.0.19045; x86_64)"}, "Originator": {"Codex Desktop"}}
	for _, observation := range []struct {
		id      string
		at      time.Time
		headers http.Header
	}{
		{"client", at, desktop},
		{"client", at.Add(time.Minute), desktop},
		{"client", at.Add(-time.Hour), http.Header{"User-Agent": {"older"}}},
		{"client", at.Add(time.Minute).UTC(), http.Header{"User-Agent": {"same instant"}}},
		{"unknown-client", at.Add(-time.Minute), http.Header{"User-Agent": {"unrecognized-agent"}}},
		{"", at.Add(time.Hour), desktop},
	} {
		if err := repo.ObserveClient(ctx, observation.id, observation.headers, observation.at); err != nil {
			t.Fatal(err)
		}
	}
	requests, err := NewRepository(repo.db).ListClientRequests(ctx)
	if err != nil || len(requests) != 2 {
		t.Fatalf("requests=%+v err=%v", requests, err)
	}
	newest := requests[0]
	if newest.ClientID != "client" || !newest.ObservedAt.Equal(at.Add(time.Minute)) ||
		newest.Tuple != windowsDesktop || newest.ClientVersion != "0.153.4" ||
		newest.UserAgent != desktop.Get("User-Agent") || newest.Originator != "Codex Desktop" {
		t.Fatalf("newest ingress was not preserved: %+v", newest)
	}
	if requests[1].UserAgent != "unrecognized-agent" || requests[1].Tuple.Valid() {
		t.Fatalf("unknown client lost its request evidence: %+v", requests[1])
	}
	snapshot, err := repo.Export(ctx)
	if err != nil || len(snapshot.Samples) != 0 || len(snapshot.References) != 0 {
		t.Fatalf("unselected clients must not create reference profiles: %+v err=%v", snapshot, err)
	}
	// Missing fields describe the latest request, rather than a synthetic mixture
	// of unrelated requests made with a shared key.
	if err := repo.ObserveClient(ctx, "client", nil, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	requests, err = repo.ListClientRequests(ctx)
	if err != nil || requests[0].UserAgent != "" || requests[0].Tuple != (Tuple{}) {
		t.Fatalf("requests=%+v err=%v", requests, err)
	}
}

func TestClientActivityRecordsConflictingAndUnversionedRequests(t *testing.T) {
	for _, headers := range []http.Header{
		{"User-Agent": {"Codex Desktop/0.153.4 (Windows; x86_64)"}, "X-Client-Platform": {"Linux"}},
		{"User-Agent": {"Codex Desktop (Windows; x86_64)"}},
	} {
		repo := testRepository(t)
		at := time.Now()
		repo.now = func() time.Time { return at }
		if err := repo.ObserveClient(context.Background(), "client", headers, time.Time{}); err != nil {
			t.Fatal(err)
		}
		requests, err := repo.ListClientRequests(context.Background())
		if err != nil || len(requests) != 1 || !requests[0].ObservedAt.Equal(at) {
			t.Fatalf("requests=%+v err=%v", requests, err)
		}
		if headers.Get("X-Client-Platform") != "" && requests[0].Tuple.Platform != "" {
			t.Fatal("conflicting platform presented as certain")
		}
	}
}

func TestClientActivityReportsPersistenceFailures(t *testing.T) {
	repo := testRepository(t)
	if err := repo.db.Migrator().DropTable(&ClientRequestObservation{}); err != nil {
		t.Fatal(err)
	}
	if err := repo.ObserveClient(context.Background(), "client", nil, time.Now()); err == nil {
		t.Fatal("missing write failure")
	}
	if _, err := repo.ListClientRequests(context.Background()); err == nil {
		t.Fatal("missing read failure")
	}
}
