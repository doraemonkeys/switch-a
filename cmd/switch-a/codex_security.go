package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"slices"
	"sync/atomic"

	"github.com/doraemonkeys/switch-a/internal/codex/keyring"
	"github.com/doraemonkeys/switch-a/internal/codex/startup"
	"github.com/doraemonkeys/switch-a/internal/config"
	"github.com/doraemonkeys/switch-a/internal/store"

	"go.uber.org/zap"
)

type startupFileReader func(path string) ([]byte, error)

var _ store.StaticCredentialSubjectSigner = (*codexkeyring.Keyring)(nil)

type applicationCodexCapabilityReader interface {
	RequiredCredentialSubjectKeyVersions(ctx context.Context) ([]string, error)
	CredentialSubjectsResolved(ctx context.Context) (bool, error)
	InspectCodexPersistence(ctx context.Context) (store.CodexKeyVersions, error)
}

type applicationCodexSecurity struct {
	keyring *codexkeyring.Keyring
}

type applicationCodexFeatureValidator struct {
	capabilities applicationCodexCapabilityReader
	security     *applicationCodexSecurity
}

func newApplicationCodexFeatureValidator(
	capabilities applicationCodexCapabilityReader,
	security *applicationCodexSecurity,
) *applicationCodexFeatureValidator {
	return &applicationCodexFeatureValidator{
		capabilities: capabilities,
		security:     security,
	}
}

// applicationCodexFeatureController is the single atomic publication point for
// runtime feature state. HTTP requests and WebSocket sessions capture one value
// from Snapshot; admin mutations publish only after durable persistence.
type applicationCodexFeatureController struct {
	validator *applicationCodexFeatureValidator
	snapshot  atomic.Pointer[codexstartup.Snapshot]
}

func newApplicationCodexFeatureController(
	initial codexstartup.Snapshot,
	capabilities applicationCodexCapabilityReader,
	security *applicationCodexSecurity,
) *applicationCodexFeatureController {
	controller := &applicationCodexFeatureController{
		validator: newApplicationCodexFeatureValidator(capabilities, security),
	}
	controller.snapshot.Store(&initial)
	return controller
}

func (controller *applicationCodexFeatureController) Snapshot() codexstartup.Snapshot {
	if controller == nil {
		return codexstartup.Snapshot{}
	}
	snapshot := controller.snapshot.Load()
	if snapshot == nil {
		return codexstartup.Snapshot{}
	}
	return *snapshot
}

func (controller *applicationCodexFeatureController) ValidateCodexFeatures(
	ctx context.Context,
	snapshot codexstartup.Snapshot,
) error {
	if controller == nil || controller.validator == nil {
		return fmt.Errorf("validate Codex features: feature controller is unavailable")
	}
	return controller.validator.ValidateCodexFeatures(ctx, snapshot)
}

func (controller *applicationCodexFeatureController) PublishCodexFeatures(snapshot codexstartup.Snapshot) error {
	if controller == nil {
		return fmt.Errorf("publish Codex features: feature controller is unavailable")
	}
	controller.snapshot.Store(&snapshot)
	return nil
}

func loadApplicationConfigAndCodexSecurity() (*config.Config, *applicationCodexSecurity, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	security, err := loadApplicationCodexSecurity(cfg.CodexKeyringFile, os.ReadFile, rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	return cfg, security, nil
}

func (security *applicationCodexSecurity) staticSubjectSigners() []store.StaticCredentialSubjectSigner {
	if security == nil || security.keyring == nil {
		return nil
	}
	return []store.StaticCredentialSubjectSigner{security.keyring}
}

// loadApplicationCodexSecurity is the only file-to-secret boundary. The
// keyring module receives bytes and randomness by injection and never reaches
// into startup configuration or the environment itself.
func loadApplicationCodexSecurity(
	path string,
	readFile startupFileReader,
	random io.Reader,
) (*applicationCodexSecurity, error) {
	if path == "" {
		return &applicationCodexSecurity{}, nil
	}
	if readFile == nil {
		return nil, fmt.Errorf("load Codex keyring: file reader is required")
	}
	document, err := readFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Codex keyring file: %w", err)
	}
	defer clear(document)
	keyring, err := codexkeyring.Parse(document, random)
	if err != nil {
		return nil, fmt.Errorf("parse Codex keyring file: %w", err)
	}
	return &applicationCodexSecurity{keyring: keyring}, nil
}

func loadAndValidateCodexStartup(
	ctx context.Context,
	reader codexstartup.ConfigReader,
	capabilities applicationCodexCapabilityReader,
	security *applicationCodexSecurity,
) (codexstartup.Snapshot, error) {
	snapshot, err := codexstartup.Load(ctx, reader)
	if err != nil {
		return codexstartup.Snapshot{}, fmt.Errorf("load Codex startup feature snapshot: %w", err)
	}
	validator := newApplicationCodexFeatureValidator(capabilities, security)
	if err := validator.ValidateCodexFeatures(ctx, snapshot); err != nil {
		return codexstartup.Snapshot{}, err
	}
	return snapshot, nil
}

