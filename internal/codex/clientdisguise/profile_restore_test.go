package clientdisguise

import (
	"context"
	"testing"
	"time"
)

func TestAutoRestoreReconcilesWinningTrackBeforeDuplicateObservation(t *testing.T) {
	for _, version := range []string{"1.0.0", "2.0.0"} {
		t.Run(version, func(t *testing.T) {
			r := testRepository(t)
			ctx := context.Background()
			if err := r.SaveReference(ctx, ReferenceSource{ID: "source", Name: "Source", ClientIdentityID: "client"}); err != nil {
				t.Fatal(err)
			}
			target, err := r.CommitTarget(ctx, candidateFor(t, r, "login", windowsDesktop))
			if err != nil {
				t.Fatal(err)
			}
			binding := target.Binding
			binding.ReferenceSourceID = "source"
			if _, err := r.SetBinding(ctx, binding); err != nil {
				t.Fatal(err)
			}
			at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			learn := func(id, version, ua string, hour time.Duration) LearnResult {
				t.Helper()
				result, err := r.LearnSample(ctx, Sample{ID: id, SourceID: "source", CapturedAt: at.Add(hour * time.Hour), Tuple: windowsDesktop, ClientVersion: version, Features: Features{UserAgent: ua}})
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
			first := learn("first", "1.0.0", "first", 0)
			old, err := r.Export(ctx)
			if err != nil {
				t.Fatal(err)
			}
			current := learn("current", version, "current", 1)
			duplicate := learn("duplicate", version, "current", 3)
			if duplicate.Created {
				t.Fatal("duplicate revision")
			}
			if err := r.Import(ctx, old); err != nil {
				t.Fatal(err)
			}
			assertCurrent := func() {
				t.Helper()
				bindings, err := r.ListBindings(ctx)
				if err != nil || bindings[0].RevisionID != current.Revision.ID {
					t.Fatal("auto restore stranded binding", bindings, err)
				}
			}
			assertCurrent()
			late := learn("late", version, "late", 2)
			if len(late.AdvancedSessions) != 0 {
				t.Fatal("late sample advanced", late)
			}
			learn("duplicate-after-restore", version, "current", 4)
			assertCurrent()
			head, err := loadProfileTrack(r.db, "source", windowsDesktop)
			if err != nil || !head.CapturedAt.Equal(at.Add(4*time.Hour)) {
				t.Fatal(head, err)
			}
			old.Bindings[0].Mode = ModePinned
			if err := r.Import(ctx, old); err != nil {
				t.Fatal(err)
			}
			bindings, _ := r.ListBindings(ctx)
			if bindings[0].RevisionID != first.Revision.ID {
				t.Fatal("pinned restore advanced", bindings)
			}
		})
	}
}

func TestAutoRestoreUsesSelectedSourceAndAdvancesExistingBindings(t *testing.T) {
	r := testRepository(t)
	ctx := context.Background()
	for _, id := range []string{"a", "b", "empty"} {
		if err := r.SaveReference(ctx, ReferenceSource{ID: id, Name: id, ClientIdentityID: "client"}); err != nil {
			t.Fatal(err)
		}
	}
	target, err := r.CommitTarget(ctx, candidateFor(t, r, "login", windowsDesktop))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	learn := func(source, version string) ProfileRevision {
		t.Helper()
		result, err := r.LearnSample(ctx, Sample{ID: source + version, SourceID: source, Tuple: windowsDesktop, CapturedAt: at, ClientVersion: version, Features: Features{UserAgent: source + version}})
		if err != nil {
			t.Fatal(err)
		}
		return result.Revision
	}
	a := learn("a", "1.0.0")
	b := learn("b", "1.0.0")
	binding := target.Binding
	binding.ReferenceSourceID = "a"
	binding.RevisionID = a.ID
	if _, err := r.SetBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, source, revision, want string
		mode                         string
	}{
		{"selected source", "b", a.ID, b.ID, ModeAuto},
		{"no source track", "empty", a.ID, a.ID, ModeAuto},
		{"pinned explicit", "b", a.ID, a.ID, ModePinned},
	} {
		t.Run(test.name, func(t *testing.T) {
			incoming := binding
			incoming.ReferenceSourceID = test.source
			incoming.Mode = test.mode
			incoming.RevisionID = test.revision
			if err := r.Import(ctx, Snapshot{Bindings: []ProfileBinding{incoming}}); err != nil {
				t.Fatal(err)
			}
			bindings, _ := r.ListBindings(ctx)
			if bindings[0].RevisionID != test.want {
				t.Fatal(bindings)
			}
		})
	}
	newer := learn("a", "2.0.0")
	binding.ReferenceSourceID = "a"
	binding.RevisionID = newer.ID
	if _, err := r.SetBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	incoming := binding
	incoming.ReferenceSourceID = "b"
	if err := r.Import(ctx, Snapshot{Bindings: []ProfileBinding{incoming}}); err != nil {
		t.Fatal(err)
	}
	bindings, _ := r.ListBindings(ctx)
	if bindings[0].RevisionID != newer.ID {
		t.Fatal("source selection downgraded client version", bindings)
	}
	// Importing a head without importing its login configuration must also update
	// live auto bindings that already follow that source.
	binding.ReferenceSourceID = "a"
	binding.RevisionID = a.ID
	sourceState, _ := r.Export(ctx)
	destination := testRepository(t)
	initial := sourceState
	initial.Tracks = nil
	initial.Bindings = []ProfileBinding{binding}
	if err := destination.Import(ctx, initial); err != nil {
		t.Fatal(err)
	}
	if err := destination.Import(ctx, Snapshot{Tracks: sourceState.Tracks}); err != nil {
		t.Fatal(err)
	}
	bindings, _ = destination.ListBindings(ctx)
	if bindings[0].RevisionID != newer.ID {
		t.Fatal("imported head left existing auto binding behind", bindings)
	}
}
