// Package codexstartup owns the immutable startup feature snapshot and its
// fail-closed dependency/capability contract.
package codexstartup

import (
	"context"
	"fmt"

	"github.com/doraemonkeys/switch-a/internal/codex/keyring"
)

// ConfigReader is deliberately defined at the consumer boundary so startup
// validation does not depend on SQLite, admin, or the broad application Store.
type ConfigReader interface {
	GetConfig(ctx context.Context, key string) (string, error)
}

type Snapshot struct {
	UpstreamHeaderHygiene bool
	WebSocketSubprotocol  bool
	Continuity            bool
	ProviderCookieJar     bool
}

// CompiledCapabilities prevents a durable flag from activating a partially
// integrated feature. Integration owners opt in only after each dependency is
// present in the composition root.
type CompiledCapabilities struct {
	UpstreamHeaderHygiene      bool
	WebSocketSubprotocol       bool
	CredentialSessions         bool
	CredentialSubjectsResolved bool
	ContinuitySchema           bool
	ProtocolCatalog            bool
	Identity                   bool
	AppliedIdentity            bool
	ProviderCookieSchema       bool
}

type ReferencedKeyVersions struct {
	HMAC []string
	AEAD []string
}

// Requirements is the complete capability proof for one immutable snapshot.
// It deliberately carries the keyring by reference without exposing any
// serializable key material.
type Requirements struct {
	Compiled              CompiledCapabilities
	Keyring               *codexkeyring.Keyring
	ReferencedKeyVersions ReferencedKeyVersions
}

// Load reads every field before returning. A read or parse failure never yields
// a permissive partial/default snapshot.
func Load(ctx context.Context, reader ConfigReader) (Snapshot, error) {
	if reader == nil {
		return Snapshot{}, wrap(ErrorConfigUnavailable, "runtime_config", "reader", fmt.Errorf("config reader is required"))
	}
	values := make(map[string]string, len(featureRegistry))
	for _, spec := range featureRegistry {
		key := spec.definition.Key
		value, err := reader.GetConfig(ctx, key)
		if err != nil {
			return Snapshot{}, wrap(ErrorConfigUnavailable, key, "runtime_config", err)
		}
		values[key] = value
	}
	return Parse(values)
}

// Parse treats an absent/empty key as the ADR-defined false default while
// rejecting any non-empty value outside the existing runtime bool contract.
func Parse(values map[string]string) (Snapshot, error) {
	var snapshot Snapshot
	for _, spec := range featureRegistry {
		value, exists := values[spec.definition.Key]
		if !exists || value == "" {
			spec.set(&snapshot, spec.definition.Default)
			continue
		}
		enabled, err := parseBoolean(spec.definition.Key, value)
		if err != nil {
			return Snapshot{}, err
		}
		spec.set(&snapshot, enabled)
	}
	return snapshot, nil
}

// Validate rejects invalid combinations and unavailable integrations before a
// listener opens. referenced versions are checked only when a key-backed
// feature is enabled, preserving disabled deployments without a keyring.
func (snapshot Snapshot) Validate(
	compiled CompiledCapabilities,
	keyring *codexkeyring.Keyring,
	referenced ReferencedKeyVersions,
) error {
	return snapshot.ValidateRequirements(Requirements{
		Compiled:              compiled,
		Keyring:               keyring,
		ReferencedKeyVersions: referenced,
	})
}

func (snapshot Snapshot) ValidateRequirements(requirements Requirements) error {
	if err := snapshot.ValidateDependencies(); err != nil {
		return err
	}
	needHMAC := false
	needAEAD := false
	for _, spec := range featureRegistry {
		if !spec.enabled(snapshot) {
			continue
		}
		for _, capability := range spec.capabilities {
			if !capability.available(requirements.Compiled) {
				return missingCapability(spec.definition.Key, capability.name)
			}
		}
		needHMAC = needHMAC || spec.needHMAC
		needAEAD = needAEAD || spec.needAEAD
	}

	keyRequirements := codexkeyring.Requirements{
		NeedHMAC: needHMAC,
		NeedAEAD: needAEAD,
	}
	// A configured keyring must also prove it has every generation still
	// referenced by durable rows. With no keyring and every dependent feature
	// disabled, references remain dormant and the ADR explicitly permits startup.
	if requirements.Keyring != nil || keyRequirements.NeedHMAC {
		keyRequirements.HMACVersions = append([]string(nil), requirements.ReferencedKeyVersions.HMAC...)
	}
	if requirements.Keyring != nil || keyRequirements.NeedAEAD {
		keyRequirements.AEADVersions = append([]string(nil), requirements.ReferencedKeyVersions.AEAD...)
	}
	if err := codexkeyring.ValidateCapabilities(requirements.Keyring, keyRequirements); err != nil {
		return wrap(ErrorCapabilityMissing, "keyring", "cryptographic_keys", err)
	}
	return nil
}

func (snapshot Snapshot) ValidateDependencies() error {
	for _, spec := range featureRegistry {
		if !spec.enabled(snapshot) {
			continue
		}
		for _, dependency := range spec.definition.Requires {
			required, exists := specByFeature(dependency)
			if !exists || !required.enabled(snapshot) {
				capability := fmt.Sprint(dependency)
				if exists {
					capability = required.definition.Key
				}
				return wrap(ErrorDependency, spec.definition.Key, capability, nil)
			}
		}
	}
	return nil
}

func missingCapability(feature, capability string) error {
	return wrap(ErrorCapabilityMissing, feature, capability, nil)
}
