package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/selector"

	"go.uber.org/zap"
)

func commitSwitchPreview(t *testing.T, tracker *providerSwitchTracker) model.SwitchMode {
	t.Helper()
	preview := tracker.previewProviderSwitch()
	mode := preview.tracker.currentMode()
	if err := tracker.commitProviderSwitch(preview); err != nil {
		t.Fatalf("commitProviderSwitch() error = %v", err)
	}
	return mode
}

func TestProviderSwitchTracker_PreVisibleReplacementUsesExplicitReplacementState(t *testing.T) {
	t.Parallel()

	selectReq := &model.SelectRequest{APIType: "claude"}
	tracker := newProviderSwitchTracker(selectReq, 4, nil)
	initialProvider := &model.Provider{ID: "initial-provider"}

	if mode := tracker.recordSelection(initialProvider, selector.BuildSelectionMetadata(selectReq, selector.SelectionSourceStrategy)); mode != model.SwitchModeInitial {
		t.Fatalf("recordSelection() mode = %q, want %q", mode, model.SwitchModeInitial)
	}
	if mode := commitSwitchPreview(t, &tracker); mode != model.SwitchModeReplacement {
		t.Fatalf("committed switch preview mode = %q, want %q", mode, model.SwitchModeReplacement)
	}
	if mode := tracker.prepareSelection(); mode != model.SwitchModeReplacement {
		t.Fatalf("prepareSelection() mode = %q, want %q", mode, model.SwitchModeReplacement)
	}
	if selectReq.SwitchMode != model.SwitchModeReplacement {
		t.Fatalf("SwitchMode = %q, want %q", selectReq.SwitchMode, model.SwitchModeReplacement)
	}
	if selectReq.FailoverContext != nil {
		t.Fatalf("FailoverContext = %+v, want nil once explicit switch state is wired", selectReq.FailoverContext)
	}
	if selectReq.MaxProviderSwitches != 3 {
		t.Fatalf("MaxProviderSwitches = %d, want 3 for total-attempt budget 4", selectReq.MaxProviderSwitches)
	}
}

func TestProviderSwitchTracker_StickyReentryConsumesSeedAndAttachesContinuity(t *testing.T) {
	t.Parallel()

	seedStore := NewVisibleContinuitySeedStore()
	selectReq := &model.SelectRequest{
		ClientIP:   "10.0.0.8",
		User:       "seed-user",
		APIType:    "codex",
		Model:      "gpt-5.4",
		StickyMode: model.StickyModeModel,
	}
	key := selector.BuildContinuityKey(selectReq)
	seedStore.Store(model.VisibleContinuitySeed{
		SeedID:           "seed-1",
		ContinuityKey:    key,
		OriginProviderID: "origin-provider",
		OriginVendor:     "vendor-a",
		ContaminatedVendors: []string{
			"vendor-a",
		},
		StrictestScope: model.ScopeVendor,
		ObservedAt:     time.Now().Add(-1500 * time.Millisecond),
	})

	tracker := newProviderSwitchTracker(selectReq, 3, seedStore)
	if !tracker.lookupVisibleContinuityCandidate() {
		t.Fatal("lookupVisibleContinuityCandidate() = false, want continuity candidate")
	}
	if tracker.continuityCandidate == nil {
		t.Fatal("expected continuity candidate snapshot")
	}

	originProvider := &model.Provider{
		ID:            "origin-provider",
		Vendor:        "vendor-a",
		FailoverScope: model.ScopeVendor,
	}
	mode := tracker.recordSelection(originProvider, selector.BuildSelectionMetadata(selectReq, selector.SelectionSourceStickyContinuity))
	if mode != model.SwitchModeInitial {
		t.Fatalf("recordSelection() mode = %q, want %q", mode, model.SwitchModeInitial)
	}
	if tracker.continuityContext == nil {
		t.Fatal("expected continuity context after successful sticky re-entry")
	}
	if tracker.continuityContext.VisibleOriginProviderID != originProvider.ID {
		t.Fatalf("VisibleOriginProviderID = %q, want %q", tracker.continuityContext.VisibleOriginProviderID, originProvider.ID)
	}
	if tracker.continuityCandidate == nil {
		t.Fatal("expected request-local seed provenance to remain available after consume")
	}
	if seedStore.Len() != 0 {
		t.Fatalf("seed store len = %d, want 0 after compare-and-consume", seedStore.Len())
	}

	if mode := commitSwitchPreview(t, &tracker); mode != model.SwitchModeFailover {
		t.Fatalf("committed switch preview mode = %q, want %q after attached continuity leaves origin", mode, model.SwitchModeFailover)
	}
	tracker.prepareSelection()
	if selectReq.SwitchMode != model.SwitchModeFailover {
		t.Fatalf("SwitchMode = %q, want %q", selectReq.SwitchMode, model.SwitchModeFailover)
	}
	if selectReq.ProviderContinuityContext == nil {
		t.Fatal("expected request-local continuity context on failover selection")
	}
}

