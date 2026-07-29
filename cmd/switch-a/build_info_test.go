package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/buildinfo"
)

func TestIsVersionRequest(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "version flag", args: []string{versionFlag}, want: true},
		{name: "no arguments", args: nil, want: false},
		{name: "unknown flag", args: []string{"--help"}, want: false},
		{name: "version with extra argument", args: []string{versionFlag, "extra"}, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isVersionRequest(test.args); got != test.want {
				t.Fatalf("isVersionRequest(%q) = %t, want %t", test.args, got, test.want)
			}
		})
	}
}

func TestWriteVersion(t *testing.T) {
	originalVersion, originalCommit, originalBuiltAt := buildinfo.Version, buildinfo.Commit, buildinfo.BuiltAt
	t.Cleanup(func() {
		buildinfo.Version, buildinfo.Commit, buildinfo.BuiltAt = originalVersion, originalCommit, originalBuiltAt
	})

	buildinfo.Version = "v0.1.0"
	buildinfo.Commit = "0123456789abcdef"
	buildinfo.BuiltAt = "2026-07-29T00:00:00Z"

	var output bytes.Buffer
	if err := writeVersion(&output); err != nil {
		t.Fatalf("writeVersion() error = %v", err)
	}
	if want := "switch-a v0.1.0 (commit 0123456789abcdef, built 2026-07-29T00:00:00Z)"; !strings.Contains(output.String(), want) {
		t.Fatalf("writeVersion() output = %q, want it to contain %q", output.String(), want)
	}
}

func TestWriteVersionPropagatesWriterFailure(t *testing.T) {
	wantErr := errors.New("write failed")
	writer := failingVersionWriter{err: wantErr}

	if err := writeVersion(writer); !errors.Is(err, wantErr) {
		t.Fatalf("writeVersion() error = %v, want %v", err, wantErr)
	}
}

type failingVersionWriter struct {
	err error
}

func (w failingVersionWriter) Write(_ []byte) (int, error) {
	return 0, w.err
}
