package codexstartup

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/keyring"
)

func TestParseCoversAllFeatureCombinations(t *testing.T) {
	keyring := startupTestKeyring(t)
	compiled := allCompiledCapabilities()
	for bits := 0; bits < 16; bits++ {
		headerHygiene := bits&1 != 0
		webSocketSubprotocol := bits&2 != 0
		continuity := bits&4 != 0
		providerCookieJar := bits&8 != 0
		name := fmt.Sprintf(
			"hygiene=%t/subprotocol=%t/continuity=%t/cookie=%t",
			headerHygiene,
			webSocketSubprotocol,
			continuity,
			providerCookieJar,
		)
		t.Run(name, func(t *testing.T) {
			values := map[string]string{
				KeyUpstreamHeaderHygiene: fmt.Sprint(headerHygiene),
				KeyWebSocketSubprotocol:  fmt.Sprint(webSocketSubprotocol),
				KeyContinuity:            fmt.Sprint(continuity),
				KeyProviderCookieJar:     fmt.Sprint(providerCookieJar),
			}
			snapshot, err := Parse(values)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			wantDependencyFailure := !headerHygiene && (continuity || providerCookieJar)
			err = snapshot.Validate(compiled, keyring, ReferencedKeyVersions{})
			if wantDependencyFailure && !IsError(err, ErrorDependency) {
				t.Fatalf("Validate() error = %v, want dependency failure", err)
			}
			if !wantDependencyFailure && err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestParseBooleanContractAndDefaults(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "missing", value: "", want: false},
		{name: "false", value: "false", want: false},
		{name: "zero", value: "0", want: false},
		{name: "true", value: "true", want: true},
		{name: "uppercase", value: "TRUE", want: true},
		{name: "one", value: "1", want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot, err := Parse(map[string]string{KeyContinuity: test.value})
			if err != nil || snapshot.Continuity != test.want {
				t.Fatalf("Parse() = %+v, %v, want continuity=%t", snapshot, err, test.want)
			}
		})
	}
	for _, key := range Keys() {
		if _, err := Parse(map[string]string{key: "yes"}); !IsError(err, ErrorInvalidConfig) {
			t.Fatalf("Parse(%q=yes) error = %v", key, err)
		}
	}
}

func TestLoadReadsCompleteSnapshotAndFailsClosed(t *testing.T) {
	reader := &recordingReader{values: map[string]string{
		KeyUpstreamHeaderHygiene: "1",
		KeyWebSocketSubprotocol:  "true",
		KeyContinuity:            "false",
		KeyProviderCookieJar:     "0",
	}}
	snapshot, err := Load(context.Background(), reader)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if snapshot != (Snapshot{UpstreamHeaderHygiene: true, WebSocketSubprotocol: true}) {
		t.Fatalf("Load() = %+v", snapshot)
	}
	if !slices.Equal(reader.keys, Keys()) {
		t.Fatalf("read keys = %v, want %v", reader.keys, Keys())
	}

	readFailure := errors.New("database unavailable")
	reader = &recordingReader{values: map[string]string{}, errKey: KeyContinuity, err: readFailure}
	_, err = Load(context.Background(), reader)
	if !IsError(err, ErrorConfigUnavailable) || !errors.Is(err, readFailure) {
		t.Fatalf("Load(read failure) error = %v", err)
	}
	if _, err := Load(context.Background(), nil); !IsError(err, ErrorConfigUnavailable) {
		t.Fatalf("Load(nil) error = %v", err)
	}
}

func TestValidateRequiresEveryCompiledCapability(t *testing.T) {
	keyring := startupTestKeyring(t)
	tests := []struct {
		name       string
		snapshot   Snapshot
		compiled   CompiledCapabilities
		capability string
	}{
		{name: "hygiene", snapshot: Snapshot{UpstreamHeaderHygiene: true}, capability: "upstream_header_hygiene"},
		{name: "subprotocol", snapshot: Snapshot{WebSocketSubprotocol: true}, capability: "websocket_subprotocol"},
		{name: "credential sessions", snapshot: Snapshot{UpstreamHeaderHygiene: true, Continuity: true}, compiled: CompiledCapabilities{UpstreamHeaderHygiene: true}, capability: "credential_sessions"},
		{name: "resolved credential subjects", snapshot: Snapshot{UpstreamHeaderHygiene: true, Continuity: true}, compiled: CompiledCapabilities{UpstreamHeaderHygiene: true, CredentialSessions: true}, capability: "credential_subjects_resolved"},
		{name: "continuity schema", snapshot: Snapshot{UpstreamHeaderHygiene: true, Continuity: true}, compiled: CompiledCapabilities{UpstreamHeaderHygiene: true, CredentialSessions: true, CredentialSubjectsResolved: true}, capability: "continuity_schema"},
		{name: "protocol catalog", snapshot: Snapshot{UpstreamHeaderHygiene: true, Continuity: true}, compiled: CompiledCapabilities{UpstreamHeaderHygiene: true, CredentialSessions: true, CredentialSubjectsResolved: true, ContinuitySchema: true}, capability: "protocol_catalog"},
		{name: "continuity identity", snapshot: Snapshot{UpstreamHeaderHygiene: true, Continuity: true}, compiled: CompiledCapabilities{UpstreamHeaderHygiene: true, CredentialSessions: true, CredentialSubjectsResolved: true, ContinuitySchema: true, ProtocolCatalog: true}, capability: "identity"},
		{name: "continuity applied identity", snapshot: Snapshot{UpstreamHeaderHygiene: true, Continuity: true}, compiled: CompiledCapabilities{UpstreamHeaderHygiene: true, CredentialSessions: true, CredentialSubjectsResolved: true, ContinuitySchema: true, ProtocolCatalog: true, Identity: true}, capability: "applied_identity"},
		{name: "cookie schema", snapshot: Snapshot{UpstreamHeaderHygiene: true, ProviderCookieJar: true}, compiled: CompiledCapabilities{UpstreamHeaderHygiene: true, CredentialSessions: true, CredentialSubjectsResolved: true}, capability: "provider_cookie_schema"},
		{name: "cookie identity", snapshot: Snapshot{UpstreamHeaderHygiene: true, ProviderCookieJar: true}, compiled: CompiledCapabilities{UpstreamHeaderHygiene: true, CredentialSessions: true, CredentialSubjectsResolved: true, ProviderCookieSchema: true}, capability: "identity"},
		{name: "cookie applied identity", snapshot: Snapshot{UpstreamHeaderHygiene: true, ProviderCookieJar: true}, compiled: CompiledCapabilities{UpstreamHeaderHygiene: true, CredentialSessions: true, CredentialSubjectsResolved: true, ProviderCookieSchema: true, Identity: true}, capability: "applied_identity"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.snapshot.Validate(test.compiled, keyring, ReferencedKeyVersions{})
			if !IsError(err, ErrorCapabilityMissing) {
				t.Fatalf("Validate() error = %v", err)
			}
			var typed *Error
			if !errors.As(err, &typed) || typed.Capability != test.capability {
				t.Fatalf("capability error = %+v, want %q", typed, test.capability)
			}
		})
	}
}