func TestHandler_ServeHTTP_PreVisibleReplacementSkipsFailoverIsolationAndPopulatesAttemptFields(t *testing.T) {
	t.Parallel()

	var retrySelections atomic.Int32

	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"retry upstream"}`))
	}))
	defer primaryServer.Close()

	fallbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"provider":"fallback"}`))
	}))
	defer fallbackServer.Close()

	primaryProvider := withTestStaticCredential(&model.Provider{
		ID:   "http-replacement-primary",
		Name: "HTTP Replacement Primary",

		AuthMode:       "bearer",
		Enabled:        true,
		FailoverScope:  model.ScopeNone,
		AcceptFailover: model.ScopeNone,
		APITypes:       []model.ProviderAPIType{{ProviderID: "http-replacement-primary", APIType: "claude", BaseURL: primaryServer.URL}},
	}, "", "primary-key")
	fallbackProvider := withTestStaticCredential(&model.Provider{
		ID:   "http-replacement-fallback",
		Name: "HTTP Replacement Fallback",

		AuthMode:       "bearer",
		Enabled:        true,
		FailoverScope:  model.ScopeNone,
		AcceptFailover: model.ScopeNone,
		APITypes:       []model.ProviderAPIType{{ProviderID: "http-replacement-fallback", APIType: "claude", BaseURL: fallbackServer.URL}},
	}, "", "fallback-key")

	store := newMockStore()
	store.configs[ConfigKeyGlobalMaxAttempts] = "2"
	store.providers = []model.Provider{*primaryProvider, *fallbackProvider}
	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, req *model.SelectRequest) (*selectResult, error) {
			if req.SwitchMode != model.SwitchModeInitial {
				t.Fatalf("initial selection SwitchMode = %q, want %q", req.SwitchMode, model.SwitchModeInitial)
			}
			return &selectResult{Provider: primaryProvider, FromStickyCache: false}, nil
		},
		selectExcludingFunc: func(_ context.Context, req *model.SelectRequest, excludeIDs map[string]bool) (*model.Provider, error) {
			retrySelections.Add(1)
			if !excludeIDs[primaryProvider.ID] {
				t.Fatalf("excludeIDs = %v, want %q excluded", excludeIDs, primaryProvider.ID)
			}
			if req.SwitchMode != model.SwitchModeReplacement {
				t.Fatalf("replacement selection SwitchMode = %q, want %q", req.SwitchMode, model.SwitchModeReplacement)
			}
			if req.ProviderContinuityContext != nil {
				t.Fatalf("replacement must not attach continuity context, got %+v", req.ProviderContinuityContext)
			}
			if req.FailoverContext != nil {
				t.Fatalf("replacement must not rely on legacy failover context, got %+v", req.FailoverContext)
			}
			if req.MaxProviderSwitches != 1 {
				t.Fatalf("MaxProviderSwitches = %d, want 1 for two total attempts", req.MaxProviderSwitches)
			}
			return fallbackProvider, nil
		},
	}

	handler := NewHandler(Config{
		Store:    store,
		Selector: mockSel,
		Logger:   zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-3","message":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if body := w.Body.String(); !strings.Contains(body, `"provider":"fallback"`) {
		t.Fatalf("body = %q, want fallback response", body)
	}
	if got := retrySelections.Load(); got != 1 {
		t.Fatalf("retry selections = %d, want 1", got)
	}

	waitFor(t, func() bool { return store.AttemptsLen() >= 2 }, testPollTimeout)
	attempts := store.LastAttempts(2)
	if len(attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(attempts))
	}
	if attempts[0].SwitchMode != model.RequestAttemptSwitchModeInitial {
		t.Fatalf("first attempt SwitchMode = %q, want %q", attempts[0].SwitchMode, model.RequestAttemptSwitchModeInitial)
	}
	if attempts[1].SwitchMode != model.RequestAttemptSwitchModeReplacement {
		t.Fatalf("second attempt SwitchMode = %q, want %q", attempts[1].SwitchMode, model.RequestAttemptSwitchModeReplacement)
	}
	if attempts[0].ProviderAttempt != 1 || attempts[1].ProviderAttempt != 1 {
		t.Fatalf("provider attempts = [%d %d], want [1 1]", attempts[0].ProviderAttempt, attempts[1].ProviderAttempt)
	}
	if attempts[0].ProviderSwitchCount != 0 || attempts[1].ProviderSwitchCount != 1 {
		t.Fatalf("provider switch counts = [%d %d], want [0 1]", attempts[0].ProviderSwitchCount, attempts[1].ProviderSwitchCount)
	}
}

