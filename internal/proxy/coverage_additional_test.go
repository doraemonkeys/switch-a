package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"

	"go.uber.org/zap"
)

type failingSuspendHealthManager struct {
	err   error
	calls int
}

func (m *failingSuspendHealthManager) MarkSuccess(context.Context, string) {}

func (m *failingSuspendHealthManager) MarkFailure(context.Context, string, error) bool {
	return false
}

func (m *failingSuspendHealthManager) RecoverIfExpired(context.Context, string) bool {
	return false
}

func (m *failingSuspendHealthManager) IsAvailable(context.Context, string) bool {
	return true
}

func (m *failingSuspendHealthManager) SuspendUntil(context.Context, string, time.Time, string) error {
	m.calls++
	return m.err
}

func (m *failingSuspendHealthManager) ManualDisable(context.Context, string, string) error {
	return nil
}

func (m *failingSuspendHealthManager) ManualEnable(context.Context, string) error {
	return nil
}

func (m *failingSuspendHealthManager) ResetCircuitBreaker(string) {}

func TestActiveRequestRegistrySetStickyPerModelKeepsRequestDerivedKey(t *testing.T) {
	r := NewActiveRequestRegistry()
	req := &model.SelectRequest{
		ClientIP:   "192.168.1.20",
		User:       "user-1",
		APIType:    "codex",
		Model:      "gpt-5.4",
		StickyMode: model.StickyModeModel,
	}

	before := r.buildKeyFromRequest(req)
	after := r.buildKeyFromRequest(req)

	if after != before {
		t.Fatalf("continuity key changed after compatibility no-op: before=%#v after=%#v", before, after)
	}
	if after.Model != "gpt-5.4" {
		t.Fatalf("key.Model = %q, want %q", after.Model, "gpt-5.4")
	}
}

func TestActiveRequestRegistryBuildKeyAndRemovalHandleNilAndExplicitKeys(t *testing.T) {
	r := NewActiveRequestRegistry()

	if got := r.buildKey(nil); got != (model.StickyKey{}) {
		t.Fatalf("buildKey(nil) = %#v, want zero-value sticky key", got)
	}

	explicit := model.StickyKey{
		IP:      "192.168.1.20",
		User:    "user-1",
		APIType: "codex",
		Model:   "gpt-5.4",
	}
	if got := r.buildKey(&ActiveRequest{ContinuityKey: explicit}); got != explicit {
		t.Fatalf("buildKey(explicit) = %#v, want %#v", got, explicit)
	}

	derived := r.buildKey(&ActiveRequest{
		ClientIP:   "192.168.1.20",
		UserID:     "user-1",
		APIType:    "codex",
		Model:      "gpt-5.4",
		StickyMode: model.StickyModeModel,
	})
	if derived != explicit {
		t.Fatalf("buildKey(derived) = %#v, want %#v", derived, explicit)
	}

	r.stickyIndex[explicit] = map[string]struct{}{"req-1": {}}
	r.keyIndex["req-1"] = explicit

	r.removeFromStickyIndex("req-1")
	if _, ok := r.keyIndex["req-1"]; ok {
		t.Fatal("keyIndex still contains req-1 after removal")
	}
	if _, ok := r.stickyIndex[explicit]; ok {
		t.Fatal("stickyIndex bucket still exists after last request removal")
	}

	r.removeFromStickyIndex("missing")
}

func TestHandlerRegisterActiveRequestTracksSelectedProvider(t *testing.T) {
	startTime := time.Date(2026, time.March, 30, 12, 0, 0, 0, time.UTC)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	pctx := &proxyContext{
		r:         request,
		apiType:   "codex",
		info:      RequestInfo{Model: "gpt-5.4", UserID: "user-1", ClientIP: "192.168.1.20"},
		selectReq: &model.SelectRequest{ClientIP: "192.168.1.20", User: "user-1", APIType: "codex", Model: "gpt-5.4", StickyMode: model.StickyModeModel},
		startTime: startTime,
		requestID: "req-1",
	}
	provider := &model.Provider{ID: "provider-1"}

	t.Run("missing registry skips registration", func(t *testing.T) {
		handler := NewHandler(Config{
			Store:  newMockStore(),
			Logger: zap.NewNop(),
		})
		state := &retryState{
			currentProvider: provider,
			currentLease:    newLocalProviderLease(provider),
		}

		handler.registerActiveRequest(pctx, state)

		if state.activeRegistered {
			t.Fatal("activeRegistered = true, want false when registry is absent")
		}
	})

	t.Run("active registry stores the selected provider snapshot", func(t *testing.T) {
		registry := NewActiveRequestRegistry()
		handler := NewHandler(Config{
			Store:          newMockStore(),
			ActiveRegistry: registry,
			Logger:         zap.NewNop(),
		})
		state := &retryState{
			currentProvider: provider,
			currentLease:    newLocalProviderLease(provider),
		}

		handler.registerActiveRequest(pctx, state)

		if !state.activeRegistered {
			t.Fatal("activeRegistered = false, want true")
		}

		entry, ok := registry.requests[pctx.requestID]
		if !ok {
			t.Fatalf("registry missing request %q", pctx.requestID)
		}
		if entry.request.ProviderID != provider.ID {
			t.Fatalf("ProviderID = %q, want %q", entry.request.ProviderID, provider.ID)
		}
		if entry.request.Model != pctx.info.Model || entry.request.APIType != pctx.apiType {
			t.Fatalf("registered request = %#v, want model/api copied from proxy context", entry.request)
		}
		if entry.request.ContinuityKey == (model.StickyKey{}) {
			t.Fatal("ContinuityKey = zero-value, want derived sticky routing key")
		}
		if !entry.request.StartedAt.Equal(startTime) {
			t.Fatalf("StartedAt = %v, want %v", entry.request.StartedAt, startTime)
		}
	})
}

