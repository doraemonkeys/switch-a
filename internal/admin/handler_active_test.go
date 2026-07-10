package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"switch-a/internal/model"
	"switch-a/internal/proxy"

	"go.uber.org/zap"
)

// mockActiveRequestLister implements ActiveRequestLister for testing.
type mockActiveRequestLister struct {
	requests []proxy.ActiveRequest
}

func (m *mockActiveRequestLister) List() []proxy.ActiveRequest {
	return m.requests
}

func TestGetActiveRequests_NoRegistry(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	st := newMockStore()

	// Create handler without active request lister
	h := NewHandler(Config{
		Store:       st,
		Concurrency: &mockConcurrencyTracker{},
		Logger:      logger,
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/api/requests/active", nil)
	w := httptest.NewRecorder()

	h.GetActiveRequests(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp ActiveRequestsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Count != 0 {
		t.Errorf("count = %d, want 0", resp.Count)
	}
	if len(resp.Requests) != 0 {
		t.Errorf("len(requests) = %d, want 0", len(resp.Requests))
	}
}

func TestGetActiveRequests_EmptyList(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	st := newMockStore()
	mockLister := &mockActiveRequestLister{
		requests: []proxy.ActiveRequest{},
	}

	h := NewHandler(Config{
		Store:         st,
		Concurrency:   &mockConcurrencyTracker{},
		ActiveReqList: mockLister,
		Logger:        logger,
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/api/requests/active", nil)
	w := httptest.NewRecorder()

	h.GetActiveRequests(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp ActiveRequestsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Count != 0 {
		t.Errorf("count = %d, want 0", resp.Count)
	}
	if len(resp.Requests) != 0 {
		t.Errorf("len(requests) = %d, want 0", len(resp.Requests))
	}
}

func TestGetActiveRequests_WithRequests(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	st := newMockStore()
	startTime := time.Now()
	reasoningState := model.ReasoningObservationCaptured
	reasoningEffort := "high"
	mockLister := &mockActiveRequestLister{
		requests: []proxy.ActiveRequest{
			{
				RequestID:  "req-1",
				ProviderID: "provider-1",
				Model:      "claude-3-opus",
				APIType:    "claude",
				UserID:     "user-1",
				ClientIP:   "192.168.1.1",
				IsSSE:      true,
				StartedAt:  startTime,
				RequestedReasoningObservation: model.RequestedReasoningObservation{
					State:  &reasoningState,
					Effort: &reasoningEffort,
				},
				BytesSent:     1024,
				BytesReceived: 8192,
			},
			{
				RequestID:  "req-2",
				ProviderID: "provider-2",
				Model:      "gpt-4",
				APIType:    "openai",
				UserID:     "user-2",
				ClientIP:   "192.168.1.2",
				IsSSE:      false,
				StartedAt:  startTime.Add(-time.Second),
			},
		},
	}

	h := NewHandler(Config{
		Store:         st,
		Concurrency:   &mockConcurrencyTracker{},
		ActiveReqList: mockLister,
		Logger:        logger,
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/api/requests/active", nil)
	w := httptest.NewRecorder()

	h.GetActiveRequests(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	// Verify Content-Type header
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var resp ActiveRequestsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Count != 2 {
		t.Errorf("count = %d, want 2", resp.Count)
	}
	if len(resp.Requests) != 2 {
		t.Errorf("len(requests) = %d, want 2", len(resp.Requests))
	}

	// Verify first request fields
	found := false
	for _, r := range resp.Requests {
		if r.RequestID == "req-1" {
			found = true
			if r.ProviderID != "provider-1" {
				t.Errorf("ProviderID = %q, want %q", r.ProviderID, "provider-1")
			}
			if r.Model != "claude-3-opus" {
				t.Errorf("Model = %q, want %q", r.Model, "claude-3-opus")
			}
			if r.APIType != "claude" {
				t.Errorf("APIType = %q, want %q", r.APIType, "claude")
			}
			if r.UserID != "user-1" {
				t.Errorf("UserID = %q, want %q", r.UserID, "user-1")
			}
			if r.ClientIP != "192.168.1.1" {
				t.Errorf("ClientIP = %q, want %q", r.ClientIP, "192.168.1.1")
			}
			if !r.IsSSE {
				t.Error("IsSSE should be true")
			}
			if r.State == nil || *r.State != model.ReasoningObservationCaptured {
				t.Errorf("Reasoning state = %v, want captured", r.State)
			}
			if r.Effort == nil || *r.Effort != reasoningEffort {
				t.Errorf("Reasoning effort = %v, want %q", r.Effort, reasoningEffort)
			}
			if r.BytesSent != 1024 || r.BytesReceived != 8192 {
				t.Errorf("traffic = sent:%d received:%d, want sent:1024 received:8192", r.BytesSent, r.BytesReceived)
			}
		}
	}
	if !found {
		t.Error("request req-1 not found in response")
	}
}

func TestGetActiveRequests_JSONSerialization(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	st := newMockStore()
	startTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	mockLister := &mockActiveRequestLister{
		requests: []proxy.ActiveRequest{
			{
				RequestID:  "test-uuid",
				ProviderID: "prov",
				Model:      "model",
				APIType:    "claude",
				UserID:     "",
				ClientIP:   "127.0.0.1",
				IsSSE:      false,
				StartedAt:  startTime,
			},
		},
	}

	h := NewHandler(Config{
		Store:         st,
		Concurrency:   &mockConcurrencyTracker{},
		ActiveReqList: mockLister,
		Logger:        logger,
	})

	req := httptest.NewRequest(http.MethodGet, "/admin/api/requests/active", nil)
	w := httptest.NewRecorder()

	h.GetActiveRequests(w, req)

	// Verify JSON structure matches expected field names
	body := w.Body.String()
	expectedFields := []string{
		`"request_id"`,
		`"provider_id"`,
		`"model"`,
		`"api_type"`,
		`"user_id"`,
		`"client_ip"`,
		`"is_sse"`,
		`"is_websocket"`,
		`"started_at"`,
		`"requests"`,
		`"count"`,
	}

	for _, field := range expectedFields {
		if !strings.Contains(body, field) {
			t.Errorf("response body missing expected field %s", field)
		}
	}
}
