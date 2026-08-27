package debugcapture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestStartSessionResolvesSafeProviderIdentities(t *testing.T) {
	catalog := &stubProviderCatalog{providers: []model.Provider{
		{ID: "provider-a", Name: "Provider A"},
		{ID: "provider-b", Name: "Provider B"},
	}}
	var captured requestcapture.StartRequest
	service := &stubCaptureService{startFn: func(input requestcapture.StartRequest) (requestcapture.SessionInfo, error) {
		captured = input
		return requestcapture.SessionInfo{
			SessionID:                   "session-1",
			ProviderIDs:                 []string{"provider-a", "provider-b"},
			CompletedRecordsPerProvider: input.CompletedRecordsPerProvider,
			RetainedBytesLimit:          input.RetainedBytesLimit,
		}, nil
	}}
	handler := NewHandler(Config{Providers: catalog, Sessions: service})
	body := `{"provider_ids":["provider-a","provider-b"],"completed_records_per_provider":7,"retained_bytes_limit":4096,"acknowledge_raw_payload_risk":true}`
	req := httptest.NewRequest(http.MethodPost, "/admin/api/debug-capture/sessions", strings.NewReader(body))
	recorder := httptest.NewRecorder()

	SensitiveResponses(http.HandlerFunc(handler.StartSession)).ServeHTTP(recorder, req)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusCreated, recorder.Body.String())
	}
	if got := recorder.Header().Get("Location"); got != "/admin/api/debug-capture/sessions/session-1" {
		t.Fatalf("Location = %q", got)
	}
	assertSensitiveHeaders(t, recorder)
	if len(captured.Providers) != 2 || captured.Providers[0] != (requestcapture.ProviderIdentity{ID: "provider-a", Name: "Provider A"}) {
		t.Fatalf("resolved providers = %#v", captured.Providers)
	}
	if captured.CompletedRecordsPerProvider != 7 || captured.RetainedBytesLimit != 4096 || !captured.AcknowledgeRawPayloadRisk {
		t.Fatalf("start request = %#v", captured)
	}
}

func TestStartSessionRejectsInvalidInputBeforeStarting(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		providers []model.Provider
	}{
		{name: "risk not acknowledged", body: `{"provider_ids":["provider-a"],"acknowledge_raw_payload_risk":false}`, providers: []model.Provider{{ID: "provider-a"}}},
		{name: "duplicate provider", body: `{"provider_ids":["provider-a","provider-a"],"acknowledge_raw_payload_risk":true}`, providers: []model.Provider{{ID: "provider-a"}}},
		{name: "noncanonical provider", body: `{"provider_ids":[" provider-a "],"acknowledge_raw_payload_risk":true}`, providers: []model.Provider{{ID: "provider-a"}}},
		{name: "unknown provider", body: `{"provider_ids":["missing"],"acknowledge_raw_payload_risk":true}`, providers: []model.Provider{{ID: "provider-a"}}},
		{name: "unknown JSON field", body: `{"provider_ids":["provider-a"],"acknowledge_raw_payload_risk":true,"replace":true}`, providers: []model.Provider{{ID: "provider-a"}}},
		{name: "trailing JSON", body: `{"provider_ids":["provider-a"],"acknowledge_raw_payload_risk":true}{}`, providers: []model.Provider{{ID: "provider-a"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			started := false
			service := &stubCaptureService{startFn: func(requestcapture.StartRequest) (requestcapture.SessionInfo, error) {
				started = true
				return requestcapture.SessionInfo{}, nil
			}}
			handler := NewHandler(Config{Providers: &stubProviderCatalog{providers: test.providers}, Sessions: service})
			recorder := httptest.NewRecorder()
			handler.StartSession(recorder, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body)))
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
			}
			if started {
				t.Fatal("capture session started for invalid input")
			}
		})
	}
}

func TestStartSessionMapsCatalogAndDomainFailures(t *testing.T) {
	t.Run("catalog", func(t *testing.T) {
		const sensitiveErrorText = "database unavailable: password=must-not-be-logged"
		observedCore, observedLogs := observer.New(zap.ErrorLevel)
		handler := NewHandler(Config{
			Providers: &stubProviderCatalog{err: errors.New(sensitiveErrorText)},
			Sessions:  &stubCaptureService{},
			Logger:    zap.New(observedCore),
		})
		recorder := httptest.NewRecorder()
		handler.StartSession(recorder, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"provider_ids":["provider-a"],"acknowledge_raw_payload_risk":true}`)))
		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
		}
		if strings.Contains(recorder.Body.String(), sensitiveErrorText) || strings.Contains(fmt.Sprint(observedLogs.All()), sensitiveErrorText) {
			t.Fatal("provider catalog error text crossed the admin observability boundary")
		}
	})

	t.Run("canceled provider lookup", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		observedCore, observedLogs := observer.New(zap.ErrorLevel)
		handler := NewHandler(Config{
			Providers: &stubProviderCatalog{err: context.Canceled},
			Sessions:  &stubCaptureService{},
			Logger:    zap.New(observedCore),
		})
		w := &trackingResponseWriter{}
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"provider_ids":["provider-a"],"acknowledge_raw_payload_risk":true}`)).WithContext(ctx)
		handler.StartSession(w, req)
		if w.wroteResponse() || observedLogs.Len() != 0 {
			t.Fatalf("canceled provider lookup produced response/logs: status=%d body=%q logs=%#v", w.status, w.body.String(), observedLogs.All())
		}
	})

	t.Run("active session", func(t *testing.T) {
		handler := NewHandler(Config{
			Providers: &stubProviderCatalog{providers: []model.Provider{{ID: "provider-a"}}},
			Sessions: &stubCaptureService{startFn: func(requestcapture.StartRequest) (requestcapture.SessionInfo, error) {
				return requestcapture.SessionInfo{}, requestcapture.ErrSessionActive
			}},
		})
		recorder := httptest.NewRecorder()
		handler.StartSession(recorder, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"provider_ids":["provider-a"],"acknowledge_raw_payload_risk":true}`)))
		assertErrorResponse(t, recorder, http.StatusConflict, errorCodeSessionActive)
	})
}

func TestStatusAndConditionalStop(t *testing.T) {
	manager, session := newAdminQueryManager(t)
	handler := NewHandler(Config{Sessions: manager})
	recorder := httptest.NewRecorder()
	handler.Status(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	var status struct {
		Session *struct {
			SessionID string                            `json:"session_id"`
			Providers []requestcapture.ProviderIdentity `json:"providers"`
		} `json:"session"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.Session == nil || status.Session.SessionID != session.SessionID ||
		len(status.Session.Providers) != 1 || status.Session.Providers[0].ID != "provider-1" {
		t.Fatalf("status payload = %#v", status)
	}

	service := &stubCaptureService{}
	service.stopFn = func(sessionID string) error {
		if sessionID != testOtherSessionID {
			t.Fatalf("sessionID = %q", sessionID)
		}
		return requestcapture.ErrSessionMismatch
	}
	stopReq := httptest.NewRequest(http.MethodDelete, "/", nil)
	stopReq.SetPathValue("session_id", testOtherSessionID)
	stopRecorder := httptest.NewRecorder()
	handler.StopSession(stopRecorder, stopReq)
	assertErrorResponse(t, stopRecorder, http.StatusNotFound, errorCodeSessionNotFound)
}
