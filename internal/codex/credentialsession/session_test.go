package credentialsession

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestSubjectAndSessionValidation(t *testing.T) {
	account, err := AccountSubject(" account-1 ")
	if err != nil || string(account.Value) != "account-1" || !account.Resolved() {
		t.Fatalf("AccountSubject() = (%#v, %v)", account, err)
	}
	if _, err := AccountSubject(" "); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("AccountSubject(blank) error = %v", err)
	}
	if !account.Equal(account.Clone()) || account.Equal(Subject{Kind: SubjectAccount, Value: []byte("account-2")}) {
		t.Fatal("account subject equality did not preserve the stable identity boundary")
	}
	digest := bytes.Repeat([]byte{7}, staticSubjectDigestSize)
	keyed, err := KeyedDigestSubject(" h1 ", digest)
	if err != nil || keyed.KeyVersion != "h1" || !keyed.Resolved() {
		t.Fatalf("KeyedDigestSubject() = (%#v, %v)", keyed, err)
	}
	digest[0] = 9
	if keyed.Value[0] != 7 {
		t.Fatal("KeyedDigestSubject retained caller-owned bytes")
	}
	changedKeyVersion := keyed.Clone()
	changedKeyVersion.KeyVersion = "h2"
	if keyed.Equal(changedKeyVersion) {
		t.Fatal("keyed subjects with different key versions compared equal")
	}
	for _, subject := range []Subject{
		{Kind: SubjectPending, Value: []byte("x")},
		{Kind: SubjectAccount},
		{Kind: SubjectAccount, Value: []byte(" account-1 ")},
		{Kind: SubjectAccount, Value: []byte("x"), KeyVersion: "h1"},
		{Kind: SubjectKeyedDigest, Value: []byte("short"), KeyVersion: "h1"},
		{Kind: "unknown"},
	} {
		if err := subject.Validate(); !errors.Is(err, ErrInvalidSession) {
			t.Fatalf("Subject.Validate(%#v) error = %v", subject, err)
		}
	}
	if PendingSubject().Resolved() {
		t.Fatal("pending subject resolved")
	}
	if _, err := KeyedDigestSubject("", make([]byte, staticSubjectDigestSize)); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("KeyedDigestSubject(blank version) error = %v", err)
	}

	now := time.Date(2026, 8, 27, 1, 2, 3, 0, time.UTC)
	session := &Session{ID: "s1", Kind: KindChatGPT, SecretData: "secret", Version: 1,
		AuthState: AuthState{Status: AuthStatusActive, LastTransitionAt: &now, UsageSnapshot: &UsageSnapshot{FetchedAt: &now}}}
	if err := session.SetSubject(account); err != nil {
		t.Fatal(err)
	}
	if err := session.Validate(); err != nil {
		t.Fatal(err)
	}
	mismatchedAccount := session.Clone()
	mismatchedAccount.AuthState.AccountID = "different-account"
	if err := mismatchedAccount.Validate(); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("Validate(mismatched account) = %v, want ErrInvalidSession", err)
	}
	recovery := &Session{
		ID: "recovery", Kind: KindChatGPT, Version: 1,
		SubjectKind: SubjectPending,
		AuthState:   AuthState{Status: AuthStatusReauthRequired, AccountID: "diagnostic-only"},
	}
	if err := recovery.Validate(); err != nil {
		t.Fatalf("Validate(recovery pending) = %v", err)
	}
	if recovery.HasCredentialMaterial() || !recovery.IsReauthenticationPlaceholder() {
		t.Fatalf("recovery material/placeholder = %t/%t", recovery.HasCredentialMaterial(), recovery.IsReauthenticationPlaceholder())
	}
	if snapshot, err := recovery.Snapshot(); err != nil || !errors.Is(snapshot.RequireResolvedSubject(), ErrSubjectPending) {
		t.Fatalf("Snapshot(recovery pending) = (%#v, %v)", snapshot, err)
	}
	activePending := recovery.Clone()
	activePending.AuthState.Status = AuthStatusActive
	if err := activePending.Validate(); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("Validate(active pending) = %v, want ErrInvalidSession", err)
	}
	clone := session.Clone()
	clone.SubjectValue[0] = 'X'
	*clone.AuthState.LastTransitionAt = now.Add(time.Hour)
	*clone.AuthState.UsageSnapshot.FetchedAt = now.Add(time.Hour)
	if string(session.SubjectValue) != "account-1" || !session.AuthState.LastTransitionAt.Equal(now) || !session.AuthState.UsageSnapshot.FetchedAt.Equal(now) {
		t.Fatal("Session.Clone() did not deep copy mutable fields")
	}
	snapshot, err := session.Snapshot()
	if err != nil || snapshot.SessionID != session.ID || snapshot.RequireResolvedSubject() != nil {
		t.Fatalf("Snapshot() = (%#v, %v)", snapshot, err)
	}
	snapshot.Subject = PendingSubject()
	if !errors.Is(snapshot.RequireResolvedSubject(), ErrSubjectPending) {
		t.Fatalf("RequireResolvedSubject() error = %v", snapshot.RequireResolvedSubject())
	}
	if (&Session{}).Validate() == nil || (*Session)(nil).Validate() == nil || (*Session)(nil).SetSubject(account) == nil {
		t.Fatal("invalid sessions unexpectedly passed validation")
	}
	for _, invalid := range []*Session{
		{ID: "s", Kind: KindAPIKey, SecretData: "", Version: 1, SubjectKind: SubjectPending},
		{ID: "s", Kind: KindAPIKey, SecretData: "secret", Version: 0, SubjectKind: SubjectPending},
		{ID: "s", Kind: KindAPIKey, SecretData: "secret", Version: 1, SubjectKind: "bad"},
		{ID: "s", Kind: KindAPIKey, SecretData: "secret", Version: 1, SubjectKind: SubjectAccount, SubjectValue: []byte("account-1")},
		{ID: "s", Kind: KindChatGPT, SecretData: "", Version: 1, SubjectKind: SubjectAccount, SubjectValue: []byte("account-1"), AuthState: AuthState{Status: AuthStatusActive}},
		{ID: "s", Kind: KindChatGPT, SecretData: "secret", Version: 1, SubjectKind: SubjectKeyedDigest, SubjectValue: keyed.Value, SubjectKeyVersion: keyed.KeyVersion},
	} {
		if !errors.Is(invalid.Validate(), ErrInvalidSession) {
			t.Fatalf("Session.Validate(%#v) succeeded", invalid)
		}
		if _, err := invalid.Snapshot(); err == nil {
			t.Fatalf("Session.Snapshot(%#v) succeeded", invalid)
		}
	}
	if (*Session)(nil).Subject().Kind != "" || (*Session)(nil).Clone() != nil {
		t.Fatal("nil session helpers returned non-zero values")
	}
}