func TestHandler_ServeHTTP_SeededContinuityReentryTurnsSubsequentSwitchIntoFailover(t *testing.T) {
	t.Parallel()

	var retrySelections atomic.Int32

	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"origin failed before current response became visible"}`))
	}))
	defer primaryServer.Close()

	fallbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"provider":"fallback"}`))
	}))
	defer fallbackServer.Close()

	seedStore := NewVisibleContinuitySeedStore()
	requestTemplate := &model.SelectRequest{
		ClientIP:   "192.0.2.8",
		User:       "continuity-user",
		APIType:    "claude",
		Model:      "claude-3",
		StickyMode: model.StickyModeModel,
	}
	seedKey := selector.BuildContinuityKey(requestTemplate)
	seedStore.Store(model.VisibleContinuitySeed{
		SeedID:              "seed-http-1",
		ContinuityKey:       seedKey,
		OriginProviderID:    "http-seeded-primary",
		OriginVendor:        "vendor-a",
		ContaminatedVendors: []string{"vendor-a"},
		StrictestScope:      model.ScopeVendor,
		ObservedAt:          time.Now().Add(-2 * time.Second),
	})

	primaryProvider := withTestStaticCredential(&model.Provider{
		ID:   "http-seeded-primary",
		Name: "HTTP Seeded Primary",

		AuthMode:       "bearer",
		Enabled:        true,
		Vendor:         "vendor-a",
		FailoverScope:  model.ScopeVendor,
		AcceptFailover: model.ScopeAny,
		APITypes:       []model.ProviderAPIType{{ProviderID: "http-seeded-primary", APIType: "claude", BaseURL: primaryServer.URL}},
	}, "", "primary-key")
	fallbackProvider := withTestStaticCredential(&model.Provider{
		ID:   "http-seeded-fallback",
		Name: "HTTP Seeded Fallback",

		AuthMode:       "bearer",
		Enabled:        true,
		Vendor:         "vendor-a",
		FailoverScope:  model.ScopeAny,
		AcceptFailover: model.ScopeVendor,
		APITypes:       []model.ProviderAPIType{{ProviderID: "http-seeded-fallback", APIType: "claude", BaseURL: fallbackServer.URL}},
	}, "", "fallback-key")

	store := newMockStore()
	store.configs[ConfigKeyGlobalMaxAttempts] = "2"
	store.providers = []model.Provider{*primaryProvider, *fallbackProvider}
	mockSel := &mockSelector{
		selectWithMetadataFunc: func(_ context.Context, req *model.SelectRequest) (*selectResult, error) {
			if req.Model != "claude-3" {
				t.Fatalf("initial selection model = %q, want %q", req.Model, "claude-3")
			}
			if req.SwitchMode != model.SwitchModeInitial {
				t.Fatalf("initial seeded selection SwitchMode = %q, want %q", req.SwitchMode, model.SwitchModeInitial)
			}
			if req.VisibleContinuitySeedCandidate == nil {
				t.Fatal("expected continuity seed candidate before first selection")
			}
			if req.VisibleContinuitySeedCandidate.OriginProviderID != primaryProvider.ID {
				t.Fatalf("candidate origin = %q, want %q", req.VisibleContinuitySeedCandidate.OriginProviderID, primaryProvider.ID)
			}
			return &selectResult{Provider: primaryProvider, FromStickyCache: true}, nil
		},
		selectExcludingFunc: func(_ context.Context, req *model.SelectRequest, excludeIDs map[string]bool) (*model.Provider, error) {
			retrySelections.Add(1)
			if !excludeIDs[primaryProvider.ID] {
				t.Fatalf("excludeIDs = %v, want %q excluded", excludeIDs, primaryProvider.ID)
			}
			if req.SwitchMode != model.SwitchModeFailover {
				t.Fatalf("post-reentry switch SwitchMode = %q, want %q", req.SwitchMode, model.SwitchModeFailover)
			}
			if req.ProviderContinuityContext == nil {
				t.Fatal("expected request-local continuity context after seed re-entry")
			}
			if req.ProviderContinuityContext.VisibleOriginProviderID != primaryProvider.ID {
				t.Fatalf("VisibleOriginProviderID = %q, want %q", req.ProviderContinuityContext.VisibleOriginProviderID, primaryProvider.ID)
			}
			if req.VisibleContinuitySeedCandidate == nil || req.VisibleContinuitySeedCandidate.SeedID != "seed-http-1" {
				t.Fatalf("expected consumed request-local seed provenance, got %+v", req.VisibleContinuitySeedCandidate)
			}
			return fallbackProvider, nil
		},
	}

	handler := NewHandler(Config{
		Store:                      store,
		Selector:                   mockSel,
		VisibleContinuitySeedStore: seedStore,
		Logger:                     zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-3","message":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", "continuity-user")
	req.RemoteAddr = "192.0.2.8:1234"
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if got := retrySelections.Load(); got != 1 {
		t.Fatalf("retry selections = %d, want 1", got)
	}
	if seedStore.Len() != 0 {
		t.Fatalf("seed store len = %d, want 0 after compare-and-consume", seedStore.Len())
	}

	waitFor(t, func() bool { return store.AttemptsLen() >= 2 }, testPollTimeout)
	attempts := store.LastAttempts(2)
	if attempts[0].SwitchMode != model.RequestAttemptSwitchModeInitial {
		t.Fatalf("first attempt SwitchMode = %q, want %q", attempts[0].SwitchMode, model.RequestAttemptSwitchModeInitial)
	}
	if !attempts[0].ContinuitySeeded {
		t.Fatal("first attempt should retain continuity-seeded provenance")
	}
	if attempts[0].ContinuityOriginProviderID != primaryProvider.ID {
		t.Fatalf("first attempt ContinuityOriginProviderID = %q, want %q", attempts[0].ContinuityOriginProviderID, primaryProvider.ID)
	}
	if attempts[0].ContinuitySeedAgeMs == nil {
		t.Fatal("first attempt should record continuity seed age")
	}
	if attempts[1].SwitchMode != model.RequestAttemptSwitchModeFailover {
		t.Fatalf("second attempt SwitchMode = %q, want %q", attempts[1].SwitchMode, model.RequestAttemptSwitchModeFailover)
	}
	if attempts[1].ProviderSwitchCount != 1 {
		t.Fatalf("second attempt ProviderSwitchCount = %d, want 1", attempts[1].ProviderSwitchCount)
	}
	if attempts[1].ContinuityOriginProviderID != primaryProvider.ID {
		t.Fatalf("second attempt ContinuityOriginProviderID = %q, want %q", attempts[1].ContinuityOriginProviderID, primaryProvider.ID)
	}
}

