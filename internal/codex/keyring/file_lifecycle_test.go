package codexkeyring

import (
	"bytes"
	cryptorand "crypto/rand"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestLoadOrCreateFileGenerateOnceAndRestartStable(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "codex-keyring.json")

	created, err := LoadOrCreateFile(path, HistoricalVersions{}, cryptorand.Reader)
	if err != nil {
		t.Fatalf("LoadOrCreateFile(create) error = %v", err)
	}
	if created.Source != FileSourceCreated {
		t.Fatalf("create source = %q, want %q", created.Source, FileSourceCreated)
	}
	createdBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(created) error = %v", err)
	}
	createdDigest, err := created.Keyring.Sign(HMACCredentialSubject, []byte("stable credential"))
	if err != nil {
		t.Fatalf("Sign(created) error = %v", err)
	}

	restarted, err := LoadOrCreateFile(path, HistoricalVersions{
		HMAC: []string{createdDigest.Version},
		AEAD: []string{created.Keyring.Capabilities().AEADCurrent},
	}, cryptorand.Reader)
	if err != nil {
		t.Fatalf("LoadOrCreateFile(restart) error = %v", err)
	}
	if restarted.Source != FileSourceExisting {
		t.Fatalf("restart source = %q, want %q", restarted.Source, FileSourceExisting)
	}
	restartedBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(restarted) error = %v", err)
	}
	if !bytes.Equal(restartedBytes, createdBytes) {
		t.Fatal("restart changed keyring file bytes")
	}
	restartedDigest, err := restarted.Keyring.Sign(HMACCredentialSubject, []byte("stable credential"))
	if err != nil {
		t.Fatalf("Sign(restarted) error = %v", err)
	}
	if restartedDigest != createdDigest {
		t.Fatalf("restart digest = %+v, want %+v", restartedDigest, createdDigest)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat() error = %v", err)
		}
		if got := info.Mode().Perm(); got != keyringFileMode {
			t.Fatalf("keyring mode = %o, want %o", got, keyringFileMode)
		}
	}
	assertNoPublicationArtifacts(t, directory)
}

func TestLoadOrCreateFileConcurrentProcessesUseOneWinner(t *testing.T) {
	const contenders = 16
	path := filepath.Join(t.TempDir(), "codex-keyring.json")
	start := make(chan struct{})
	results := make(chan LoadedFile, contenders)
	errorsFound := make(chan error, contenders)
	var workers sync.WaitGroup
	workers.Add(contenders)
	for range contenders {
		go func() {
			defer workers.Done()
			<-start
			loaded, err := LoadOrCreateFile(path, HistoricalVersions{}, cryptorand.Reader)
			if err != nil {
				errorsFound <- err
				return
			}
			results <- loaded
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("LoadOrCreateFile() error = %v", err)
	}

	createdCount := 0
	var winnerDigest Digest
	for result := range results {
		if result.Source == FileSourceCreated {
			createdCount++
		}
		digest, err := result.Keyring.Sign(HMACClientScope, []byte("same client"))
		if err != nil {
			t.Fatalf("Sign() error = %v", err)
		}
		if winnerDigest.Version == "" {
			winnerDigest = digest
		} else if digest != winnerDigest {
			t.Fatalf("contender loaded a different key: %+v, want %+v", digest, winnerDigest)
		}
	}
	if createdCount != 1 {
		t.Fatalf("created result count = %d, want exactly one", createdCount)
	}
	assertNoPublicationArtifacts(t, filepath.Dir(path))
}

func TestLoadOrCreateFileNeverRewritesInvalidExistingFile(t *testing.T) {
	valid, err := GenerateDocument(distinctRandomReader())
	if err != nil {
		t.Fatalf("GenerateDocument() error = %v", err)
	}
	tests := []struct {
		name string
		data []byte
	}{
		{name: "empty"},
		{name: "corrupt", data: []byte("{")},
		{name: "incomplete", data: bytes.Replace(valid, []byte(`"aead"`), []byte(`"missing_aead"`), 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "codex-keyring.json")
			if err := os.WriteFile(path, test.data, keyringFileMode); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}

			_, err := LoadOrCreateFile(path, HistoricalVersions{}, cryptorand.Reader)
			if !IsError(err, ErrorInvalidDocument) {
				t.Fatalf("LoadOrCreateFile() error = %v, want invalid document", err)
			}
			after, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatalf("ReadFile() error = %v", readErr)
			}
			if !bytes.Equal(after, test.data) {
				t.Fatalf("invalid existing file changed: got %q, want %q", after, test.data)
			}
		})
	}
}

