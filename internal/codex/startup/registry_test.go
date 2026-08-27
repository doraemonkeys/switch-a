package codexstartup

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"testing"
)

func TestFeatureRegistryContract(t *testing.T) {
	wantKeys := []string{
		KeyUpstreamHeaderHygiene,
		KeyWebSocketSubprotocol,
		KeyContinuity,
		KeyProviderCookieJar,
	}
	if got := Keys(); !slices.Equal(got, wantKeys) {
		t.Fatalf("Keys() = %v, want %v", got, wantKeys)
	}
	definitions := Definitions()
	if len(definitions) != len(wantKeys) {
		t.Fatalf("Definitions() length = %d, want %d", len(definitions), len(wantKeys))
	}
	defaults := Defaults()
	for index, definition := range definitions {
		if definition.Key != wantKeys[index] {
			t.Errorf("definition[%d].Key = %q, want %q", index, definition.Key, wantKeys[index])
		}
		if definition.Default || defaults[definition.Key] != "false" {
			t.Errorf("default for %q = %t/%q, want false", definition.Key, definition.Default, defaults[definition.Key])
		}
		if !IsKey(definition.Key) {
			t.Errorf("IsKey(%q) = false", definition.Key)
		}
	}
	if IsKey("codex_unknown_enabled") {
		t.Fatal("unknown feature key was accepted")
	}

	definitions[2].Requires[0] = FeatureWebSocketSubprotocol
	if got := Definitions()[2].Requires; !slices.Equal(got, []Feature{FeatureUpstreamHeaderHygiene}) {
		t.Fatalf("Definitions() exposed mutable dependencies: %v", got)
	}
	keys := Keys()
	keys[0] = "mutated"
	if Keys()[0] != KeyUpstreamHeaderHygiene {
		t.Fatal("Keys() exposed mutable registry storage")
	}
}

func TestFeatureRegistryValueValidation(t *testing.T) {
	for _, value := range []string{"", "false", "FALSE", "0", "true", "TRUE", "1"} {
		if err := ValidateValue(KeyContinuity, value); err != nil {
			t.Errorf("ValidateValue(%q) error = %v", value, err)
		}
	}
	if err := ValidateValue(KeyContinuity, "yes"); !IsError(err, ErrorInvalidConfig) {
		t.Fatalf("invalid bool error = %v", err)
	}
	if err := ValidateValue("codex_unknown_enabled", "false"); !IsError(err, ErrorInvalidConfig) {
		t.Fatalf("unknown key error = %v", err)
	}
	if _, found := specByFeature(Feature(255)); found {
		t.Fatal("unknown feature resolved from registry")
	}
}

func TestFeatureDependenciesComeFromRegistry(t *testing.T) {
	for _, snapshot := range []Snapshot{
		{Continuity: true},
		{ProviderCookieJar: true},
	} {
		if err := snapshot.ValidateDependencies(); !IsError(err, ErrorDependency) {
			t.Fatalf("ValidateDependencies(%+v) error = %v", snapshot, err)
		}
	}
	for _, snapshot := range []Snapshot{
		{},
		{UpstreamHeaderHygiene: true},
		{WebSocketSubprotocol: true},
		{UpstreamHeaderHygiene: true, Continuity: true},
		{UpstreamHeaderHygiene: true, ProviderCookieJar: true},
		{UpstreamHeaderHygiene: true, Continuity: true, ProviderCookieJar: true},
	} {
		if err := snapshot.ValidateDependencies(); err != nil {
			t.Fatalf("ValidateDependencies(%+v) error = %v", snapshot, err)
		}
	}
}

func TestFrontendFeatureKeySetMatchesRegistry(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "src", "config", "constants.ts"))
	if err != nil {
		t.Fatalf("read frontend config constants: %v", err)
	}
	matches := regexp.MustCompile(`["'](codex_[a-z0-9_]+_enabled)["']`).FindAllSubmatch(source, -1)
	frontendSet := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		frontendSet[string(match[1])] = struct{}{}
	}
	frontendKeys := make([]string, 0, len(frontendSet))
	for key := range frontendSet {
		frontendKeys = append(frontendKeys, key)
	}
	backendKeys := Keys()
	sort.Strings(frontendKeys)
	sort.Strings(backendKeys)
	if !slices.Equal(frontendKeys, backendKeys) {
		t.Fatalf("frontend feature keys = %v, backend registry keys = %v", frontendKeys, backendKeys)
	}
}
