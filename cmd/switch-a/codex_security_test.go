package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	codexkeyring "github.com/doraemonkeys/switch-a/internal/codex/keyring"
	"github.com/doraemonkeys/switch-a/internal/store"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestBootstrapApplicationCodexSecurityFreshStartAndStableRestart(t *testing.T) {
	files := &applicationMemoryKeyringStore{}
	path := filepath.Join(t.TempDir(), "codex-keyring.json")
	firstPersistence := &applicationCodexPersistenceStub{inventories: []store.CodexPersistenceInventory{{}, {}}}
	first, err := bootstrapApplicationCodexSecurity(
		context.Background(), "startup-one", path, firstPersistence, files,
		applicationRandom(1), zap.NewNop(), nil,
	)
	if err != nil {
		t.Fatalf("fresh bootstrap error = %v", err)
	}
	if first.fileSource != codexkeyring.FileSourceCreated || files.createCalls != 1 || firstPersistence.finalizeCalls != 1 {
		t.Fatalf("fresh bootstrap = source:%q creates:%d finalizes:%d", first.fileSource, files.createCalls, firstPersistence.finalizeCalls)
	}
	firstDigest, err := first.keyring.Sign(codexkeyring.HMACCredentialSubject, []byte("stable-credential"))
	if err != nil {
		t.Fatal(err)
	}
	written := append([]byte(nil), files.data...)

	secondPersistence := &applicationCodexPersistenceStub{inventories: []store.CodexPersistenceInventory{{}, {}}}
	second, err := bootstrapApplicationCodexSecurity(
		context.Background(), "startup-two", path, secondPersistence, files,
		applicationRandom(2), zap.NewNop(), nil,
	)
	if err != nil {
		t.Fatalf("restart bootstrap error = %v", err)
	}
	secondDigest, err := second.keyring.Sign(codexkeyring.HMACCredentialSubject, []byte("stable-credential"))
	if err != nil {
		t.Fatal(err)
	}
	if second.fileSource != codexkeyring.FileSourceExisting || files.createCalls != 1 || !bytes.Equal(files.data, written) || firstDigest != secondDigest {
		t.Fatalf("restart changed durable key identity: source=%q creates=%d digest_equal=%t", second.fileSource, files.createCalls, firstDigest == secondDigest)
	}
}

func TestBootstrapApplicationCodexSecurityReportsEveryHistoryFamily(t *testing.T) {
	tests := []struct {
		name      string
		inventory store.CodexPersistenceInventory
		family    string
	}{
		{name: "credential", inventory: store.CodexPersistenceInventory{CredentialHMACVersions: []string{"h9"}}, family: "credential_subject_hmac"},
		{name: "continuity", inventory: store.CodexPersistenceInventory{ContinuityHMACVersions: []string{"h9"}}, family: "continuity_hmac"},
		{name: "cookie hmac", inventory: store.CodexPersistenceInventory{ProviderCookieHMACVersions: []string{"h9"}}, family: "provider_cookie_hmac"},
		{name: "cookie aead", inventory: store.CodexPersistenceInventory{ProviderCookieAEADVersions: []string{"a9"}}, family: "provider_cookie_aead"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			files := &applicationMemoryKeyringStore{data: []byte(applicationKeyringDocument())}
			persistence := &applicationCodexPersistenceStub{inventories: []store.CodexPersistenceInventory{test.inventory}}
			_, err := bootstrapApplicationCodexSecurity(
				context.Background(), "startup-history", filepath.Join(t.TempDir(), "keyring.json"), persistence, files,
				applicationRandom(3), zap.NewNop(), nil,
			)
			if err == nil || !strings.Contains(err.Error(), test.family) || !strings.Contains(err.Error(), "9") {
				t.Fatalf("history error = %v, want family %q and version", err, test.family)
			}
			if files.createCalls != 0 || persistence.finalizeCalls != 0 {
				t.Fatalf("failure mutated startup state: creates=%d finalizes=%d", files.createCalls, persistence.finalizeCalls)
			}
		})
	}
}

func TestBootstrapApplicationCodexSecurityMissingFileWithHistoryNeverCreates(t *testing.T) {
	files := &applicationMemoryKeyringStore{}
	persistence := &applicationCodexPersistenceStub{inventories: []store.CodexPersistenceInventory{{ContinuityHMACVersions: []string{"h1"}}}}
	_, err := bootstrapApplicationCodexSecurity(
		context.Background(), "startup-missing", filepath.Join(t.TempDir(), "keyring.json"), persistence, files,
		applicationRandom(4), zap.NewNop(), nil,
	)
	if err == nil || !strings.Contains(err.Error(), "continuity_hmac") || files.createCalls != 0 {
		t.Fatalf("missing-history result = err:%v creates:%d", err, files.createCalls)
	}
}

