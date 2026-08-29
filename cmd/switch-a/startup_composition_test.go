package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/admin"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/config"
	"github.com/doraemonkeys/switch-a/internal/defaults"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/store"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

const (
	adminServerReadyTimeout = 5 * time.Second
	adminServerPollInterval = 10 * time.Millisecond
	adminRequestTimeout     = time.Second
)

func TestComposeApplicationRuntimeBuildsAndOwnsOneProcessGraph(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "application.db")
	clock := internal.RealClock{}
	log := zap.NewNop()
	sqlStore, err := openApplicationStore(databasePath, clock, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlStore.Close() })

	security := testApplicationCodexSecurity(t, 31)
	if err := sqlStore.FinalizeStaticCredentialSubjects(context.Background(), security.keyring); err != nil {
		t.Fatal(err)
	}
	analytics, err := newApplicationAnalytics(databasePath, clock, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = analytics.Close() })

	cachedStore := store.NewCachedStore(store.CachedStoreConfig{Store: sqlStore})
	cfg := startupCompositionTestConfig()

	// A failed security preflight must not bind the process-wide error runtime;
	// the same storage graph remains valid for the corrected composition.
	failedRuntime, err := composeApplicationRuntime(cfg, clock, log, sqlStore, cachedStore, nil, analytics)
	if failedRuntime != nil || err == nil || !strings.Contains(err.Error(), "initialize Codex runtime") {
		t.Fatalf("failed composition runtime = %#v, error = %v", failedRuntime, err)
	}

	runtime, err := composeApplicationRuntime(cfg, clock, log, sqlStore, cachedStore, security, analytics)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.captures.Close() })

	if runtime.store != cachedStore ||
		runtime.errorRules.ruleRepository != cachedStore.InternalErrorRuleRepository() ||
		runtime.codex == nil || runtime.codex.HTTP == nil || runtime.codex.WebSocket == nil ||
		runtime.codex.maintenance == nil || runtime.health == nil || runtime.sticky == nil ||
		runtime.activeRequests == nil || runtime.continuitySeeds == nil || runtime.auth == nil ||
		runtime.proxyServer == nil || runtime.adminServer == nil {
		t.Fatalf("composed runtime did not preserve the singleton dependency graph: %#v", runtime)
	}
	if got := runtime.captures.Status().ProcessMemory.CeilingBytes; got != cfg.DebugCaptureMemoryCeilingBytes {
		t.Fatalf("capture process ceiling = %d, want %d", got, cfg.DebugCaptureMemoryCeilingBytes)
	}

	var events []applicationLifecycleEvent
	activation, err := runtime.activateBackgrounds(
		"startup-composition",
		applicationLifecycleRecorderFunc(func(event applicationLifecycleEvent) {
			events = append(events, event)
		}),
		log,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = activation.Shutdown() })
	if err := activation.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if err := activation.Shutdown(); err != nil {
		t.Fatalf("idempotent background shutdown failed: %v", err)
	}

	started := []string{
		"codex_maintenance",
		"health_cleanup",
		"sticky_cache",
		"log_cleanup",
		"active_registry",
		"visible_continuity_seed",
		"rule_statistics",
	}
	wantEvents := make([]applicationLifecycleEvent, 0, len(started)*2)
	for _, component := range started {
		wantEvents = append(wantEvents, applicationLifecycleEvent{
			StartupID: "startup-composition",
			Phase:     startupPhaseBackgroundOwners,
			Component: component,
		})
	}
	for index := len(started) - 1; index >= 0; index-- {
		wantEvents = append(wantEvents, applicationLifecycleEvent{
			StartupID: "startup-composition",
			Phase:     startupPhaseShutdownBackgrounds,
			Component: started[index],
		})
	}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("background lifecycle events = %+v, want %+v", events, wantEvents)
	}
}