func TestProviderSwitchTracker_SeedCandidateDowngradesWithoutOriginReentry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source selector.SelectionSource
	}{
		{
			name:   "continuity_hit_but_other_provider_selected",
			source: selector.SelectionSourceStickyContinuity,
		},
		{
			name:   "origin_unavailable_falls_back_to_strategy_selection",
			source: selector.SelectionSourceStrategy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seedStore := NewVisibleContinuitySeedStore()
			selectReq := &model.SelectRequest{
				ClientIP:   "198.51.100.31",
				User:       "seed-user",
				APIType:    "claude",
				Model:      "claude-3",
				StickyMode: model.StickyModeModel,
			}

			key := selector.BuildContinuityKey(selectReq)
			seedStore.Store(model.VisibleContinuitySeed{
				SeedID:           "seed-downgrade",
				ContinuityKey:    key,
				OriginProviderID: "origin-provider",
				OriginVendor:     "vendor-a",
				ObservedAt:       time.Now().Add(-250 * time.Millisecond),
			})

			tracker := newProviderSwitchTracker(selectReq, 3, seedStore)
			if !tracker.lookupVisibleContinuityCandidate() {
				t.Fatal("lookupVisibleContinuityCandidate() = false, want continuity candidate")
			}

			selectedProvider := &model.Provider{ID: "fallback-provider", Vendor: "vendor-b"}
			if mode := tracker.recordSelection(selectedProvider, selector.BuildSelectionMetadata(selectReq, tt.source)); mode != model.SwitchModeInitial {
				t.Fatalf("recordSelection() mode = %q, want %q", mode, model.SwitchModeInitial)
			}
			if selectReq.ProviderContinuityContext != nil {
				t.Fatalf("ProviderContinuityContext = %+v, want nil after continuity downgrade", selectReq.ProviderContinuityContext)
			}
			if selectReq.VisibleContinuitySeedCandidate != nil {
				t.Fatalf("VisibleContinuitySeedCandidate = %+v, want nil after continuity downgrade", selectReq.VisibleContinuitySeedCandidate)
			}
			if seedStore.Len() != 1 {
				t.Fatalf("seed store len = %d, want 1 when seed was not consumed", seedStore.Len())
			}
			if mode := commitSwitchPreview(t, &tracker); mode != model.SwitchModeReplacement {
				t.Fatalf("committed switch preview mode = %q, want %q", mode, model.SwitchModeReplacement)
			}
			if selectReq.SwitchMode != model.SwitchModeReplacement {
				t.Fatalf("SwitchMode = %q, want %q", selectReq.SwitchMode, model.SwitchModeReplacement)
			}
			if selectReq.FailoverContext != nil {
				t.Fatalf("FailoverContext = %+v, want nil when downgrade stays in replacement semantics", selectReq.FailoverContext)
			}
		})
	}
}

