package clientdisguise

import (
	"context"
	"testing"
	"time"
)

func TestReferenceTrackDuplicateWatermarkAndResume(t *testing.T) {
	r := testRepository(t)
	ctx := context.Background()
	if err := r.SaveReference(ctx, ReferenceSource{ID: "track", Name: "Track", ClientIdentityID: "client"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CommitTarget(ctx, candidateFor(t, r, "track-login", windowsDesktop)); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	learn := func(id, ua string, offset time.Duration) LearnResult {
		t.Helper()
		result, err := r.LearnSample(ctx, Sample{ID: id, SourceID: "track", CapturedAt: at.Add(offset), Tuple: windowsDesktop, ClientVersion: "1.0.0", Features: Features{UserAgent: ua}})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := learn("first", "current", 0)
	duplicate := learn("duplicate", "current", 2*time.Hour)
	if duplicate.Created {
		t.Fatal("duplicate created revision")
	}
	late := learn("late", "historical", time.Hour)
	if !late.Created || len(late.AdvancedSessions) != 0 {
		t.Fatal(late)
	}
	equal := learn("equal", "equal-time", 2*time.Hour)
	if len(equal.AdvancedSessions) != 0 {
		t.Fatal(equal)
	}
	bindings, _ := r.ListBindings(ctx)
	binding := bindings[0]
	binding.ReferenceSourceID = "track"
	binding.RevisionID = first.Revision.ID
	resumed, err := r.SetBinding(ctx, binding)
	if err != nil || resumed.RevisionID != first.Revision.ID {
		t.Fatal(resumed, err)
	}
	snapshot, err := r.Export(ctx)
	if err != nil || len(snapshot.Tracks) != 1 {
		t.Fatal(snapshot, err)
	}
	if snapshot.Tracks[0].RevisionID != first.Revision.ID || !snapshot.Tracks[0].CapturedAt.Equal(at.Add(2*time.Hour)) {
		t.Fatal(snapshot.Tracks)
	}
	restored := testRepository(t)
	if err := restored.Import(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	newer := learn("new", "next", 3*time.Hour)
	latest, _ := r.Export(ctx)
	if err := restored.Import(ctx, latest); err != nil {
		t.Fatal(err)
	}
	if err := restored.Import(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	heads, _ := restored.Export(ctx)
	if heads.Tracks[0].RevisionID != newer.Revision.ID {
		t.Fatal("old restore rewound track")
	}
	binding = resumed
	binding.Mode = ModePinned
	binding.RevisionID = late.Revision.ID
	if _, err := restored.SetBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	binding.Mode = ModeAuto
	got, err := restored.SetBinding(ctx, binding)
	if err != nil || got.RevisionID != newer.Revision.ID {
		t.Fatal(got, err)
	}
}
