package websocketproxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/websocket"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/selector"

	"go.uber.org/zap/zaptest"
)

type routingTestSelector struct {
	initial        ProviderSelection
	initialErr     error
	alternate      ProviderSelection
	alternateErr   error
	active         ProviderSelection
	activeErr      error
	evicted        []string
	activeRequests []ProviderLease
}

func (s *routingTestSelector) SelectInitial(context.Context, *model.SelectRequest) (ProviderSelection, error) {
	return s.initial, s.initialErr
}

func (s *routingTestSelector) SelectAlternate(context.Context, *model.SelectRequest, map[string]bool) (ProviderSelection, error) {
	return s.alternate, s.alternateErr
}

func (s *routingTestSelector) SelectActive(_ context.Context, _ *model.SelectRequest, active ProviderLease) (ProviderSelection, error) {
	s.activeRequests = append(s.activeRequests, active)
	return s.active, s.activeErr
}

func (*routingTestSelector) UpdateStickyWithTTL(*model.SelectRequest, string, time.Duration) {}

func (s *routingTestSelector) EvictProviderContinuity(providerID string) {
	s.evicted = append(s.evicted, providerID)
}

type routingTestActiveSessions struct {
	lease ProviderLease
	found bool
}

func (*routingTestActiveSessions) NewLiveTraffic() LiveTraffic { return nil }
func (*routingTestActiveSessions) Register(ActiveSession, <-chan struct{}, LiveTraffic) bool {
	return true
}
func (*routingTestActiveSessions) Unregister(string) bool     { return true }
func (*routingTestActiveSessions) UpdateModel(string, string) {}
func (*routingTestActiveSessions) MarkDataReceived(string)    {}
func (sessions *routingTestActiveSessions) FindActiveLeaseForRequest(*model.SelectRequest) (ProviderLease, bool) {
	return sessions.lease, sessions.found
}

type routingTestSeedStore struct {
	candidate *model.VisibleContinuitySeedCandidate
	seed      *model.VisibleContinuitySeed
	stored    *model.VisibleContinuitySeed
}

func (store *routingTestSeedStore) Lookup(model.StickyKey) (*model.VisibleContinuitySeedCandidate, bool) {
	return store.candidate, store.candidate != nil
}

func (store *routingTestSeedStore) Store(seed model.VisibleContinuitySeed) {
	copy := seed
	store.stored = &copy
}

func (store *routingTestSeedStore) CompareAndConsume(model.StickyKey, string) (*model.VisibleContinuitySeed, bool) {
	return store.seed, store.seed != nil
}

func routingTestProvider(id string) model.Provider {
	return model.Provider{
		ID:                 id,
		Name:               id,
		Enabled:            true,
		APITypes:           []model.ProviderAPIType{{ProviderID: id, APIType: APITypeCodex, BaseURL: "https://" + id + ".example"}},
		CredentialSessions: testCredentialSessions(id, APITypeCodex, credentialsession.KindAPIKey, id+"-key"),
	}
}

func routingTestSelection(provider *model.Provider, source selector.SelectionSource, generation uint64) ProviderSelection {
	lease := &fallbackProviderLease{provider: provider, generation: generation}
	lease.held.Store(true)
	return ProviderSelection{
		Lease:    lease,
		Metadata: selector.SelectionMetadata{Source: source},
	}
}

func TestPrepareWebSocketProviderAttemptUsesImmutableLeaseCredential(t *testing.T) {
	t.Parallel()

	provider := routingTestProvider("immutable")
	gateway := &Gateway{logger: zaptest.NewLogger(t), auth: providerauth.NewService(providerauth.Config{})}
	lease := gateway.newFallbackProviderLease(&provider, APITypeCodex)
	selected, ok := lease.CandidateSnapshot()
	if !ok {
		t.Fatal("fallback lease did not freeze a credential candidate")
	}
	selectedCredential := selected.Credential()
	provider.CredentialSessions[0].Credential.SecretData = "rotated-after-selection"
	orchestrator := &WebSocketSessionOrchestrator{
		handler:        gateway,
		apiType:        APITypeCodex,
		globalAuthMode: "bearer",
	}
	request := httptest.NewRequest(http.MethodGet, "http://gateway.test/v1/responses?trace=1", nil)

	prepared, failureCode, err := orchestrator.prepareProviderAttempt(context.Background(), request, &provider, lease)
	if err != nil {
		t.Fatalf("prepareProviderAttempt() error = %v", err)
	}
	if failureCode != "" {
		t.Fatalf("failure code = %q, want empty", failureCode)
	}
	if got := prepared.headers.Get("Authorization"); got != "Bearer "+selectedCredential.SecretData {
		t.Fatalf("Authorization = %q, want frozen selection credential", got)
	}
	if got := prepared.finalURL.String(); got != "wss://immutable.example/responses?trace=1" {
		t.Fatalf("final dial URL = %q", got)
	}
	if prepared.credential.SecretData != selectedCredential.SecretData {
		t.Fatalf("prepared credential = %q, want frozen %q", prepared.credential.SecretData, selectedCredential.SecretData)
	}
}