func TestValidateKeyRequirementsRespectEnablementAndReferences(t *testing.T) {
	compiled := allCompiledCapabilities()
	keyring := startupTestKeyring(t)
	if err := (Snapshot{}).Validate(
		CompiledCapabilities{},
		nil,
		ReferencedKeyVersions{HMAC: []string{"missing"}, AEAD: []string{"missing"}},
	); err != nil {
		t.Fatalf("disabled Validate() error = %v", err)
	}
	if err := (Snapshot{}).Validate(
		CompiledCapabilities{},
		keyring,
		ReferencedKeyVersions{HMAC: []string{"h9"}},
	); !IsError(err, ErrorCapabilityMissing) {
		t.Fatalf("configured keyring missing referenced legacy key error = %v", err)
	}
	if err := (Snapshot{}).Validate(
		CompiledCapabilities{},
		keyring,
		ReferencedKeyVersions{HMAC: []string{"h1"}, AEAD: []string{"a1"}},
	); err != nil {
		t.Fatalf("configured keyring with referenced legacy keys error = %v", err)
	}

	continuity := Snapshot{UpstreamHeaderHygiene: true, Continuity: true}
	if err := continuity.Validate(compiled, nil, ReferencedKeyVersions{}); !IsError(err, ErrorCapabilityMissing) {
		t.Fatalf("continuity without keyring error = %v", err)
	}
	if err := continuity.Validate(compiled, keyring, ReferencedKeyVersions{HMAC: []string{"h9"}}); !IsError(err, ErrorCapabilityMissing) {
		t.Fatalf("continuity missing legacy HMAC error = %v", err)
	}
	if err := continuity.Validate(compiled, keyring, ReferencedKeyVersions{HMAC: []string{"h1"}}); err != nil {
		t.Fatalf("continuity with HMAC error = %v", err)
	}

	cookie := Snapshot{UpstreamHeaderHygiene: true, ProviderCookieJar: true}
	if err := cookie.Validate(compiled, keyring, ReferencedKeyVersions{AEAD: []string{"a9"}}); !IsError(err, ErrorCapabilityMissing) {
		t.Fatalf("cookie missing legacy AEAD error = %v", err)
	}
	if err := cookie.Validate(compiled, keyring, ReferencedKeyVersions{HMAC: []string{"h1"}, AEAD: []string{"a1"}}); err != nil {
		t.Fatalf("cookie with key versions error = %v", err)
	}
}

