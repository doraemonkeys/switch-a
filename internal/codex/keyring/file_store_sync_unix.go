//go:build !windows

package codexkeyring

import "os"

func syncPublishedPath(string) error { return nil }

func syncPublishedDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
