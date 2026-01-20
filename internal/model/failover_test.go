package model

import (
	"testing"
)

func TestVendorMatch(t *testing.T) {
	tests := []struct {
		name     string
		a        string
		b        string
		expected bool
	}{
		{"empty a", "", "yescode", false},
		{"empty b", "yescode", "", false},
		{"both empty", "", "", false},
		{"exact match", "yescode", "yescode", true},
		{"different vendors", "yescode", "openrouter", false},
		{"wildcard a", "*", "yescode", true},
		{"wildcard b", "yescode", "*", true},
		{"both wildcards", "*", "*", true},
		{"wildcard vs empty", "*", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := VendorMatch(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("VendorMatch(%q, %q) = %v, want %v", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}

func TestAnyVendorMatch(t *testing.T) {
	tests := []struct {
		name         string
		contaminated []string
		candidate    string
		expected     bool
	}{
		{"empty contaminated", []string{}, "yescode", false},
		{"single match", []string{"yescode"}, "yescode", true},
		{"single no match", []string{"yescode"}, "openrouter", false},
		{"multiple with match", []string{"yescode", "openrouter"}, "yescode", true},
		{"multiple no match", []string{"yescode", "openrouter"}, "gemini", false},
		{"wildcard candidate matches specific", []string{"yescode"}, "*", true},
		{"specific matches wildcard candidate", []string{"*"}, "yescode", false}, // Wildcard in contaminated is skipped
		{"wildcard in contaminated is skipped", []string{"*", "yescode"}, "yescode", true},
		{"empty candidate", []string{"yescode"}, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := AnyVendorMatch(tt.contaminated, tt.candidate)
			if result != tt.expected {
				t.Errorf("AnyVendorMatch(%v, %q) = %v, want %v", tt.contaminated, tt.candidate, result, tt.expected)
			}
		})
	}
}

func TestAllVendorsMatch(t *testing.T) {
	tests := []struct {
		name         string
		contaminated []string
		candidate    string
		expected     bool
	}{
		{"empty contaminated", []string{}, "yescode", false},
		{"single match", []string{"yescode"}, "yescode", true},
		{"single no match", []string{"yescode"}, "openrouter", false},
		{"multiple all match wildcard", []string{"yescode", "openrouter"}, "*", true},
		{"multiple partial match", []string{"yescode", "openrouter"}, "yescode", false},
		{"wildcard in contaminated", []string{"*"}, "yescode", true},
		{"multiple with wildcard", []string{"yescode", "*"}, "yescode", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := AllVendorsMatch(tt.contaminated, tt.candidate)
			if result != tt.expected {
				t.Errorf("AllVendorsMatch(%v, %q) = %v, want %v", tt.contaminated, tt.candidate, result, tt.expected)
			}
		})
	}
}

func TestStricterScope(t *testing.T) {
	tests := []struct {
		name     string
		a        Scope
		b        Scope
		expected Scope
	}{
		{"none vs any", ScopeNone, ScopeAny, ScopeNone},
		{"any vs none", ScopeAny, ScopeNone, ScopeNone},
		{"vendor vs any", ScopeVendor, ScopeAny, ScopeVendor},
		{"any vs vendor", ScopeAny, ScopeVendor, ScopeVendor},
		{"none vs vendor", ScopeNone, ScopeVendor, ScopeNone},
		{"same none", ScopeNone, ScopeNone, ScopeNone},
		{"same any", ScopeAny, ScopeAny, ScopeAny},
		{"empty treated as any", "", ScopeVendor, ScopeVendor},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := StricterScope(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("StricterScope(%q, %q) = %q, want %q", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}

func TestNewFailoverContext(t *testing.T) {
	p := &Provider{
		ID:            "p1",
		Vendor:        "yescode",
		FailoverScope: ScopeVendor,
	}

	ctx := NewFailoverContext(p)

	if ctx.OriginProviderID != "p1" {
		t.Errorf("OriginProviderID = %q, want %q", ctx.OriginProviderID, "p1")
	}
	if len(ctx.AttemptChain) != 1 || ctx.AttemptChain[0] != "p1" {
		t.Errorf("AttemptChain = %v, want [p1]", ctx.AttemptChain)
	}
	if len(ctx.ContaminatedVendors) != 1 || ctx.ContaminatedVendors[0] != "yescode" {
		t.Errorf("ContaminatedVendors = %v, want [yescode]", ctx.ContaminatedVendors)
	}
	if ctx.StrictestScope != ScopeVendor {
		t.Errorf("StrictestScope = %q, want %q", ctx.StrictestScope, ScopeVendor)
	}
	if ctx.RetryCount != 0 {
		t.Errorf("RetryCount = %d, want 0", ctx.RetryCount)
	}
}

func TestFailoverContextUpdate(t *testing.T) {
	p1 := &Provider{ID: "p1", Vendor: "yescode", FailoverScope: ScopeAny}
	p2 := &Provider{ID: "p2", Vendor: "yescode", FailoverScope: ScopeVendor}

	ctx := NewFailoverContext(p1)
	ctx.Update(p2)

	if len(ctx.AttemptChain) != 2 {
		t.Errorf("AttemptChain length = %d, want 2", len(ctx.AttemptChain))
	}
	if ctx.AttemptChain[1] != "p2" {
		t.Errorf("AttemptChain[1] = %q, want p2", ctx.AttemptChain[1])
	}
	if ctx.StrictestScope != ScopeVendor {
		t.Errorf("StrictestScope = %q, want %q", ctx.StrictestScope, ScopeVendor)
	}
	if ctx.RetryCount != 1 {
		t.Errorf("RetryCount = %d, want 1", ctx.RetryCount)
	}
}

func TestIsInChain(t *testing.T) {
	tests := []struct {
		name     string
		chainIDs []string
		checkID  string
		expected bool
	}{
		{
			name:     "single provider - in chain",
			chainIDs: []string{"p1"},
			checkID:  "p1",
			expected: true,
		},
		{
			name:     "single provider - not in chain",
			chainIDs: []string{"p1"},
			checkID:  "p2",
			expected: false,
		},
		{
			name:     "multiple providers - first in chain",
			chainIDs: []string{"p1", "p2", "p3"},
			checkID:  "p1",
			expected: true,
		},
		{
			name:     "multiple providers - middle in chain",
			chainIDs: []string{"p1", "p2", "p3"},
			checkID:  "p2",
			expected: true,
		},
		{
			name:     "multiple providers - last in chain",
			chainIDs: []string{"p1", "p2", "p3"},
			checkID:  "p3",
			expected: true,
		},
		{
			name:     "multiple providers - not in chain",
			chainIDs: []string{"p1", "p2", "p3"},
			checkID:  "p4",
			expected: false,
		},
		{
			name:     "empty string ID - not in chain",
			chainIDs: []string{"p1", "p2"},
			checkID:  "",
			expected: false,
		},
		{
			name:     "empty chain",
			chainIDs: []string{},
			checkID:  "p1",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &FailoverContext{
				AttemptChain: tt.chainIDs,
			}
			result := ctx.IsInChain(tt.checkID)
			if result != tt.expected {
				t.Errorf("IsInChain(%q) = %v, want %v", tt.checkID, result, tt.expected)
			}
		})
	}
}

func TestIsFailoverAllowed(t *testing.T) {
	tests := []struct {
		name                string
		origin              *Provider
		candidate           *Provider
		maxProviderSwitches int
		expected            bool
	}{
		{
			name:                "cycle detection - same provider",
			origin:              &Provider{ID: "p1", Vendor: "v1", FailoverScope: ScopeAny, AcceptFailover: ScopeAny},
			candidate:           &Provider{ID: "p1", Vendor: "v1", FailoverScope: ScopeAny, AcceptFailover: ScopeAny},
			maxProviderSwitches: 0,
			expected:            false,
		},
		{
			name:                "scope none blocks all",
			origin:              &Provider{ID: "p1", Vendor: "v1", FailoverScope: ScopeNone, AcceptFailover: ScopeAny},
			candidate:           &Provider{ID: "p2", Vendor: "v1", FailoverScope: ScopeAny, AcceptFailover: ScopeAny},
			maxProviderSwitches: 0,
			expected:            false,
		},
		{
			name:                "scope vendor - matching vendor allowed",
			origin:              &Provider{ID: "p1", Vendor: "yescode", FailoverScope: ScopeVendor, AcceptFailover: ScopeAny},
			candidate:           &Provider{ID: "p2", Vendor: "yescode", FailoverScope: ScopeAny, AcceptFailover: ScopeAny},
			maxProviderSwitches: 0,
			expected:            true,
		},
		{
			name:                "scope vendor - different vendor blocked",
			origin:              &Provider{ID: "p1", Vendor: "yescode", FailoverScope: ScopeVendor, AcceptFailover: ScopeAny},
			candidate:           &Provider{ID: "p2", Vendor: "openrouter", FailoverScope: ScopeAny, AcceptFailover: ScopeAny},
			maxProviderSwitches: 0,
			expected:            false,
		},
		{
			name:                "accept_failover none blocks",
			origin:              &Provider{ID: "p1", Vendor: "v1", FailoverScope: ScopeAny, AcceptFailover: ScopeAny},
			candidate:           &Provider{ID: "p2", Vendor: "v1", FailoverScope: ScopeAny, AcceptFailover: ScopeNone},
			maxProviderSwitches: 0,
			expected:            false,
		},
		{
			name:                "accept_failover vendor - matching vendor allowed",
			origin:              &Provider{ID: "p1", Vendor: "yescode", FailoverScope: ScopeAny, AcceptFailover: ScopeAny},
			candidate:           &Provider{ID: "p2", Vendor: "yescode", FailoverScope: ScopeAny, AcceptFailover: ScopeVendor},
			maxProviderSwitches: 0,
			expected:            true,
		},
		{
			name:                "accept_failover vendor - different vendor blocked",
			origin:              &Provider{ID: "p1", Vendor: "yescode", FailoverScope: ScopeAny, AcceptFailover: ScopeAny},
			candidate:           &Provider{ID: "p2", Vendor: "openrouter", FailoverScope: ScopeAny, AcceptFailover: ScopeVendor},
			maxProviderSwitches: 0,
			expected:            false,
		},
		{
			name:                "wildcard candidate accepts any vendor",
			origin:              &Provider{ID: "p1", Vendor: "yescode", FailoverScope: ScopeAny, AcceptFailover: ScopeAny},
			candidate:           &Provider{ID: "p2", Vendor: "*", FailoverScope: ScopeAny, AcceptFailover: ScopeVendor},
			maxProviderSwitches: 0,
			expected:            true,
		},
		{
			name:                "depth limit reached",
			origin:              &Provider{ID: "p1", Vendor: "v1", FailoverScope: ScopeAny, AcceptFailover: ScopeAny},
			candidate:           &Provider{ID: "p2", Vendor: "v1", FailoverScope: ScopeAny, AcceptFailover: ScopeAny},
			maxProviderSwitches: 0, // 0 means no limit, let's test with 1
			expected:            true,
		},
		{
			name:                "scope any allows all",
			origin:              &Provider{ID: "p1", Vendor: "yescode", FailoverScope: ScopeAny, AcceptFailover: ScopeAny},
			candidate:           &Provider{ID: "p2", Vendor: "openrouter", FailoverScope: ScopeAny, AcceptFailover: ScopeAny},
			maxProviderSwitches: 0,
			expected:            true,
		},
		{
			name:                "empty vendor - scope any allowed",
			origin:              &Provider{ID: "p1", Vendor: "", FailoverScope: ScopeAny, AcceptFailover: ScopeAny},
			candidate:           &Provider{ID: "p2", Vendor: "", FailoverScope: ScopeAny, AcceptFailover: ScopeAny},
			maxProviderSwitches: 0,
			expected:            true,
		},
		{
			name:                "empty vendor - scope vendor blocked",
			origin:              &Provider{ID: "p1", Vendor: "", FailoverScope: ScopeVendor, AcceptFailover: ScopeAny},
			candidate:           &Provider{ID: "p2", Vendor: "", FailoverScope: ScopeAny, AcceptFailover: ScopeAny},
			maxProviderSwitches: 0,
			expected:            false,
		},
		{
			name:                "empty accept_failover treated as any",
			origin:              &Provider{ID: "p1", Vendor: "v1", FailoverScope: ScopeAny, AcceptFailover: ScopeAny},
			candidate:           &Provider{ID: "p2", Vendor: "v2", FailoverScope: ScopeAny, AcceptFailover: ""},
			maxProviderSwitches: 0,
			expected:            true,
		},
		{
			name:                "empty failover_scope treated as any",
			origin:              &Provider{ID: "p1", Vendor: "v1", FailoverScope: "", AcceptFailover: ScopeAny},
			candidate:           &Provider{ID: "p2", Vendor: "v2", FailoverScope: ScopeAny, AcceptFailover: ScopeAny},
			maxProviderSwitches: 0,
			expected:            true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := NewFailoverContext(tt.origin)
			result := IsFailoverAllowed(tt.candidate, ctx, tt.maxProviderSwitches)
			if result != tt.expected {
				t.Errorf("IsFailoverAllowed() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestIsFailoverAllowed_DepthLimit(t *testing.T) {
	p1 := &Provider{ID: "p1", Vendor: "v1", FailoverScope: ScopeAny, AcceptFailover: ScopeAny}
	p2 := &Provider{ID: "p2", Vendor: "v1", FailoverScope: ScopeAny, AcceptFailover: ScopeAny}
	p3 := &Provider{ID: "p3", Vendor: "v1", FailoverScope: ScopeAny, AcceptFailover: ScopeAny}
	p4 := &Provider{ID: "p4", Vendor: "v1", FailoverScope: ScopeAny, AcceptFailover: ScopeAny}

	ctx := NewFailoverContext(p1) // RetryCount = 0

	// With maxRetries=3, we can do 3 retries (p2, p3, p4)
	if !IsFailoverAllowed(p2, ctx, 3) {
		t.Error("p2 should be allowed (retry 1)")
	}
	ctx.Update(p2) // RetryCount = 1

	if !IsFailoverAllowed(p3, ctx, 3) {
		t.Error("p3 should be allowed (retry 2)")
	}
	ctx.Update(p3) // RetryCount = 2

	if !IsFailoverAllowed(p4, ctx, 3) {
		t.Error("p4 should be allowed (retry 3)")
	}
	ctx.Update(p4) // RetryCount = 3

	// Now at max retries, no more allowed
	p5 := &Provider{ID: "p5", Vendor: "v1", FailoverScope: ScopeAny, AcceptFailover: ScopeAny}
	if IsFailoverAllowed(p5, ctx, 3) {
		t.Error("p5 should be blocked (max retries reached)")
	}
}

func TestIsFailoverAllowed_ChainedVendorIsolation(t *testing.T) {
	// Test scenario: yescode -> * -> openrouter should be blocked
	yescode1 := &Provider{ID: "yc1", Vendor: "yescode", FailoverScope: ScopeVendor, AcceptFailover: ScopeAny}
	fallback := &Provider{ID: "fb1", Vendor: "*", FailoverScope: ScopeVendor, AcceptFailover: ScopeVendor}
	openrouter := &Provider{ID: "or1", Vendor: "openrouter", FailoverScope: ScopeAny, AcceptFailover: ScopeAny}

	ctx := NewFailoverContext(yescode1)

	// Fallback with wildcard should be allowed (matches yescode via wildcard)
	if !IsFailoverAllowed(fallback, ctx, 0) {
		t.Error("fallback should be allowed after yescode")
	}
	ctx.Update(fallback)

	// OpenRouter should be blocked because:
	// - StrictestScope is ScopeVendor (from yescode1 and fallback)
	// - ContaminatedVendors = ["yescode", "*"]
	// - AnyVendorMatch skips "*", so checks if "yescode" matches "openrouter" -> false
	if IsFailoverAllowed(openrouter, ctx, 0) {
		t.Error("openrouter should be blocked - yescode's vendor isolation prevents cross-vendor failover")
	}
}

func TestIsFailoverAllowed_MultipleVendorChains(t *testing.T) {
	t.Run("openrouter(any) -> * -> yescode should be ALLOWED", func(t *testing.T) {
		// openrouter uses ScopeAny, so no vendor restrictions
		openrouter := &Provider{ID: "or1", Vendor: "openrouter", FailoverScope: ScopeAny, AcceptFailover: ScopeAny}
		fallback := &Provider{ID: "fb1", Vendor: "*", FailoverScope: ScopeAny, AcceptFailover: ScopeVendor}
		yescode := &Provider{ID: "yc1", Vendor: "yescode", FailoverScope: ScopeAny, AcceptFailover: ScopeAny}

		ctx := NewFailoverContext(openrouter)

		// Fallback should be allowed (openrouter has ScopeAny)
		if !IsFailoverAllowed(fallback, ctx, 0) {
			t.Error("fallback should be allowed after openrouter with ScopeAny")
		}
		ctx.Update(fallback)

		// Yescode should be allowed because:
		// - StrictestScope is ScopeAny (from both providers)
		// - ScopeAny allows failover to any vendor
		if !IsFailoverAllowed(yescode, ctx, 0) {
			t.Error("yescode should be allowed - openrouter's ScopeAny allows cross-vendor failover")
		}
	})

	t.Run("yescode -> yescode2 -> openrouter should be BLOCKED", func(t *testing.T) {
		// Both yescode providers use ScopeVendor, double contamination
		yescode1 := &Provider{ID: "yc1", Vendor: "yescode", FailoverScope: ScopeVendor, AcceptFailover: ScopeAny}
		yescode2 := &Provider{ID: "yc2", Vendor: "yescode", FailoverScope: ScopeVendor, AcceptFailover: ScopeVendor}
		openrouter := &Provider{ID: "or1", Vendor: "openrouter", FailoverScope: ScopeAny, AcceptFailover: ScopeAny}

		ctx := NewFailoverContext(yescode1)

		// yescode2 should be allowed (same vendor)
		if !IsFailoverAllowed(yescode2, ctx, 0) {
			t.Error("yescode2 should be allowed - same vendor as yescode1")
		}
		ctx.Update(yescode2)

		// OpenRouter should be blocked because:
		// - StrictestScope is ScopeVendor
		// - ContaminatedVendors = ["yescode", "yescode"]
		// - AnyVendorMatch checks if "yescode" matches "openrouter" -> false
		if IsFailoverAllowed(openrouter, ctx, 0) {
			t.Error("openrouter should be blocked - double yescode contamination with ScopeVendor")
		}
	})

	t.Run("empty vendor -> yescode -> empty vendor should test edge cases", func(t *testing.T) {
		// Empty vendor with ScopeAny allows failover anywhere
		empty1 := &Provider{ID: "e1", Vendor: "", FailoverScope: ScopeAny, AcceptFailover: ScopeAny}
		yescode := &Provider{ID: "yc1", Vendor: "yescode", FailoverScope: ScopeVendor, AcceptFailover: ScopeAny}
		empty2 := &Provider{ID: "e2", Vendor: "", FailoverScope: ScopeAny, AcceptFailover: ScopeAny}

		ctx := NewFailoverContext(empty1)

		// Yescode should be allowed (empty1 has ScopeAny)
		if !IsFailoverAllowed(yescode, ctx, 0) {
			t.Error("yescode should be allowed after empty vendor with ScopeAny")
		}
		ctx.Update(yescode)

		// Empty2 should be blocked because:
		// - StrictestScope is ScopeVendor (from yescode)
		// - AnyVendorMatch checks if "yescode" matches "" -> false (empty is island)
		if IsFailoverAllowed(empty2, ctx, 0) {
			t.Error("empty2 should be blocked - yescode's ScopeVendor restricts to matching vendors, empty doesn't match")
		}
	})

	t.Run("unknown scope treated as ScopeAny", func(t *testing.T) {
		// Test that unknown scopes are treated as least restrictive (ScopeAny)
		unknownScope := &Provider{ID: "u1", Vendor: "vendor1", FailoverScope: Scope("unknown"), AcceptFailover: ScopeAny}
		other := &Provider{ID: "o1", Vendor: "vendor2", FailoverScope: ScopeAny, AcceptFailover: ScopeAny}

		ctx := NewFailoverContext(unknownScope)

		// Should be allowed because unknown scope is treated as ScopeAny
		if !IsFailoverAllowed(other, ctx, 0) {
			t.Error("other should be allowed - unknown scope should be treated as ScopeAny")
		}
	})
}

func TestHasContradictoryConfig(t *testing.T) {
	tests := []struct {
		name             string
		vendor           string
		failoverScope    Scope
		acceptFailover   Scope
		expectedWarnings int
	}{
		{"no warning - vendor set", "yescode", ScopeVendor, ScopeVendor, 0},
		{"no warning - scope any", "", ScopeAny, ScopeAny, 0},
		{"one warning - failover vendor but empty vendor", "", ScopeVendor, ScopeAny, 1},
		{"one warning - accept vendor but empty vendor", "", ScopeAny, ScopeVendor, 1},
		{"two warnings - both vendor but empty vendor", "", ScopeVendor, ScopeVendor, 2},
		{"no warning - scope none with empty vendor", "", ScopeNone, ScopeNone, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Provider{
				Vendor:         tt.vendor,
				FailoverScope:  tt.failoverScope,
				AcceptFailover: tt.acceptFailover,
			}
			warnings := HasContradictoryConfig(p)
			if len(warnings) != tt.expectedWarnings {
				t.Errorf("HasContradictoryConfig() returned %d warnings %v, expected %d", len(warnings), warnings, tt.expectedWarnings)
			}
		})
	}
}

func TestIsValidScope(t *testing.T) {
	tests := []struct {
		scope    Scope
		expected bool
	}{
		{ScopeNone, true},
		{ScopeVendor, true},
		{ScopeAny, true},
		{"", true},
		{"invalid", false},
		{"NONE", false},
	}

	for _, tt := range tests {
		t.Run(string(tt.scope), func(t *testing.T) {
			result := IsValidScope(tt.scope)
			if result != tt.expected {
				t.Errorf("IsValidScope(%q) = %v, want %v", tt.scope, result, tt.expected)
			}
		})
	}
}

func TestNewFailoverContext_EmptyVendorChain(t *testing.T) {
	// Edge case: provider with empty vendor trying to use ScopeVendor
	empty := &Provider{ID: "e1", Vendor: "", FailoverScope: ScopeVendor}
	ctx := NewFailoverContext(empty)

	// ContaminatedVendors should be empty since vendor is ""
	if len(ctx.ContaminatedVendors) != 0 {
		t.Errorf("ContaminatedVendors should be empty for empty vendor, got %v", ctx.ContaminatedVendors)
	}

	// AttemptChain should contain the provider ID
	if len(ctx.AttemptChain) != 1 || ctx.AttemptChain[0] != "e1" {
		t.Errorf("AttemptChain = %v, want [e1]", ctx.AttemptChain)
	}

	// StrictestScope should be ScopeVendor
	if ctx.StrictestScope != ScopeVendor {
		t.Errorf("StrictestScope = %q, want %q", ctx.StrictestScope, ScopeVendor)
	}
}

func TestAllVendorsMatch_EmptyCandidate(t *testing.T) {
	// Edge case: empty candidate should return false
	contaminated := []string{"yescode", "openrouter"}

	result := AllVendorsMatch(contaminated, "")
	if result {
		t.Errorf("AllVendorsMatch(%v, \"\") = true, want false", contaminated)
	}
}