func TestKeysAndErrorsDoNotExposeMutableOrSecretState(t *testing.T) {
	keys := Keys()
	keys[0] = "modified"
	if Keys()[0] == "modified" {
		t.Fatal("Keys() exposed mutable storage")
	}
	err := invalidBoolean(KeyContinuity, "secret-looking-value")
	if err.Error() == "" || !errors.Is(err, &Error{Kind: ErrorInvalidConfig}) {
		t.Fatalf("typed error = %v", err)
	}
	var typed *Error
	if !errors.As(err, &typed) || !errors.Is(typed.Unwrap(), typed.cause) {
		t.Fatalf("error unwrap = %+v", typed)
	}
	if (*Error)(nil).Error() != "<nil>" {
		t.Fatal("nil Error formatting changed")
	}
}

func allCompiledCapabilities() CompiledCapabilities {
	return CompiledCapabilities{
		UpstreamHeaderHygiene:      true,
		WebSocketSubprotocol:       true,
		CredentialSessions:         true,
		CredentialSubjectsResolved: true,
		ContinuitySchema:           true,
		ProtocolCatalog:            true,
		Identity:                   true,
		AppliedIdentity:            true,
		ProviderCookieSchema:       true,
	}
}

func startupTestKeyring(t *testing.T) *codexkeyring.Keyring {
	t.Helper()
	document := `{"schema_version":1,` +
		`"hmac":{"current":"h2","keys":{"h1":"` + startupMaterial(1) + `","h2":"` + startupMaterial(2) + `"}},` +
		`"aead":{"current":"a2","keys":{"a1":"` + startupMaterial(11) + `","a2":"` + startupMaterial(12) + `"}}}`
	keyring, err := codexkeyring.Parse([]byte(document), bytes.NewReader(bytes.Repeat([]byte{1}, 128)))
	if err != nil {
		t.Fatalf("codexkeyring.Parse() error = %v", err)
	}
	return keyring
}

func startupMaterial(value byte) string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{value}, 32))
}

type recordingReader struct {
	values map[string]string
	keys   []string
	errKey string
	err    error
}

func (reader *recordingReader) GetConfig(_ context.Context, key string) (string, error) {
	reader.keys = append(reader.keys, key)
	if key == reader.errKey {
		return "", reader.err
	}
	return reader.values[key], nil
}
