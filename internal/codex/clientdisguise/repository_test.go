package clientdisguise

import (
	"context"
	"errors"
	"fmt"
	"github.com/doraemonkeys/switch-a/internal/codex/clientidentity"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func testRepository(t *testing.T) *Repository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "disguise.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := clientidentity.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&clientidentity.Client{ID: "client"}).Error; err != nil {
		t.Fatal(err)
	}
	return NewRepository(db)
}
func account(value string) AccountBasis { return AccountBasis{Kind: "account", Value: []byte(value)} }
func candidateFor(t *testing.T, r *Repository, session string, tuple Tuple) Candidate {
	t.Helper()
	candidate, err := r.EvaluateCandidate(context.Background(), session, account("account"), Policy{Enabled: true}, PlatformFacts{Tuple: tuple})
	if err != nil {
		t.Fatal(err)
	}
	return candidate
}

var windowsDesktop = Tuple{ClientType: "desktop", Platform: "windows", Arch: "amd64"}

func TestLoginLifecycleAndConcurrentBinding(t *testing.T) {
	r := testRepository(t)
	ctx := context.Background()
	a, err := r.SyncLoginAccount(ctx, "a", account("account"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.SyncLoginAccount(ctx, "b", account("account"))
	if err != nil {
		t.Fatal(err)
	}
	if a.DeviceID == b.DeviceID {
		t.Fatal("same-account logins share device")
	}
	refreshed, err := r.SyncLoginAccount(ctx, "a", account("account"))
	if err != nil || refreshed.DeviceID != a.DeviceID {
		t.Fatal("refresh changed device", err)
	}
	pending, err := r.SyncLoginAccount(ctx, "a", AccountBasis{Kind: "pending"})
	if err != nil || pending.DeviceID != a.DeviceID {
		t.Fatal("placeholder lost identity", err)
	}
	if _, err := r.SyncLoginAccount(ctx, "pending", AccountBasis{Kind: "pending"}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	candidate := candidateFor(t, r, "a", windowsDesktop)
	var wg sync.WaitGroup
	results := make(chan TargetSnapshot, 12)
	failures := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			target, err := r.CommitTarget(ctx, candidate)
			results <- target
			failures <- err
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	for target := range results {
		if target.Login.DeviceID != a.DeviceID || target.Profile.Tuple != windowsDesktop {
			t.Fatal(target)
		}
	}
	mapping, err := r.MapIdentity(ctx, MappingKey{GenerationID: a.GenerationID, ClientIdentityID: "client", Namespace: "thread", Original: "thread-a"})
	if err != nil {
		t.Fatal(err)
	}
	same, _ := r.MapIdentity(ctx, MappingKey{GenerationID: a.GenerationID, ClientIdentityID: "client", Namespace: "thread", Original: "thread-a"})
	other, _ := r.MapIdentity(ctx, MappingKey{GenerationID: a.GenerationID, ClientIdentityID: "another", Namespace: "thread", Original: "thread-a"})
	if mapping != same || other == mapping {
		t.Fatal("mapping scope")
	}
	original, ok, err := r.RestoreIdentity(ctx, a.GenerationID, "client", "thread", mapping)
	if err != nil || !ok || original != "thread-a" {
		t.Fatal(original, ok, err)
	}
	changed, err := r.SyncLoginAccount(ctx, "a", account("different"))
	if err != nil || changed.DeviceID == a.DeviceID {
		t.Fatal("account change preserved identity", err)
	}
	bindings, _ := r.ListBindings(ctx)
	if len(bindings) != 0 {
		t.Fatal("account change retained binding")
	}
	snapshot, _ := r.Export(ctx)
	if len(snapshot.LoginHistory) != 1 || len(snapshot.Mappings) != 2 {
		t.Fatal(snapshot)
	}
}

func TestConcurrentCandidateWinnerRechecksPlatform(t *testing.T) {
	r := testRepository(t)
	ctx := context.Background()
	windows := candidateFor(t, r, "shared", windowsDesktop)
	linux := candidateFor(t, r, "shared", Tuple{ClientType: "desktop", Platform: "linux", Arch: "amd64"})
	if _, err := r.CommitTarget(ctx, windows); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CommitTarget(ctx, linux); !errors.Is(err, ErrCandidateExcluded) {
		t.Fatal("concurrent platform mismatch should exclude", err)
	}
	unavailable, err := r.EvaluateCandidate(ctx, "new", account("account"), Policy{Enabled: true, UnknownPlatform: UnknownAllowCurrent}, PlatformFacts{})
	if err != nil || unavailable.Decision.Allowed || unavailable.Decision.Reason != "profile_unavailable" {
		t.Fatal(unavailable, err)
	}
	bound, err := r.EvaluateCandidate(ctx, "shared", account("account"), Policy{Enabled: true, UnknownPlatform: UnknownAllowCurrent}, PlatformFacts{})
	if err != nil || !bound.Decision.Allowed {
		t.Fatal(bound, err)
	}
}

func TestLearningRevisionOrderPinningAndRestore(t *testing.T) {
	r := testRepository(t)
	ctx := context.Background()
	if _, err := r.CommitTarget(ctx, candidateFor(t, r, "login", windowsDesktop)); err != nil {
		t.Fatal(err)
	}
	if err := r.SaveReference(ctx, ReferenceSource{ID: "reference", Name: "Desktop", ClientIdentityID: "client"}); err != nil {
		t.Fatal(err)
	}
	bindings, _ := r.ListBindings(ctx)
	binding := bindings[0]
	binding.ReferenceSourceID = "reference"
	binding, err := r.SetBinding(ctx, binding)
	if err != nil {
		t.Fatal(err)
	}
	captured := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	learn := func(id, version, ua string, at time.Time) LearnResult {
		t.Helper()
		result, err := r.LearnSample(ctx, Sample{ID: id, SourceID: "reference", Tuple: windowsDesktop, ClientVersion: version, CapturedAt: at, Features: Features{UserAgent: ua}})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := learn("first", "1.0.0", "UA-1", captured)
	if !first.Created || len(first.AdvancedSessions) != 1 {
		t.Fatal(first)
	}
	duplicate := learn("duplicate", "1.0.0", "UA-1", captured.Add(time.Hour))
	if duplicate.Created {
		t.Fatal("duplicate revision")
	}
	older := learn("older", "0.9.0", "UA-old", captured.Add(2*time.Hour))
	if len(older.AdvancedSessions) > 0 {
		t.Fatal("downgraded")
	}
	late := learn("late", "1.0.0", "UA-late", captured.Add(-time.Hour))
	if len(late.AdvancedSessions) > 0 {
		t.Fatal("late sample advanced")
	}
	sameVersion := learn("same-version", "1.0.0", "UA-2", captured.Add(3*time.Hour))
	if len(sameVersion.AdvancedSessions) != 1 {
		t.Fatal(sameVersion)
	}
	if _, err := r.SelectProfile(ctx, "login", first.Revision.ID); err != nil {
		t.Fatal(err)
	}
	next := learn("next", "1.1.0", "UA-3", captured.Add(4*time.Hour))
	if len(next.AdvancedSessions) > 0 {
		t.Fatal("pinned binding moved")
	}
	bindings, _ = r.ListBindings(ctx)
	binding = bindings[0]
	binding.Mode = ModeAuto
	resumed, err := r.SetBinding(ctx, binding)
	if err != nil || resumed.RevisionID != next.Revision.ID {
		t.Fatal("resume did not catch up", resumed, err)
	}
	snapshot, err := r.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	restored := testRepository(t)
	if err := restored.Import(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := restored.Import(ctx, snapshot); err != nil {
		t.Fatal("idempotent restore", err)
	}
	snapshot.Profiles[0].Features.UserAgent = "conflicting"
	if err := restored.Import(ctx, snapshot); !errors.Is(err, ErrConflict) {
		t.Fatal("conflict accepted", err)
	}
}

func TestPlatformEvidenceAndVersionSemantics(t *testing.T) {
	for _, test := range []struct {
		a, b string
		want int
	}{{"1.9", "1.10", -1}, {"1.0.0-alpha.9", "1.0.0-alpha.10", -1}, {"1.0.0", "1.0.0-beta", 1}, {"v1.0.0+build", "1.0.0", 0}, {"2", "1.99", 1}, {"1-a", "1-b", -1}, {"1-a.1", "1-a", -1}} {
		got := CompareVersions(test.a, test.b)
		// A longer prerelease identifier sequence has higher precedence.
		if test.a == "1-a.1" {
			test.want = 1
		}
		if got != test.want {
			t.Errorf("%s %s: %d", test.a, test.b, got)
		}
	}
	headers := http.Header{"User-Agent": {"codex_desktop/1.0 (Windows 11; x64)"}, "X-Stainless-Os": {"linux"}}
	facts := ProjectPlatform(headers)
	if !facts.Conflict || facts.Tuple.Platform != "windows" {
		t.Fatal(facts)
	}
	decision := EvaluatePlatform(Policy{Enabled: true}, facts, windowsDesktop)
	if decision.Allowed || decision.Reason != "platform_conflict" {
		t.Fatal(decision)
	}
	for _, platform := range []string{"Linux", "Windows"} {
		facts := ProjectPlatform(http.Header{"User-Agent": {fmt.Sprintf("codex_cli/1.0 (%s; x86_64)", platform)}})
		if facts.Tuple.Platform == "" {
			t.Fatal(facts)
		}
	}
	disabled := false
	if !EvaluatePlatform(Policy{Enabled: true, MatchPlatform: &disabled}, facts, windowsDesktop).Allowed {
		t.Fatal("disabled matching excluded")
	}
	if err := validateFeatures(Features{Headers: map[string]string{"Cookie": "injected"}}); err == nil {
		t.Fatal("cookie profile accepted")
	}
}
