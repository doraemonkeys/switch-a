package providerauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const callbackLifecycleObservationTimeout = time.Second

type mutableClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *mutableClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (*mutableClock) NewTicker(delay time.Duration) *time.Ticker {
	return time.NewTicker(delay)
}

func (c *mutableClock) Advance(delay time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(delay)
	c.mu.Unlock()
}

type recordingCallbackEndpoint struct {
	mu          sync.Mutex
	active      bool
	starts      int
	shutdowns   int
	startErr    error
	shutdownErr error
}

func (e *recordingCallbackEndpoint) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.startErr != nil {
		return e.startErr
	}
	if !e.active {
		e.active = true
		e.starts++
	}
	return nil
}

func (e *recordingCallbackEndpoint) Shutdown(context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.active {
		e.active = false
		e.shutdowns++
	}
	return e.shutdownErr
}

func (e *recordingCallbackEndpoint) snapshot() (active bool, starts, shutdowns int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.active, e.starts, e.shutdowns
}

type manualScheduledTask struct {
	mu      sync.Mutex
	delay   time.Duration
	task    func()
	stopped bool
}

func (t *manualScheduledTask) Stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	wasActive := !t.stopped
	t.stopped = true
	return wasActive
}

func (t *manualScheduledTask) Run() {
	t.mu.Lock()
	if t.stopped {
		t.mu.Unlock()
		return
	}
	t.stopped = true
	task := t.task
	t.mu.Unlock()
	task()
}

func (t *manualScheduledTask) RunAfterStopRace() {
	t.mu.Lock()
	task := t.task
	t.mu.Unlock()
	task()
}

func (t *manualScheduledTask) isStopped() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stopped
}

type manualScheduler struct {
	mu    sync.Mutex
	tasks []*manualScheduledTask
}

func (s *manualScheduler) AfterFunc(delay time.Duration, task func()) scheduledTask {
	scheduled := &manualScheduledTask{delay: delay, task: task}
	s.mu.Lock()
	s.tasks = append(s.tasks, scheduled)
	s.mu.Unlock()
	return scheduled
}

func (s *manualScheduler) latest(t *testing.T) *manualScheduledTask {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.tasks) == 0 {
		t.Fatal("no expiry task was scheduled")
	}
	return s.tasks[len(s.tasks)-1]
}

func (s *manualScheduler) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.tasks)
}

func newCallbackLifecycleTestService(cfg Config, endpoint callbackEndpoint, scheduler *manualScheduler) *Service {
	return newService(cfg, serviceRuntime{
		callback:      endpoint,
		scheduleAfter: scheduler.AfterFunc,
	})
}

func TestChatGPTLoginCallbackEndpoint_IsStartedOnDemandAndShared(t *testing.T) {
	now := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	clock := &mutableClock{now: now}
	endpoint := &recordingCallbackEndpoint{}
	scheduler := &manualScheduler{}
	service := newCallbackLifecycleTestService(Config{Clock: clock}, endpoint, scheduler)

	if active, starts, _ := endpoint.snapshot(); active || starts != 0 {
		t.Fatalf("endpoint before login = (active=%t, starts=%d), want idle", active, starts)
	}
	first, err := service.StartChatGPTLogin()
	if err != nil {
		t.Fatalf("first StartChatGPTLogin returned error: %v", err)
	}
	second, err := service.StartChatGPTLogin()
	if err != nil {
		t.Fatalf("second StartChatGPTLogin returned error: %v", err)
	}
	if first.LoginID == second.LoginID {
		t.Fatal("concurrent-capable login sessions should have distinct ids")
	}
	active, starts, shutdowns := endpoint.snapshot()
	if !active || starts != 1 || shutdowns != 0 {
		t.Fatalf("endpoint = (active=%t, starts=%d, shutdowns=%d), want one shared run", active, starts, shutdowns)
	}
	if latest := scheduler.latest(t); latest.delay != loginSessionTTL {
		t.Fatalf("expiry delay = %s, want %s", latest.delay, loginSessionTTL)
	}

	service.mu.Lock()
	pendingCount := len(service.pendingByLoginID)
	service.mu.Unlock()
	if pendingCount != 2 {
		t.Fatalf("pending login count = %d, want 2", pendingCount)
	}

	if err := service.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}
}

