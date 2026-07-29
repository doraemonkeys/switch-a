package buildinfo

import "testing"

func TestCurrentReturnsLinkerValues(t *testing.T) {
	originalVersion, originalCommit, originalBuiltAt := Version, Commit, BuiltAt
	t.Cleanup(func() {
		Version, Commit, BuiltAt = originalVersion, originalCommit, originalBuiltAt
	})

	Version = "v0.1.0"
	Commit = "0123456789abcdef"
	BuiltAt = "2026-07-29T00:00:00Z"

	want := Info{Version: Version, Commit: Commit, BuiltAt: BuiltAt}
	if got := Current(); got != want {
		t.Fatalf("Current() = %#v, want %#v", got, want)
	}
}

func TestInfoString(t *testing.T) {
	info := Info{
		Version: "v0.1.0",
		Commit:  "0123456789abcdef",
		BuiltAt: "2026-07-29T00:00:00Z",
	}

	want := "switch-a v0.1.0 (commit 0123456789abcdef, built 2026-07-29T00:00:00Z)"
	if got := info.String(); got != want {
		t.Fatalf("Info.String() = %q, want %q", got, want)
	}
}
