package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/admin"
	"github.com/doraemonkeys/switch-a/internal/codex/startup"
	"github.com/doraemonkeys/switch-a/internal/config"
	"github.com/doraemonkeys/switch-a/internal/store"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestLoadApplicationCodexSecurityDisabledDoesNotReadFile(t *testing.T) {
	called := false
	security, err := loadApplicationCodexSecurity("", func(string) ([]byte, error) {
		called = true
		return nil, errors.New("must not be called")
	}, nil)
	if err != nil || security == nil || security.keyring != nil {
		t.Fatalf("loadApplicationCodexSecurity() = %+v, %v", security, err)
	}
	if called {
		t.Fatal("disabled keyring attempted to read a file")
	}
}

func TestLoadApplicationConfigAndCodexSecurityUsesStartupOnlyKeyringPath(t *testing.T) {
	t.Setenv(config.EnvAdminToken, "test-token")
	t.Setenv(config.EnvCodexKeyringFile, "")
	cfg, security, err := loadApplicationConfigAndCodexSecurity()
	if err != nil {
		t.Fatalf("loadApplicationConfigAndCodexSecurity() error = %v", err)
	}
	if cfg == nil || security == nil || security.keyring != nil {
		t.Fatalf("config=%+v security=%+v", cfg, security)
	}
}

func TestLoadApplicationCodexSecurityParsesInjectedDocument(t *testing.T) {
	document := applicationKeyringDocument()
	security, err := loadApplicationCodexSecurity(
		"/secret/keyring.json",
		func(path string) ([]byte, error) {
			if path != "/secret/keyring.json" {
				t.Fatalf("path = %q", path)
			}
			return []byte(document), nil
		},
		bytes.NewReader(bytes.Repeat([]byte{1}, 64)),
	)
	if err != nil {
		t.Fatalf("loadApplicationCodexSecurity() error = %v", err)
	}
	capabilities := security.keyring.Capabilities()
	if capabilities.HMACCurrent != "h2" || capabilities.AEADCurrent != "a2" {
		t.Fatalf("capabilities = %+v", capabilities)
	}
}

func TestLoadApplicationCodexSecurityFailsClosed(t *testing.T) {
	readFailure := errors.New("permission denied")
	tests := []struct {
		name     string
		readFile startupFileReader
		random   io.Reader
		contains string
	}{
		{name: "missing reader", contains: "file reader is required"},
		{name: "read failure", readFile: func(string) ([]byte, error) { return nil, readFailure }, random: bytes.NewReader(nil), contains: "read Codex keyring file"},
		{name: "invalid document", readFile: func(string) ([]byte, error) { return []byte(`{"schema_version":99}`), nil }, random: bytes.NewReader(nil), contains: "parse Codex keyring file"},
		{name: "missing random", readFile: func(string) ([]byte, error) { return []byte(applicationKeyringDocument()), nil }, contains: "random source is required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := loadApplicationCodexSecurity("/secret/keyring.json", test.readFile, test.random)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("error = %v, want %q", err, test.contains)
			}
			if strings.Contains(err.Error(), applicationKeyMaterial(1)) {
				t.Fatalf("error exposes root key material: %v", err)
			}
		})
	}
}

func TestLoadAndValidateCodexStartupUsesPersistedSnapshot(t *testing.T) {
	reader := &applicationConfigReader{values: map[string]string{}}
	snapshot, err := loadAndValidateCodexStartup(context.Background(), reader, nil, nil)
	if err != nil || snapshot != (codexstartup.Snapshot{}) {
		t.Fatalf("loadAndValidateCodexStartup() = %+v, %v", snapshot, err)
	}
	if len(reader.keys) != len(codexstartup.Keys()) {
		t.Fatalf("read %d keys, want %d", len(reader.keys), len(codexstartup.Keys()))
	}

	reader = &applicationConfigReader{values: map[string]string{
		codexstartup.KeyUpstreamHeaderHygiene: "true",
		codexstartup.KeyContinuity:            "true",
	}}
	credentialSubjects := &applicationCredentialSubjects{resolved: true}
	if _, err := loadAndValidateCodexStartup(context.Background(), reader, credentialSubjects, nil); !codexstartup.IsError(err, codexstartup.ErrorCapabilityMissing) {
		t.Fatalf("enabled unavailable feature error = %v", err)
	}
	if credentialSubjects.resolvedCalls != 1 || credentialSubjects.versionCalls != 1 {
		t.Fatalf("credential subject preflight calls = resolved:%d versions:%d", credentialSubjects.resolvedCalls, credentialSubjects.versionCalls)
	}

	reader = &applicationConfigReader{err: errors.New("database unavailable")}
	if _, err := loadAndValidateCodexStartup(context.Background(), reader, nil, nil); !codexstartup.IsError(err, codexstartup.ErrorConfigUnavailable) {
		t.Fatalf("config failure error = %v", err)
	}
}