func TestStartChatGPTLogin_StoresPendingSessionAndBuildsAuthorizeURL(t *testing.T) {
	now := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)
	endpoint := &recordingCallbackEndpoint{}
	scheduler := &manualScheduler{}
	service := newCallbackLifecycleTestService(Config{Clock: fixedClock{now: now}}, endpoint, scheduler)
	t.Cleanup(func() {
		if err := service.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown returned error: %v", err)
		}
	})

	service.mu.Lock()
	service.storePendingLoginLocked(pendingLogin{
		loginID:      "expired-login",
		state:        "expired-state",
		codeVerifier: "expired-verifier",
		expiresAt:    now.Add(-time.Second),
	})
	service.completed["expired-completed"] = completedLogin{
		loginID:   "expired-completed",
		expiresAt: now.Add(-time.Second),
	}
	service.mu.Unlock()

	response, err := service.StartChatGPTLogin()
	if err != nil {
		t.Fatalf("StartChatGPTLogin returned error: %v", err)
	}

	parsedURL, err := url.Parse(response.AuthURL)
	if err != nil {
		t.Fatalf("url.Parse returned error: %v", err)
	}
	if parsedURL.Scheme != "https" || parsedURL.Host != "auth.openai.com" || parsedURL.Path != "/oauth/authorize" {
		t.Fatalf("authURL = %s, want auth.openai.com/oauth/authorize", parsedURL.String())
	}

	query := parsedURL.Query()
	if query.Get("response_type") != "code" {
		t.Fatalf("response_type = %q, want %q", query.Get("response_type"), "code")
	}
	if query.Get("client_id") != defaultOAuthClientID {
		t.Fatalf("client_id = %q, want %q", query.Get("client_id"), defaultOAuthClientID)
	}
	if query.Get("redirect_uri") != LoopbackCallbackAddress() {
		t.Fatalf("redirect_uri = %q, want %q", query.Get("redirect_uri"), LoopbackCallbackAddress())
	}
	if query.Get("scope") != defaultOAuthScope {
		t.Fatalf("scope = %q, want %q", query.Get("scope"), defaultOAuthScope)
	}
	if query.Get("originator") != defaultOAuthOriginator {
		t.Fatalf("originator = %q, want %q", query.Get("originator"), defaultOAuthOriginator)
	}
	if query.Get("code_challenge") == "" {
		t.Fatal("code_challenge = empty, want PKCE challenge")
	}

	service.mu.Lock()
	pending, ok := service.pendingByLoginID[response.LoginID]
	_, expiredPendingStillTracked := service.pendingByLoginID["expired-login"]
	_, expiredCompletedStillTracked := service.completed["expired-completed"]
	service.mu.Unlock()

	if !ok {
		t.Fatalf("pendingByLoginID[%q] missing", response.LoginID)
	}
	if pending.state == "" || pending.codeVerifier == "" {
		t.Fatalf("pending login = %#v, want state and verifier", pending)
	}
	if !pending.expiresAt.Equal(now.Add(loginSessionTTL)) {
		t.Fatalf("expiresAt = %s, want %s", pending.expiresAt, now.Add(loginSessionTTL))
	}
	if query.Get("state") != pending.state {
		t.Fatalf("state query = %q, want %q", query.Get("state"), pending.state)
	}
	if expiredPendingStillTracked {
		t.Fatal("expired pending login should be pruned before storing a new one")
	}
	if expiredCompletedStillTracked {
		t.Fatal("expired completed login should be pruned before storing a new one")
	}
}

func TestChatGPTLoginCallbackEndpoint_ConcurrentStartsShareOneRun(t *testing.T) {
	const loginCount = 12
	endpoint := &recordingCallbackEndpoint{}
	scheduler := &manualScheduler{}
	service := newCallbackLifecycleTestService(Config{}, endpoint, scheduler)

	loginIDs := make(chan string, loginCount)
	errorsCh := make(chan error, loginCount)
	var wg sync.WaitGroup
	for range loginCount {
		wg.Go(func() {
			login, err := service.StartChatGPTLogin()
			if err != nil {
				errorsCh <- err
				return
			}
			loginIDs <- login.LoginID
		})
	}
	wg.Wait()
	close(loginIDs)
	close(errorsCh)
	for err := range errorsCh {
		t.Errorf("StartChatGPTLogin returned error: %v", err)
	}
	uniqueLoginIDs := make(map[string]struct{}, loginCount)
	for loginID := range loginIDs {
		uniqueLoginIDs[loginID] = struct{}{}
	}
	if len(uniqueLoginIDs) != loginCount {
		t.Fatalf("unique login ids = %d, want %d", len(uniqueLoginIDs), loginCount)
	}
	if active, starts, shutdowns := endpoint.snapshot(); !active || starts != 1 || shutdowns != 0 {
		t.Fatalf("endpoint = (active=%t, starts=%d, shutdowns=%d), want one run", active, starts, shutdowns)
	}
	service.mu.Lock()
	pendingCount := len(service.pendingByLoginID)
	service.mu.Unlock()
	if pendingCount != loginCount {
		t.Fatalf("pending login count = %d, want %d", pendingCount, loginCount)
	}
	if err := service.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}
}

