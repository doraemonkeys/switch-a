package main

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	continuity "github.com/doraemonkeys/switch-a/internal/codex/continuity"
	cookie "github.com/doraemonkeys/switch-a/internal/codex/cookie"
	codexkeyring "github.com/doraemonkeys/switch-a/internal/codex/keyring"
	"github.com/doraemonkeys/switch-a/internal/store"

	glebarezsqlite "github.com/glebarez/sqlite"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"gorm.io/gorm"
)

func TestDefaultContinuityPolicyCoversEveryDurableKind(t *testing.T) {
	policy, err := defaultContinuityPolicy()
	if err != nil {
		t.Fatal(err)
	}
	want := continuity.Limits{
		PendingTTL: defaultContinuityPendingTTL, CommittedTTL: defaultContinuityCommittedTTL,
		TombstoneTTL: defaultContinuityTombstoneTTL, MaxBindings: defaultContinuityMaxPerKind,
	}
	for _, kind := range []continuity.Kind{
		continuity.KindThreadID, continuity.KindSessionID, continuity.KindConversationID,
		continuity.KindWindowID, continuity.KindTurnState, continuity.KindTurnMetadata,
		continuity.KindResponseReference,
	} {
		limits, exists := policy.Limits(kind)
		if !exists || limits != want {
			t.Errorf("policy[%s] = %+v, %t; want %+v", kind, limits, exists, want)
		}
	}
	if defaultContinuityPendingTTL != 24*time.Hour || defaultContinuityCommittedTTL != 30*24*time.Hour ||
		defaultContinuityTombstoneTTL != 7*24*time.Hour || defaultContinuityMaxPerKind != 10_000 {
		t.Fatal("continuity composition defaults changed without an explicit policy decision")
	}
}

func TestNewApplicationCodexRuntimeAlwaysComposesHTTPAndWebSocket(t *testing.T) {
	security := testApplicationCodexSecurity(t, 7)
	persistence, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "runtime.db"), internal.RealClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer persistence.Close()
	if err := persistence.FinalizeStaticCredentialSubjects(context.Background(), security.keyring); err != nil {
		t.Fatal(err)
	}
	runtime, err := newApplicationCodexRuntime(
		context.Background(), persistence, security, internal.RealClock{}, zap.NewNop(),
	)
	if err != nil {
		t.Fatalf("newApplicationCodexRuntime() error = %v", err)
	}
	if runtime.HTTP == nil || runtime.WebSocket == nil || runtime.continuity == nil || runtime.providerCookies == nil {
		t.Fatalf("runtime = %+v", runtime)
	}
}

func TestNewApplicationCodexRuntimeRequiresEveryDependency(t *testing.T) {
	if _, err := newApplicationCodexRuntime(context.Background(), nil, nil, nil, nil); err == nil {
		t.Fatal("runtime composition accepted missing dependencies")
	}
	if _, _, _, err := newApplicationCodexServices(context.Background(), nil, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("service composition accepted missing dependencies")
	}
}

func TestNewApplicationCodexRuntimeFailsClosedOnRepositoryComposition(t *testing.T) {
	security := testApplicationCodexSecurity(t, 8)
	databasePath := filepath.Join(t.TempDir(), "corrupt.db")
	persistence, err := store.NewSQLiteStore(databasePath, internal.RealClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer persistence.Close()
	if err := persistence.FinalizeStaticCredentialSubjects(context.Background(), security.keyring); err != nil {
		t.Fatal(err)
	}
	database, err := gorm.Open(glebarezsqlite.Open(databasePath), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Exec("DROP TABLE codex_provider_cookie_entries").Error; err != nil {
		t.Fatal(err)
	}
	sqlDatabase, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDatabase.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := newApplicationCodexRuntime(
		context.Background(), persistence, security, internal.RealClock{}, zap.NewNop(),
	); err == nil {
		t.Fatal("runtime composition accepted an incomplete provider-Cookie schema")
	}
}

func TestCodexRuntimeObserversEmitStructuredMilestones(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	log := zap.New(core)
	continuityLogObserver(log).ObserveContinuity(continuity.Event{
		Action: "claim", Outcome: "committed", OperationID: "operation-1",
	})
	providerCookieLogTrace(log).RecordProviderCookieTrace(cookie.TraceEvent{
		Milestone: "merge", Decision: "accepted", OperationID: "operation-2",
	})
	entries := observed.All()
	if len(entries) != 2 || entries[0].Message != "codex.continuity_decision" || entries[1].Message != "codex.provider_cookie_decision" {
		t.Fatalf("observer entries = %+v", entries)
	}
}

func testApplicationCodexSecurity(t *testing.T, randomByte byte) *applicationCodexSecurity {
	t.Helper()
	keyring, err := codexkeyring.Parse(
		[]byte(applicationKeyringDocument()), bytes.NewReader(bytes.Repeat([]byte{randomByte}, 256)),
	)
	if err != nil {
		t.Fatal(err)
	}
	return &applicationCodexSecurity{keyring: keyring}
}