func TestBootstrapApplicationCodexSecurityFinalizesStaticAndPreservesChatGPTPending(t *testing.T) {
	before := store.CodexPersistenceInventory{
		PendingStaticCredentialSessionIDs: []string{"static-a", "static-b"},
		PendingChatGPTReauthSessionIDs:    []string{"chatgpt-a"},
	}
	after := store.CodexPersistenceInventory{PendingChatGPTReauthSessionIDs: []string{"chatgpt-a"}}
	persistence := &applicationCodexPersistenceStub{inventories: []store.CodexPersistenceInventory{before, after}}
	var events []applicationLifecycleEvent
	core, observed := observer.New(zapcore.DebugLevel)
	security, err := bootstrapApplicationCodexSecurity(
		context.Background(), "startup-finalize", filepath.Join(t.TempDir(), "keyring.json"), persistence,
		&applicationMemoryKeyringStore{}, applicationRandom(5), zap.New(core),
		applicationLifecycleRecorderFunc(func(event applicationLifecycleEvent) { events = append(events, event) }),
	)
	if err != nil {
		t.Fatal(err)
	}
	if persistence.finalizeCalls != 1 || security.postcondition.PendingStaticCredentialSubjectCount() != 0 || security.postcondition.PendingChatGPTReauthSubjectCount() != 1 {
		t.Fatalf("postcondition = %+v, finalize calls = %d", security.postcondition, persistence.finalizeCalls)
	}
	wantPhases := []applicationStartupPhase{startupPhaseInventory, startupPhaseKeyring, startupPhaseStaticFinalization, startupPhaseCodexPostcondition}
	var phases []applicationStartupPhase
	for _, event := range events {
		phases = append(phases, event.Phase)
	}
	if !reflect.DeepEqual(phases, wantPhases) {
		t.Fatalf("phases = %v, want %v", phases, wantPhases)
	}
	entries := observed.FilterMessage("codex.startup_phase").All()
	if len(entries) != 4 {
		t.Fatalf("startup trace count = %d, want 4", len(entries))
	}
	for _, entry := range entries {
		fields := entry.ContextMap()
		if fields["startup_id"] != "startup-finalize" {
			t.Fatalf("startup trace lost correlation ID: %+v", fields)
		}
		serialized := entry.Message + fmt.Sprint(fields)
		if strings.Contains(serialized, applicationKeyMaterial(1)) || strings.Contains(serialized, "static-a") || strings.Contains(serialized, "chatgpt-a") {
			t.Fatalf("startup trace exposed secret-bearing state: %+v", fields)
		}
	}
	keyringFields := entries[1].ContextMap()
	if keyringFields["resolved_path"] == "" || keyringFields["file_source"] != string(codexkeyring.FileSourceCreated) || keyringFields["hmac_key_version_count"] != int64(1) || keyringFields["aead_key_version_count"] != int64(1) {
		t.Fatalf("keyring startup trace = %+v", keyringFields)
	}
	finalizationFields := entries[2].ContextMap()
	if finalizationFields["finalized_static_subject_count"] != int64(2) || finalizationFields["chatgpt_reauth_pending_count"] != int64(1) {
		t.Fatalf("finalization startup trace = %+v", finalizationFields)
	}
}

func TestBootstrapApplicationCodexSecurityRecordsEveryFailurePhaseWithoutActivationOrSecrets(t *testing.T) {
	failure := errors.New("injected failure")
	tests := []struct {
		name        string
		phase       applicationStartupPhase
		persistence *applicationCodexPersistenceStub
		files       *applicationMemoryKeyringStore
	}{
		{name: "inventory", phase: startupPhaseInventory, persistence: &applicationCodexPersistenceStub{inspectErr: failure}, files: &applicationMemoryKeyringStore{}},
		{name: "keyring", phase: startupPhaseKeyring, persistence: &applicationCodexPersistenceStub{inventories: []store.CodexPersistenceInventory{{}}}, files: &applicationMemoryKeyringStore{readErr: failure}},
		{name: "static finalization", phase: startupPhaseStaticFinalization, persistence: &applicationCodexPersistenceStub{inventories: []store.CodexPersistenceInventory{{}}, finalizeErr: failure}, files: &applicationMemoryKeyringStore{}},
		{name: "postcondition", phase: startupPhaseCodexPostcondition, persistence: &applicationCodexPersistenceStub{inventories: []store.CodexPersistenceInventory{{}, {PendingStaticCredentialSessionIDs: []string{"still-pending"}}}}, files: &applicationMemoryKeyringStore{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			core, observed := observer.New(zapcore.DebugLevel)
			var events []applicationLifecycleEvent
			_, err := bootstrapApplicationCodexSecurity(
				context.Background(), "startup-failure", filepath.Join(t.TempDir(), "keyring.json"), test.persistence,
				test.files, applicationRandom(6), zap.New(core),
				applicationLifecycleRecorderFunc(func(event applicationLifecycleEvent) { events = append(events, event) }),
			)
			if err == nil {
				t.Fatal("bootstrap error = nil")
			}
			entries := observed.FilterMessage("codex.startup_failed").All()
			if len(entries) != 1 || entries[0].ContextMap()["startup_id"] != "startup-failure" || entries[0].ContextMap()["failure_phase"] != string(test.phase) {
				t.Fatalf("failure trace = %+v", entries)
			}
			if strings.Contains(entries[0].Message+entries[0].ContextMap()["error"].(string), applicationKeyMaterial(1)) {
				t.Fatal("startup trace exposed key material")
			}
			for _, event := range events {
				if event.Phase == startupPhaseBackgroundOwners || event.Phase == startupPhaseListeners {
					t.Fatalf("preflight failure activated application: %+v", events)
				}
			}
		})
	}
}

