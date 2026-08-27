package codexstartup

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	KeyUpstreamHeaderHygiene = "codex_upstream_header_hygiene_enabled"
	KeyWebSocketSubprotocol  = "codex_websocket_subprotocol_enabled"
	KeyContinuity            = "codex_continuity_enabled"
	KeyProviderCookieJar     = "codex_provider_cookie_jar_enabled"
)

// Feature is the stable semantic identity behind a persisted runtime-config key.
// Keeping dependencies on this type prevents callers from rebuilding feature
// relationships with string comparisons.
type Feature uint8

const (
	FeatureUpstreamHeaderHygiene Feature = iota + 1
	FeatureWebSocketSubprotocol
	FeatureContinuity
	FeatureProviderCookieJar
)

// Definition is the public, detached projection of one registry entry.
type Definition struct {
	Feature  Feature
	Key      string
	Default  bool
	Requires []Feature
}

type compiledCapability struct {
	name      string
	available func(CompiledCapabilities) bool
}

type featureSpec struct {
	definition   Definition
	enabled      func(Snapshot) bool
	set          func(*Snapshot, bool)
	capabilities []compiledCapability
	needHMAC     bool
	needAEAD     bool
}

var featureRegistry = [...]featureSpec{
	{
		definition: Definition{Feature: FeatureUpstreamHeaderHygiene, Key: KeyUpstreamHeaderHygiene, Default: false},
		enabled:    func(snapshot Snapshot) bool { return snapshot.UpstreamHeaderHygiene },
		set:        func(snapshot *Snapshot, enabled bool) { snapshot.UpstreamHeaderHygiene = enabled },
		capabilities: []compiledCapability{
			{name: "upstream_header_hygiene", available: func(compiled CompiledCapabilities) bool { return compiled.UpstreamHeaderHygiene }},
		},
	},
	{
		definition: Definition{Feature: FeatureWebSocketSubprotocol, Key: KeyWebSocketSubprotocol, Default: false},
		enabled:    func(snapshot Snapshot) bool { return snapshot.WebSocketSubprotocol },
		set:        func(snapshot *Snapshot, enabled bool) { snapshot.WebSocketSubprotocol = enabled },
		capabilities: []compiledCapability{
			{name: "websocket_subprotocol", available: func(compiled CompiledCapabilities) bool { return compiled.WebSocketSubprotocol }},
		},
	},
	{
		definition: Definition{
			Feature:  FeatureContinuity,
			Key:      KeyContinuity,
			Default:  false,
			Requires: []Feature{FeatureUpstreamHeaderHygiene},
		},
		enabled:  func(snapshot Snapshot) bool { return snapshot.Continuity },
		set:      func(snapshot *Snapshot, enabled bool) { snapshot.Continuity = enabled },
		needHMAC: true,
		capabilities: []compiledCapability{
			{name: "credential_sessions", available: func(compiled CompiledCapabilities) bool { return compiled.CredentialSessions }},
			{name: "credential_subjects_resolved", available: func(compiled CompiledCapabilities) bool { return compiled.CredentialSubjectsResolved }},
			{name: "continuity_schema", available: func(compiled CompiledCapabilities) bool { return compiled.ContinuitySchema }},
			{name: "protocol_catalog", available: func(compiled CompiledCapabilities) bool { return compiled.ProtocolCatalog }},
			{name: "identity", available: func(compiled CompiledCapabilities) bool { return compiled.Identity }},
			{name: "applied_identity", available: func(compiled CompiledCapabilities) bool { return compiled.AppliedIdentity }},
		},
	},
	{
		definition: Definition{
			Feature:  FeatureProviderCookieJar,
			Key:      KeyProviderCookieJar,
			Default:  false,
			Requires: []Feature{FeatureUpstreamHeaderHygiene},
		},
		enabled:  func(snapshot Snapshot) bool { return snapshot.ProviderCookieJar },
		set:      func(snapshot *Snapshot, enabled bool) { snapshot.ProviderCookieJar = enabled },
		needHMAC: true,
		needAEAD: true,
		capabilities: []compiledCapability{
			{name: "credential_sessions", available: func(compiled CompiledCapabilities) bool { return compiled.CredentialSessions }},
			{name: "credential_subjects_resolved", available: func(compiled CompiledCapabilities) bool { return compiled.CredentialSubjectsResolved }},
			{name: "provider_cookie_schema", available: func(compiled CompiledCapabilities) bool { return compiled.ProviderCookieSchema }},
			{name: "identity", available: func(compiled CompiledCapabilities) bool { return compiled.Identity }},
			{name: "applied_identity", available: func(compiled CompiledCapabilities) bool { return compiled.AppliedIdentity }},
		},
	},
}

// Definitions returns a deep copy so the registry remains the only mutable
// authority for feature relationships.
func Definitions() []Definition {
	definitions := make([]Definition, len(featureRegistry))
	for index, spec := range featureRegistry {
		definitions[index] = spec.definition
		definitions[index].Requires = append([]Feature(nil), spec.definition.Requires...)
	}
	return definitions
}

// Keys returns the stable persistence/admin/export order.
func Keys() []string {
	keys := make([]string, len(featureRegistry))
	for index, spec := range featureRegistry {
		keys[index] = spec.definition.Key
	}
	return keys
}

// Defaults returns a fresh runtime-config projection of the typed defaults.
func Defaults() map[string]string {
	defaults := make(map[string]string, len(featureRegistry))
	for _, spec := range featureRegistry {
		defaults[spec.definition.Key] = strconv.FormatBool(spec.definition.Default)
	}
	return defaults
}

func IsKey(key string) bool {
	_, exists := specByKey(key)
	return exists
}

// ValidateValue applies the exact parsing contract used by startup snapshots.
func ValidateValue(key, value string) error {
	if _, exists := specByKey(key); !exists {
		return wrap(ErrorInvalidConfig, key, "feature_key", fmt.Errorf("unknown feature key"))
	}
	_, err := parseBoolean(key, value)
	return err
}

func specByKey(key string) (featureSpec, bool) {
	for _, spec := range featureRegistry {
		if spec.definition.Key == key {
			return spec, true
		}
	}
	return featureSpec{}, false
}

func specByFeature(feature Feature) (featureSpec, bool) {
	for _, spec := range featureRegistry {
		if spec.definition.Feature == feature {
			return spec, true
		}
	}
	return featureSpec{}, false
}

func parseBoolean(key, value string) (bool, error) {
	switch strings.ToLower(value) {
	case "", "false", "0":
		return false, nil
	case "true", "1":
		return true, nil
	default:
		return false, invalidBoolean(key, value)
	}
}
