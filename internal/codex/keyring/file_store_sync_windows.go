//go:build windows

package codexkeyring

import "os"

func syncPublishedPath(path string) error {
	// Flushing through the published name makes the hard-link metadata part of
	// the same durability boundary as the already-synced file contents.
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

// Windows does not expose directory fsync through os.File. The post-link file
// flush above is the portable metadata durability boundary on this platform.
func syncPublishedDirectory(string) error { return nil }