func TestAuthenticationNormalizationAndStaticSubjectEncoding(t *testing.T) {
	state := NormalizeAuthState(KindChatGPT, AuthState{
		Status: "invalid", StatusReason: " reason ", LastError: " error ", Email: " e@example.com ",
		AccountID: " account ", RefreshFailCount: -2, UsageSnapshot: &UsageSnapshot{PlanType: " pro "},
	})
	if state.Status != AuthStatusNotConnected || state.StatusReason != "reason" || state.Email != "e@example.com" || state.PlanType != "pro" || state.RefreshFailCount != 0 {
		t.Fatalf("NormalizeAuthState() = %#v", state)
	}
	if DefaultAuthStatus(KindAPIKey) != AuthStatusActive || !IsValidAuthStatus(AuthStatusReauthRequired) || IsValidAuthStatus("bad") {
		t.Fatal("auth status helpers returned invalid result")
	}
	if !IsValidKind(KindAPIKey) || IsValidKind("bad") {
		t.Fatal("kind validation returned invalid result")
	}
	one, err := StaticSubjectInput(KindAPIKey, "ab:c")
	if err != nil {
		t.Fatal(err)
	}
	two, _ := StaticSubjectInput(KindAPIKey, "ab:c")
	other, _ := StaticSubjectInput(KindAPIKey, "ab")
	if !bytes.Equal(one, two) || bytes.Equal(one, other) {
		t.Fatal("StaticSubjectInput is not deterministic and unambiguous")
	}
	if _, err := StaticSubjectInput(KindChatGPT, "secret"); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("StaticSubjectInput(chatgpt) error = %v", err)
	}
	if (RouteBinding{}).Validate() == nil || (RouteBinding{RouteTargetID: "p", APIType: "codex", SessionID: "s"}).Validate() != nil {
		t.Fatal("RouteBinding validation returned invalid result")
	}
	reset := time.Now()
	clone := (AuthState{UsageSnapshot: &UsageSnapshot{
		FiveHour: &UsageWindow{ResetAt: &reset}, OneWeek: &UsageWindow{ResetAt: &reset},
	}}).Clone()
	*clone.UsageSnapshot.FiveHour.ResetAt = reset.Add(time.Hour)
	if clone.UsageSnapshot.OneWeek == nil || clone.UsageSnapshot.FiveHour.ResetAt.Equal(*clone.UsageSnapshot.OneWeek.ResetAt) {
		t.Fatal("AuthState.Clone did not independently clone usage windows")
	}
}

