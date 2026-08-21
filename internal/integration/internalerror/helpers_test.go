package internalerror_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/attemptevidence"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	errorrulesqlite "github.com/doraemonkeys/switch-a/internal/errorrule/sqlite"
	"github.com/doraemonkeys/switch-a/internal/errorrule/statistics"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/proxy"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis"
	"github.com/doraemonkeys/switch-a/internal/selector"
	"github.com/doraemonkeys/switch-a/internal/store"
	"go.uber.org/zap"
)

const (
	acceptanceRuleID    = errorrule.RuleID("11111111-1111-4111-8111-111111111111")
	acceptanceKeyword   = "overloaded"
	acceptanceModel     = "v5b-model"
	acceptanceClientIP  = "203.0.113.17"
	acceptanceUser      = "v5b-user"
	primaryProviderID   = "v5b-primary"
	secondaryProviderID = "v5b-secondary"
	providerGroupID     = "v5b-group"
)

type wireResponse struct {
	status      int
	contentType string
	body        []byte
	beforeWrite func()
}

type upstreamSequence struct {
	server    *httptest.Server
	responses []wireResponse
	calls     atomic.Int64
}

func newUpstreamSequence(t *testing.T, responses ...wireResponse) *upstreamSequence {
	t.Helper()
	sequence := &upstreamSequence{responses: append([]wireResponse(nil), responses...)}
	sequence.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		index := int(sequence.calls.Add(1)) - 1
		if index >= len(sequence.responses) {
			http.Error(w, "unexpected upstream call", http.StatusInternalServerError)
			return
		}
		response := sequence.responses[index]
		if response.beforeWrite != nil {
			response.beforeWrite()
		}
		if response.contentType != "" {
			w.Header().Set("Content-Type", response.contentType)
		}
		status := response.status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = w.Write(response.body)
	}))
	t.Cleanup(sequence.server.Close)
	return sequence
}

func (s *upstreamSequence) URL() string {
	if s == nil || s.server == nil {
		return ""
	}
	return s.server.URL
}

func (s *upstreamSequence) CallCount() int {
	if s == nil {
		return 0
	}
	return int(s.calls.Load())
}

type recordingHealth struct {
	mu            sync.Mutex
	available     map[string]bool
	failures      map[string]int
	successes     map[string]int
	openOnFailure bool
	onFailure     func(context.Context, string)
}

func newRecordingHealth(openOnFailure bool) *recordingHealth {
	return &recordingHealth{
		available:     make(map[string]bool),
		failures:      make(map[string]int),
		successes:     make(map[string]int),
		openOnFailure: openOnFailure,
	}
}

func (h *recordingHealth) MarkSuccess(_ context.Context, providerID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.successes[providerID]++
	h.available[providerID] = true
}

func (h *recordingHealth) MarkFailure(ctx context.Context, providerID string, _ error) bool {
	h.mu.Lock()
	h.failures[providerID]++
	if h.openOnFailure {
		h.available[providerID] = false
	}
	callback := h.onFailure
	h.mu.Unlock()
	if callback != nil {
		callback(ctx, providerID)
	}
	return h.openOnFailure
}

func (h *recordingHealth) RecoverIfExpired(_ context.Context, _ string) bool { return false }

func (h *recordingHealth) IsAvailable(_ context.Context, providerID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	available, found := h.available[providerID]
	return !found || available
}

func (h *recordingHealth) SuspendUntil(_ context.Context, providerID string, _ time.Time, _ string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.available[providerID] = false
	return nil
}

func (h *recordingHealth) ManualDisable(_ context.Context, providerID string, _ string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.available[providerID] = false
	return nil
}

func (h *recordingHealth) ManualEnable(_ context.Context, providerID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.available[providerID] = true
	return nil
}

func (h *recordingHealth) ResetCircuitBreaker(providerID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.available, providerID)
}

func (h *recordingHealth) FailureCount(providerID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.failures[providerID]
}

func (h *recordingHealth) SuccessCount(providerID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.successes[providerID]
}

type immediateBackoff struct{}

func (immediateBackoff) Wait(ctx context.Context, _ time.Duration) error {
	return ctx.Err()
}

type ruleSetSpy struct {
	source *errorrulesqlite.Repository
	calls  atomic.Int64
}

type observedProxyStore struct {
	*store.SQLiteStore
	attemptsDone chan struct{}
	closeOnce    sync.Once
	mu           sync.Mutex
	batchErr     error
	fallbackErr  error
}

func newObservedProxyStore(backend *store.SQLiteStore) *observedProxyStore {
	return &observedProxyStore{SQLiteStore: backend, attemptsDone: make(chan struct{})}
}