func TestComposeApplicationRuntimePreservesAdminCredentialCapabilities(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "application-admin.db")
	clock := internal.RealClock{}
	log := zap.NewNop()
	sqlStore, err := openApplicationStore(databasePath, clock, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlStore.Close() })

	security := testApplicationCodexSecurity(t, 37)
	if err := sqlStore.FinalizeStaticCredentialSubjects(context.Background(), security.keyring); err != nil {
		t.Fatal(err)
	}
	analytics, err := newApplicationAnalytics(databasePath, clock, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = analytics.Close() })

	cachedStore := store.NewCachedStore(store.CachedStoreConfig{Store: sqlStore})
	cfg := startupCompositionTestConfig()
	cfg.AdminToken = "composition-admin-token"
	runtime, err := composeApplicationRuntime(cfg, clock, log, sqlStore, cachedStore, security, analytics)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.captures.Close() })
	baseURL := startComposedAdminServer(t, runtime.adminServer)
	const configKey = defaults.ConfigKeyWebSocketProbeClientModel
	if err := sqlStore.SetConfig(context.Background(), configKey, "true"); err != nil {
		t.Fatal(err)
	}
	if value, err := cachedStore.GetConfig(context.Background(), configKey); err != nil || value != "true" {
		t.Fatalf("primed cached config = (%q, %v)", value, err)
	}
	response := performComposedAdminRequest(t, http.MethodPut, baseURL+"/admin/api/config", cfg.AdminToken, map[string]string{
		configKey: "false",
	})
	if response.status != http.StatusOK {
		t.Fatalf("config update = %d %s", response.status, response.body)
	}
	if value, err := cachedStore.GetConfig(context.Background(), configKey); err != nil || value != "false" {
		t.Fatalf("cached config after admin update = (%q, %v)", value, err)
	}

	response = performComposedAdminRequest(t, http.MethodGet, baseURL+"/admin/api/credential-sessions", cfg.AdminToken, nil)
	if response.status != http.StatusOK {
		t.Fatalf("initial credential session list = %d %s", response.status, response.body)
	}

	const sessionID = "composition-session"
	response = performComposedAdminRequest(t, http.MethodPost, baseURL+"/admin/api/providers", cfg.AdminToken, admin.CreateProviderRequest{
		ID: "composition-provider", Name: "Composition Provider", AuthMode: "bearer", Vendor: "openai",
		APITypes: []admin.APITypeInput{{
			APIType: "claude", BaseURL: "https://api.example.com", CredentialSessionID: sessionID,
		}},
		NewCredentialSessions: []admin.NewProviderCredentialSessionInput{{
			ID: sessionID, Name: "Composition Key", Kind: credentialsession.KindAPIKey, SecretData: "composition-secret",
		}},
	})
	if response.status != http.StatusCreated {
		t.Fatalf("provider materialization = %d %s", response.status, response.body)
	}

	response = performComposedAdminRequest(t, http.MethodGet, baseURL+"/admin/api/credential-sessions", cfg.AdminToken, nil)
	if response.status != http.StatusOK {
		t.Fatalf("credential session list = %d %s", response.status, response.body)
	}
	var sessions []admin.CredentialSessionPayload
	if err := json.Unmarshal(response.body, &sessions); err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != sessionID || len(sessions[0].RouteReferences) != 1 {
		t.Fatalf("credential sessions = %#v", sessions)
	}

	response = performComposedAdminRequest(t, http.MethodPatch, baseURL+"/admin/api/credential-sessions/"+sessionID+"/name", cfg.AdminToken, admin.RenameCredentialSessionRequest{
		ExpectedVersion: sessions[0].Version,
		Name:            "Renamed Composition Key",
	})
	if response.status != http.StatusOK {
		t.Fatalf("credential session rename = %d %s", response.status, response.body)
	}
	var renamed admin.CredentialSessionPayload
	if err := json.Unmarshal(response.body, &renamed); err != nil {
		t.Fatal(err)
	}
	if renamed.Name != "Renamed Composition Key" || len(renamed.RouteReferences) != 1 {
		t.Fatalf("renamed credential session = %#v", renamed)
	}
}

type composedAdminServer interface {
	Start() error
	Shutdown(context.Context) error
	Addr() string
}

type composedAdminResponse struct {
	status int
	body   []byte
}

func startComposedAdminServer(t *testing.T, server composedAdminServer) string {
	t.Helper()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Start() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), adminServerReadyTimeout)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Errorf("shutdown composed admin server: %v", err)
		}
		select {
		case err := <-errCh:
			if err != nil {
				t.Errorf("composed admin server stopped: %v", err)
			}
		case <-time.After(adminServerReadyTimeout):
			t.Error("composed admin server did not stop")
		}
	})

	client := &http.Client{Timeout: adminRequestTimeout}
	deadline := time.Now().Add(adminServerReadyTimeout)
	for time.Now().Before(deadline) {
		_, port, err := net.SplitHostPort(server.Addr())
		if err == nil && port != "0" {
			baseURL := "http://127.0.0.1:" + port
			response, requestErr := client.Get(baseURL + "/health")
			if requestErr == nil {
				_ = response.Body.Close()
				if response.StatusCode == http.StatusOK {
					return baseURL
				}
			}
		}
		time.Sleep(adminServerPollInterval)
	}
	t.Fatal("composed admin server did not become ready")
	return ""
}