type repositoryTestClock struct{ now time.Time }

func (c repositoryTestClock) Now() time.Time { return c.now }

type repositoryTestIDs struct{ next int }

func (g *repositoryTestIDs) NewID() string {
	g.next++
	return fmt.Sprintf("generated-%d", g.next)
}

func newRepositoryTestDB(t *testing.T) (*gorm.DB, *Repository, repositoryTestClock) {
	t.Helper()
	// A file in the test's private temporary directory keeps every invocation,
	// including -count repetitions, independent across the full connection pool.
	databasePath := filepath.Join(t.TempDir(), "repository.sqlite")
	db, err := gorm.Open(sqlite.Open(databasePath), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	// Cleanup is registered after TempDir so the pool closes before Go removes
	// the database directory, which also avoids leaking handles between repeats.
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close repository test database: %v", err)
		}
	})
	for _, statement := range []string{
		`CREATE TABLE providers (id TEXT PRIMARY KEY, vendor TEXT NOT NULL)`,
		`CREATE TABLE provider_api_types (provider_id TEXT NOT NULL, api_type TEXT NOT NULL, PRIMARY KEY(provider_id, api_type))`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.AutoMigrate(&Session{}, &RouteBinding{}); err != nil {
		t.Fatal(err)
	}
	clock := repositoryTestClock{now: time.Date(2026, 8, 27, 2, 3, 4, 0, time.UTC)}
	repo, err := NewRepository(db, clock, &repositoryTestIDs{})
	if err != nil {
		t.Fatal(err)
	}
	return db, repo, clock
}

func newRepositoryTestSession(t *testing.T, id string, subject Subject) *Session {
	t.Helper()
	session := &Session{ID: id, Kind: KindAPIKey, SecretData: "secret", Version: 1, AuthState: AuthState{Status: AuthStatusActive}}
	if err := session.SetSubject(subject); err != nil {
		t.Fatal(err)
	}
	return session
}

