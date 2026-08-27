package codexkeyring

import (
	"fmt"
	"os"
	"path/filepath"
)

func publishNoClobber(temporaryPath, destinationPath string) error {
	// A same-directory hard link publishes one complete, synced inode and fails
	// atomically if a concurrent process already owns the destination name.
	if err := os.Link(temporaryPath, destinationPath); err != nil {
		return err
	}
	if err := syncPublishedPath(destinationPath); err != nil {
		return err
	}
	if err := syncPublishedDirectory(filepath.Dir(destinationPath)); err != nil {
		return fmt.Errorf("sync keyring directory: %w", err)
	}
	return nil
}
