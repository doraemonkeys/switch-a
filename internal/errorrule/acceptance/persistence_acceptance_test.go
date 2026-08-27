package internalerror_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	errorrulesqlite "github.com/doraemonkeys/switch-a/internal/errorrule/sqlite"
	"github.com/doraemonkeys/switch-a/internal/errorrule/statistics"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/proxy"
	"github.com/doraemonkeys/switch-a/internal/selector"
	"github.com/doraemonkeys/switch-a/internal/store"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	persistenceGroupID   = "v5b-persistence-group"
	persistenceProviderA = "v5b-persistence-a"
	persistenceProviderB = "v5b-persistence-b"
	concurrentStatHits   = 64
)

var (
	persistenceGlobalRuleID = errorrule.RuleID("22222222-2222-4222-8222-222222222222")
	persistenceRuleAID      = errorrule.RuleID("33333333-3333-4333-8333-333333333333")
	persistenceRuleBID      = errorrule.RuleID("44444444-4444-4444-8444-444444444444")
	persistenceRuleA2ID     = errorrule.RuleID("55555555-5555-4555-8555-555555555555")
	errInjectedStatsFlush   = errors.New("injected statistics flush failure")
)

func TestV5BConfigRevisionRestartAndProviderCascade(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "config-restart.db")
	backend, err := store.NewSQLiteStore(databasePath, internal.RealClock{}, nil)
	if err != nil {
		t.Fatalf("open backend: %v", err)
	}
	t.Cleanup(func() {
		if backend != nil {
			_ = backend.Close()
		}
	})
	if err := backend.InitDefaultConfig(ctx); err != nil {
		t.Fatalf("initialize config: %v", err)
	}

	groupID := persistenceGroupID
	providerA, credentialA := persistenceProvider(t, persistenceProviderA, &groupID)
	providerB, credentialB := persistenceProvider(t, persistenceProviderB, &groupID)
	expected := errorrule.Revision(0)
	if err := backend.ApplyConfigImport(ctx, &store.ConfigImportBundle{
		Groups: []model.Group{{
			ID: persistenceGroupID, Name: "Persistence", Strategy: selector.StrategyPriority,
			Weight: 1, Enabled: true,
		}},
		CredentialSessions: []credentialsession.Session{credentialA, credentialB},
		Providers:          []model.Provider{providerA, providerB},
		RoutingPolicyMode:  store.ConfigImportRoutingPolicyModePreserve,
		Settings: map[string]string{
			proxy.ConfigKeyStickyMode: string(model.StickyModeOff),
		},
		RuleImport: errorrulesqlite.ImportRequest{
			Mode: errorrulesqlite.ImportModeFull,
			Rules: []errorrulesqlite.ImportedRule{
				importedRule(t, persistenceGlobalRuleID, "global", "", "global"),
				importedRule(t, persistenceRuleAID, "provider-a", persistenceProviderA, "provider-a"),
				importedRule(t, persistenceRuleBID, "provider-b", persistenceProviderB, "provider-b"),
			},
		},
		ExpectedRuleRevision: &expected,
	}); err != nil {
		t.Fatalf("full config import: %v", err)
	}
	repository := backend.InternalErrorRuleRepository()
	assertRuleIDs(t, repository, 1, persistenceGlobalRuleID, persistenceRuleAID, persistenceRuleBID)

	// Provider validation happens after the config callback but inside the same
	// transaction; this proves a rejected rule cannot leak earlier setting writes.
	missingTarget, err := errorrule.NewProviderTarget("missing-provider")
	if err != nil {
		t.Fatal(err)
	}
	invalidExpected := errorrule.Revision(1)
	invalidErr := backend.ApplyConfigImport(ctx, &store.ConfigImportBundle{
		RoutingPolicyMode: store.ConfigImportRoutingPolicyModePreserve,
		Settings: map[string]string{
			proxy.ConfigKeyStickyMode: string(model.StickyModeModel),
		},
		RuleImport: errorrulesqlite.ImportRequest{
			Mode: errorrulesqlite.ImportModeFull,
			Rules: []errorrulesqlite.ImportedRule{{
				ID: persistenceGlobalRuleID,
				RuleSpec: errorrule.RuleSpec{
					Name: "invalid", Enabled: true, Target: missingTarget,
					Keywords: []string{"invalid"}, MatchMode: errorrule.MatchAny,
					Action: errorrule.NewPassthroughAction(),
				},
			}},
		},
		ExpectedRuleRevision: &invalidExpected,
	})
	if invalidErr == nil {
		t.Fatal("invalid provider-scoped import unexpectedly committed")
	}
	stickyMode, err := backend.GetConfig(ctx, proxy.ConfigKeyStickyMode)
	if err != nil || stickyMode != string(model.StickyModeOff) {
		t.Fatalf("rolled-back sticky mode=%q err=%v", stickyMode, err)
	}
	assertRuleIDs(t, repository, 1, persistenceGlobalRuleID, persistenceRuleAID, persistenceRuleBID)

	if err := backend.ApplyConfigImport(ctx, &store.ConfigImportBundle{
		RoutingPolicyMode: store.ConfigImportRoutingPolicyModePreserve,
		Settings:          map[string]string{"v5b_marker": "settings-only"},
		RuleImport:        errorrulesqlite.ImportRequest{Mode: errorrulesqlite.ImportModePreserve},
	}); err != nil {
		t.Fatalf("settings-only import: %v", err)
	}
	if revision, _ := repository.ListRules(); revision != 1 {
		t.Fatalf("settings-only revision=%d, want 1", revision)
	}
	if marker, err := backend.GetConfig(ctx, "v5b_marker"); err != nil || marker != "settings-only" {
		t.Fatalf("settings-only marker=%q err=%v", marker, err)
	}

	_, initialRules := repository.ListRules()
	initialOrder := ruleIDs(initialRules)
	noOp, err := repository.ReorderRules(ctx, 1, initialOrder)
	if err != nil || noOp.Changed || noOp.Revision != 1 {
		t.Fatalf("no-op reorder=%#v err=%v", noOp, err)
	}

	selectionExpected := errorrule.Revision(1)
	if err := backend.ApplyConfigImport(ctx, &store.ConfigImportBundle{
		RoutingPolicyMode: store.ConfigImportRoutingPolicyModePreserve,
		RuleImport: errorrulesqlite.ImportRequest{
			Mode:                errorrulesqlite.ImportModeSelection,
			SelectedProviderIDs: []string{persistenceProviderA},
			Rules: []errorrulesqlite.ImportedRule{
				importedRule(t, persistenceRuleA2ID, "provider-a-v2", persistenceProviderA, "provider-a-v2"),
			},
		},
		ExpectedRuleRevision: &selectionExpected,
	}); err != nil {
		t.Fatalf("selection import: %v", err)
	}
	assertRuleIDs(t, repository, 2, persistenceGlobalRuleID, persistenceRuleBID, persistenceRuleA2ID)

	reordered, err := repository.ReorderRules(ctx, 2, []errorrule.RuleID{
		persistenceRuleA2ID, persistenceGlobalRuleID, persistenceRuleBID,
	})
	if err != nil || !reordered.Changed || reordered.Revision != 3 {
		t.Fatalf("reorder=%#v err=%v", reordered, err)
	}

	_, rulesAtThree := repository.ListRules()
	globalAtThree := findRule(t, rulesAtThree, persistenceGlobalRuleID)
	start := make(chan struct{})
	results := make(chan error, 2)
	for index := 0; index < 2; index++ {
		index := index
		go func() {
			<-start
			spec := globalAtThree.RuleSpec
			spec.Name = fmt.Sprintf("concurrent-writer-%d", index)
			_, updateErr := repository.UpdateRule(ctx, 3, persistenceGlobalRuleID, spec)
			results <- updateErr
		}()
	}
	close(start)
	var successes, revisionConflicts int
	for range 2 {
		updateErr := <-results
		if updateErr == nil {
			successes++
			continue
		}
		var conflict *errorrulesqlite.RevisionMismatchError
		if errors.As(updateErr, &conflict) {
			revisionConflicts++
			continue
		}
		t.Fatalf("concurrent update error: %v", updateErr)
	}
	if successes != 1 || revisionConflicts != 1 {
		t.Fatalf("concurrent updates successes=%d conflicts=%d", successes, revisionConflicts)
	}
	if revision, _ := repository.ListRules(); revision != 4 {
		t.Fatalf("concurrent update revision=%d, want 4", revision)
	}

	if err := backend.DeleteProvider(ctx, persistenceProviderB); err != nil {
		t.Fatalf("delete provider B: %v", err)
	}
	assertRuleIDs(t, repository, 5, persistenceRuleA2ID, persistenceGlobalRuleID)
	if _, err := backend.GetProvider(ctx, persistenceProviderB); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted provider lookup error=%v", err)
	}

	if err := backend.Close(); err != nil {
		t.Fatalf("close backend before restart: %v", err)
	}
	backend = nil
	restarted, err := store.NewSQLiteStore(databasePath, internal.RealClock{}, nil)
	if err != nil {
		t.Fatalf("restart backend: %v", err)
	}
	backend = restarted
	repository = restarted.InternalErrorRuleRepository()
	assertRuleIDs(t, repository, 5, persistenceRuleA2ID, persistenceGlobalRuleID)
	if marker, err := restarted.GetConfig(ctx, "v5b_marker"); err != nil || marker != "settings-only" {
		t.Fatalf("restarted marker=%q err=%v", marker, err)
	}
	groups, err := restarted.ListGroups(ctx)
	if err != nil || len(groups) != 1 || len(groups[0].Providers) != 1 || groups[0].Providers[0].ID != persistenceProviderA {
		t.Fatalf("restarted groups=%#v err=%v", groups, err)
	}

	_, restartedRules := repository.ListRules()
	globalAfterRestart := findRule(t, restartedRules, persistenceGlobalRuleID)
	updatedSpec := globalAfterRestart.RuleSpec
	updatedSpec.Name = "post-restart"
	mutation, err := repository.UpdateRule(ctx, 5, persistenceGlobalRuleID, updatedSpec)
	if err != nil || mutation.Revision != 6 || !mutation.Changed {
		t.Fatalf("post-restart mutation=%#v err=%v", mutation, err)
	}
}

