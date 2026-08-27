package main

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/startup"
	"github.com/doraemonkeys/switch-a/internal/store"

	"github.com/glebarez/sqlite"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"gorm.io/gorm"
)

func TestDefaultContinuityPolicyConfiguresEveryKindWithNamedBounds(t *testing.T) {
	policy, err := defaultContinuityPolicy()
	if err != nil {
		t.Fatalf("defaultContinuityPolicy() error = %v", err)
	}
	want := codexcontinuity.Limits{
		PendingTTL:   defaultContinuityPendingTTL,
		CommittedTTL: defaultContinuityCommittedTTL,
		TombstoneTTL: defaultContinuityTombstoneTTL,
		MaxBindings:  defaultContinuityMaxPerKind,
	}
	for _, kind := range []codexcontinuity.Kind{
		codexcontinuity.KindThreadID,
		codexcontinuity.KindSessionID,
		codexcontinuity.KindConversationID,
		codexcontinuity.KindWindowID,
		codexcontinuity.KindTurnState,
		codexcontinuity.KindTurnMetadata,
		codexcontinuity.KindResponseReference,
	} {
		limits, exists := policy.Limits(kind)
		if !exists || limits != want {
			t.Errorf("policy[%s] = %+v, %t; want %+v", kind, limits, exists, want)
		}
	}
	if defaultContinuityPendingTTL != 24*time.Hour ||
		defaultContinuityCommittedTTL != 30*24*time.Hour ||
		defaultContinuityTombstoneTTL != 7*24*time.Hour ||
		defaultContinuityMaxPerKind != 10_000 {
		t.Fatalf("continuity composition defaults changed without an explicit policy decision")
	}
}

func TestNewApplicationCodexRuntimeComposesNarrowHTTPAndWebSocketDependencies(t *testing.T) {
	security, err := loadApplicationCodexSecurity(
		"keyring.json",
		func(string) ([]byte, error) { return []byte(applicationKeyringDocument()), nil },
		bytes.NewReader(bytes.Repeat([]byte{7}, 64)),
	)
	if err != nil {
		t.Fatal(err)
	}
	persistence, err := store.NewSQLiteStore(
		filepath.Join(t.TempDir(), "runtime.db"),
		internal.RealClock{},
		nil,
		security.staticSubjectSigners()...,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer persistence.Close()
	initial := codexstartup.Snapshot{
		UpstreamHeaderHygiene: true,
		WebSocketSubprotocol:  true,
		Continuity:            true,
		ProviderCookieJar:     true,
	}
	runtime, err := newApplicationCodexRuntime(
		context.Background(), initial, persistence, security, internal.RealClock{}, zap.NewNop(),
	)
	if err != nil {
		t.Fatalf("newApplicationCodexRuntime() error = %v", err)
	}
	if runtime.Features == nil || runtime.HTTP == nil || runtime.WebSocket == nil {
		t.Fatalf("runtime = %+v", runtime)
	}
	if got := runtime.Features.Snapshot(); got != initial {
		t.Fatalf("runtime feature snapshot = %+v, want %+v", got, initial)
	}
	if _, _, _, err := newApplicationCodexServices(
		context.Background(), persistence, security, nil, nil, internal.RealClock{}, zap.NewNop(),
	); err == nil {
		t.Fatal("Codex service composition accepted a missing Cookie host canonicalizer")
	}
}

func TestNewApplicationCodexRuntimeDisabledFeaturesNeedNoKeyring(t *testing.T) {
	persistence, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "disabled.db"), internal.RealClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer persistence.Close()
	runtime, err := newApplicationCodexRuntime(
		context.Background(), codexstartup.Snapshot{}, persistence, nil, internal.RealClock{}, zap.NewNop(),
	)
	if err != nil {
		t.Fatalf("disabled newApplicationCodexRuntime() error = %v", err)
	}
	if runtime.Features == nil || runtime.HTTP == nil || runtime.WebSocket == nil {
		t.Fatalf("disabled runtime = %+v", runtime)
	}
}

func TestNewApplicationCodexRuntimeRequiresCompositionDependencies(t *testing.T) {
	if _, err := newApplicationCodexRuntime(context.Background(), codexstartup.Snapshot{}, nil, nil, nil, nil); err == nil {
		t.Fatal("runtime composition accepted missing dependencies")
	}
}

func TestNewApplicationCodexRuntimeFailsClosedOnRepositoryComposition(t *testing.T) {
	security, err := loadApplicationCodexSecurity(
		"keyring.json",
		func(string) ([]byte, error) { return []byte(applicationKeyringDocument()), nil },
		bytes.NewReader(bytes.Repeat([]byte{8}, 64)),
	)
	if err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(t.TempDir(), "corrupt.db")
	persistence, err := store.NewSQLiteStore(
		databasePath, internal.RealClock{}, nil, security.staticSubjectSigners()...,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer persistence.Close()
	// The runtime constructor must validate through the repository boundary; it
	// cannot silently recreate a schema after listener preflight.
	database, err := gorm.Open(sqlite.Open(databasePath), &gorm.Config{})
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
		context.Background(), codexstartup.Snapshot{}, persistence, security, internal.RealClock{}, zap.NewNop(),
	); err == nil {
		t.Fatal("runtime composition accepted an incomplete provider-Cookie schema")
	}
}

func TestCodexRuntimeObserversEmitStructuredMilestones(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	log := zap.New(core)
	continuityLogObserver(log).ObserveContinuity(codexcontinuity.Event{
		Action: "claim", Outcome: "committed", OperationID: "operation-1",
	})
	providerCookieLogTrace(log).RecordProviderCookieTrace(providercookie.TraceEvent{
		Milestone: "merge", Decision: "accepted", OperationID: "operation-2",
	})
	entries := observed.All()
	if len(entries) != 2 || entries[0].Message != "codex.continuity_decision" || entries[1].Message != "codex.provider_cookie_decision" {
		t.Fatalf("observer entries = %+v", entries)
	}
}
