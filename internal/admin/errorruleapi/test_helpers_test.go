package errorruleapi

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	errorrulesqlite "github.com/doraemonkeys/switch-a/internal/errorrule/sqlite"
	"github.com/doraemonkeys/switch-a/internal/errorrule/statistics"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/store"
	"github.com/glebarez/sqlite"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var errTestFailure = errors.New("test failure")

type fixedClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fixedClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

type sequenceIDs struct {
	mu     sync.Mutex
	values []string
}

func (g *sequenceIDs) NewID() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.values) == 0 {
		panic("test ID sequence exhausted")
	}
	value := g.values[0]
	g.values = g.values[1:]
	return value
}

type providerCatalogStub struct {
	mu      sync.Mutex
	items   map[string]*model.Provider
	err     error
	onGet   func(string)
	getCall int
}

func (p *providerCatalogStub) GetProvider(_ context.Context, id string) (*model.Provider, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.getCall++
	if p.onGet != nil {
		p.onGet(id)
	}
	if p.err != nil {
		return nil, p.err
	}
	provider := p.items[id]
	if provider == nil {
		return nil, store.ErrNotFound
	}
	clone := *provider
	return &clone, nil
}

type statsReaderStub struct {
	mu       sync.Mutex
	revision errorrule.Revision
	values   []errorrule.RuleStats
	err      error
	onList   func()
	calls    int
}

func (s *statsReaderStub) ListStatsSnapshot(context.Context) (errorrule.Revision, []errorrule.RuleStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.onList != nil {
		s.onList()
	}
	if s.err != nil {
		return 0, nil, s.err
	}
	return s.revision, append([]errorrule.RuleStats(nil), s.values...), nil
}

type overlayStub struct {
	values []errorrule.RuleStats
	calls  int
}

func (o *overlayStub) Overlay(values []errorrule.RuleStats, _ []errorrule.Rule) []errorrule.RuleStats {
	o.calls++
	if o.values != nil {
		return append([]errorrule.RuleStats(nil), o.values...)
	}
	return append([]errorrule.RuleStats(nil), values...)
}

type fakeRuleService struct {
	mu        sync.RWMutex
	snapshot  *errorrule.CompiledRuleSet
	getErr    error
	mutErr    error
	mutations int
}

func (s *fakeRuleService) CurrentRuleSet() *errorrule.CompiledRuleSet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot
}

func (s *fakeRuleService) setSnapshot(snapshot *errorrule.CompiledRuleSet) {
	s.mu.Lock()
	s.snapshot = snapshot
	s.mu.Unlock()
}

func (s *fakeRuleService) ListRules() (errorrule.Revision, []errorrule.Rule) {
	snapshot := s.CurrentRuleSet()
	if snapshot == nil {
		return 0, nil
	}
	return snapshot.Revision(), snapshot.Rules()
}

func (s *fakeRuleService) GetRule(id errorrule.RuleID) (errorrule.Revision, errorrule.Rule, error) {
	if s.getErr != nil {
		return 0, errorrule.Rule{}, s.getErr
	}
	snapshot := s.CurrentRuleSet()
	if snapshot == nil {
		return 0, errorrule.Rule{}, errorrulesqlite.ErrRuleNotFound
	}
	rule, found := snapshot.Rule(id)
	if !found {
		return snapshot.Revision(), errorrule.Rule{}, errorrulesqlite.ErrRuleNotFound
	}
	return snapshot.Revision(), rule, nil
}

func (s *fakeRuleService) CreateRule(context.Context, errorrule.Revision, errorrule.RuleSpec) (errorrulesqlite.MutationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mutations++
	return errorrulesqlite.MutationResult{}, s.mutErr
}

func (s *fakeRuleService) UpdateRule(context.Context, errorrule.Revision, errorrule.RuleID, errorrule.RuleSpec) (errorrulesqlite.MutationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mutations++
	return errorrulesqlite.MutationResult{}, s.mutErr
}

