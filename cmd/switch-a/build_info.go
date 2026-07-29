package main

import (
	"fmt"
	"io"

	"github.com/doraemonkeys/switch-a/internal/buildinfo"
)

const versionFlag = "--version"

func isVersionRequest(args []string) bool {
	return len(args) == 1 && args[0] == versionFlag
}

func writeVersion(writer io.Writer) error {
	_, err := fmt.Fprintln(writer, buildinfo.Current())
	return err
}