func TestLoadAndValidateCodexStartupCoversFeatureAndKeyringMatrix(t *testing.T) {
	security, err := loadApplicationCodexSecurity(
		"keyring.json",
		func(string) ([]byte, error) { return []byte(applicationKeyringDocument()), nil },
		bytes.NewReader(bytes.Repeat([]byte{5}, 64)),
	)
	if err != nil {
		t.Fatal(err)
	}
	for bits := 0; bits < 16; bits++ {
		for _, keyringConfigured := range []bool{false, true} {
			name := fmt.Sprintf("features_%04b_keyring_%t", bits, keyringConfigured)
			t.Run(name, func(t *testing.T) {
				reader := &applicationConfigReader{values: map[string]string{
					codexstartup.KeyUpstreamHeaderHygiene: fmt.Sprint(bits&1 != 0),
					codexstartup.KeyWebSocketSubprotocol:  fmt.Sprint(bits&2 != 0),
					codexstartup.KeyContinuity:            fmt.Sprint(bits&4 != 0),
					codexstartup.KeyProviderCookieJar:     fmt.Sprint(bits&8 != 0),
				}}
				capabilities := &applicationCredentialSubjects{resolved: true}
				var configured *applicationCodexSecurity
				if keyringConfigured {
					configured = security
				}
				_, err := loadAndValidateCodexStartup(context.Background(), reader, capabilities, configured)
				keyBacked := bits&4 != 0 || bits&8 != 0
				invalidDependency := keyBacked && bits&1 == 0
				switch {
				case invalidDependency:
					if !codexstartup.IsError(err, codexstartup.ErrorDependency) {
						t.Fatalf("dependency error = %v", err)
					}
				case keyBacked && !keyringConfigured:
					if !codexstartup.IsError(err, codexstartup.ErrorCapabilityMissing) {
						t.Fatalf("missing keyring error = %v", err)
					}
				default:
					if err != nil {
						t.Fatalf("valid combination error = %v", err)
					}
				}
			})
		}
	}
}

func TestLoadAndValidateCodexStartupFailsOnCredentialSubjectPreflight(t *testing.T) {
	reader := &applicationConfigReader{values: map[string]string{
		codexstartup.KeyUpstreamHeaderHygiene: "true",
		codexstartup.KeyContinuity:            "true",
	}}
	if _, err := loadAndValidateCodexStartup(context.Background(), reader, nil, nil); err == nil {
		t.Fatal("missing credential subject reader did not fail")
	}
	wantErr := errors.New("preflight unavailable")
	for _, credentials := range []*applicationCredentialSubjects{
		{resolvedErr: wantErr},
		{resolved: true, versionsErr: wantErr},
	} {
		if _, err := loadAndValidateCodexStartup(context.Background(), reader, credentials, nil); !errors.Is(err, wantErr) {
			t.Fatalf("preflight error = %v, want %v", err, wantErr)
		}
	}
}

