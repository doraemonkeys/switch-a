package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"slices"

	codexkeyring "github.com/doraemonkeys/switch-a/internal/codex/keyring"
	"github.com/doraemonkeys/switch-a/internal/store"

	"go.uber.org/zap"
)

var _ store.StaticCredentialSubjectSigner = (*codexkeyring.Keyring)(nil)

type applicationStartupPhase string

const (
	startupPhaseConfig              applicationStartupPhase = "config"
	startupPhaseLogger              applicationStartupPhase = "logger"
	startupPhaseDatabase            applicationStartupPhase = "database"
	startupPhaseDefaults            applicationStartupPhase = "defaults"
	startupPhaseInventory           applicationStartupPhase = "codex_inventory"
	startupPhaseKeyring             applicationStartupPhase = "codex_keyring"
	startupPhaseStaticFinalization  applicationStartupPhase = "codex_static_finalization"
	startupPhaseCodexPostcondition  applicationStartupPhase = "codex_postcondition"
	startupPhaseComposition         applicationStartupPhase = "composition"
	startupPhaseBackgroundOwners    applicationStartupPhase = "background_owners"
	startupPhaseListeners           applicationStartupPhase = "listeners"
	startupPhaseShutdownListeners   applicationStartupPhase = "shutdown_listeners"
	startupPhaseShutdownBackgrounds applicationStartupPhase = "shutdown_background_owners"
	startupPhaseShutdownStorage     applicationStartupPhase = "shutdown_storage"
)

type applicationLifecycleEvent struct {
	StartupID string
	Phase     applicationStartupPhase
	Component string
}

type applicationLifecycleRecorder interface {
	RecordApplicationLifecycle(applicationLifecycleEvent)
}

type applicationLifecycleRecorderFunc func(applicationLifecycleEvent)

func (f applicationLifecycleRecorderFunc) RecordApplicationLifecycle(event applicationLifecycleEvent) {
	if f != nil {
		f(event)
	}
}

func recordApplicationLifecycle(recorder applicationLifecycleRecorder, startupID string, phase applicationStartupPhase) {
	recordApplicationComponent(recorder, startupID, phase, "")
}

func recordApplicationComponent(recorder applicationLifecycleRecorder, startupID string, phase applicationStartupPhase, component string) {
	if recorder != nil {
		recorder.RecordApplicationLifecycle(applicationLifecycleEvent{StartupID: startupID, Phase: phase, Component: component})
	}
}

type applicationCodexPersistence interface {
	InspectCodexPersistence(context.Context) (store.CodexPersistenceInventory, error)
	FinalizeStaticCredentialSubjects(context.Context, store.StaticCredentialSubjectSigner) error
}

type applicationCodexSecurity struct {
	keyring       *codexkeyring.Keyring
	resolvedPath  string
	fileSource    codexkeyring.FileSource
	preflight     store.CodexPersistenceInventory
	postcondition store.CodexPersistenceInventory
}

func resolveCodexKeyringPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("resolve Codex keyring path: path is required")
	}
	resolved, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve Codex keyring path %q: %w", path, err)
	}
	return filepath.Clean(resolved), nil
}