func (s *observedProxyStore) InsertAttempts(ctx context.Context, attempts []model.RequestAttempt) error {
	batchErr := s.SQLiteStore.InsertAttempts(ctx, attempts)
	var fallbackErr error
	if batchErr != nil {
		// A failed production batch is retained as acceptance evidence. Single-row
		// fallback is test-only and lets later policy assertions identify independent
		// failures without hiding the original persistence defect.
		for i := range attempts {
			attempt := attempts[i]
			if err := s.SQLiteStore.InsertAttempts(ctx, []model.RequestAttempt{attempt}); err != nil {
				fallbackErr = err
				break
			}
		}
	}
	s.mu.Lock()
	s.batchErr = batchErr
	s.fallbackErr = fallbackErr
	s.mu.Unlock()
	s.closeOnce.Do(func() { close(s.attemptsDone) })
	return batchErr
}

func (s *observedProxyStore) waitForAttempts(t *testing.T) {
	t.Helper()
	select {
	case <-s.attemptsDone:
	case <-time.After(5 * time.Second):
		t.Fatal("request-attempt persistence did not complete")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fallbackErr != nil {
		t.Fatalf("diagnostic attempt persistence fallback: %v", s.fallbackErr)
	}
}

func (s *ruleSetSpy) CurrentRuleSet() *errorrule.CompiledRuleSet {
	s.calls.Add(1)
	return s.source.CurrentRuleSet()
}

type proxyHarnessOptions struct {
	action             errorrule.Action
	globalMaxAttempts  int
	primary            *upstreamSequence
	secondary          *upstreamSequence
	primaryMaxRetries  int
	health             *recordingHealth
	capture            bool
	stickyMode         model.StickyMode
	stickyCache        internal.StickyCache
	continuitySeeds    model.VisibleContinuitySeedStore
	analysisScheduler  responseanalysis.Scheduler
	analysisProbeLimit time.Duration
	ruleProviderID     string
}

type proxyHarness struct {
	store        *store.SQLiteStore
	proxyStore   *observedProxyStore
	handler      *proxy.Handler
	health       *recordingHealth
	stats        *statistics.Accumulator
	rule         errorrule.Rule
	ruleSetReads *ruleSetSpy
	capture      *requestcapture.Manager
	session      requestcapture.SessionInfo
}

func newProxyHarness(t *testing.T, options proxyHarnessOptions) *proxyHarness {
	t.Helper()
	if options.primary == nil {
		t.Fatal("primary upstream is required")
	}
	if options.globalMaxAttempts == 0 {
		options.globalMaxAttempts = 4
	}
	if options.stickyMode == "" {
		options.stickyMode = model.StickyModeOff
	}
	if options.health == nil {
		options.health = newRecordingHealth(true)
	}

	databasePath := filepath.Join(t.TempDir(), "v5b.db")
	backend, err := store.NewSQLiteStore(databasePath, internal.RealClock{}, nil)
	if err != nil {
		t.Fatalf("open backend: %v", err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	ctx := context.Background()
	if err := backend.InitDefaultConfig(ctx); err != nil {
		t.Fatalf("initialize runtime config: %v", err)
	}
	if err := backend.SetConfigs(ctx, map[string]string{
		proxy.ConfigKeyGlobalMaxAttempts: strconv.Itoa(options.globalMaxAttempts),
		proxy.ConfigKeyStickyMode:        string(options.stickyMode),
		proxy.ConfigKeyUserHeader:        "X-User-ID",
	}); err != nil {
		t.Fatalf("set runtime config: %v", err)
	}

	groupID := providerGroupID
	providers := []model.Provider{newAcceptanceProvider(
		primaryProviderID,
		"V5B Primary",
		options.primary.URL(),
		&groupID,
		0,
		options.primaryMaxRetries,
	)}
	if options.secondary != nil {
		providers = append(providers, newAcceptanceProvider(
			secondaryProviderID,
			"V5B Secondary",
			options.secondary.URL(),
			&groupID,
			1,
			0,
		))
	}
	expected := errorrule.Revision(0)
	ruleTarget := errorrule.NewGlobalTarget()
	if options.ruleProviderID != "" {
		ruleTarget, err = errorrule.NewProviderTarget(errorrule.ProviderID(options.ruleProviderID))
		if err != nil {
			t.Fatalf("create provider rule target: %v", err)
		}
	}
	if err := backend.ApplyConfigImport(ctx, &store.ConfigImportBundle{
		Groups: []model.Group{{
			ID: providerGroupID, Name: "V5B", Strategy: selector.StrategyPriority,
			Priority: 0, Weight: 1, Enabled: true,
		}},
		Providers:         providers,
		RoutingPolicyMode: store.ConfigImportRoutingPolicyModePreserve,
		RuleImport: errorrulesqlite.ImportRequest{
			Mode: errorrulesqlite.ImportModeFull,
			Rules: []errorrulesqlite.ImportedRule{{
				ID: acceptanceRuleID,
				RuleSpec: errorrule.RuleSpec{
					Name:      "V5B semantic overload",
					Enabled:   true,
					Target:    ruleTarget,
					Keywords:  []string{acceptanceKeyword},
					MatchMode: errorrule.MatchAny,
					Action:    options.action,
				},
			}},
		},
		ExpectedRuleRevision: &expected,
	}); err != nil {
		t.Fatalf("compose backend config: %v", err)
	}

	repository := backend.InternalErrorRuleRepository()
	accumulator, err := statistics.New(repository)
	if err != nil {
		t.Fatalf("create statistics accumulator: %v", err)
	}
	if err := repository.BindStatsGenerationRetirer(accumulator.Retire); err != nil {
		t.Fatalf("bind statistics generations: %v", err)
	}
	_, rules := repository.ListRules()
	if len(rules) != 1 {
		t.Fatalf("composed rules = %#v", rules)
	}

	var captureManager *requestcapture.Manager
	var session requestcapture.SessionInfo
	if options.capture {
		captureManager, err = requestcapture.NewManager(requestcapture.Config{})
		if err != nil {
			t.Fatalf("create capture manager: %v", err)
		}
		t.Cleanup(func() { _ = captureManager.Close() })
		identities := make([]requestcapture.ProviderIdentity, 0, len(providers))
		for _, provider := range providers {
			identities = append(identities, requestcapture.ProviderIdentity{ID: provider.ID, Name: provider.Name})
		}
		session, err = captureManager.Start(requestcapture.StartRequest{
			Providers: identities, AcknowledgeRawPayloadRisk: true,
		})
		if err != nil {
			t.Fatalf("start capture session: %v", err)
		}
	}

	budget, err := responseanalysis.NewDefaultProcessMemoryBudget()
	if err != nil {
		t.Fatalf("create analysis budget: %v", err)
	}
	probeLimit := options.analysisProbeLimit
	if probeLimit == 0 {
		probeLimit = time.Second
	}
	analyzer, err := responseanalysis.NewAnalyzer(
		responseanalysis.NewRegistry(),
		budget,
		responseanalysis.AnalyzerOptions{
			ProbeDuration: probeLimit,
			Scheduler:     options.analysisScheduler,
		},
	)
	if err != nil {
		t.Fatalf("create response analyzer: %v", err)
	}
	providerSelector := selector.NewSelector(selector.Config{
		Store: backend, HealthChecker: options.health, StickyCache: options.stickyCache,
		Clock: internal.RealClock{}, Logger: zap.NewNop(),
	})
	ruleReads := &ruleSetSpy{source: repository}
	observedStore := newObservedProxyStore(backend)
	handler := proxy.NewHandler(proxy.Config{
		Store:                      observedStore,
		Selector:                   providerSelector,
		Health:                     options.health,
		VisibleContinuitySeedStore: options.continuitySeeds,
		Capture:                    captureManager,
		RuleSetProvider:            ruleReads,
		ResponseAnalyzer:           analyzer,
		RuleStatistics:             accumulator,
		BackoffWaiter:              immediateBackoff{},
		Logger:                     zap.NewNop(),
	})
	return &proxyHarness{
		store: backend, proxyStore: observedStore, handler: handler, health: options.health, stats: accumulator,
		rule: rules[0], ruleSetReads: ruleReads, capture: captureManager, session: session,
	}
}

func newAcceptanceProvider(
	id, name, baseURL string,
	groupID *string,
	priority, maxRetries int,
) model.Provider {
	return model.Provider{
		ID: id, Name: name, APIKey: "v5b-secret", AuthMode: "bearer", Enabled: true,
		GroupID: groupID, Priority: priority, Weight: 1, MaxRetries: maxRetries,
		Vendor: "v5b-vendor", FailoverScope: model.ScopeAny, AcceptFailover: model.ScopeAny,
		APITypes: []model.ProviderAPIType{{
			ProviderID: id, APIType: proxy.APITypeCodex, BaseURL: baseURL, APIKey: "v5b-secret",
		}},
	}
}

func (h *proxyHarness) serve(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	requestBody := fmt.Sprintf(`{"model":%q,"input":"acceptance"}`, acceptanceModel)
	request := httptest.NewRequest(http.MethodPost, proxy.RouteCodexResponses, bytes.NewBufferString(requestBody))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-User-ID", acceptanceUser)
	request.RemoteAddr = acceptanceClientIP + ":42000"
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, request)
	return recorder
}

func (h *proxyHarness) attempts(t *testing.T) []model.RequestAttempt {
	t.Helper()
	h.proxyStore.waitForAttempts(t)
	logs, err := h.store.ListLogs(context.Background(), model.LogFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list request logs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("request logs = %#v", logs)
	}
	attempts, err := h.store.GetAttemptsByRequestID(context.Background(), logs[0].RequestID)
	if err != nil {
		t.Fatalf("list request attempts: %v", err)
	}
	return attempts
}

func (h *proxyHarness) capturePage(t *testing.T) requestcapture.RecordPage {
	t.Helper()
	if h.capture == nil {
		t.Fatal("capture was not enabled")
	}
	lease, err := h.capture.OpenRecordPage(context.Background(), h.session.SessionID, requestcapture.ListQuery{Limit: 20})
	if err != nil {
		t.Fatalf("open capture page: %v", err)
	}
	var encoded bytes.Buffer
	if err := lease.WriteJSON(context.Background(), &encoded); err != nil {
		t.Fatalf("write capture page: %v", err)
	}
	var page requestcapture.RecordPage
	if err := json.Unmarshal(encoded.Bytes(), &page); err != nil {
		t.Fatalf("decode capture page: %v", err)
	}
	return page
}

func (h *proxyHarness) captureDetail(t *testing.T, recordID string) requestcapture.RecordDetail {
	t.Helper()
	if h.capture == nil {
		t.Fatal("capture was not enabled")
	}
	lease, err := h.capture.OpenRecordDetail(context.Background(), h.session.SessionID, recordID, 0)
	if err != nil {
		t.Fatalf("open capture detail: %v", err)
	}
	var encoded bytes.Buffer
	if err := lease.WriteJSON(context.Background(), &encoded); err != nil {
		t.Fatalf("write capture detail: %v", err)
	}
	var detail requestcapture.RecordDetail
	if err := json.Unmarshal(encoded.Bytes(), &detail); err != nil {
		t.Fatalf("decode capture detail: %v", err)
	}
	return detail
}

func decodeSemanticEvidence(t *testing.T, encoded *string) attemptevidence.SemanticError {
	t.Helper()
	if encoded == nil {
		t.Fatal("semantic attempt evidence is absent")
	}
	if len(*encoded) > attemptevidence.MaxAttemptEvidenceBytes {
		t.Fatalf("semantic attempt evidence is %d bytes", len(*encoded))
	}
	var envelope struct {
		Version       int                            `json:"v"`
		SemanticError *attemptevidence.SemanticError `json:"semantic_error"`
	}
	if err := json.Unmarshal([]byte(*encoded), &envelope); err != nil {
		t.Fatalf("decode semantic attempt evidence: %v", err)
	}
	if envelope.Version != attemptevidence.EnvelopeVersion || envelope.SemanticError == nil {
		t.Fatalf("semantic attempt envelope = %#v", envelope)
	}
	return *envelope.SemanticError
}

func assertAttemptAxes(
	t *testing.T,
	attempt model.RequestAttempt,
	wantOutcome model.RequestAttemptOutcome,
	wantVisible bool,
	wantVerdict model.RequestAttemptHealthVerdict,
	wantCause model.RequestAttemptHealthCause,
) {
	t.Helper()
	if attempt.Outcome == nil || *attempt.Outcome != wantOutcome {
		t.Fatalf("attempt outcome = %#v, want %q", attempt.Outcome, wantOutcome)
	}
	if attempt.ResultVisibleToClient == nil || *attempt.ResultVisibleToClient != wantVisible {
		t.Fatalf("attempt visibility = %#v, want %t", attempt.ResultVisibleToClient, wantVisible)
	}
	if attempt.HealthVerdict == nil || *attempt.HealthVerdict != wantVerdict {
		t.Fatalf("attempt health verdict = %#v, want %q", attempt.HealthVerdict, wantVerdict)
	}
	if attempt.HealthCause == nil || *attempt.HealthCause != wantCause {
		t.Fatalf("attempt health cause = %#v, want %q", attempt.HealthCause, wantCause)
	}
}

type controlledTimer struct {
	fired atomic.Bool
}

func (t *controlledTimer) Stop() bool {
	return !t.fired.Swap(true)
}

type scheduledCall struct {
	timer    *controlledTimer
	callback func()
}

func (c scheduledCall) Fire() bool {
	if !c.timer.fired.CompareAndSwap(false, true) {
		return false
	}
	c.callback()
	return true
}

type controlledScheduler struct {
	calls chan scheduledCall
}

func newControlledScheduler() *controlledScheduler {
	return &controlledScheduler{calls: make(chan scheduledCall, 4)}
}

func (s *controlledScheduler) AfterFunc(_ time.Duration, callback func()) responseanalysis.Timer {
	timer := &controlledTimer{}
	s.calls <- scheduledCall{timer: timer, callback: callback}
	return timer
}