func (s *fakeRuleService) DeleteRule(context.Context, errorrule.Revision, errorrule.RuleID) (errorrulesqlite.MutationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mutations++
	return errorrulesqlite.MutationResult{}, s.mutErr
}

func (s *fakeRuleService) ReorderRules(context.Context, errorrule.Revision, []errorrule.RuleID) (errorrulesqlite.MutationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mutations++
	return errorrulesqlite.MutationResult{}, s.mutErr
}

func (s *fakeRuleService) mutationCalls() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mutations
}

type analyzerStub struct {
	observed []AnalyzedError
	result   MessageAnalysisResult
	calls    int
	consumed int
}

func (a *analyzerStub) Analyze(_ context.Context, _ MessageAnalysisInput, consume func(AnalyzedError) bool) MessageAnalysisResult {
	a.calls++
	for _, observed := range a.observed {
		a.consumed++
		if !consume(observed) {
			break
		}
	}
	return a.result
}

func compileRules(t *testing.T, revision errorrule.Revision, rules ...errorrule.Rule) *errorrule.CompiledRuleSet {
	t.Helper()
	snapshot, err := errorrule.CompileRuleSet(revision, rules)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func testRule(t *testing.T, id, generation, name string, position int64, action errorrule.Action) errorrule.Rule {
	t.Helper()
	parsedGeneration, err := errorrule.ParseRuleGeneration(generation)
	if err != nil {
		t.Fatal(err)
	}
	apiType := apicontract.APITypeCodex
	return errorrule.NewRule(errorrule.RuleSpec{
		Name: name, Enabled: true, Target: errorrule.NewGlobalTarget(), APIType: &apiType,
		Keywords: []string{"server_is_overloaded"}, MatchMode: errorrule.MatchAny, Action: action,
	}, errorrule.RuleMetadata{
		ID: errorrule.RuleID(id), Generation: parsedGeneration, Position: position,
		CreatedAt: time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC),
		UpdatedAt: time.Date(2026, 8, 3, 1, 2, 3, 0, time.UTC),
	})
}

func openRepositoryHandler(t *testing.T, repositoryIDs ...string) (*gorm.DB, *errorrulesqlite.Repository, *statistics.Accumulator, *Handler) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "rules.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Exec("CREATE TABLE providers (id TEXT PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	clock := &fixedClock{now: time.Date(2026, 8, 3, 1, 4, 0, 0, time.UTC)}
	repository, err := errorrulesqlite.Open(context.Background(), errorrulesqlite.Config{
		DB: db, Clock: clock, IDGenerator: &sequenceIDs{values: append([]string(nil), repositoryIDs...)},
	})
	if err != nil {
		t.Fatal(err)
	}
	accumulator, err := statistics.New(repository)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.BindStatsGenerationRetirer(accumulator.Retire); err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(Config{
		Rules: repository, Stats: repository, StatsOverlay: accumulator,
		Providers: &providerCatalogStub{items: map[string]*model.Provider{}},
		Logger:    zap.NewNop(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return db, repository, accumulator, handler
}

func validPassthroughMutation(name string) string {
	return `{"schema_version":1,"rule":{"name":"` + name + `","enabled":true,"target":{"kind":"global"},"api_type":"codex","keywords":["server_is_overloaded"],"match_mode":"any","action":{"type":"passthrough"}}}`
}

func newReadHandler(t *testing.T, snapshot *errorrule.CompiledRuleSet, analyzer MessageAnalyzer) (*Handler, *fakeRuleService, *providerCatalogStub) {
	t.Helper()
	rules := &fakeRuleService{snapshot: snapshot}
	providers := &providerCatalogStub{items: map[string]*model.Provider{
		"provider-codex": {ID: "provider-codex"},
	}}
	handler, err := NewHandler(Config{
		Rules: rules, Stats: &statsReaderStub{}, StatsOverlay: &overlayStub{},
		Providers: providers, Analyzer: analyzer, Logger: zap.NewNop(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler, rules, providers
}