func TestChatGPTLoginCallbackEndpoint_StartFailureDoesNotPublishSession(t *testing.T) {
	listenErr := errors.New("callback port unavailable")
	endpoint := &recordingCallbackEndpoint{startErr: listenErr}
	scheduler := &manualScheduler{}
	service := newCallbackLifecycleTestService(Config{}, endpoint, scheduler)

	response, err := service.StartChatGPTLogin()
	if !errors.Is(err, listenErr) {
		t.Fatalf("StartChatGPTLogin error = %v, want %v", err, listenErr)
	}
	if response != nil {
		t.Fatalf("response = %#v, want nil after callback start failure", response)
	}
	service.mu.Lock()
	pendingCount := len(service.pendingByLoginID)
	callbackActive := service.callbackActive
	service.mu.Unlock()
	if pendingCount != 0 || callbackActive {
		t.Fatalf("failed login published lifecycle state: pending=%d active=%t", pendingCount, callbackActive)
	}
	if scheduler.count() != 0 {
		t.Fatalf("scheduled expiry tasks = %d, want 0", scheduler.count())
	}
}

func TestChatGPTLoginCallbackEndpoint_StopsAfterLastSessionAndRestarts(t *testing.T) {
	endpoint := &recordingCallbackEndpoint{}
	scheduler := &manualScheduler{}
	service := newCallbackLifecycleTestService(Config{}, endpoint, scheduler)

	first := mustStartChatGPTLogin(t, service)
	second := mustStartChatGPTLogin(t, service)
	cancelLoginThroughCallback(t, service, first.AuthURL)
	if active, starts, shutdowns := endpoint.snapshot(); !active || starts != 1 || shutdowns != 0 {
		t.Fatalf("endpoint after first cancellation = (active=%t, starts=%d, shutdowns=%d), want shared run retained", active, starts, shutdowns)
	}

	cancelLoginThroughCallback(t, service, second.AuthURL)
	waitForCallbackEndpointState(t, endpoint, false, 1, 1)

	mustStartChatGPTLogin(t, service)
	if active, starts, shutdowns := endpoint.snapshot(); !active || starts != 2 || shutdowns != 1 {
		t.Fatalf("endpoint after new login = (active=%t, starts=%d, shutdowns=%d), want fresh run", active, starts, shutdowns)
	}
	if err := service.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}
}

func TestChatGPTLoginCallbackEndpoint_ExpiresAndReleasesPort(t *testing.T) {
	now := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	clock := &mutableClock{now: now}
	endpoint := &recordingCallbackEndpoint{}
	scheduler := &manualScheduler{}
	service := newCallbackLifecycleTestService(Config{Clock: clock}, endpoint, scheduler)
	login := mustStartChatGPTLogin(t, service)
	expiry := scheduler.latest(t)

	clock.Advance(loginSessionTTL)
	expiry.Run()
	if active, starts, shutdowns := endpoint.snapshot(); active || starts != 1 || shutdowns != 1 {
		t.Fatalf("endpoint after expiry = (active=%t, starts=%d, shutdowns=%d), want released", active, starts, shutdowns)
	}
	status, err := service.GetChatGPTLoginStatus(login.LoginID)
	if err != nil {
		t.Fatalf("GetChatGPTLoginStatus returned error: %v", err)
	}
	if status.Status != ChatGPTLoginStatusExpired {
		t.Fatalf("status = %q, want %q", status.Status, ChatGPTLoginStatusExpired)
	}
}

func TestChatGPTLoginCallbackEndpoint_StaleExpiryCannotStopNewerSessions(t *testing.T) {
	endpoint := &recordingCallbackEndpoint{}
	scheduler := &manualScheduler{}
	service := newCallbackLifecycleTestService(Config{}, endpoint, scheduler)
	mustStartChatGPTLogin(t, service)
	staleExpiry := scheduler.latest(t)
	mustStartChatGPTLogin(t, service)
	if !staleExpiry.isStopped() {
		t.Fatal("earlier expiry task was not cancelled when session set changed")
	}

	// time.Timer.Stop may race with a callback that has already begun. The epoch
	// guard must make that stale callback harmless even in that narrow window.
	staleExpiry.RunAfterStopRace()
	if active, starts, shutdowns := endpoint.snapshot(); !active || starts != 1 || shutdowns != 0 {
		t.Fatalf("endpoint after stale expiry = (active=%t, starts=%d, shutdowns=%d), want unchanged", active, starts, shutdowns)
	}
	if err := service.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}
}