func TestLoadAndValidateCodexStartupPreflightsConfiguredKeyringWhileFeaturesAreDisabled(t *testing.T) {
	security, err := loadApplicationCodexSecurity(
		"keyring.json",
		func(string) ([]byte, error) { return []byte(applicationKeyringDocument()), nil },
		bytes.NewReader(bytes.Repeat([]byte{3}, 64)),
	)
	if err != nil {
		t.Fatalf("loadApplicationCodexSecurity() error = %v", err)
	}
	reader := &applicationConfigReader{values: map[string]string{}}
	credentialSubjects := &applicationCredentialSubjects{versions: []string{"h1"}}
	if _, err := loadAndValidateCodexStartup(context.Background(), reader, credentialSubjects, security); err != nil {
		t.Fatalf("legacy version preflight error = %v", err)
	}
	if credentialSubjects.versionCalls != 1 || credentialSubjects.resolvedCalls != 0 || credentialSubjects.persistenceCalls != 1 {
		t.Fatalf("disabled preflight calls = resolved:%d versions:%d persistence:%d", credentialSubjects.resolvedCalls, credentialSubjects.versionCalls, credentialSubjects.persistenceCalls)
	}

	credentialSubjects = &applicationCredentialSubjects{versions: []string{"h9"}}
	if _, err := loadAndValidateCodexStartup(context.Background(), reader, credentialSubjects, security); !codexstartup.IsError(err, codexstartup.ErrorCapabilityMissing) {
		t.Fatalf("missing referenced legacy version error = %v", err)
	}
}

func TestLoadAndValidateCodexStartupAggregatesAllPersistedKeyVersions(t *testing.T) {
	security, err := loadApplicationCodexSecurity(
		"keyring.json",
		func(string) ([]byte, error) { return []byte(applicationKeyringDocument()), nil },
		bytes.NewReader(bytes.Repeat([]byte{4}, 64)),
	)
	if err != nil {
		t.Fatalf("loadApplicationCodexSecurity() error = %v", err)
	}
	reader := &applicationConfigReader{values: map[string]string{
		codexstartup.KeyUpstreamHeaderHygiene: "true",
		codexstartup.KeyContinuity:            "true",
		codexstartup.KeyProviderCookieJar:     "true",
	}}
	capabilities := &applicationCredentialSubjects{
		versions: []string{"h1"},
		resolved: true,
		persistence: store.CodexKeyVersions{
			HMAC: []string{"h2", "h1", "h2"},
			AEAD: []string{"a1"},
		},
	}
	if _, err := loadAndValidateCodexStartup(context.Background(), reader, capabilities, security); err != nil {
		t.Fatalf("aggregated preflight error = %v", err)
	}
	if capabilities.resolvedCalls != 1 || capabilities.versionCalls != 1 || capabilities.persistenceCalls != 1 {
		t.Fatalf("preflight calls = resolved:%d versions:%d persistence:%d", capabilities.resolvedCalls, capabilities.versionCalls, capabilities.persistenceCalls)
	}

	capabilities.persistence.AEAD = []string{"a9"}
	if _, err := loadAndValidateCodexStartup(context.Background(), reader, capabilities, security); !codexstartup.IsError(err, codexstartup.ErrorCapabilityMissing) {
		t.Fatalf("missing persisted AEAD generation error = %v", err)
	}
	capabilities.persistence.AEAD = []string{""}
	if _, err := loadAndValidateCodexStartup(context.Background(), reader, capabilities, security); !codexstartup.IsError(err, codexstartup.ErrorCapabilityMissing) {
		t.Fatalf("empty persisted AEAD generation error = %v", err)
	}
}

func TestLoadAndValidateCodexStartupFailsClosedOnPersistenceInspection(t *testing.T) {
	wantErr := errors.New("persistence unavailable")
	reader := &applicationConfigReader{values: map[string]string{
		codexstartup.KeyUpstreamHeaderHygiene: "true",
		codexstartup.KeyContinuity:            "true",
	}}
	capabilities := &applicationCredentialSubjects{resolved: true, persistenceErr: wantErr}
	if _, err := loadAndValidateCodexStartup(context.Background(), reader, capabilities, nil); !errors.Is(err, wantErr) {
		t.Fatalf("persistence inspection error = %v, want %v", err, wantErr)
	}
}