func TestPrepareWebSocketProviderAttemptAppliesCodexHygieneOnlyToCodexOperations(t *testing.T) {
	tests := []struct {
		name    string
		apiType string
	}{
		{name: "Codex operation is sanitized", apiType: APITypeCodex},
		{name: "non-Codex operation preserves legacy headers", apiType: "claude"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := "/codex/v1/responses"
			if test.apiType != APITypeCodex {
				path = "/v1/messages"
			}
			request := httptest.NewRequest(http.MethodGet, "http://gateway.test"+path, nil)
			request.Header.Set("Authorization", "Bearer client-key")
			request.Header.Set("X-Api-Key", "client-key")
			request.Header.Set("ChatGPT-Account-Id", "client-account")
			request.Header.Set("X-Client-Request-Id", "logical-request")
			request.Header.Set("Sec-WebSocket-Key", "transport-owned")
			var operation *codexws.Operation
			if test.apiType == APITypeCodex {
				var err error
				operation, err = testCodexRuntime(t).Begin(context.Background(), request, test.apiType, "ws-hygiene", "")
				if err != nil {
					t.Fatal(err)
				}
			}

			provider := routingTestProviderForAPI("provider", test.apiType)
			gateway := &Gateway{logger: zaptest.NewLogger(t), auth: providerauth.NewService(providerauth.Config{})}
			lease := gateway.newFallbackProviderLease(&provider, test.apiType)
			orchestrator := &WebSocketSessionOrchestrator{
				handler: gateway, apiType: test.apiType, globalAuthMode: "bearer", codexOperation: operation,
			}
			prepared, failureCode, err := orchestrator.prepareProviderAttempt(context.Background(), request, &provider, lease)
			if err != nil || failureCode != "" {
				t.Fatalf("prepareProviderAttempt() = failure:%q err:%v", failureCode, err)
			}
			if got := prepared.headers.Get("Authorization"); got != "Bearer provider-key" {
				t.Fatalf("Authorization = %q", got)
			}
			if got := prepared.headers.Get("X-Client-Request-Id"); got != "logical-request" {
				t.Fatalf("X-Client-Request-Id = %q", got)
			}
			if got := prepared.headers.Get("Sec-WebSocket-Key"); got != "" {
				t.Fatalf("transport-owned header survived: %q", got)
			}
			wantAPIKey, wantAccount := "", ""
			if test.apiType != APITypeCodex {
				wantAPIKey, wantAccount = "client-key", "client-account"
			}
			if got := prepared.headers.Get("X-Api-Key"); got != wantAPIKey {
				t.Fatalf("X-Api-Key = %q, want %q", got, wantAPIKey)
			}
			if got := prepared.headers.Get("ChatGPT-Account-Id"); got != wantAccount {
				t.Fatalf("ChatGPT-Account-Id = %q, want %q", got, wantAccount)
			}
		})
	}
}

func routingTestProviderForAPI(id, apiType string) model.Provider {
	return model.Provider{
		ID: id, Name: id, Enabled: true, AuthMode: "bearer",
		APITypes:           []model.ProviderAPIType{{ProviderID: id, APIType: apiType, BaseURL: "https://" + id + ".example"}},
		CredentialSessions: testCredentialSessions(id, apiType, credentialsession.KindAPIKey, "provider-key"),
	}
}