func TestRepositorySharedSessionLifecycleAndCAS(t *testing.T) {
	db, repo, clock := newRepositoryTestDB(t)
	if _, err := NewRepository(nil, clock, nil); err == nil {
		t.Fatal("NewRepository(nil) succeeded")
	}
	if _, err := NewRepository(db, nil, nil); err == nil {
		t.Fatal("NewRepository(nil clock) succeeded")
	}
	defaultIDs, err := NewRepository(db, clock, nil)
	if err != nil || defaultIDs.ids.NewID() == "" {
		t.Fatalf("default ID generator = (%#v, %v)", defaultIDs, err)
	}
	var nilRepo *Repository
	if _, err := nilRepo.WithDB(db); err == nil {
		t.Fatal("nil Repository.WithDB succeeded")
	}
	if _, err := repo.Create(context.Background(), nil); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("Create(nil) error = %v", err)
	}

	digest, _ := KeyedDigestSubject("h1", bytes.Repeat([]byte{1}, staticSubjectDigestSize))
	session := newRepositoryTestSession(t, "", digest)
	created, err := repo.Create(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "generated-1" ||
		!created.CreatedAt.Equal(clock.now) ||
		!created.UpdatedAt.Equal(clock.now) ||
		created.Version != 1 {
		t.Fatalf("Create() = %#v", created)
	}
	created.SecretData = "caller mutation"
	stored, err := repo.Get(context.Background(), "generated-1")
	if err != nil || stored.SecretData != "secret" {
		t.Fatalf("Get() = (%#v, %v)", stored, err)
	}
	if _, err := repo.Get(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(missing) error = %v", err)
	}
	listed, err := repo.List(context.Background())
	if err != nil || len(listed) != 1 {
		t.Fatalf("List() = (%#v, %v)", listed, err)
	}

	if err := db.Exec(`INSERT INTO providers(id,vendor) VALUES ('p1','openai'),('p2','openai'),('cross-vendor','anthropic'),('blank-vendor','')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO provider_api_types(provider_id,api_type) VALUES ('p1','codex'),('p1','responses'),('p2','codex'),('cross-vendor','codex'),('blank-vendor','codex')`).Error; err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := repo.Bind(ctx, RouteBinding{RouteTargetID: "missing", APIType: "codex", SessionID: created.ID}); !errors.Is(err, ErrInvalidRouteBinding) {
		t.Fatalf("Bind(missing route) error = %v", err)
	}
	if err := repo.Bind(ctx, RouteBinding{}); !errors.Is(err, ErrInvalidRouteBinding) {
		t.Fatalf("Bind(invalid) error = %v", err)
	}
	if err := repo.Bind(ctx, RouteBinding{RouteTargetID: "p1", APIType: "codex", SessionID: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Bind(missing session) error = %v", err)
	}
	for routeTargetID, wantVendorScope := range map[string]string{"cross-vendor": "anthropic", "blank-vendor": ""} {
		if err := repo.Bind(ctx, RouteBinding{RouteTargetID: routeTargetID, APIType: "codex", SessionID: created.ID}); err != nil {
			t.Fatalf("Bind(%s) error = %v", routeTargetID, err)
		}
		resolvedRoute, err := repo.Resolve(ctx, routeTargetID, "codex")
		if err != nil || resolvedRoute.VendorScope != wantVendorScope {
			t.Fatalf("Resolve(%s) = (%#v, %v), want vendor scope %q", routeTargetID, resolvedRoute, err, wantVendorScope)
		}
		if err := repo.DeleteRouteBindings(ctx, routeTargetID); err != nil {
			t.Fatal(err)
		}
	}
	for _, binding := range []RouteBinding{
		{RouteTargetID: "p1", APIType: "codex", SessionID: created.ID},
		{RouteTargetID: "p2", APIType: "codex", SessionID: created.ID},
	} {
		if err := repo.Bind(ctx, binding); err != nil {
			t.Fatal(err)
		}
	}
	resolved, err := repo.Resolve(ctx, "p1", "codex")
	if err != nil || resolved.Credential.SessionID != created.ID {
		t.Fatalf("Resolve() = (%#v, %v)", resolved, err)
	}
	if _, err := repo.Resolve(ctx, "p1", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve(missing) error = %v", err)
	}
	snapshots, err := repo.ListRouteSnapshots(ctx, []string{"p2", "p1"})
	if err != nil || len(snapshots["p1"]) != 1 || len(snapshots["p2"]) != 1 {
		t.Fatalf("ListRouteSnapshots() = (%#v, %v)", snapshots, err)
	}
	ids, err := repo.ListRouteTargetIDs(ctx, created.ID)
	if err != nil || !reflect.DeepEqual(ids, []string{"p1", "p2"}) {
		t.Fatalf("ListRouteTargetIDs() = (%#v, %v)", ids, err)
	}
	if err := repo.DeleteIfUnreferenced(ctx, created.ID); !errors.Is(err, ErrSessionReferenced) {
		t.Fatalf("DeleteIfUnreferenced(referenced) error = %v", err)
	}

	next, err := repo.UpdateCredentialCAS(ctx, created.ID, 1, "rotated", digest, AuthState{Status: AuthStatusActive})
	if err != nil || next != 2 {
		t.Fatalf("UpdateCredentialCAS() = (%d, %v)", next, err)
	}
	if _, err := repo.UpdateCredentialCAS(ctx, created.ID, 1, "stale", digest, AuthState{}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("UpdateCredentialCAS(stale) error = %v", err)
	}
	if _, err := repo.UpdateCredentialCAS(ctx, "missing", 1, "secret", digest, AuthState{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateCredentialCAS(missing) error = %v", err)
	}
	if _, err := repo.UpdateCredentialCAS(ctx, created.ID, 0, "", digest, AuthState{}); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("UpdateCredentialCAS(invalid) error = %v", err)
	}
	accountSubject, _ := AccountSubject("account-1")
	if _, err := repo.UpdateCredentialCAS(ctx, created.ID, 2, "rotated", accountSubject, AuthState{}); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("UpdateCredentialCAS(cross-kind subject) error = %v", err)
	}
	next, err = repo.UpdateAuthStateCAS(ctx, created.ID, 2, AuthState{Status: AuthStatusReauthRequired, StatusReason: " invalid_grant "})
	if err != nil || next != 3 {
		t.Fatalf("UpdateAuthStateCAS() = (%d, %v)", next, err)
	}
	if _, err := repo.UpdateAuthStateCAS(ctx, created.ID, 0, AuthState{}); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("UpdateAuthStateCAS(invalid) error = %v", err)
	}
	if _, err := repo.UpdateAuthStateCAS(ctx, "missing", 1, AuthState{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateAuthStateCAS(missing) error = %v", err)
	}
	if _, err := repo.UpdateAuthStateCAS(ctx, created.ID, 2, AuthState{}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("UpdateAuthStateCAS(stale) error = %v", err)
	}

	if err := repo.DeleteRouteBindings(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteRouteBindings(ctx, "p2"); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteIfUnreferenced(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteIfUnreferenced(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteIfUnreferenced(missing) error = %v", err)
	}
}

func TestRepositoryCASPersistsUsageSnapshot(t *testing.T) {
	_, repo, _ := newRepositoryTestDB(t)
	ctx := context.Background()
	subject, err := AccountSubject("account-1")
	if err != nil {
		t.Fatal(err)
	}
	session := &Session{
		ID: "chatgpt-usage", Kind: KindChatGPT, SecretData: "secret", Version: 1,
		AuthState: AuthState{Status: AuthStatusActive, AccountID: "account-1"},
	}
	if err := session.SetSubject(subject); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(ctx, session); err != nil {
		t.Fatal(err)
	}

	firstFetchedAt := time.Date(2026, 8, 27, 3, 4, 5, 0, time.UTC)
	firstState := AuthState{
		Status: AuthStatusActive, AccountID: "account-1", PlanType: "pro",
		UsageSnapshot: &UsageSnapshot{
			FetchedAt: &firstFetchedAt, PlanType: "pro",
			FiveHour: &UsageWindow{UsedPercent: 12.5, WindowSeconds: 18_000},
		},
	}
	version, err := repo.UpdateAuthStateCAS(ctx, session.ID, 1, firstState)
	if err != nil || version != 2 {
		t.Fatalf("UpdateAuthStateCAS() = (%d, %v)", version, err)
	}
	stored, err := repo.Get(ctx, session.ID)
	if err != nil || !reflect.DeepEqual(stored.AuthState.UsageSnapshot, firstState.UsageSnapshot) {
		t.Fatalf("usage after UpdateAuthStateCAS = (%#v, %v), want %#v", stored, err, firstState.UsageSnapshot)
	}

	secondFetchedAt := firstFetchedAt.Add(time.Hour)
	secondState := firstState.Clone()
	secondState.UsageSnapshot = &UsageSnapshot{
		FetchedAt: &secondFetchedAt, PlanType: "pro",
		OneWeek: &UsageWindow{UsedPercent: 37.5, WindowSeconds: 604_800},
	}
	version, err = repo.UpdateCredentialCAS(ctx, session.ID, 2, "rotated", subject, secondState)
	if err != nil || version != 3 {
		t.Fatalf("UpdateCredentialCAS() = (%d, %v)", version, err)
	}
	stored, err = repo.Get(ctx, session.ID)
	if err != nil || stored.SecretData != "rotated" || !reflect.DeepEqual(stored.AuthState.UsageSnapshot, secondState.UsageSnapshot) {
		t.Fatalf("session after UpdateCredentialCAS = (%#v, %v), want usage %#v", stored, err, secondState.UsageSnapshot)
	}
}

func TestRepositoryCreatePreservesProvidedTimestamps(t *testing.T) {
	_, repo, clock := newRepositoryTestDB(t)
	digest, err := KeyedDigestSubject("h1", bytes.Repeat([]byte{1}, staticSubjectDigestSize))
	if err != nil {
		t.Fatal(err)
	}
	session := newRepositoryTestSession(t, "imported", digest)
	session.CreatedAt = clock.now.Add(-2 * time.Hour)
	session.UpdatedAt = clock.now.Add(-time.Hour)

	created, err := repo.Create(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if !created.CreatedAt.Equal(session.CreatedAt) || !created.UpdatedAt.Equal(session.UpdatedAt) {
		t.Fatalf("Create() timestamps = (%v, %v), want (%v, %v)", created.CreatedAt, created.UpdatedAt, session.CreatedAt, session.UpdatedAt)
	}
}

func TestRepositoryRejectsChatGPTAccountMismatchDuringCAS(t *testing.T) {
	_, repo, _ := newRepositoryTestDB(t)
	subject, err := AccountSubject("account-1")
	if err != nil {
		t.Fatal(err)
	}
	session := &Session{
		ID: "chatgpt-session", Kind: KindChatGPT,
		SecretData: "secret", Version: 1,
		AuthState: AuthState{Status: AuthStatusActive, AccountID: "account-1"},
	}
	if err := session.SetSubject(subject); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(context.Background(), session); err != nil {
		t.Fatal(err)
	}

	mismatched := AuthState{Status: AuthStatusActive, AccountID: "account-2"}
	if _, err := repo.UpdateCredentialCAS(context.Background(), session.ID, 1, "rotated", subject, mismatched); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("UpdateCredentialCAS(mismatched account) error = %v", err)
	}
	if _, err := repo.UpdateAuthStateCAS(context.Background(), session.ID, 1, mismatched); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("UpdateAuthStateCAS(mismatched account) error = %v", err)
	}
	stored, err := repo.Get(context.Background(), session.ID)
	if err != nil || stored.Version != 1 || stored.SecretData != "secret" {
		t.Fatalf("mismatched CAS mutated session: (%#v, %v)", stored, err)
	}
}

func TestRepositoryRequiresProvenSubjectWhenRecoveryBecomesActive(t *testing.T) {
	_, repo, _ := newRepositoryTestDB(t)
	recovery := &Session{
		ID: "chatgpt-recovery", Kind: KindChatGPT,
		SecretData: "recovery-secret", Version: 1, SubjectKind: SubjectPending,
		AuthState: AuthState{Status: AuthStatusReauthRequired, AccountID: "diagnostic-only"},
	}
	if _, err := repo.Create(context.Background(), recovery); err != nil {
		t.Fatal(err)
	}
	active := AuthState{Status: AuthStatusActive, AccountID: "account-1"}
	if _, err := repo.UpdateAuthStateCAS(context.Background(), recovery.ID, 1, active); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("UpdateAuthStateCAS(active pending) error = %v", err)
	}
	account, err := AccountSubject("account-1")
	if err != nil {
		t.Fatal(err)
	}
	version, err := repo.UpdateCredentialCAS(context.Background(), recovery.ID, 1, "proven-secret", account, active)
	if err != nil || version != 2 {
		t.Fatalf("UpdateCredentialCAS(proven subject) = (%d, %v)", version, err)
	}
	stored, err := repo.Get(context.Background(), recovery.ID)
	if err != nil || stored.SubjectKind != SubjectAccount || string(stored.SubjectValue) != "account-1" || stored.AuthState.Status != AuthStatusActive {
		t.Fatalf("restored session = (%#v, %v)", stored, err)
	}
}

