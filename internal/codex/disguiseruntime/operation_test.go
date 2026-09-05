package disguiseruntime

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setup(t *testing.T) (*clientdisguise.Repository, []model.Provider) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "runtime.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	connection, _ := db.DB()
	connection.SetMaxOpenConns(1)
	t.Cleanup(func() { connection.Close() })
	if err := clientdisguise.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	subject, _ := credentialsession.AccountSubject("account")
	providers := []model.Provider{{ID: "a", ClientDisguise: clientdisguise.Policy{Enabled: true}}, {ID: "b", ClientDisguise: clientdisguise.Policy{Enabled: false}}}
	for i := range providers {
		providers[i].CredentialSessions = []credentialsession.RouteSnapshot{{APIType: APIType, RouteTargetID: providers[i].ID, Credential: credentialsession.Snapshot{SessionID: "login", Subject: subject}}}
	}
	return clientdisguise.NewRepository(db), providers
}
func desktopHeaders() http.Header {
	return http.Header{"User-Agent": {"Codex Desktop/1.0.0 (Windows 11; x86_64)"}}
}
func TestLogicalBoundaryAndCrossModeSharedLogin(t *testing.T) {
	repository, providers := setup(t)
	ctx := context.Background()
	operation, err := New(ctx, repository, providers, desktopHeaders(), "op")
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := operation.Evaluate(ctx, &providers[0])
	if err != nil || !candidate.Decision.Allowed {
		t.Fatal(candidate, err)
	}
	snapshot, _ := repository.Export(ctx)
	if len(snapshot.Logins) != 0 || len(snapshot.Bindings) != 0 {
		t.Fatal("candidate evaluation wrote login")
	}
	target, err := operation.Commit(ctx, &providers[0])
	if err != nil {
		t.Fatal(err)
	}
	if target.Login.DeviceID == "" || target.Profile.Tuple.Platform != "windows" {
		t.Fatal(target)
	}
	providers[0].ClientDisguise.Enabled = false
	cached, err := operation.Commit(ctx, &providers[0])
	if err != nil || !cached.Policy.Enabled || cached.Login.DeviceID != target.Login.DeviceID {
		t.Fatal(cached, err)
	}
	disabled, err := operation.Commit(ctx, &providers[1])
	if err != nil || disabled.Policy.Enabled {
		t.Fatal(disabled, err)
	}
	current, err := New(ctx, repository, providers, desktopHeaders(), "new")
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := current.Commit(ctx, &providers[0])
	if err != nil || fresh.Policy.Enabled {
		t.Fatal(fresh, err)
	}
	providers[0].ClientDisguise.Enabled = true
	resumed, err := New(ctx, repository, providers, desktopHeaders(), "resumed")
	if err != nil {
		t.Fatal(err)
	}
	original, err := resumed.Commit(ctx, &providers[0])
	if err != nil || original.Login.DeviceID != target.Login.DeviceID {
		t.Fatal(original, err)
	}
	if operation.OperationID() != "op" || operation.Facts().Tuple.Platform != "windows" {
		t.Fatal("operation facts")
	}
}
func TestSnapshotAndConcurrentWinnerExclusion(t *testing.T) {
	repository, providers := setup(t)
	ctx := context.Background()
	windows, _ := New(ctx, repository, providers, desktopHeaders(), "windows")
	linux, _ := New(ctx, repository, providers, http.Header{"User-Agent": {"Codex Desktop/1.0.0 (Linux; x86_64)"}}, "linux")
	if _, err := windows.Commit(ctx, &providers[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := linux.Commit(ctx, &providers[0]); !errors.Is(err, clientdisguise.ErrCandidateExcluded) {
		t.Fatal(err)
	}
	candidate, err := linux.Evaluate(ctx, &providers[0])
	if err != nil || candidate.Decision.Allowed {
		t.Fatal(candidate, err)
	}
	exclusions := linux.Exclusions()
	if len(exclusions) != 1 {
		t.Fatal(exclusions)
	}
	exclusions[0].Decision.Facts.Evidence[0].Value = "mutated"
	if linux.Exclusions()[0].Decision.Facts.Evidence[0].Value == "mutated" {
		t.Fatal("evidence mutated")
	}
	frozen, _ := New(ctx, repository, providers, desktopHeaders(), "frozen")
	binding, _ := repository.SelectProfile(ctx, "login", "builtin-desktop-linux-amd64")
	if binding.Tuple.Platform != "linux" {
		t.Fatal(binding)
	}
	preserved, err := frozen.Commit(ctx, &providers[0])
	if err != nil || preserved.Profile.Tuple.Platform != "windows" {
		t.Fatal(preserved, err)
	}
	returned, ok := frozen.Target("a", "login")
	if !ok {
		t.Fatal("no target")
	}
	returned.Login.AccountBasis.Value[0] = 'X'
	retained, _ := frozen.Target("a", "login")
	if string(retained.Login.AccountBasis.Value) != "account" {
		t.Fatal("target leaked mutability")
	}
	newer, _ := New(ctx, repository, providers, desktopHeaders(), "newer")
	candidate, err = newer.Evaluate(ctx, &providers[0])
	if err != nil || candidate.Decision.Allowed {
		t.Fatal(candidate, err)
	}
}

type failingRepository struct{ cause error }

func (f failingRepository) EvaluateCandidate(context.Context, string, clientdisguise.AccountBasis, clientdisguise.Policy, clientdisguise.PlatformFacts) (clientdisguise.Candidate, error) {
	return clientdisguise.Candidate{}, f.cause
}
func (f failingRepository) CommitTarget(context.Context, clientdisguise.Candidate) (clientdisguise.TargetSnapshot, error) {
	return clientdisguise.TargetSnapshot{}, f.cause
}
func TestOperationDependencyAndLookupErrors(t *testing.T) {
	repo, providers := setup(t)
	ctx := context.Background()
	boom := errors.New("unavailable")
	if _, err := New(ctx, nil, providers, desktopHeaders(), "op"); err == nil {
		t.Fatal("nil repository")
	}
	if _, err := New(ctx, failingRepository{boom}, providers, desktopHeaders(), "op"); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	op, _ := New(ctx, repo, providers, desktopHeaders(), "op")
	if _, err := op.Evaluate(ctx, nil); err == nil {
		t.Fatal("nil provider")
	}
	other := providers[0]
	other.ID = "missing"
	if _, err := op.Evaluate(ctx, &other); err == nil {
		t.Fatal("outside snapshot")
	}
	if _, err := op.Commit(ctx, &other); err == nil {
		t.Fatal("outside commit")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := op.Evaluate(cancelled, &providers[0]); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	op.repository = failingRepository{boom}
	if _, err := op.Commit(ctx, &providers[0]); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	noSession := model.Provider{ID: "none"}
	if _, err := op.Commit(ctx, &noSession); err == nil {
		t.Fatal("missing credential")
	}
}