func TestBootstrapApplicationCodexSecurityRejectsInvalidDependenciesAndPostcondition(t *testing.T) {
	if _, err := bootstrapApplicationCodexSecurity(nil, "", "", nil, nil, nil, nil, nil); err == nil {
		t.Fatal("bootstrap accepted missing dependencies")
	}
	persistence := &applicationCodexPersistenceStub{inventories: []store.CodexPersistenceInventory{{}, {PendingStaticCredentialSessionIDs: []string{"still-pending"}}}}
	_, err := bootstrapApplicationCodexSecurity(
		context.Background(), "startup-postcondition", filepath.Join(t.TempDir(), "keyring.json"), persistence,
		&applicationMemoryKeyringStore{}, applicationRandom(7), zap.NewNop(), nil,
	)
	if err == nil || !strings.Contains(err.Error(), "pending static") {
		t.Fatalf("postcondition error = %v", err)
	}
}

func TestResolveCodexKeyringPathIsAbsoluteAndMandatory(t *testing.T) {
	if _, err := resolveCodexKeyringPath(""); err == nil {
		t.Fatal("empty path was accepted")
	}
	resolved, err := resolveCodexKeyringPath("codex-keyring.json")
	if err != nil || !filepath.IsAbs(resolved) {
		t.Fatalf("resolved path = %q, %v", resolved, err)
	}
}

type applicationCodexPersistenceStub struct {
	inventories   []store.CodexPersistenceInventory
	inspectErr    error
	finalizeErr   error
	inspectCalls  int
	finalizeCalls int
}

func (p *applicationCodexPersistenceStub) InspectCodexPersistence(context.Context) (store.CodexPersistenceInventory, error) {
	if p.inspectErr != nil {
		return store.CodexPersistenceInventory{}, p.inspectErr
	}
	index := p.inspectCalls
	p.inspectCalls++
	if len(p.inventories) == 0 {
		return store.CodexPersistenceInventory{}, nil
	}
	if index >= len(p.inventories) {
		index = len(p.inventories) - 1
	}
	return p.inventories[index], nil
}

func (p *applicationCodexPersistenceStub) FinalizeStaticCredentialSubjects(context.Context, store.StaticCredentialSubjectSigner) error {
	p.finalizeCalls++
	return p.finalizeErr
}

type applicationMemoryKeyringStore struct {
	data        []byte
	readErr     error
	createErr   error
	createCalls int
}

func (s *applicationMemoryKeyringStore) ReadFile(string) ([]byte, error) {
	if s.readErr != nil {
		return nil, s.readErr
	}
	if s.data == nil {
		return nil, fs.ErrNotExist
	}
	return append([]byte(nil), s.data...), nil
}

func (s *applicationMemoryKeyringStore) CreateExclusive(_ string, data []byte) error {
	s.createCalls++
	if s.createErr != nil {
		return s.createErr
	}
	if s.data != nil {
		return fs.ErrExist
	}
	s.data = append([]byte(nil), data...)
	return nil
}

func applicationKeyringDocument() string {
	return `{"schema_version":1,` +
		`"hmac":{"current":"h2","keys":{"h1":"` + applicationKeyMaterial(1) + `","h2":"` + applicationKeyMaterial(2) + `"}},` +
		`"aead":{"current":"a2","keys":{"a1":"` + applicationKeyMaterial(11) + `","a2":"` + applicationKeyMaterial(12) + `"}}}`
}

func applicationRandom(value byte) *bytes.Reader {
	material := append(bytes.Repeat([]byte{value}, 32), bytes.Repeat([]byte{value + 1}, 32)...)
	return bytes.NewReader(material)
}

func applicationKeyMaterial(value byte) string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{value}, 32))
}
