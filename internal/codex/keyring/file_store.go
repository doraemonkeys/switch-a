package codexkeyring

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

const keyringFileMode fs.FileMode = 0o600

// FileStore is the publication seam used by the keyring lifecycle. Implementations
// must make CreateExclusive atomic and return an error matching fs.ErrExist when
// another process has already published the destination.
type FileStore interface {
	ReadFile(path string) ([]byte, error)
	CreateExclusive(path string, data []byte) error
}

// OSFileStore persists keyring documents on the local filesystem.
type OSFileStore struct{}

func (OSFileStore) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (OSFileStore) CreateExclusive(path string, data []byte) (resultErr error) {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create keyring publication file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		closeErr := temporary.Close()
		removeErr := os.Remove(temporaryPath)
		if resultErr == nil && closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
			resultErr = fmt.Errorf("close keyring publication file: %w", closeErr)
		}
		if resultErr == nil && removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			resultErr = fmt.Errorf("remove keyring publication file: %w", removeErr)
		}
	}()

	if err := temporary.Chmod(keyringFileMode); err != nil {
		return fmt.Errorf("restrict keyring publication file: %w", err)
	}
	if _, err := io.Copy(temporary, bytes.NewReader(data)); err != nil {
		return fmt.Errorf("write keyring publication file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync keyring publication file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close keyring publication file: %w", err)
	}
	if err := publishNoClobber(temporaryPath, path); err != nil {
		return fmt.Errorf("publish keyring file: %w", err)
	}
	return nil
}