func TestProviderSwitchTracker_CompetingRequestsConsumeSeedAtMostOnce(t *testing.T) {
	t.Parallel()

	seedStore := NewVisibleContinuitySeedStore()
	newSelectReq := func() *model.SelectRequest {
		return &model.SelectRequest{
			ClientIP:   "198.51.100.32",
			User:       "seed-user",
			APIType:    "claude",
			Model:      "claude-3",
			StickyMode: model.StickyModeModel,
		}
	}

	firstReq := newSelectReq()
	key := selector.BuildContinuityKey(firstReq)
	seedStore.Store(model.VisibleContinuitySeed{
		SeedID:           "seed-race",
		ContinuityKey:    key,
		OriginProviderID: "origin-provider",
		OriginVendor:     "vendor-a",
		ObservedAt:       time.Now().Add(-300 * time.Millisecond),
	})

	secondReq := newSelectReq()
	firstTracker := newProviderSwitchTracker(firstReq, 3, seedStore)
	secondTracker := newProviderSwitchTracker(secondReq, 3, seedStore)
	if !firstTracker.lookupVisibleContinuityCandidate() {
		t.Fatal("first lookupVisibleContinuityCandidate() = false, want continuity candidate")
	}
	if !secondTracker.lookupVisibleContinuityCandidate() {
		t.Fatal("second lookupVisibleContinuityCandidate() = false, want continuity candidate")
	}

	originProvider := &model.Provider{ID: "origin-provider", Vendor: "vendor-a"}
	if mode := firstTracker.recordSelection(originProvider, selector.BuildSelectionMetadata(firstReq, selector.SelectionSourceStickyContinuity)); mode != model.SwitchModeInitial {
		t.Fatalf("first recordSelection() mode = %q, want %q", mode, model.SwitchModeInitial)
	}
	if mode := secondTracker.recordSelection(originProvider, selector.BuildSelectionMetadata(secondReq, selector.SelectionSourceStickyContinuity)); mode != model.SwitchModeInitial {
		t.Fatalf("second recordSelection() mode = %q, want %q", mode, model.SwitchModeInitial)
	}

	var winnerReq, loserReq *model.SelectRequest
	var winnerTracker, loserTracker *providerSwitchTracker
	switch {
	case firstReq.ProviderContinuityContext != nil && secondReq.ProviderContinuityContext == nil:
		winnerReq, loserReq = firstReq, secondReq
		winnerTracker, loserTracker = &firstTracker, &secondTracker
	case secondReq.ProviderContinuityContext != nil && firstReq.ProviderContinuityContext == nil:
		winnerReq, loserReq = secondReq, firstReq
		winnerTracker, loserTracker = &secondTracker, &firstTracker
	default:
		t.Fatalf(
			"expected exactly one continuity attachment, got first=%+v second=%+v",
			firstReq.ProviderContinuityContext,
			secondReq.ProviderContinuityContext,
		)
	}

	if winnerReq.VisibleContinuitySeedCandidate == nil {
		t.Fatal("winner should retain request-local seed provenance after compare-and-consume")
	}
	if loserReq.ProviderContinuityContext != nil {
		t.Fatalf("loser ProviderContinuityContext = %+v, want nil after compare-and-consume miss", loserReq.ProviderContinuityContext)
	}
	if loserReq.VisibleContinuitySeedCandidate != nil {
		t.Fatalf("loser VisibleContinuitySeedCandidate = %+v, want nil after compare-and-consume miss", loserReq.VisibleContinuitySeedCandidate)
	}
	if seedStore.Len() != 0 {
		t.Fatalf("seed store len = %d, want 0 after exactly one request consumes the seed", seedStore.Len())
	}
	if mode := commitSwitchPreview(t, winnerTracker); mode != model.SwitchModeFailover {
		t.Fatalf("winner committed switch preview mode = %q, want %q", mode, model.SwitchModeFailover)
	}
	if mode := commitSwitchPreview(t, loserTracker); mode != model.SwitchModeReplacement {
		t.Fatalf("loser committed switch preview mode = %q, want %q", mode, model.SwitchModeReplacement)
	}
	if loserReq.FailoverContext != nil {
		t.Fatalf("loser FailoverContext = %+v, want nil when continuity was not attached", loserReq.FailoverContext)
	}
}

