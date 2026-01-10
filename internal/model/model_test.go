package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestProvider_JSON(t *testing.T) {
	p := Provider{
		ID:       "p1",
		Name:     "Test Provider",
		BaseURL:  "https://api.example.com",
		APIKey:   "key123",
		APITypes: []string{"claude", "codex"},
		AuthMode: "bearer",
		Enabled:  true,
	}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var p2 Provider
	if err := json.Unmarshal(data, &p2); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if p2.ID != p.ID || p2.Name != p.Name {
		t.Errorf("round-trip failed: got %+v", p2)
	}
}

func TestGroup_JSON(t *testing.T) {
	g := Group{
		ID:       "g1",
		Name:     "Test Group",
		Strategy: "priority",
		Priority: 1,
		Weight:   10,
		Enabled:  true,
	}

	data, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var g2 Group
	if err := json.Unmarshal(data, &g2); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if g2.ID != g.ID || g2.Strategy != g.Strategy {
		t.Errorf("round-trip failed: got %+v", g2)
	}
}

func TestHealthState_JSON(t *testing.T) {
	h := HealthState{
		ProviderID:   "p1",
		Available:    true,
		SuccessCount: 100,
		FailCount:    5,
		LastSuccess:  time.Now(),
	}

	data, err := json.Marshal(h)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var h2 HealthState
	if err := json.Unmarshal(data, &h2); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if h2.ProviderID != h.ProviderID || h2.SuccessCount != h.SuccessCount {
		t.Errorf("round-trip failed: got %+v", h2)
	}
}

func TestRequestLog_JSON(t *testing.T) {
	r := RequestLog{
		ID:         1,
		ProviderID: "p1",
		APIType:    "claude",
		Model:      "claude-3",
		StatusCode: 200,
		Success:    true,
	}

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var r2 RequestLog
	if err := json.Unmarshal(data, &r2); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if r2.ID != r.ID || r2.APIType != r.APIType {
		t.Errorf("round-trip failed: got %+v", r2)
	}
}

func TestGatewayError_JSON(t *testing.T) {
	e := GatewayError{}
	e.Error.Code = "PROVIDER_UNAVAILABLE"
	e.Error.Message = "No available provider"

	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var e2 GatewayError
	if err := json.Unmarshal(data, &e2); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if e2.Error.Code != e.Error.Code {
		t.Errorf("round-trip failed: got %+v", e2)
	}
}

func TestErrorResponse_JSON(t *testing.T) {
	e := ErrorResponse{
		Code:    "NOT_FOUND",
		Message: "Resource not found",
	}

	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var e2 ErrorResponse
	if err := json.Unmarshal(data, &e2); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if e2.Code != e.Code || e2.Message != e.Message {
		t.Errorf("round-trip failed: got %+v", e2)
	}
}

func TestStickyKey(t *testing.T) {
	k := StickyKey{
		IP:      "192.168.1.1",
		User:    "user1",
		APIType: "claude",
	}

	if k.IP != "192.168.1.1" {
		t.Errorf("IP = %q, want %q", k.IP, "192.168.1.1")
	}
}

func TestSelectRequest(t *testing.T) {
	r := SelectRequest{
		ClientIP: "10.0.0.1",
		User:     "testuser",
		APIType:  "codex",
		Model:    "gpt-4",
	}

	if r.APIType != "codex" {
		t.Errorf("APIType = %q, want %q", r.APIType, "codex")
	}
}