func bootstrapApplicationCodexSecurity(
	ctx context.Context,
	startupID string,
	path string,
	persistence applicationCodexPersistence,
	files codexkeyring.FileStore,
	random io.Reader,
	log *zap.Logger,
	recorder applicationLifecycleRecorder,
) (*applicationCodexSecurity, error) {
	if ctx == nil || persistence == nil || files == nil || random == nil || log == nil {
		return nil, fmt.Errorf("bootstrap Codex security: context, persistence, file store, random source, and logger are required")
	}
	resolvedPath, err := resolveCodexKeyringPath(path)
	if err != nil {
		logCodexStartupFailure(log, startupID, startupPhaseKeyring, err)
		return nil, err
	}

	inventory, err := persistence.InspectCodexPersistence(ctx)
	if err != nil {
		wrapped := fmt.Errorf("inspect Codex persistence inventory: %w", err)
		logCodexStartupFailure(log, startupID, startupPhaseInventory, wrapped)
		return nil, wrapped
	}
	recordApplicationLifecycle(recorder, startupID, startupPhaseInventory)
	logCodexInventory(log, startupID, startupPhaseInventory, inventory)

	historical := codexkeyring.HistoricalVersions{
		HMAC: mergeRequiredVersions(
			inventory.CredentialHMACVersions,
			inventory.ClientIdentityHMACVersions,
			inventory.ContinuityHMACVersions,
			inventory.ProviderCookieHMACVersions,
		),
		AEAD: mergeRequiredVersions(inventory.ProviderCookieAEADVersions),
	}
	portable, portableErr := loadApplicationPortableHMAC(ctx, persistence)
	if portableErr != nil {
		return nil, portableErr
	}
	historical.HMAC = fileRequiredHMAC(historical.HMAC, portable)
	loaded, err := codexkeyring.LoadOrCreateFileWithStore(files, resolvedPath, historical, random)
	if err != nil {
		if diagnostic := diagnoseCodexInventoryCoverage(files, resolvedPath, random, inventory, err); diagnostic != nil {
			err = diagnostic
		}
		wrapped := fmt.Errorf("load or create Codex keyring: %w", err)
		logCodexStartupFailure(log, startupID, startupPhaseKeyring, wrapped)
		return nil, wrapped
	}
	if err := loaded.Keyring.WithHMACImport(portable, nil); err != nil {
		return nil, err
	}
	if installer, ok := persistence.(interface {
		InstallCodexKeyring(context.Context, *codexkeyring.Keyring) error
	}); ok {
		if err := installer.InstallCodexKeyring(ctx, loaded.Keyring); err != nil {
			return nil, err
		}
	}
	if err := validateCodexInventoryCoverage(loaded.Keyring, inventory); err != nil {
		logCodexStartupFailure(log, startupID, startupPhaseKeyring, err)
		return nil, err
	}
	recordApplicationLifecycle(recorder, startupID, startupPhaseKeyring)
	capabilities := loaded.Keyring.Capabilities()
	log.Info("codex.startup_phase",
		zap.String("startup_id", startupID),
		zap.String("phase", string(startupPhaseKeyring)),
		zap.String("resolved_path", resolvedPath),
		zap.String("file_source", string(loaded.Source)),
		zap.String("hmac_current_key_version", capabilities.HMACCurrent),
		zap.String("aead_current_key_version", capabilities.AEADCurrent),
		zap.Int("hmac_key_version_count", len(capabilities.HMACVersions)),
		zap.Int("aead_key_version_count", len(capabilities.AEADVersions)),
	)

	if err := persistence.FinalizeStaticCredentialSubjects(ctx, loaded.Keyring); err != nil {
		wrapped := fmt.Errorf("finalize static credential subjects: %w", err)
		logCodexStartupFailure(log, startupID, startupPhaseStaticFinalization, wrapped)
		return nil, wrapped
	}
	recordApplicationLifecycle(recorder, startupID, startupPhaseStaticFinalization)
	log.Info("codex.startup_phase",
		zap.String("startup_id", startupID),
		zap.String("phase", string(startupPhaseStaticFinalization)),
		zap.Int("finalized_static_subject_count", inventory.PendingStaticCredentialSubjectCount()),
		zap.Int("chatgpt_reauth_pending_count", inventory.PendingChatGPTReauthSubjectCount()),
	)

	postcondition, err := persistence.InspectCodexPersistence(ctx)
	if err != nil {
		wrapped := fmt.Errorf("re-inspect Codex persistence after static finalization: %w", err)
		logCodexStartupFailure(log, startupID, startupPhaseCodexPostcondition, wrapped)
		return nil, wrapped
	}
	if postcondition.PendingStaticCredentialSubjectCount() != 0 {
		err := fmt.Errorf("codex startup postcondition: %d pending static credential subjects remain", postcondition.PendingStaticCredentialSubjectCount())
		logCodexStartupFailure(log, startupID, startupPhaseCodexPostcondition, err)
		return nil, err
	}
	if err := validateCodexInventoryCoverage(loaded.Keyring, postcondition); err != nil {
		logCodexStartupFailure(log, startupID, startupPhaseCodexPostcondition, err)
		return nil, err
	}
	recordApplicationLifecycle(recorder, startupID, startupPhaseCodexPostcondition)
	logCodexInventory(log, startupID, startupPhaseCodexPostcondition, postcondition)

	return &applicationCodexSecurity{
		keyring: loaded.Keyring, resolvedPath: resolvedPath, fileSource: loaded.Source,
		preflight: inventory, postcondition: postcondition,
	}, nil
}