func TestHandlerHandleExhaustedRetriesWritesExpectedGatewayResponses(t *testing.T) {
	handler := NewHandler(Config{
		Store:  newMockStore(),
		Logger: zap.NewNop(),
	})

	testCases := []struct {
		name        string
		lastErr     error
		wantCode    string
		wantMessage string
	}{
		{
			name:        "provider errors exhausted",
			lastErr:     errors.New("provider failure"),
			wantCode:    ErrCodeProviderExhausted,
			wantMessage: "All providers failed",
		},
		{
			name:        "no provider remained eligible",
			lastErr:     nil,
			wantCode:    ErrCodeProviderUnavailable,
			wantMessage: "No available provider for api_type: codex",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()

			handler.handleExhaustedRetries(&proxyContext{
				w:       recorder,
				apiType: "codex",
			}, tc.lastErr)

			if recorder.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
			}

			var payload model.GatewayError
			if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
				t.Fatalf("Unmarshal() error = %v, want nil", err)
			}
			if payload.Error.Code != tc.wantCode {
				t.Fatalf("error code = %q, want %q", payload.Error.Code, tc.wantCode)
			}
			if payload.Error.Message != tc.wantMessage {
				t.Fatalf("error message = %q, want %q", payload.Error.Message, tc.wantMessage)
			}
		})
	}
}

func TestHandlerSuspendProviderUntilEvictsContinuityOnlyAfterSuccessfulSuspension(t *testing.T) {
	until := time.Date(2026, time.March, 30, 16, 0, 0, 0, time.UTC)

	t.Run("successful suspension evicts continuity", func(t *testing.T) {
		health := newMockHealthManager()
		selector := &mockSelector{}
		handler := NewHandler(Config{
			Store:    newMockStore(),
			Health:   health,
			Selector: selector,
			Logger:   zap.NewNop(),
		})

		handler.suspendProviderUntil(context.Background(), "provider-1", until, "quota")

		if got := health.suspendedUntil["provider-1"]; !got.Equal(until) {
			t.Fatalf("suspendedUntil = %v, want %v", got, until)
		}
		if got := health.suspendReasons["provider-1"]; got != "quota" {
			t.Fatalf("suspend reason = %q, want %q", got, "quota")
		}
		evictions := selector.ContinuityEvictions()
		if len(evictions) != 1 || evictions[0] != "provider-1" {
			t.Fatalf("continuity evictions = %#v, want [provider-1]", evictions)
		}
	})

	t.Run("failed suspension skips selector eviction", func(t *testing.T) {
		health := &failingSuspendHealthManager{err: errors.New("health store unavailable")}
		selector := &mockSelector{}
		handler := NewHandler(Config{
			Store:    newMockStore(),
			Health:   health,
			Selector: selector,
			Logger:   zap.NewNop(),
		})

		handler.suspendProviderUntil(context.Background(), "provider-1", until, "quota")

		if health.calls != 1 {
			t.Fatalf("SuspendUntil calls = %d, want 1", health.calls)
		}
		if evictions := selector.ContinuityEvictions(); len(evictions) != 0 {
			t.Fatalf("continuity evictions = %#v, want none after failed suspension", evictions)
		}
	})

	t.Run("missing health manager becomes a no-op", func(t *testing.T) {
		selector := &mockSelector{}
		handler := NewHandler(Config{
			Store:    newMockStore(),
			Selector: selector,
			Logger:   zap.NewNop(),
		})

		handler.suspendProviderUntil(context.Background(), "provider-1", until, "quota")

		if evictions := selector.ContinuityEvictions(); len(evictions) != 0 {
			t.Fatalf("continuity evictions = %#v, want none when health manager is absent", evictions)
		}
	})
}
