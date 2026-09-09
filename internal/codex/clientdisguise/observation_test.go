package clientdisguise

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestObserveClientPersistsRuntimeTimestampsAndAdvancesBinding(t *testing.T) {
	const userAgent = "Codex Desktop/0.153.4 (Windows 10.0.19045; x86_64) unknown (Codex Desktop; 26.901.51231)"
	for _, captured := range []struct {
		name string
		at   time.Time
	}{
		{name: "monotonic clock", at: time.Now()},
		{name: "named timezone", at: time.Date(2026, 9, 8, 15, 0, 0, 123456789, time.FixedZone("reference-client", 8*60*60))},
	} {
		t.Run(captured.name, func(t *testing.T) {
			ctx := context.Background()
			repo := testRepository(t)
			target, err := repo.CommitTarget(ctx, candidateFor(t, repo, "login", windowsDesktop))
			if err != nil {
				t.Fatal(err)
			}
			if err := repo.SaveReference(ctx, ReferenceSource{ID: "reference", Name: "Desktop", ClientIdentityID: "client"}); err != nil {
				t.Fatal(err)
			}
			binding := target.Binding
			binding.ReferenceSourceID = "reference"
			if _, err := repo.SetBinding(ctx, binding); err != nil {
				t.Fatal(err)
			}
			headers := http.Header{"User-Agent": {userAgent}, "Originator": {"Codex Desktop"}}
			if err := repo.ObserveClient(ctx, "client", headers, captured.at); err != nil {
				t.Fatalf("observing a live reference request: %v", err)
			}
			updated := candidateFor(t, repo, "login", windowsDesktop)
			if updated.Profile.ClientVersion != "0.153.4" || updated.Profile.Features.DesktopBuild != "26.901.51231" {
				t.Fatalf("binding did not follow observed Desktop: %+v", updated.Profile)
			}
			if updated.Binding.Mode != ModeAuto || updated.Binding.ReferenceSourceID != "reference" {
				t.Fatalf("automatic source changed: %+v", updated.Binding)
			}
			snapshot, err := repo.Export(ctx)
			if err != nil || len(snapshot.Samples) != 1 {
				t.Fatalf("persisted samples: %v, %v", snapshot.Samples, err)
			}
			if !snapshot.Samples[0].CapturedAt.Equal(captured.at) {
				t.Fatalf("capture instant changed: %v", snapshot.Samples[0].CapturedAt)
			}
			if err := repo.ObserveClient(ctx, "client", headers, captured.at.Add(time.Second)); err != nil {
				t.Fatalf("repeated reference request: %v", err)
			}
			repeated := candidateFor(t, repo, "login", windowsDesktop)
			if repeated.Profile.ID != updated.Profile.ID {
				t.Fatal("unchanged client features created a new revision")
			}
		})
	}
}