func TestRepositoryReplaceBindingsAndPendingResolution(t *testing.T) {
	db, repo, _ := newRepositoryTestDB(t)
	if err := db.Exec(`INSERT INTO providers(id,vendor) VALUES ('p1','openai')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO provider_api_types(provider_id,api_type) VALUES ('p1','codex'),('p1','responses')`).Error; err != nil {
		t.Fatal(err)
	}
	pending := newRepositoryTestSession(t, "pending", PendingSubject())
	if _, err := repo.Create(context.Background(), pending); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := repo.ReplaceRouteBindings(ctx, " ", nil); !errors.Is(err, ErrInvalidRouteBinding) {
		t.Fatalf("ReplaceRouteBindings(blank) error = %v", err)
	}
	if empty, err := repo.ListRouteSnapshots(ctx, nil); err != nil || len(empty) != 0 {
		t.Fatalf("ListRouteSnapshots(empty) = (%#v, %v)", empty, err)
	}
	bindings := []RouteBinding{{APIType: "codex", SessionID: "pending"}, {APIType: "responses", SessionID: "pending"}}
	if err := repo.ReplaceRouteBindings(ctx, "p1", bindings); err != nil {
		t.Fatal(err)
	}
	if err := repo.ReplaceRouteBindings(ctx, "p1", []RouteBinding{{APIType: "codex", SessionID: "pending"}, {APIType: "codex", SessionID: "pending"}}); !errors.Is(err, ErrInvalidRouteBinding) {
		t.Fatalf("ReplaceRouteBindings(duplicate) error = %v", err)
	}
	snapshots, _ := repo.ListRouteSnapshots(ctx, []string{"p1"})
	if len(snapshots["p1"]) != 2 {
		t.Fatalf("duplicate replacement was not rolled back: %#v", snapshots)
	}
	resolved, _ := KeyedDigestSubject("h1", bytes.Repeat([]byte{1}, staticSubjectDigestSize))
	if err := repo.ResolvePendingSubject(ctx, "pending", resolved); err != nil {
		t.Fatal(err)
	}
	if err := repo.ResolvePendingSubject(ctx, "pending", resolved); err != nil {
		t.Fatalf("idempotent ResolvePendingSubject() error = %v", err)
	}
	other, _ := KeyedDigestSubject("h1", bytes.Repeat([]byte{2}, staticSubjectDigestSize))
	if err := repo.ResolvePendingSubject(ctx, "pending", other); err == nil {
		t.Fatal("ResolvePendingSubject(different) succeeded")
	}
	if err := repo.ResolvePendingSubject(ctx, "pending", PendingSubject()); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("ResolvePendingSubject(pending) error = %v", err)
	}
	accountSubject, _ := AccountSubject("account-1")
	if err := repo.ResolvePendingSubject(ctx, "pending", accountSubject); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("ResolvePendingSubject(cross-kind) error = %v", err)
	}
	if err := repo.ResolvePendingSubject(ctx, "missing", resolved); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ResolvePendingSubject(missing) error = %v", err)
	}
}