func performComposedAdminRequest(t *testing.T, method, target, token string, payload any) composedAdminResponse {
	t.Helper()
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(context.Background(), method, target, body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := (&http.Client{Timeout: adminRequestTimeout}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return composedAdminResponse{status: response.StatusCode, body: responseBody}
}

func TestNewApplicationAnalyticsRejectsUnopenableDatabase(t *testing.T) {
	notDirectory := filepath.Join(t.TempDir(), "occupied")
	if err := os.WriteFile(notDirectory, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	runtime, err := newApplicationAnalytics(
		filepath.Join(notDirectory, "analytics.db"),
		internal.RealClock{},
		zap.NewNop(),
	)
	if runtime != nil || err == nil || !strings.Contains(err.Error(), "failed to initialize token analytics repository") {
		t.Fatalf("analytics runtime = %#v, error = %v", runtime, err)
	}
}

func TestLogApplicationStartupEmitsConfigurationAndBuildContext(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	cfg := &config.Config{
		ConfigFileUsed: "C:/switch-a/config.yaml",
		Port:           "8181",
		AdminPort:      "9191",
		LogPath:        "C:/switch-a/logs",
		LogLevel:       "debug",
	}

	logApplicationStartup(zap.New(core), cfg)

	loaded := observed.FilterMessage("loaded config file").All()
	if len(loaded) != 1 || loaded[0].ContextMap()["path"] != cfg.ConfigFileUsed {
		t.Fatalf("config load trace = %+v", loaded)
	}
	started := observed.FilterMessage("starting switch-a").All()
	if len(started) != 1 {
		t.Fatalf("startup trace = %+v", started)
	}
	fields := started[0].ContextMap()
	for key, want := range map[string]string{
		"proxy_port": cfg.Port,
		"admin_port": cfg.AdminPort,
		"log_path":   cfg.LogPath,
		"log_level":  cfg.LogLevel,
	} {
		if fields[key] != want {
			t.Fatalf("startup trace field %s = %v, want %q", key, fields[key], want)
		}
	}
	for _, required := range []string{"version", "commit", "built_at"} {
		if _, exists := fields[required]; !exists {
			t.Fatalf("startup trace is missing build field %q: %+v", required, fields)
		}
	}
}

func startupCompositionTestConfig() *config.Config {
	return &config.Config{
		Port:                               "0",
		AdminPort:                          "0",
		DebugCaptureMemoryCeilingBytes:     600 << 20,
		DebugCaptureMaxActiveRecords:       requestcapture.DefaultMaxActiveRecords,
		DebugCaptureMaxActiveTraces:        requestcapture.DefaultMaxActiveTraces,
		DebugCaptureMaxTransitionsPerTrace: requestcapture.DefaultMaxTransitionsPerTrace,
		DebugCaptureMaxPendingExports:      requestcapture.DefaultMaxPendingExports,
		DebugCaptureMaxConcurrentDownloads: requestcapture.DefaultMaxActiveDownloads,
		DebugCaptureDetailPreviewBytes:     requestcapture.DefaultPreviewBytes,
		DebugCaptureDetailEventLimit:       requestcapture.DefaultDetailEventLimit,
		DebugCaptureDownloadTokenTTL:       requestcapture.DefaultDownloadTokenTTL,
		DebugCaptureMaxRecordsPerProvider:  requestcapture.DefaultMaxRecordsPerProvider,
		DebugCaptureChunkBytes:             requestcapture.DefaultChunkBytes,
		DebugCaptureExportLineBytes:        requestcapture.DefaultExportLineBytes,
	}
}

func TestApplicationActivationRejectsIncompleteOwnerAfterSynchronousRollback(t *testing.T) {
	rollbackErr := errors.New("first owner failed to stop")
	var calls []string
	var events []applicationLifecycleEvent
	recorder := applicationLifecycleRecorderFunc(func(event applicationLifecycleEvent) {
		events = append(events, event)
	})

	activation, err := activateApplicationComponents(
		"startup-invalid-owner",
		recorder,
		[]applicationActivationStep{
			{
				component: "first",
				start:     func() error { calls = append(calls, "start:first"); return nil },
				stop:      func() error { calls = append(calls, "stop:first"); return rollbackErr },
			},
			{
				component: "incomplete",
				start:     func() error { calls = append(calls, "start:incomplete"); return nil },
			},
		},
	)
	if activation != nil || err == nil ||
		!strings.Contains(err.Error(), "component, start, and stop are required") ||
		!errors.Is(err, rollbackErr) {
		t.Fatalf("activation = %#v, error = %v", activation, err)
	}
	if want := []string{"start:first", "stop:first"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("activation calls = %v, want %v", calls, want)
	}
	wantEvents := []applicationLifecycleEvent{
		{StartupID: "startup-invalid-owner", Phase: startupPhaseBackgroundOwners, Component: "first"},
		{StartupID: "startup-invalid-owner", Phase: startupPhaseShutdownBackgrounds, Component: "first"},
	}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("rollback events = %+v, want %+v", events, wantEvents)
	}
}