func (validator applicationCodexFeatureValidator) ValidateCodexFeatures(
	ctx context.Context,
	snapshot codexstartup.Snapshot,
) error {
	var keyring *codexkeyring.Keyring
	if validator.security != nil {
		keyring = validator.security.keyring
	}
	compiled := currentCodexCompiledCapabilities()
	referencedVersions := codexstartup.ReferencedKeyVersions{}
	needsKeyBackedRuntime := snapshot.Continuity || snapshot.ProviderCookieJar
	if needsKeyBackedRuntime {
		if validator.capabilities == nil {
			return fmt.Errorf("validate Codex startup capabilities: capability reader is required")
		}
		resolved, err := validator.capabilities.CredentialSubjectsResolved(ctx)
		if err != nil {
			return fmt.Errorf("inspect unresolved credential subjects: %w", err)
		}
		compiled.CredentialSubjectsResolved = resolved
	}
	if keyring != nil || needsKeyBackedRuntime {
		if validator.capabilities == nil {
			return fmt.Errorf("validate Codex startup capabilities: capability reader is required")
		}
		credentialVersions, err := validator.capabilities.RequiredCredentialSubjectKeyVersions(ctx)
		if err != nil {
			return fmt.Errorf("inspect credential subject key versions: %w", err)
		}
		persistenceVersions, err := validator.capabilities.InspectCodexPersistence(ctx)
		if err != nil {
			return fmt.Errorf("inspect Codex persistence capabilities: %w", err)
		}
		compiled.ContinuitySchema = true
		compiled.ProviderCookieSchema = true
		referencedVersions.HMAC = mergeRequiredVersions(credentialVersions, persistenceVersions.HMAC)
		referencedVersions.AEAD = mergeRequiredVersions(persistenceVersions.AEAD)
	}
	// Integration owners turn on individual compiled capabilities only after the
	// corresponding schema and protocol boundaries are injected here. Until then,
	// a manually persisted true flag must stop startup rather than expose a no-op.
	if err := snapshot.ValidateRequirements(codexstartup.Requirements{
		Compiled:              compiled,
		Keyring:               keyring,
		ReferencedKeyVersions: referencedVersions,
	}); err != nil {
		return fmt.Errorf("validate Codex startup capabilities: %w", err)
	}
	return nil
}

func currentCodexCompiledCapabilities() codexstartup.CompiledCapabilities {
	return codexstartup.CompiledCapabilities{
		UpstreamHeaderHygiene: true,
		WebSocketSubprotocol:  true,
		CredentialSessions:    true,
		ProtocolCatalog:       true,
		Identity:              true,
		AppliedIdentity:       true,
	}
}

func mergeRequiredVersions(groups ...[]string) []string {
	unique := make(map[string]struct{})
	for _, group := range groups {
		for _, version := range group {
			// Preserve an empty durable generation: keyring validation treats it as
			// corruption, while dropping it here would turn corruption into success.
			unique[version] = struct{}{}
		}
	}
	result := make([]string, 0, len(unique))
	for version := range unique {
		result = append(result, version)
	}
	slices.Sort(result)
	return result
}

func validateAndLogCodexStartup(
	ctx context.Context,
	configReader codexstartup.ConfigReader,
	capabilities applicationCodexCapabilityReader,
	security *applicationCodexSecurity,
	log *zap.Logger,
) (codexstartup.Snapshot, error) {
	snapshot, err := loadAndValidateCodexStartup(ctx, configReader, capabilities, security)
	if err != nil {
		return codexstartup.Snapshot{}, err
	}
	logCodexStartupValidated(log, security, snapshot)
	return snapshot, nil
}

func logCodexStartupValidated(
	log *zap.Logger,
	security *applicationCodexSecurity,
	snapshot codexstartup.Snapshot,
) {
	var capabilities codexkeyring.Capabilities
	if security != nil && security.keyring != nil {
		capabilities = security.keyring.Capabilities()
	}
	log.Info("validated Codex startup security capabilities",
		zap.Bool("upstream_header_hygiene_enabled", snapshot.UpstreamHeaderHygiene),
		zap.Bool("websocket_subprotocol_enabled", snapshot.WebSocketSubprotocol),
		zap.Bool("continuity_enabled", snapshot.Continuity),
		zap.Bool("provider_cookie_jar_enabled", snapshot.ProviderCookieJar),
		zap.Bool("keyring_loaded", capabilities.HMACCurrent != "" || capabilities.AEADCurrent != ""),
		zap.String("hmac_current_key_version", capabilities.HMACCurrent),
		zap.String("aead_current_key_version", capabilities.AEADCurrent),
		zap.Int("hmac_key_version_count", len(capabilities.HMACVersions)),
		zap.Int("aead_key_version_count", len(capabilities.AEADVersions)),
	)
}