func TestApplicationCodexFeatureControllerPublishesWholeSnapshots(t *testing.T) {
	initial := codexstartup.Snapshot{UpstreamHeaderHygiene: true}
	controller := newApplicationCodexFeatureController(initial, &applicationCredentialSubjects{}, nil)
	if got := controller.Snapshot(); got != initial {
		t.Fatalf("initial snapshot = %+v, want %+v", got, initial)
	}
	next := codexstartup.Snapshot{UpstreamHeaderHygiene: true, WebSocketSubprotocol: true}
	if err := controller.PublishCodexFeatures(next); err != nil {
		t.Fatalf("PublishCodexFeatures() error = %v", err)
	}
	if got := controller.Snapshot(); got != next {
		t.Fatalf("published snapshot = %+v, want %+v", got, next)
	}
	if err := (*applicationCodexFeatureController)(nil).PublishCodexFeatures(next); err == nil {
		t.Fatal("nil feature controller published a snapshot")
	}
	if got := (&applicationCodexFeatureController{}).Snapshot(); got != (codexstartup.Snapshot{}) {
		t.Fatalf("zero-value controller snapshot = %+v", got)
	}
	if got := (*applicationCodexFeatureController)(nil).Snapshot(); got != (codexstartup.Snapshot{}) {
		t.Fatalf("nil controller snapshot = %+v", got)
	}
	if err := (*applicationCodexFeatureController)(nil).ValidateCodexFeatures(context.Background(), next); err == nil {
		t.Fatal("nil feature controller validated a snapshot")
	}
	if err := controller.ValidateCodexFeatures(context.Background(), next); err != nil {
		t.Fatalf("controller ValidateCodexFeatures() error = %v", err)
	}
}

func TestApplicationCodexFeatureControllerConcurrentSnapshotsStayCoherent(t *testing.T) {
	first := codexstartup.Snapshot{UpstreamHeaderHygiene: true, Continuity: true}
	second := codexstartup.Snapshot{UpstreamHeaderHygiene: true, WebSocketSubprotocol: true, ProviderCookieJar: true}
	controller := newApplicationCodexFeatureController(first, &applicationCredentialSubjects{}, nil)
	var workers sync.WaitGroup
	for index := 0; index < 8; index++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			for iteration := 0; iteration < 100; iteration++ {
				if index%2 == 0 {
					_ = controller.PublishCodexFeatures(first)
				} else {
					_ = controller.PublishCodexFeatures(second)
				}
				got := controller.Snapshot()
				if got != first && got != second {
					t.Errorf("torn feature snapshot = %+v", got)
					return
				}
			}
		}(index)
	}
	workers.Wait()
}

func TestValidateAndLogCodexStartupReturnsPublishedSnapshot(t *testing.T) {
	reader := &applicationConfigReader{values: map[string]string{
		codexstartup.KeyUpstreamHeaderHygiene: "true",
	}}
	snapshot, err := validateAndLogCodexStartup(
		context.Background(), reader, nil, nil, zap.NewNop(),
	)
	if err != nil || !snapshot.UpstreamHeaderHygiene {
		t.Fatalf("validateAndLogCodexStartup() = %+v, %v", snapshot, err)
	}
	reader.err = errors.New("config unavailable")
	if _, err := validateAndLogCodexStartup(context.Background(), reader, nil, nil, zap.NewNop()); err == nil {
		t.Fatal("validateAndLogCodexStartup() accepted an unavailable config snapshot")
	}
}

