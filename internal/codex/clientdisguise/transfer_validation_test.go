package clientdisguise

import (
	"context"
	"testing"
	"time"
)

func TestImportInvalidGraphRollsBackMutableState(t *testing.T) {
	ctx := context.Background()
	r := testRepository(t)
	target, err := r.CommitTarget(ctx, candidateFor(t, r, "existing", windowsDesktop))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	validTrack := ProfileTrack{SourceID: "builtin", ClientType: windowsDesktop.ClientType, Platform: windowsDesktop.Platform, Arch: windowsDesktop.Arch, ClientVersion: target.Profile.ClientVersion, RevisionID: target.Profile.ID, CapturedAt: at}
	missingLogin := target.Binding
	missingLogin.CredentialSessionID = "missing"
	missingProfile := target.Binding
	missingProfile.RevisionID = "missing"
	wrongTuple := target.Binding
	wrongTuple.Tuple.Platform = "linux"
	cases := map[string]Snapshot{
		"profile":         {Profiles: []ProfileRevision{{}}},
		"reference":       {References: []ReferenceSource{{}}},
		"transport":       {TransportSamples: []TransportSample{{}}},
		"sample":          {Samples: []Sample{{}}},
		"history":         {LoginHistory: []LoginHistory{{}}},
		"login":           {Logins: []LoginIdentity{{}}},
		"binding":         {Bindings: []ProfileBinding{{}}},
		"binding_login":   {Bindings: []ProfileBinding{missingLogin}},
		"binding_profile": {Bindings: []ProfileBinding{missingProfile}},
		"binding_tuple":   {Bindings: []ProfileBinding{wrongTuple}},
		"track":           {Tracks: []ProfileTrack{{}}},
		"mapping":         {Mappings: []Mapping{{}}},
	}
	missingTrack := validTrack
	missingTrack.RevisionID = "missing"
	cases["track_profile"] = Snapshot{Tracks: []ProfileTrack{missingTrack}}
	wrongTrack := validTrack
	wrongTrack.ClientVersion = "wrong"
	cases["track_version"] = Snapshot{Tracks: []ProfileTrack{wrongTrack}}
	for name, snapshot := range cases {
		t.Run(name, func(t *testing.T) {
			snapshot.References = append([]ReferenceSource{{ID: "rollback", Name: "Must roll back"}}, snapshot.References...)
			if err := r.Import(ctx, snapshot); err == nil {
				t.Fatal("invalid graph accepted")
			}
			refs, err := r.ListReferences(ctx)
			if err != nil || len(refs) != 0 {
				t.Fatal("partial mutable state survived", refs, err)
			}
		})
	}
}

func TestLearningOverlayPreservesAbsentFieldsAndOwnsHeaders(t *testing.T) {
	previous := Features{UserAgent: "old", Originator: "old-origin", DesktopBuild: "old-build", OSVersion: "old-os", ClientVersion: "1"}
	observed := Features{Originator: "new-origin", DesktopBuild: "new-build", OSVersion: "new-os", Headers: map[string]string{"version": "2"}}
	merged := overlayFeatures(previous, observed)
	if merged.UserAgent != "old" || merged.Originator != "new-origin" || merged.DesktopBuild != "new-build" || merged.OSVersion != "new-os" || merged.Headers["version"] != "2" {
		t.Fatal(merged)
	}
	observed.Headers["version"] = "mutated"
	if merged.Headers["version"] != "2" {
		t.Fatal("aliased headers")
	}
	for _, pair := range [][2]string{{"1-a", "1-a.1"}, {"1-1", "1-alpha"}, {"1-alpha", "1-1"}, {"1.0", "1.0.1"}} {
		if CompareVersions(pair[0], pair[1]) != -CompareVersions(pair[1], pair[0]) {
			t.Fatal(pair)
		}
	}
}
