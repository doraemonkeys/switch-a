package main

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/store"
	"go.uber.org/zap"
)

func TestComposedClientAccessSharesAdminAndProxyPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "application-access.db")
	clock, log := internal.RealClock{}, zap.NewNop()
	sqlStore, err := openApplicationStore(path, clock, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlStore.Close() })
	security := testApplicationCodexSecurity(t, 41)
	if err := sqlStore.FinalizeStaticCredentialSubjects(context.Background(), security.keyring); err != nil {
		t.Fatal(err)
	}
	analytics, err := newApplicationAnalytics(path, clock, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = analytics.Close() })
	cfg := startupCompositionTestConfig()
	cfg.AdminToken = "admin-access"
	runtime, err := composeApplicationRuntime(cfg, clock, log, sqlStore, store.NewCachedStore(store.CachedStoreConfig{Store: sqlStore}), security, analytics)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.captures.Close() })
	adminURL := startComposedAdminServer(t, runtime.adminServer)
	proxyURL := startComposedAdminServer(t, runtime.proxyServer)
	checkProxy := func(token string, status int) {
		t.Helper()
		response := performComposedAdminRequest(t, http.MethodPost, proxyURL+"/v1/messages", token, map[string]any{"model": "test"})
		if response.status != status {
			t.Fatalf("proxy status %d: %s", response.status, response.body)
		}
	}
	// A fresh installation still lets arbitrary keys reach provider selection.
	checkProxy("arbitrary", http.StatusServiceUnavailable)
	response := performComposedAdminRequest(t, http.MethodPost, adminURL+"/admin/api/client-api-keys", cfg.AdminToken, map[string]string{"name": "Client", "key": "managed-client"})
	if response.status != http.StatusCreated {
		t.Fatalf("create %d: %s", response.status, response.body)
	}
	response = performComposedAdminRequest(t, http.MethodPut, adminURL+"/admin/api/client-api-keys/policy", cfg.AdminToken, map[string]string{"mode": "restricted"})
	if response.status != http.StatusOK {
		t.Fatalf("policy %d: %s", response.status, response.body)
	}
	checkProxy("arbitrary", http.StatusUnauthorized)
	checkProxy(cfg.AdminToken, http.StatusUnauthorized)
	checkProxy("managed-client", http.StatusServiceUnavailable)
}