func TestApplicationCodexSecurityStaticSubjectSigners(t *testing.T) {
	if signers := (*applicationCodexSecurity)(nil).staticSubjectSigners(); len(signers) != 0 {
		t.Fatalf("nil security signers = %+v", signers)
	}
	if signers := (&applicationCodexSecurity{}).staticSubjectSigners(); len(signers) != 0 {
		t.Fatalf("empty security signers = %+v", signers)
	}
	security, err := loadApplicationCodexSecurity(
		"keyring.json",
		func(string) ([]byte, error) { return []byte(applicationKeyringDocument()), nil },
		bytes.NewReader(bytes.Repeat([]byte{6}, 64)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if signers := security.staticSubjectSigners(); len(signers) != 1 || signers[0] != security.keyring {
		t.Fatalf("loaded security signers = %+v", signers)
	}
}

func TestLogCodexStartupValidatedContainsOnlyNonSecretCapabilities(t *testing.T) {
	security, err := loadApplicationCodexSecurity(
		"keyring.json",
		func(string) ([]byte, error) { return []byte(applicationKeyringDocument()), nil },
		bytes.NewReader(bytes.Repeat([]byte{2}, 64)),
	)
	if err != nil {
		t.Fatalf("loadApplicationCodexSecurity() error = %v", err)
	}
	core, observed := observer.New(zapcore.InfoLevel)
	logCodexStartupValidated(zap.New(core), security, codexstartup.Snapshot{})
	entries := observed.All()
	if len(entries) != 1 {
		t.Fatalf("log entries = %d", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["keyring_loaded"] != true ||
		fields["hmac_current_key_version"] != "h2" ||
		fields["aead_current_key_version"] != "a2" ||
		fields["hmac_key_version_count"] != int64(2) ||
		fields["aead_key_version_count"] != int64(2) {
		t.Fatalf("fields = %+v", fields)
	}
	encoded := entries[0].Message
	for _, field := range entries[0].Context {
		encoded += field.String
	}
	if strings.Contains(encoded, applicationKeyMaterial(1)) {
		t.Fatal("structured log contains root key material")
	}

	core, observed = observer.New(zapcore.InfoLevel)
	logCodexStartupValidated(zap.New(core), nil, codexstartup.Snapshot{})
	if got := observed.All()[0].ContextMap()["keyring_loaded"]; got != false {
		t.Fatalf("nil security keyring_loaded = %v", got)
	}
}

func TestCodexKeyringPathIsNotRuntimeOrAdminConfig(t *testing.T) {
	if _, exists := store.GetDefaultConfigs()[config.KeyCodexKeyringFile]; exists {
		t.Fatalf("%q must not enter RuntimeConfig defaults", config.KeyCodexKeyringFile)
	}
	if admin.IsValidConfigKey(config.KeyCodexKeyringFile) {
		t.Fatalf("%q must not be accepted or exported by the admin config API", config.KeyCodexKeyringFile)
	}
}

type applicationConfigReader struct {
	values map[string]string
	keys   []string
	err    error
}

type applicationCredentialSubjects struct {
	versions         []string
	resolved         bool
	persistence      store.CodexKeyVersions
	versionsErr      error
	resolvedErr      error
	persistenceErr   error
	versionCalls     int
	resolvedCalls    int
	persistenceCalls int
}

func (reader *applicationCredentialSubjects) RequiredCredentialSubjectKeyVersions(context.Context) ([]string, error) {
	reader.versionCalls++
	return append([]string(nil), reader.versions...), reader.versionsErr
}

func (reader *applicationCredentialSubjects) CredentialSubjectsResolved(context.Context) (bool, error) {
	reader.resolvedCalls++
	return reader.resolved, reader.resolvedErr
}

func (reader *applicationCredentialSubjects) InspectCodexPersistence(context.Context) (store.CodexKeyVersions, error) {
	reader.persistenceCalls++
	return store.CodexKeyVersions{
		HMAC: append([]string(nil), reader.persistence.HMAC...),
		AEAD: append([]string(nil), reader.persistence.AEAD...),
	}, reader.persistenceErr
}

func (reader *applicationConfigReader) GetConfig(_ context.Context, key string) (string, error) {
	reader.keys = append(reader.keys, key)
	if reader.err != nil {
		return "", reader.err
	}
	return reader.values[key], nil
}

func applicationKeyringDocument() string {
	return `{"schema_version":1,` +
		`"hmac":{"current":"h2","keys":{"h1":"` + applicationKeyMaterial(1) + `","h2":"` + applicationKeyMaterial(2) + `"}},` +
		`"aead":{"current":"a2","keys":{"a1":"` + applicationKeyMaterial(11) + `","a2":"` + applicationKeyMaterial(12) + `"}}}`
}

func applicationKeyMaterial(value byte) string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{value}, 32))
}