func TestGatewaySelectProviderWithTracking_PrefersEligibleActiveContinuity(t *testing.T) {
	strategyProvider := routingTestProvider("strategy")
	activeProvider := routingTestProvider("active")
	store := newMockStore()
	store.providers = []model.Provider{strategyProvider, activeProvider}
	initial := routingTestSelection(&strategyProvider, selector.SelectionSourceStrategy, 1)
	activeSource := routingTestSelection(&activeProvider, selector.SelectionSourceStrategy, 2)
	activeAttempt := routingTestSelection(&activeProvider, selector.SelectionSourceActiveContinuity, 2)
	selection := &routingTestSelector{initial: initial, active: activeAttempt}
	gateway := newTestGateway(t, Config{
		Store:          store,
		Selector:       selection,
		ActiveSessions: &routingTestActiveSessions{lease: activeSource.Lease, found: true},
		Logger:         zaptest.NewLogger(t),
	})
	req := &model.SelectRequest{ClientIP: "192.0.2.7", APIType: APITypeCodex, Model: "gpt-5", StickyMode: model.StickyModeModel}

	selected, err := gateway.selectProviderWithTracking(context.Background(), req, 0, nil)
	if err != nil {
		t.Fatalf("select provider: %v", err)
	}
	if selected.Provider().ID != activeProvider.ID || selected.Metadata.Source != selector.SelectionSourceActiveContinuity {
		t.Fatalf("selection = (%q, %q), want active continuity", selected.Provider().ID, selected.Metadata.Source)
	}
	if initial.Lease.Held() {
		t.Fatal("superseded strategy lease remained held")
	}
	if !activeSource.Lease.Held() || !selected.Lease.Held() || selected.Lease == activeSource.Lease {
		t.Fatal("active continuity must acquire a distinct lease without releasing its source")
	}
	if len(selection.activeRequests) != 1 || selection.activeRequests[0] != activeSource.Lease {
		t.Fatalf("active selection inputs = %#v", selection.activeRequests)
	}
}

func TestGatewaySelectProviderWithTracking_RejectsSharedOrWrongGenerationActiveLease(t *testing.T) {
	strategyProvider := routingTestProvider("strategy")
	activeProvider := routingTestProvider("active")
	req := &model.SelectRequest{APIType: APITypeCodex, Model: "gpt-5", StickyMode: model.StickyModeModel}

	tests := []struct {
		name      string
		active    func(ProviderSelection) ProviderSelection
		activeErr error
	}{
		{
			name: "source capability returned",
			active: func(source ProviderSelection) ProviderSelection {
				return source
			},
		},
		{
			name: "source capability returned with error",
			active: func(source ProviderSelection) ProviderSelection {
				return source
			},
			activeErr: errors.New("active lookup failed"),
		},
		{
			name: "different lifecycle generation returned",
			active: func(ProviderSelection) ProviderSelection {
				return routingTestSelection(&activeProvider, selector.SelectionSourceActiveContinuity, 99)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initial := routingTestSelection(&strategyProvider, selector.SelectionSourceStrategy, 1)
			activeSource := routingTestSelection(&activeProvider, selector.SelectionSourceStrategy, 2)
			activeResult := tt.active(activeSource)
			selection := &routingTestSelector{initial: initial, active: activeResult, activeErr: tt.activeErr}
			gateway := newTestGateway(t, Config{
				Store:          newMockStore(),
				Selector:       selection,
				ActiveSessions: &routingTestActiveSessions{lease: activeSource.Lease, found: true},
				Logger:         zaptest.NewLogger(t),
			})

			selected, err := gateway.selectProviderWithTracking(context.Background(), req, 0, nil)
			if err != nil || selected.Lease != initial.Lease {
				t.Fatalf("selection = (%#v, %v), want retained strategy selection", selected, err)
			}
			if !activeSource.Lease.Held() {
				t.Fatal("existing active session capability was released")
			}
			if activeResult.Lease != activeSource.Lease && activeResult.Lease.Held() {
				t.Fatal("invalid distinct active capability was not rolled back")
			}
			selected.Lease.Release()
			activeSource.Lease.Release()
		})
	}
}