func TestChatGPTLoginCallbackEndpoint_SuccessfulExchangeReleasesPort(t *testing.T) {
	now := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	idToken := makeTestJWT(t, map[string]any{
		"exp": now.Add(time.Hour).Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_lifecycle",
		},
	})
	endpoint := &recordingCallbackEndpoint{}
	scheduler := &manualScheduler{}
	service := newCallbackLifecycleTestService(Config{
		Clock: fixedClock{now: now},
		HTTPClient: stubHTTPDoer{do: func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == "/oauth/token" {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(strings.NewReader(`{
						"access_token":"access-token",
						"refresh_token":"refresh-token",
						"id_token":"` + idToken + `"
					}`)),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Body:       io.NopCloser(strings.NewReader("usage unavailable")),
			}, nil
		}},
	}, endpoint, scheduler)
	login := mustStartChatGPTLogin(t, service)
	state := loginStateFromAuthURL(t, login.AuthURL)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, LoopbackCallbackAddress()+"?state="+url.QueryEscape(state)+"&code=auth-code", nil)
	service.handleChatGPTOAuthCallback(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "GPT account connected") {
		t.Fatalf("callback response = (%d, %q), want successful login page", recorder.Code, recorder.Body.String())
	}
	waitForCallbackEndpointState(t, endpoint, false, 1, 1)

	status, err := service.GetChatGPTLoginStatus(login.LoginID)
	if err != nil {
		t.Fatalf("GetChatGPTLoginStatus returned error: %v", err)
	}
	if status.Status != ChatGPTLoginStatusCompleted {
		t.Fatalf("status = %q, want %q", status.Status, ChatGPTLoginStatusCompleted)
	}
}

func TestProviderAuthServiceShutdown_ReleasesEndpointAndRejectsNewLogin(t *testing.T) {
	endpoint := &recordingCallbackEndpoint{shutdownErr: errors.New("shutdown timeout")}
	scheduler := &manualScheduler{}
	service := newCallbackLifecycleTestService(Config{}, endpoint, scheduler)
	mustStartChatGPTLogin(t, service)
	expiry := scheduler.latest(t)

	err := service.Shutdown(context.Background())
	if !errors.Is(err, endpoint.shutdownErr) {
		t.Fatalf("Shutdown error = %v, want %v", err, endpoint.shutdownErr)
	}
	if !expiry.isStopped() {
		t.Fatal("Shutdown did not cancel the pending expiry task")
	}
	if active, starts, shutdowns := endpoint.snapshot(); active || starts != 1 || shutdowns != 1 {
		t.Fatalf("endpoint after Shutdown = (active=%t, starts=%d, shutdowns=%d), want released", active, starts, shutdowns)
	}
	if _, err := service.StartChatGPTLogin(); !errors.Is(err, errProviderAuthServiceShutdown) {
		t.Fatalf("StartChatGPTLogin after Shutdown error = %v, want %v", err, errProviderAuthServiceShutdown)
	}
	if err := service.Shutdown(context.Background()); err != nil {
		t.Fatalf("second Shutdown returned error: %v", err)
	}
}

func mustStartChatGPTLogin(t *testing.T, service *Service) *ChatGPTLoginStartResponse {
	t.Helper()
	login, err := service.StartChatGPTLogin()
	if err != nil {
		t.Fatalf("StartChatGPTLogin returned error: %v", err)
	}
	return login
}

func cancelLoginThroughCallback(t *testing.T, service *Service, authURL string) {
	t.Helper()
	state := loginStateFromAuthURL(t, authURL)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, LoopbackCallbackAddress()+"?state="+url.QueryEscape(state)+"&error=access_denied", nil)
	service.handleChatGPTOAuthCallback(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "access_denied") {
		t.Fatalf("cancellation response = (%d, %q), want oauth error page", recorder.Code, recorder.Body.String())
	}
}

func loginStateFromAuthURL(t *testing.T, authURL string) string {
	t.Helper()
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse auth URL: %v", err)
	}
	state := parsed.Query().Get("state")
	if state == "" {
		t.Fatal("auth URL has no state")
	}
	return state
}

func waitForCallbackEndpointState(t *testing.T, endpoint *recordingCallbackEndpoint, wantActive bool, wantStarts, wantShutdowns int) {
	t.Helper()
	deadline := time.Now().Add(callbackLifecycleObservationTimeout)
	for time.Now().Before(deadline) {
		active, starts, shutdowns := endpoint.snapshot()
		if active == wantActive && starts == wantStarts && shutdowns == wantShutdowns {
			return
		}
		time.Sleep(time.Millisecond)
	}
	active, starts, shutdowns := endpoint.snapshot()
	t.Fatalf(
		"endpoint = (active=%t, starts=%d, shutdowns=%d), want (active=%t, starts=%d, shutdowns=%d)",
		active,
		starts,
		shutdowns,
		wantActive,
		wantStarts,
		wantShutdowns,
	)
}