func TestV5BStatisticsRetryAndGenerationIsolation(t *testing.T) {
	ctx := context.Background()
	backend, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "statistics.db"), internal.RealClock{}, nil)
	if err != nil {
		t.Fatalf("open backend: %v", err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	repository := backend.InternalErrorRuleRepository()
	sink := &failOnceStatsSink{delegate: repository}
	sink.fail.Store(true)
	accumulator, err := statistics.New(sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.BindStatsGenerationRetirer(accumulator.Retire); err != nil {
		t.Fatal(err)
	}

	expected := errorrule.Revision(0)
	if err := backend.ApplyConfigImport(ctx, &store.ConfigImportBundle{
		RoutingPolicyMode: store.ConfigImportRoutingPolicyModePreserve,
		RuleImport: errorrulesqlite.ImportRequest{
			Mode:  errorrulesqlite.ImportModeFull,
			Rules: []errorrulesqlite.ImportedRule{importedRule(t, acceptanceRuleID, "stats", "", "stats")},
		},
		ExpectedRuleRevision: &expected,
	}); err != nil {
		t.Fatal(err)
	}
	_, rules := repository.ListRules()
	oldHandle := statistics.HandleFor(rules[0])
	firstHitAt := time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC)
	if err := accumulator.Hit(oldHandle, firstHitAt); err != nil {
		t.Fatal(err)
	}
	if err := accumulator.Flush(ctx); !errors.Is(err, errInjectedStatsFlush) {
		t.Fatalf("first flush error=%v", err)
	}
	if pending := accumulator.Pending(oldHandle); pending.HitCount != 1 || pending.LastHitAt == nil || !pending.LastHitAt.Equal(firstHitAt) {
		t.Fatalf("restored pending statistics=%#v", pending)
	}
	if err := accumulator.Flush(ctx); err != nil {
		t.Fatalf("retry flush: %v", err)
	}
	_, persisted, err := repository.ListStatsSnapshot(ctx)
	if err != nil || len(persisted) != 1 || persisted[0].HitCount != 1 {
		t.Fatalf("persisted statistics=%#v err=%v", persisted, err)
	}

	if _, err := repository.DeleteRule(ctx, 1, acceptanceRuleID); err != nil {
		t.Fatalf("delete old generation: %v", err)
	}
	if pending := accumulator.Pending(oldHandle); pending.HitCount != 0 {
		t.Fatalf("retired generation pending=%#v", pending)
	}
	reimportExpected := errorrule.Revision(2)
	if err := backend.ApplyConfigImport(ctx, &store.ConfigImportBundle{
		RoutingPolicyMode: store.ConfigImportRoutingPolicyModePreserve,
		RuleImport: errorrulesqlite.ImportRequest{
			Mode:  errorrulesqlite.ImportModeFull,
			Rules: []errorrulesqlite.ImportedRule{importedRule(t, acceptanceRuleID, "stats-v2", "", "stats")},
		},
		ExpectedRuleRevision: &reimportExpected,
	}); err != nil {
		t.Fatalf("reimport same public rule ID: %v", err)
	}
	_, replacementRules := repository.ListRules()
	newHandle := statistics.HandleFor(replacementRules[0])
	if oldHandle.Generation.String() == newHandle.Generation.String() {
		t.Fatal("reimport reused a retired generation")
	}
	if err := accumulator.Hit(oldHandle, firstHitAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	var waitGroup sync.WaitGroup
	hitErrors := make(chan error, concurrentStatHits)
	for index := 0; index < concurrentStatHits; index++ {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			hitErrors <- accumulator.Hit(newHandle, firstHitAt.Add(time.Duration(index+1)*time.Second))
		}(index)
	}
	waitGroup.Wait()
	close(hitErrors)
	for hitErr := range hitErrors {
		if hitErr != nil {
			t.Fatalf("concurrent statistics hit: %v", hitErr)
		}
	}
	if err := accumulator.Flush(ctx); err != nil {
		t.Fatalf("generation-qualified flush: %v", err)
	}
	revision, persisted, err := repository.ListStatsSnapshot(ctx)
	if err != nil || revision != 3 || len(persisted) != 1 || persisted[0].HitCount != concurrentStatHits {
		t.Fatalf("replacement statistics revision=%d stats=%#v err=%v", revision, persisted, err)
	}
	if pending := accumulator.Pending(oldHandle); pending.HitCount != 0 {
		t.Fatalf("late retired generation survived missing classification: %#v", pending)
	}
}

func TestV5BRuleSnapshotReadsDoNotQuerySQLite(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "query-spy.db")
	database, err := gorm.Open(sqlite.Open(databasePath), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDatabase, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDatabase.Close() })
	repository, err := errorrulesqlite.Open(context.Background(), errorrulesqlite.Config{DB: database})
	if err != nil {
		t.Fatal(err)
	}
	created, err := repository.CreateRule(context.Background(), 0, errorrule.RuleSpec{
		Name: "query-spy", Enabled: true, Target: errorrule.NewGlobalTarget(),
		Keywords: []string{"query-spy"}, MatchMode: errorrule.MatchAny,
		Action: errorrule.NewPassthroughAction(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ruleID := created.Rules[0].ID

	var queryCount atomic.Int64
	if err := database.Callback().Query().Before("gorm:query").Register("v5b:query-spy", func(*gorm.DB) {
		queryCount.Add(1)
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.Callback().Raw().Before("gorm:raw").Register("v5b:raw-spy", func(*gorm.DB) {
		queryCount.Add(1)
	}); err != nil {
		t.Fatal(err)
	}
	queryCount.Store(0)
	for range 1_000 {
		if repository.CurrentRuleSet() == nil {
			t.Fatal("current rule set is nil")
		}
		revision, rules := repository.ListRules()
		if revision != 1 || len(rules) != 1 {
			t.Fatalf("snapshot revision=%d rules=%#v", revision, rules)
		}
		if revision, _, err := repository.GetRule(ruleID); err != nil || revision != 1 {
			t.Fatalf("get snapshot rule revision=%d err=%v", revision, err)
		}
	}
	if queries := queryCount.Load(); queries != 0 {
		t.Fatalf("snapshot reads executed %d SQLite queries", queries)
	}
}

func TestV5BMultiAttemptPersistenceAcceptsHeterogeneousEvidence(t *testing.T) {
	backend, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "attempts.db"), internal.RealClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	requestID := "v5b-multi-attempt"
	semanticOutcome := model.RequestAttemptOutcomeUpstreamSemanticError
	completedOutcome := model.RequestAttemptOutcomeUpstreamCompleted
	failureVerdict := model.RequestAttemptHealthFailure
	successVerdict := model.RequestAttemptHealthSuccess
	semanticCause := model.RequestAttemptHealthCauseSemanticRetryThenSwitch
	successCause := model.RequestAttemptHealthCauseNormalCompletion
	visible, hidden := true, false
	clientStatus := httpStatusOK
	evidence := `{"v":2,"semantic_error":{"schema_version":1}}`
	attempts := []model.RequestAttempt{
		{
			RequestID: requestID, ProviderID: persistenceProviderA, Attempt: 0,
			SwitchMode: model.RequestAttemptSwitchModeInitial,
			Outcome:    &semanticOutcome, ResultVisibleToClient: &hidden,
			HealthVerdict: &failureVerdict, HealthCause: &semanticCause,
			AttemptEvidenceJSON: &evidence, CreatedAt: time.Now(),
		},
		{
			RequestID: requestID, ProviderID: persistenceProviderB, Attempt: 1,
			SwitchMode: model.RequestAttemptSwitchModeReplacement,
			Outcome:    &completedOutcome, ResultVisibleToClient: &visible,
			ClientTransportStatusCode: &clientStatus,
			HealthVerdict:             &successVerdict, HealthCause: &successCause,
			CreatedAt: time.Now(),
		},
	}
	if err := backend.InsertAttempts(context.Background(), attempts); err != nil {
		t.Fatalf("[V5B-B01] heterogeneous multi-attempt batch persistence: %v", err)
	}
	persisted, err := backend.GetAttemptsByRequestID(context.Background(), requestID)
	if err != nil || len(persisted) != 2 {
		t.Fatalf("persisted attempts=%#v err=%v", persisted, err)
	}
}

const httpStatusOK = 200

type failOnceStatsSink struct {
	delegate statistics.Sink
	fail     atomic.Bool
}

func (s *failOnceStatsSink) ApplyRuleStatDeltas(
	ctx context.Context,
	deltas []statistics.Delta,
) (statistics.ApplyResult, error) {
	if s.fail.CompareAndSwap(true, false) {
		return statistics.ApplyResult{}, errInjectedStatsFlush
	}
	return s.delegate.ApplyRuleStatDeltas(ctx, deltas)
}

func persistenceProvider(t *testing.T, id string, groupID *string) (model.Provider, credentialsession.Session) {
	t.Helper()
	const vendor = "persistence-vendor"
	session, route := newStaticCredentialRoute(t, id, proxy.APITypeCodex, vendor, "secret")
	provider := model.Provider{
		ID: id, Name: id, AuthMode: "bearer", Enabled: true,
		GroupID: groupID, Weight: 1, Vendor: vendor,
		FailoverScope: model.ScopeAny, AcceptFailover: model.ScopeAny,
		APITypes: []model.ProviderAPIType{{
			ProviderID: id, APIType: proxy.APITypeCodex, BaseURL: "https://example.invalid",
		}},
		CredentialSessions: []credentialsession.RouteSnapshot{route},
	}
	return provider, session
}

func importedRule(
	t *testing.T,
	id errorrule.RuleID,
	name, providerID, keyword string,
) errorrulesqlite.ImportedRule {
	t.Helper()
	target := errorrule.NewGlobalTarget()
	var err error
	if providerID != "" {
		target, err = errorrule.NewProviderTarget(errorrule.ProviderID(providerID))
		if err != nil {
			t.Fatal(err)
		}
	}
	return errorrulesqlite.ImportedRule{
		ID: id,
		RuleSpec: errorrule.RuleSpec{
			Name: name, Enabled: true, Target: target,
			Keywords: []string{keyword}, MatchMode: errorrule.MatchAny,
			Action: errorrule.NewPassthroughAction(),
		},
	}
}

func assertRuleIDs(
	t *testing.T,
	repository *errorrulesqlite.Repository,
	wantRevision errorrule.Revision,
	wantIDs ...errorrule.RuleID,
) {
	t.Helper()
	revision, rules := repository.ListRules()
	if revision != wantRevision {
		t.Fatalf("revision=%d, want %d", revision, wantRevision)
	}
	gotIDs := ruleIDs(rules)
	if len(gotIDs) != len(wantIDs) {
		t.Fatalf("rule IDs=%v, want %v", gotIDs, wantIDs)
	}
	for index := range wantIDs {
		if gotIDs[index] != wantIDs[index] {
			t.Fatalf("rule IDs=%v, want %v", gotIDs, wantIDs)
		}
	}
}

func ruleIDs(rules []errorrule.Rule) []errorrule.RuleID {
	result := make([]errorrule.RuleID, len(rules))
	for index := range rules {
		result[index] = rules[index].ID
	}
	return result
}

func findRule(t *testing.T, rules []errorrule.Rule, id errorrule.RuleID) errorrule.Rule {
	t.Helper()
	for _, rule := range rules {
		if rule.ID == id {
			return rule
		}
	}
	t.Fatalf("rule %q not found in %#v", id, rules)
	return errorrule.Rule{}
}