func TestGatewaySelectProviderWithTracking_PreservesSelectorContinuityAndRejectsExcludedRetry(t *testing.T) {
	provider := routingTestProvider("selected")
	initial := routingTestSelection(&provider, selector.SelectionSourceStickyContinuity, 1)
	selection := &routingTestSelector{initial: initial}
	gateway := newTestGateway(t, Config{Store: newMockStore(), Selector: selection, Logger: zaptest.NewLogger(t)})
	req := &model.SelectRequest{APIType: APITypeCodex, Model: "gpt-5", StickyMode: model.StickyModeModel}

	selected, err := gateway.selectProviderWithTracking(context.Background(), req, 0, nil)
	if err != nil || selected.Provider() != &provider || selected.Metadata.Source != selector.SelectionSourceStickyContinuity {
		t.Fatalf("continuity selection = (%#v, %v)", selected, err)
	}

	alternate := routingTestSelection(&provider, selector.SelectionSourceAlternate, 2)
	selection.alternate = alternate
	selected, err = gateway.selectProviderWithTracking(context.Background(), req, 1, map[string]bool{provider.ID: true})
	if selected.Provider() != nil || !errors.Is(err, internal.ErrNoProvider) {
		t.Fatalf("excluded retry = (%#v, %v), want no provider", selected, err)
	}
	if alternate.Lease.Held() {
		t.Fatal("excluded alternate lease remained held")
	}

	selection.alternate = ProviderSelection{}
	selection.alternateErr = errors.New("selector unavailable")
	if _, err := gateway.selectProviderWithTracking(context.Background(), req, 1, nil); err == nil {
		t.Fatal("retry selector error = nil")
	}
}

func TestGatewaySelectProviderWithTracking_FallbackNormalizesMissingProvider(t *testing.T) {
	store := newMockStore()
	provider := routingTestProvider("fallback")
	store.providers = []model.Provider{provider}
	gateway := newTestGateway(t, Config{Store: store, Logger: zaptest.NewLogger(t)})
	req := &model.SelectRequest{APIType: APITypeCodex, Model: "gpt-5"}

	selected, err := gateway.selectProviderWithTracking(context.Background(), req, 0, nil)
	if err != nil || selected.Provider().ID != provider.ID || selected.Metadata.Source != selector.SelectionSourceStrategy || !selected.Lease.Held() {
		t.Fatalf("fallback selection = (%#v, %v)", selected, err)
	}
	selected.Lease.Release()

	store.providers = nil
	if _, err := gateway.selectProviderWithTracking(context.Background(), req, 0, nil); !errors.Is(err, internal.ErrNoProvider) {
		t.Fatalf("empty fallback error = %v, want no provider", err)
	}
}

func TestSelectorResultNormalization(t *testing.T) {
	wantErr := errors.New("selection failed")
	if result, err := normalizeProviderSelection(ProviderSelection{}, wantErr); result.Provider() != nil || !errors.Is(err, wantErr) {
		t.Fatalf("selector error normalization = (%#v, %v)", result, err)
	}
	if result, err := normalizeProviderSelection(ProviderSelection{}, nil); result.Provider() != nil || !errors.Is(err, internal.ErrNoProvider) {
		t.Fatalf("nil selector result = (%#v, %v)", result, err)
	}
	if provider, err := normalizeSelectedProvider(nil, wantErr); provider != nil || !errors.Is(err, wantErr) {
		t.Fatalf("provider error normalization = (%#v, %v)", provider, err)
	}
	if provider, err := normalizeSelectedProvider(nil, nil); provider != nil || !errors.Is(err, internal.ErrNoProvider) {
		t.Fatalf("nil provider = (%#v, %v)", provider, err)
	}
}

func TestSelectRequestForSameProviderRetry_RemovesCrossProviderState(t *testing.T) {
	req := &model.SelectRequest{
		APIType: APITypeCodex, Model: "gpt-5", SwitchMode: model.SwitchModeFailover,
		ProviderSwitchHistory:          &model.ProviderSwitchHistory{},
		ProviderContinuityContext:      &model.ProviderContinuityContext{},
		VisibleContinuitySeedCandidate: &model.VisibleContinuitySeedCandidate{},
		FailoverContext:                &model.FailoverContext{},
		MaxProviderSwitches:            3,
	}

	retry := selectRequestForSameProviderRetry(req)
	if retry == req || retry.SwitchMode != model.SwitchModeInitial || retry.ProviderSwitchHistory != nil ||
		retry.ProviderContinuityContext != nil || retry.VisibleContinuitySeedCandidate != nil ||
		retry.FailoverContext != nil || retry.MaxProviderSwitches != 0 {
		t.Fatalf("retry request retained cross-provider state: %#v", retry)
	}
	if req.SwitchMode != model.SwitchModeFailover || req.MaxProviderSwitches != 3 {
		t.Fatalf("source request mutated: %#v", req)
	}
	if selectRequestForSameProviderRetry(nil) != nil {
		t.Fatal("nil retry request should remain nil")
	}
}

