package codexkeyring

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
)

// FileSource records which immutable file won publication without exposing
// key material to startup observability.
type FileSource string

const (
	FileSourceExisting         FileSource = "existing"
	FileSourceCreated          FileSource = "created"
	FileSourceConcurrentWinner FileSource = "concurrent_winner"
)

// LoadedFile is the validated result of the keyring file lifecycle.
type LoadedFile struct {
	Keyring *Keyring
	Source  FileSource
}

// LoadOrCreateFile uses the production no-clobber filesystem store.
func LoadOrCreateFile(path string, historical HistoricalVersions, random io.Reader) (LoadedFile, error) {
	return LoadOrCreateFileWithStore(OSFileStore{}, path, historical, random)
}

// LoadOrCreateFileWithStore strictly loads an existing document or creates a
// first-generation document only when durable state references no key version.
func LoadOrCreateFileWithStore(
	store FileStore,
	path string,
	historical HistoricalVersions,
	random io.Reader,
) (LoadedFile, error) {
	if store == nil {
		return LoadedFile{}, errorOf(ErrorInvalidInput, "file_store", "", "file store is required", nil)
	}
	if path == "" {
		return LoadedFile{}, errorOf(ErrorInvalidInput, "path", "", "keyring file path is required", nil)
	}
	if random == nil {
		return LoadedFile{}, errorOf(ErrorInvalidInput, "random", "", "random source is required", nil)
	}

	loaded, err := loadValidatedFile(store, path, historical, random)
	if err == nil {
		loaded.Source = FileSourceExisting
		return loaded, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return LoadedFile{}, err
	}
	if historical.hasReferences() {
		return LoadedFile{}, errorOf(
			ErrorCapabilityMissing,
			"document",
			"",
			"historical key versions require an existing keyring file",
			err,
		)
	}

	generated, err := GenerateDocument(random)
	if err != nil {
		return LoadedFile{}, err
	}
	defer clear(generated)
	if err := store.CreateExclusive(path, generated); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return LoadedFile{}, fmt.Errorf("create codex keyring file %q: %w", path, err)
		}
		winner, winnerErr := loadValidatedFile(store, path, historical, random)
		if winnerErr != nil {
			return LoadedFile{}, winnerErr
		}
		winner.Source = FileSourceConcurrentWinner
		return winner, nil
	}

	created, err := loadValidatedFile(store, path, historical, random)
	if err != nil {
		return LoadedFile{}, err
	}
	created.Source = FileSourceCreated
	return created, nil
}

func loadValidatedFile(
	store FileStore,
	path string,
	historical HistoricalVersions,
	random io.Reader,
) (LoadedFile, error) {
	serialized, err := store.ReadFile(path)
	if err != nil {
		return LoadedFile{}, fmt.Errorf("read codex keyring file %q: %w", path, err)
	}
	defer clear(serialized)

	keyring, err := Parse(serialized, random)
	if err != nil {
		return LoadedFile{}, fmt.Errorf("parse codex keyring file %q: %w", path, err)
	}
	if err := validateCompleteCapabilities(keyring, historical); err != nil {
		return LoadedFile{}, fmt.Errorf("validate codex keyring file %q: %w", path, err)
	}
	return LoadedFile{Keyring: keyring}, nil
}