func TestLoadOrCreateFileNeverRewritesHistoricallyIncompleteFile(t *testing.T) {
	serialized, err := GenerateDocument(distinctRandomReader())
	if err != nil {
		t.Fatalf("GenerateDocument() error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "codex-keyring.json")
	if err := os.WriteFile(path, serialized, keyringFileMode); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err = LoadOrCreateFile(path, HistoricalVersions{
		HMAC: []string{"hmac-legacy"},
		AEAD: []string{"aead-legacy"},
	}, cryptorand.Reader)
	if !IsError(err, ErrorCapabilityMissing) {
		t.Fatalf("LoadOrCreateFile() error = %v, want missing historical capability", err)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("ReadFile() error = %v", readErr)
	}
	if !bytes.Equal(after, serialized) {
		t.Fatal("historically incomplete existing file changed")
	}
}

func TestLoadOrCreateFileUnreadableExistingFileDoesNotCreate(t *testing.T) {
	want := fs.ErrPermission
	store := &readFailureStore{err: want}

	_, err := LoadOrCreateFileWithStore(store, "codex-keyring.json", HistoricalVersions{}, cryptorand.Reader)
	if !errors.Is(err, want) {
		t.Fatalf("LoadOrCreateFileWithStore() error = %v, want permission failure", err)
	}
	if store.createCalls != 0 {
		t.Fatalf("CreateExclusive() calls = %d, want zero", store.createCalls)
	}
}

func TestLoadOrCreateFileMissingWithHistoryCreatesNothing(t *testing.T) {
	tests := []struct {
		name       string
		historical HistoricalVersions
	}{
		{name: "hmac", historical: HistoricalVersions{HMAC: []string{"hmac-0"}}},
		{name: "aead", historical: HistoricalVersions{AEAD: []string{"aead-0"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "codex-keyring.json")
			_, err := LoadOrCreateFile(path, test.historical, cryptorand.Reader)
			if !IsError(err, ErrorCapabilityMissing) || !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("LoadOrCreateFile() error = %v, want missing historical keyring", err)
			}
			if _, statErr := os.Stat(path); !errors.Is(statErr, fs.ErrNotExist) {
				t.Fatalf("Stat() error = %v, want no created file", statErr)
			}
		})
	}
}

func TestLoadOrCreateFileValidatesPublishedAndConcurrentWinnerFiles(t *testing.T) {
	winnerDocument, err := GenerateDocument(distinctRandomReader())
	if err != nil {
		t.Fatalf("GenerateDocument(winner) error = %v", err)
	}
	winnerKeyring, err := Parse(winnerDocument, cryptorand.Reader)
	if err != nil {
		t.Fatalf("Parse(winner) error = %v", err)
	}
	winnerDigest, err := winnerKeyring.Sign(HMACClientScope, []byte("winner"))
	if err != nil {
		t.Fatalf("Sign(winner) error = %v", err)
	}

	store := &winnerStore{winner: winnerDocument}
	loaded, err := LoadOrCreateFileWithStore(store, "codex-keyring.json", HistoricalVersions{}, distinctRandomReader())
	if err != nil {
		t.Fatalf("LoadOrCreateFileWithStore() error = %v", err)
	}
	if loaded.Source != FileSourceConcurrentWinner {
		t.Fatalf("source = %q, want %q", loaded.Source, FileSourceConcurrentWinner)
	}
	loadedDigest, err := loaded.Keyring.Sign(HMACClientScope, []byte("winner"))
	if err != nil {
		t.Fatalf("Sign(loaded) error = %v", err)
	}
	if loadedDigest != winnerDigest {
		t.Fatalf("loaded digest = %+v, want concurrent winner %+v", loadedDigest, winnerDigest)
	}

	corrupting := &corruptingCreateStore{}
	_, err = LoadOrCreateFileWithStore(corrupting, "codex-keyring.json", HistoricalVersions{}, distinctRandomReader())
	if !IsError(err, ErrorInvalidDocument) {
		t.Fatalf("LoadOrCreateFileWithStore(corrupt write) error = %v, want invalid document", err)
	}
}

func TestOSFileStoreCreateExclusiveNeverOverwrites(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "codex-keyring.json")
	original := []byte("winner")
	if err := os.WriteFile(path, original, keyringFileMode); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err := (OSFileStore{}).CreateExclusive(path, []byte("loser"))
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("CreateExclusive() error = %v, want fs.ErrExist", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !bytes.Equal(after, original) {
		t.Fatalf("destination = %q, want unchanged %q", after, original)
	}
	assertNoPublicationArtifacts(t, directory)
}

func TestLoadOrCreateFileRejectsInvalidDependencies(t *testing.T) {
	tests := []struct {
		name   string
		store  FileStore
		path   string
		random io.Reader
	}{
		{name: "nil store", path: "keyring.json", random: distinctRandomReader()},
		{name: "empty path", store: OSFileStore{}, random: distinctRandomReader()},
		{name: "nil random", store: OSFileStore{}, path: "keyring.json"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadOrCreateFileWithStore(test.store, test.path, HistoricalVersions{}, test.random)
			if !IsError(err, ErrorInvalidInput) {
				t.Fatalf("LoadOrCreateFileWithStore() error = %v, want invalid input", err)
			}
		})
	}
}

type readFailureStore struct {
	err         error
	createCalls int
}

func (store *readFailureStore) ReadFile(string) ([]byte, error) { return nil, store.err }

func (store *readFailureStore) CreateExclusive(string, []byte) error {
	store.createCalls++
	return nil
}

type winnerStore struct {
	winner   []byte
	readCall int
}

func (store *winnerStore) ReadFile(string) ([]byte, error) {
	store.readCall++
	if store.readCall == 1 {
		return nil, fs.ErrNotExist
	}
	return append([]byte(nil), store.winner...), nil
}

func (*winnerStore) CreateExclusive(string, []byte) error { return fs.ErrExist }

type corruptingCreateStore struct {
	created bool
}

func (store *corruptingCreateStore) ReadFile(string) ([]byte, error) {
	if !store.created {
		return nil, fs.ErrNotExist
	}
	return []byte("{"), nil
}

func (store *corruptingCreateStore) CreateExclusive(string, []byte) error {
	store.created = true
	return nil
}

func distinctRandomReader() *bytes.Reader {
	material := make([]byte, generatedRootCount*keyMaterialBytes)
	for index := range material {
		material[index] = byte(index + 1)
	}
	return bytes.NewReader(material)
}

func assertNoPublicationArtifacts(t *testing.T, directory string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(directory, ".codex-keyring.json.tmp-*"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("publication artifacts remain: %v", matches)
	}
}
