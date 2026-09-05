package clientdisguise

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestRetirementPreservesHistoricalMappingAndRestores(t *testing.T) {
	ctx := context.Background()
	r := testRepository(t)
	target, err := r.CommitTarget(ctx, candidateFor(t, r, "retire", windowsDesktop))
	if err != nil {
		t.Fatal(err)
	}
	key := MappingKey{GenerationID: target.Login.GenerationID, ClientIdentityID: "client", Namespace: "thread", Original: "original"}
	mapped, err := r.MapIdentity(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.RetireLogin(ctx, "retire"); err != nil {
		t.Fatal(err)
	}
	if err := r.RetireLogin(ctx, "retire"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.GetLogin(ctx, "retire"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	snapshot, err := r.Export(ctx)
	if err != nil || len(snapshot.Logins) != 0 || len(snapshot.Bindings) != 0 || len(snapshot.LoginHistory) != 1 || len(snapshot.Mappings) != 1 {
		t.Fatal(snapshot, err)
	}
	restored := testRepository(t)
	if err := restored.Import(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	original, found, err := restored.RestoreIdentity(ctx, key.GenerationID, key.ClientIdentityID, key.Namespace, mapped)
	if err != nil || !found || original != "original" {
		t.Fatal(original, found, err)
	}
}
func TestPlatformTupleRecognitionAndCandidateAccountRace(t *testing.T) {
	facts := ProjectPlatform(http.Header{"User-Agent": {"codex-tui/1.0 (Darwin; aarch64)"}, "Originator": {"codex-tui"}})
	if facts.Tuple != (Tuple{ClientType: "tui", Platform: "macos", Arch: "arm64"}) {
		t.Fatal(facts)
	}
	unknown := ProjectPlatform(http.Header{"User-Agent": {"custom unknown"}})
	if unknown.Tuple.Valid() {
		t.Fatal(unknown)
	}
	ctx := context.Background()
	r := testRepository(t)
	candidate := candidateFor(t, r, "race", windowsDesktop)
	if _, err := r.SyncLoginAccount(ctx, "race", account("changed")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CommitTarget(ctx, candidate); !errors.Is(err, ErrAccountChanged) {
		t.Fatal(err)
	}
	if _, err := r.CommitTarget(ctx, Candidate{Policy: Policy{Enabled: true}}); !errors.Is(err, ErrCandidateExcluded) {
		t.Fatal(err)
	}
	if _, err := r.SelectProfile(ctx, "missing", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