func diagnoseCodexInventoryCoverage(
	files codexkeyring.FileStore,
	path string,
	random io.Reader,
	inventory store.CodexPersistenceInventory,
	lifecycleErr error,
) error {
	serialized, readErr := files.ReadFile(path)
	if readErr != nil {
		if !errors.Is(readErr, fs.ErrNotExist) {
			return nil
		}
		for _, family := range codexHistoryFamilies(inventory) {
			if len(family.hmac) != 0 || len(family.aead) != 0 {
				return fmt.Errorf("codex keyring history family %s requires durable versions: %w", family.name, lifecycleErr)
			}
		}
		return nil
	}
	defer clear(serialized)
	parsed, parseErr := codexkeyring.Parse(serialized, random)
	if parseErr != nil {
		return nil
	}
	if err := validateCodexInventoryCoverage(parsed, inventory); err != nil {
		return errors.Join(err, lifecycleErr)
	}
	return nil
}

func bootstrapApplicationCodexSecurityWithOS(
	ctx context.Context,
	startupID string,
	path string,
	persistence applicationCodexPersistence,
	log *zap.Logger,
	recorder applicationLifecycleRecorder,
) (*applicationCodexSecurity, error) {
	return bootstrapApplicationCodexSecurity(
		ctx, startupID, path, persistence, codexkeyring.OSFileStore{}, rand.Reader, log, recorder,
	)
}

func validateCodexInventoryCoverage(keyring *codexkeyring.Keyring, inventory store.CodexPersistenceInventory) error {
	if err := codexkeyring.ValidateCapabilities(keyring, codexkeyring.Requirements{NeedHMAC: true, NeedAEAD: true}); err != nil {
		return fmt.Errorf("validate current Codex keyring capabilities: %w", err)
	}
	for _, family := range codexHistoryFamilies(inventory) {
		if err := codexkeyring.ValidateCapabilities(keyring, codexkeyring.Requirements{
			HMACVersions: family.hmac,
			AEADVersions: family.aead,
		}); err != nil {
			return fmt.Errorf("validate Codex keyring history family %s: %w", family.name, err)
		}
	}
	return nil
}

type codexHistoryFamily struct {
	name       string
	hmac, aead []string
}

func codexHistoryFamilies(inventory store.CodexPersistenceInventory) []codexHistoryFamily {
	return []codexHistoryFamily{
		{name: "credential_subject_hmac", hmac: inventory.CredentialHMACVersions},
		{name: "client_identity_hmac", hmac: inventory.ClientIdentityHMACVersions},
		{name: "continuity_hmac", hmac: inventory.ContinuityHMACVersions},
		{name: "provider_cookie_hmac", hmac: inventory.ProviderCookieHMACVersions},
		{name: "provider_cookie_aead", aead: inventory.ProviderCookieAEADVersions},
	}
}

func mergeRequiredVersions(groups ...[]string) []string {
	unique := make(map[string]struct{})
	for _, group := range groups {
		for _, version := range group {
			// Empty durable generations must remain visible to strict keyring
			// validation instead of being mistaken for an empty history.
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

func logCodexInventory(log *zap.Logger, startupID string, phase applicationStartupPhase, inventory store.CodexPersistenceInventory) {
	log.Info("codex.startup_phase",
		zap.String("startup_id", startupID),
		zap.String("phase", string(phase)),
		zap.Int("credential_subject_count", len(inventory.CredentialSubjects)),
		zap.Int("credential_hmac_version_count", len(inventory.CredentialHMACVersions)),
		zap.Int("continuity_hmac_version_count", len(inventory.ContinuityHMACVersions)),
		zap.Int("provider_cookie_hmac_version_count", len(inventory.ProviderCookieHMACVersions)),
		zap.Int("provider_cookie_aead_version_count", len(inventory.ProviderCookieAEADVersions)),
		zap.Int("pending_static_subject_count", inventory.PendingStaticCredentialSubjectCount()),
		zap.Int("chatgpt_reauth_pending_count", inventory.PendingChatGPTReauthSubjectCount()),
	)
}

func logCodexStartupFailure(log *zap.Logger, startupID string, phase applicationStartupPhase, err error) {
	if log == nil {
		return
	}
	log.Error("codex.startup_failed",
		zap.String("startup_id", startupID),
		zap.String("failure_phase", string(phase)),
		zap.Error(err),
	)
}