func TestHandler_ServeHTTP_HTTPNormalCompletionDoesNotStoreContinuitySeed(t *testing.T) {
	t.Parallel()

	seedStore := NewVisibleContinuitySeedStore()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	provider := withTestStaticCredential(model.Provider{
		ID:   "http-normal-provider",
		Name: "HTTP Normal Provider",

		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "http-normal-provider", APIType: "claude", BaseURL: upstream.URL}},
	}, "", "normal-key")

	store := newMockStore()
	store.providers = []model.Provider{provider}
	store.configs[ConfigKeyGlobalMaxAttempts] = "1"

	handler := NewHandler(Config{
		Store:                      store,
		VisibleContinuitySeedStore: seedStore,
		Logger:                     zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-3","message":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if seedStore.Len() != 0 {
		t.Fatalf("seed store len = %d, want 0 after normal HTTP completion", seedStore.Len())
	}
}

func TestHandler_ServeHTTP_ExhaustedStatusResponseStoresVisibleContinuitySeed(t *testing.T) {
	t.Parallel()

	seedStore := NewVisibleContinuitySeedStore()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"upstream unavailable"}`))
	}))
	defer upstream.Close()

	provider := withTestStaticCredential(model.Provider{
		ID:   "http-failure-provider",
		Name: "HTTP Failure Provider",

		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "http-failure-provider", APIType: "claude", BaseURL: upstream.URL}},
	}, "", "failure-key")

	store := newMockStore()
	store.providers = []model.Provider{provider}
	store.configs[ConfigKeyGlobalMaxAttempts] = "1"

	handler := NewHandler(Config{
		Store:                      store,
		VisibleContinuitySeedStore: seedStore,
		Logger:                     zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-3","message":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("status = %d, want non-success gateway failure", w.Code)
	}
	if seedStore.Len() != 1 {
		t.Fatalf("seed store len = %d, want 1 after the exhausted status body became client-visible", seedStore.Len())
	}
}

func TestHandler_ServeHTTP_HTTPClientDisconnectDoesNotStoreContinuitySeed(t *testing.T) {
	t.Parallel()

	seedStore := NewVisibleContinuitySeedStore()
	clientDisconnected := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("ResponseWriter doesn't support Flusher")
		}

		_, _ = w.Write([]byte("data: {\"chunk\":1}\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("data: {\"chunk\":2}\n\n"))
		flusher.Flush()

		<-r.Context().Done()
		close(clientDisconnected)
	}))
	defer upstream.Close()

	provider := withTestStaticCredential(model.Provider{
		ID:   "http-disconnect-provider",
		Name: "HTTP Disconnect Provider",

		AuthMode: "bearer",
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: "http-disconnect-provider", APIType: "codex", BaseURL: upstream.URL}},
	}, "", "disconnect-key")

	store := newMockStore()
	store.providers = []model.Provider{provider}

	handler := NewHandler(Config{
		Store:                      store,
		VisibleContinuitySeedStore: seedStore,
		Logger:                     zap.NewNop(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader(`{"model":"o3-pro"}`))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(w, req)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not return after client disconnect")
	}

	select {
	case <-clientDisconnected:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream did not see client disconnect")
	}

	if seedStore.Len() != 0 {
		t.Fatalf("seed store len = %d, want 0 after HTTP client disconnect", seedStore.Len())
	}
}

func TestHandler_ServeHTTP_SameProviderRetryIncrementsProviderAttemptWithoutSwitchCount(t *testing.T) {
	t.Parallel()

	var upstreamAttempts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch upstreamAttempts.Add(1) {
		case 1:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"retry me"}`))
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer upstream.Close()

	provider := withTestStaticCredential(model.Provider{
		ID:   "same-provider-retry",
		Name: "Same Provider Retry",

		AuthMode:   "bearer",
		Enabled:    true,
		MaxRetries: 1,
		APITypes:   []model.ProviderAPIType{{ProviderID: "same-provider-retry", APIType: "claude", BaseURL: upstream.URL}},
	}, "", "retry-key")

	store := newMockStore()
	store.providers = []model.Provider{provider}
	store.configs[ConfigKeyGlobalMaxAttempts] = "2"

	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-3","message":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	waitFor(t, func() bool { return store.AttemptsLen() >= 2 }, testPollTimeout)
	attempts := store.LastAttempts(2)
	if attempts[0].ProviderID != provider.ID || attempts[1].ProviderID != provider.ID {
		t.Fatalf("provider IDs = [%q %q], want both %q", attempts[0].ProviderID, attempts[1].ProviderID, provider.ID)
	}
	if attempts[0].ProviderAttempt != 1 {
		t.Fatalf("first attempt ProviderAttempt = %d, want 1", attempts[0].ProviderAttempt)
	}
	if attempts[1].ProviderAttempt != 2 {
		t.Fatalf("second attempt ProviderAttempt = %d, want 2", attempts[1].ProviderAttempt)
	}
	if attempts[0].ProviderSwitchCount != 0 {
		t.Fatalf("first attempt ProviderSwitchCount = %d, want 0", attempts[0].ProviderSwitchCount)
	}
	if attempts[1].ProviderSwitchCount != 0 {
		t.Fatalf("second attempt ProviderSwitchCount = %d, want 0", attempts[1].ProviderSwitchCount)
	}
}