func TestGatewayStoresVisibleContinuitySeedFromContext(t *testing.T) {
	seedStore := &routingTestSeedStore{}
	gateway := newTestGateway(t, Config{Store: newMockStore(), VisibleContinuitySeedStore: seedStore, Logger: zaptest.NewLogger(t)})
	observedAt := time.Date(2026, time.August, 3, 4, 5, 6, 0, time.UTC)
	req := &model.SelectRequest{ClientIP: "192.0.2.9", APIType: APITypeCodex, Model: "gpt-5", StickyMode: model.StickyModeModel}
	continuity := &model.ProviderContinuityContext{VisibleOriginProviderID: "origin", VisibleOriginVendor: "openai"}

	gateway.storeVisibleContinuitySeedFromContext(req, continuity, observedAt, nil)
	if seedStore.stored == nil {
		t.Fatal("visible continuity seed was not stored")
	}
	if seedStore.stored.OriginProviderID != "origin" || seedStore.stored.StrictestScope != model.ScopeAny ||
		len(seedStore.stored.ContaminatedVendors) != 1 || seedStore.stored.ContaminatedVendors[0] != "openai" {
		t.Fatalf("stored seed = %#v", seedStore.stored)
	}

	explicit := &model.VisibleContinuitySeed{SeedID: "explicit", ContinuityKey: model.StickyKey{APIType: APITypeCodex}}
	gateway.storeVisibleContinuitySeedFromContext(nil, nil, observedAt, explicit)
	if seedStore.stored.SeedID != explicit.SeedID {
		t.Fatalf("explicit seed = %#v", seedStore.stored)
	}

	gateway.storeVisibleContinuitySeedFromContext(nil, nil, observedAt, nil)
	(*Gateway)(nil).storeVisibleContinuitySeedFromContext(req, continuity, observedAt, nil)
}

func TestGatewayContextAndRequestHelpers(t *testing.T) {
	if outcome := gatewayCaptureOutcome(context.Background()); outcome.TerminationReason != "" {
		t.Fatalf("background outcome = %#v", outcome)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if outcome := gatewayCaptureOutcome(canceled); outcome.TerminationReason != requestcapture.TerminationReasonClientDisconnect || outcome.Failure.Primary.Code != requestcapture.FailureCodeGatewayContext {
		t.Fatalf("canceled outcome = %#v", outcome)
	}
	timedOut, timeoutCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer timeoutCancel()
	if outcome := gatewayCaptureOutcome(timedOut); outcome.TerminationReason != requestcapture.TerminationReasonTimeout || outcome.Failure.Primary.Code != requestcapture.FailureCodeGatewayContext {
		t.Fatalf("timeout outcome = %#v", outcome)
	}
	if !errors.Is(contextError(canceled), context.Canceled) {
		t.Fatalf("contextError did not preserve cancellation")
	}

	request := httptest.NewRequest(http.MethodGet, "http://gateway.test/responses", nil)
	request.RemoteAddr = "198.51.100.8:1234"
	if got := extractClientIP(request, false); got != "198.51.100.8" {
		t.Fatalf("remote client IP = %q", got)
	}
	request.Header.Set("X-Forwarded-For", " 203.0.113.7, 203.0.113.8")
	if got := extractClientIP(request, true); got != "203.0.113.7" {
		t.Fatalf("forwarded client IP = %q", got)
	}
	request.Header.Set("X-Forwarded-For", " , 203.0.113.8")
	request.Header.Set("X-Real-IP", " 203.0.113.9 ")
	if got := extractClientIP(request, true); got != "203.0.113.9" {
		t.Fatalf("real client IP = %q", got)
	}
	request.RemoteAddr = "local-socket"
	if got := extractClientIP(request, false); got != "local-socket" {
		t.Fatalf("unparsed remote address = %q", got)
	}
}
