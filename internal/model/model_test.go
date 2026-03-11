package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestProvider_JSON(t *testing.T) {
	groupID := "g1"
	p := Provider{
		ID:     "p1",
		Name:   "Test Provider",
		APIKey: "key123",
		APITypes: []ProviderAPIType{{
			ProviderID: "p1",
			APIType:    "claude",
			BaseURL:    "https://api.example.com",
			APIKey:     "type-key",
		}},
		AuthMode: "bearer",
		GroupID:  &groupID,
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

func TestProviderAPIType_JSON(t *testing.T) {
	pat := ProviderAPIType{
		ProviderID: "p1",
		APIType:    "claude",
		BaseURL:    "https://api.example.com",
		APIKey:     "type-key",
	}

	data, err := json.Marshal(pat)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var pat2 ProviderAPIType
	if err := json.Unmarshal(data, &pat2); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if pat2.ProviderID != pat.ProviderID ||
		pat2.APIType != pat.APIType ||
		pat2.BaseURL != pat.BaseURL ||
		pat2.APIKey != pat.APIKey {
		t.Errorf("round-trip failed: got %+v", pat2)
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
	now := time.Now()
	h := HealthState{
		ProviderID:   "p1",
		Available:    true,
		SuccessCount: 100,
		FailCount:    5,
		LastSuccess:  &now,
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

func TestRuntimeConfig_JSON(t *testing.T) {
	rc := RuntimeConfig{
		Key:       "sticky_ttl",
		Value:     "300",
		UpdatedAt: time.Now(),
	}

	data, err := json.Marshal(rc)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var rc2 RuntimeConfig
	if err := json.Unmarshal(data, &rc2); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if rc2.Key != rc.Key || rc2.Value != rc.Value {
		t.Errorf("round-trip failed: got %+v", rc2)
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
	e := NewGatewayError("PROVIDER_UNAVAILABLE", "No available provider")

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

func TestNewGatewayError(t *testing.T) {
	e := NewGatewayError("TEST_CODE", "Test message")
	if e.Error.Code != "TEST_CODE" {
		t.Errorf("Code = %q, want %q", e.Error.Code, "TEST_CODE")
	}
	if e.Error.Message != "Test message" {
		t.Errorf("Message = %q, want %q", e.Error.Message, "Test message")
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
		Model:   "claude-sonnet-4",
	}

	if k.IP != "192.168.1.1" {
		t.Errorf("IP = %q, want %q", k.IP, "192.168.1.1")
	}
	if k.Model != "claude-sonnet-4" {
		t.Errorf("Model = %q, want %q", k.Model, "claude-sonnet-4")
	}
}

func TestProvider_BaseURLForAPIType(t *testing.T) {
	p := Provider{
		ID: "p1",
		APITypes: []ProviderAPIType{
			{ProviderID: "p1", APIType: "claude", BaseURL: "https://claude.example.com"},
			{ProviderID: "p1", APIType: "codex", BaseURL: "https://codex.example.com"},
		},
	}

	// Matching API type returns the corresponding BaseURL.
	if got := p.BaseURLForAPIType("claude"); got != "https://claude.example.com" {
		t.Errorf("BaseURLForAPIType(claude) = %q, want %q", got, "https://claude.example.com")
	}
	if got := p.BaseURLForAPIType("codex"); got != "https://codex.example.com" {
		t.Errorf("BaseURLForAPIType(codex) = %q, want %q", got, "https://codex.example.com")
	}

	// Non-existent API type returns empty string.
	if got := p.BaseURLForAPIType("gemini"); got != "" {
		t.Errorf("BaseURLForAPIType(gemini) = %q, want empty", got)
	}

	// Empty APITypes returns empty string.
	empty := Provider{ID: "p2"}
	if got := empty.BaseURLForAPIType("claude"); got != "" {
		t.Errorf("BaseURLForAPIType on empty provider = %q, want empty", got)
	}
}

func TestProvider_APIKeyForAPIType(t *testing.T) {
	p := Provider{
		ID:     "p1",
		APIKey: "default-key",
		APITypes: []ProviderAPIType{
			{ProviderID: "p1", APIType: "claude", BaseURL: "https://claude.example.com"},
			{
				ProviderID: "p1",
				APIType:    "codex",
				BaseURL:    "https://codex.example.com",
				APIKey:     "codex-key",
			},
		},
	}

	if got := p.APIKeyForAPIType("claude"); got != "default-key" {
		t.Errorf("APIKeyForAPIType(claude) = %q, want %q", got, "default-key")
	}
	if got := p.APIKeyForAPIType("codex"); got != "codex-key" {
		t.Errorf("APIKeyForAPIType(codex) = %q, want %q", got, "codex-key")
	}

	// Unknown API type falls back to the provider default because callers may
	// resolve credentials separately from endpoint validation.
	if got := p.APIKeyForAPIType("gemini"); got != "default-key" {
		t.Errorf("APIKeyForAPIType(gemini) = %q, want %q", got, "default-key")
	}
}

func TestProvider_APIKeyForAPIType_IgnoresWhitespaceOverride(t *testing.T) {
	p := Provider{
		ID:     "p1",
		APIKey: " default-key ",
		APITypes: []ProviderAPIType{{
			ProviderID: "p1",
			APIType:    "claude",
			BaseURL:    "https://claude.example.com",
			APIKey:     "   ",
		}},
	}

	if got := p.APIKeyForAPIType("claude"); got != "default-key" {
		t.Errorf("APIKeyForAPIType(claude) = %q, want %q", got, "default-key")
	}
}

func TestSelectRequest(t *testing.T) {
	r := SelectRequest{
		ClientIP:   "10.0.0.1",
		User:       "testuser",
		APIType:    "codex",
		Model:      "gpt-4",
		StickyMode: StickyModeModel,
	}

	if r.APIType != "codex" {
		t.Errorf("APIType = %q, want %q", r.APIType, "codex")
	}
	if r.StickyMode != StickyModeModel {
		t.Errorf("StickyMode = %q, want %q", r.StickyMode, StickyModeModel)
	}
}

func TestIsValidStickyMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode StickyMode
		want bool
	}{
		{name: "off", mode: StickyModeOff, want: true},
		{name: "api_type", mode: StickyModeAPIType, want: true},
		{name: "model", mode: StickyModeModel, want: true},
		{name: "empty", mode: "", want: false},
		{name: "invalid", mode: StickyMode("unknown"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := IsValidStickyMode(tt.mode); got != tt.want {
				t.Fatalf("IsValidStickyMode(%q) = %v, want %v", tt.mode, got, tt.want)
			}
		})
	}
}
