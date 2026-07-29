// Package buildinfo exposes immutable metadata injected by the release build.
package buildinfo

import "fmt"

const (
	applicationName    = "switch-a"
	developmentVersion = "dev"
	unknownValue       = "unknown"
)

// Linker-injected values live in one package so every runtime surface reports
// the same release identity instead of inventing its own version semantics.
var (
	Version = developmentVersion
	Commit  = unknownValue
	BuiltAt = unknownValue
)

// Info is the release identity of the running binary.
type Info struct {
	Version string
	Commit  string
	BuiltAt string
}

// Current returns a snapshot so callers cannot mutate release metadata.
func Current() Info {
	return Info{
		Version: Version,
		Commit:  Commit,
		BuiltAt: BuiltAt,
	}
}

func (i Info) String() string {
	return fmt.Sprintf("%s %s (commit %s, built %s)", applicationName, i.Version, i.Commit, i.BuiltAt)
}
