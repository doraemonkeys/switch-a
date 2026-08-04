package admin

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"

	"go.uber.org/zap"
)

func TestEnableProvider(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["test-provider"] = &model.Provider{ID: "test-provider", Name: "Test", Enabled: false}

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/test-provider/enable", nil)
	setPathValue(req, "id", "test-provider")
	w := httptest.NewRecorder()

	h.EnableProvider(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	if !st.providers["test-provider"].Enabled {
		t.Error("provider should be enabled")
	}
}

func TestEnableProvider_NotFound(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/non-existent/enable", nil)
	setPathValue(req, "id", "non-existent")
	w := httptest.NewRecorder()

	h.EnableProvider(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestEnableProvider_EmptyID(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers//enable", nil)
	w := httptest.NewRecorder()

	h.EnableProvider(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestEnableProvider_UpdateError(t *testing.T) {
	h, st, _ := testHandler()
	lifecycles := h.providerLifecycles.(*mockProviderLifecycleCoordinator)

	st.providers["test"] = &model.Provider{ID: "test", Name: "Test", Enabled: false}
	st.updateErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/test/enable", nil)
	setPathValue(req, "id", "test")
	w := httptest.NewRecorder()

	h.EnableProvider(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if len(lifecycles.retiredProviderIDs) != 1 || lifecycles.retiredProviderIDs[0] != "test" {
		t.Fatalf("retired = %v, want conservative retirement before failed write", lifecycles.retiredProviderIDs)
	}
}

func TestEnableProvider_GetError(t *testing.T) {
	h, st, _ := testHandler()

	st.getErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/test/enable", nil)
	setPathValue(req, "id", "test")
	w := httptest.NewRecorder()

	h.EnableProvider(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestDisableProvider(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["test-provider"] = &model.Provider{ID: "test-provider", Name: "Test", Enabled: true}

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/test-provider/disable", nil)
	setPathValue(req, "id", "test-provider")
	w := httptest.NewRecorder()

	h.DisableProvider(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	if st.providers["test-provider"].Enabled {
		t.Error("provider should be disabled")
	}
}

func TestProviderStateTransitionRetiresConcurrencyGeneration(t *testing.T) {
	for _, test := range []struct {
		name    string
		initial bool
		enabled bool
	}{
		{name: "enable", initial: false, enabled: true},
		{name: "disable", initial: true, enabled: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			h, st, _ := testHandler()
			lifecycles := h.providerLifecycles.(*mockProviderLifecycleCoordinator)
			st.providers["test-provider"] = &model.Provider{ID: "test-provider", Name: "Test", Enabled: test.initial}

			req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/test-provider/state", nil)
			setPathValue(req, "id", "test-provider")
			response := httptest.NewRecorder()
			if test.enabled {
				h.EnableProvider(response, req)
			} else {
				h.DisableProvider(response, req)
			}

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
			}
			if len(lifecycles.retiredProviderIDs) != 1 || lifecycles.retiredProviderIDs[0] != "test-provider" {
				t.Fatalf("retired = %v, want [test-provider]", lifecycles.retiredProviderIDs)
			}
		})
	}
}

func TestDisableProvider_NotFound(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/non-existent/disable", nil)
	setPathValue(req, "id", "non-existent")
	w := httptest.NewRecorder()

	h.DisableProvider(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestDisableProvider_EmptyID(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers//disable", nil)
	w := httptest.NewRecorder()

	h.DisableProvider(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestDisableProvider_GetError(t *testing.T) {
	h, st, _ := testHandler()

	st.getErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/test/disable", nil)
	setPathValue(req, "id", "test")
	w := httptest.NewRecorder()

	h.DisableProvider(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestDisableProvider_UpdateError(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["test"] = &model.Provider{ID: "test", Name: "Test", Enabled: true}
	st.updateErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/test/disable", nil)
	setPathValue(req, "id", "test")
	w := httptest.NewRecorder()

	h.DisableProvider(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestResetProvider(t *testing.T) {
	h, st, _ := testHandler()

	st.providers["test-provider"] = &model.Provider{ID: "test-provider", Name: "Test"}

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/test-provider/reset", nil)
	setPathValue(req, "id", "test-provider")
	w := httptest.NewRecorder()

	h.ResetProvider(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestResetProvider_NotFound(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/non-existent/reset", nil)
	setPathValue(req, "id", "non-existent")
	w := httptest.NewRecorder()

	h.ResetProvider(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestResetProvider_EmptyID(t *testing.T) {
	h, _, _ := testHandler()

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers//reset", nil)
	w := httptest.NewRecorder()

	h.ResetProvider(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestResetProvider_HealthEnableError(t *testing.T) {
	h, st, health := testHandler()

	st.providers["test"] = &model.Provider{ID: "test", Name: "Test"}
	health.enableErr = errors.New("health manager error")

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/test/reset", nil)
	setPathValue(req, "id", "test")
	w := httptest.NewRecorder()

	h.ResetProvider(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestResetProvider_GetError(t *testing.T) {
	h, st, _ := testHandler()

	st.getErr = errors.New("database error")

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/test/reset", nil)
	setPathValue(req, "id", "test")
	w := httptest.NewRecorder()

	h.ResetProvider(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestResetProvider_NoHealthManager(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	st := newMockStore()

	// Create handler without health manager
	h := NewHandler(Config{
		Store:       st,
		Health:      nil,
		Concurrency: &mockConcurrencyTracker{},
		Logger:      logger,
	})

	st.providers["test-provider"] = &model.Provider{ID: "test-provider", Name: "Test"}

	req := httptest.NewRequest(http.MethodPost, "/admin/api/providers/test-provider/reset", nil)
	setPathValue(req, "id", "test-provider")
	w := httptest.NewRecorder()

	h.ResetProvider(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}