func TestMutationCoordinatorSerializesAndScopesOwnership(t *testing.T) {
	var nilCoordinator *MutationCoordinator
	if _, _, err := nilCoordinator.With(context.Background(), []string{"s"}); err == nil || nilCoordinator.Owns(context.Background(), "s") {
		t.Fatal("nil coordinator accepted mutation")
	}
	coordinator := NewMutationCoordinator()
	if _, _, err := coordinator.With(nil, []string{"s"}); err == nil {
		t.Fatal("With(nil context) succeeded")
	}
	if _, _, err := coordinator.With(context.Background(), []string{" "}); err == nil {
		t.Fatal("With(blank session) succeeded")
	}
	owned, release, err := coordinator.With(context.Background(), []string{" b ", "a", "b"})
	if err != nil || !coordinator.Owns(owned, "a") || !coordinator.Owns(owned, "b") {
		t.Fatalf("With() ownership = (%v, %v)", coordinator.Owns(owned, "a"), err)
	}
	nested, nestedRelease, err := coordinator.With(owned, []string{"a"})
	if err != nil || nested != owned {
		t.Fatalf("nested With() = (%v, %v)", nested, err)
	}
	nestedRelease()
	if _, _, err := coordinator.With(owned, []string{"c"}); err == nil {
		t.Fatal("nested lease expansion succeeded")
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := coordinator.With(canceled, []string{"z"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("With(canceled) error = %v", err)
	}
	waitCtx, waitCancel := context.WithCancel(context.Background())
	waitResult := make(chan error, 1)
	go func() {
		_, waiterRelease, waitErr := coordinator.With(waitCtx, []string{"a"})
		if waiterRelease != nil {
			waiterRelease()
		}
		waitResult <- waitErr
	}()
	waitCancel()
	if err := <-waitResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("contending With(canceled) error = %v", err)
	}
	release()
	release()
	if coordinator.Owns(owned, "a") {
		t.Fatal("released context retained mutation ownership")
	}

	acquired := make(chan struct{})
	first, firstRelease, err := coordinator.With(context.Background(), []string{"shared"})
	if err != nil || !coordinator.Owns(first, "shared") {
		t.Fatal(err)
	}
	go func() {
		_, done, acquireErr := coordinator.With(context.Background(), []string{"shared"})
		if acquireErr == nil {
			close(acquired)
			done()
		}
	}()
	select {
	case <-acquired:
		t.Fatal("second lease acquired before release")
	case <-time.After(20 * time.Millisecond):
	}
	firstRelease()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("second lease did not acquire after release")
	}
}
