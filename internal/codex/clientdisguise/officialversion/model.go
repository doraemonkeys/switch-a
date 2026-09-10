// Package officialversion tracks the published Codex release independently of
// observed client environments and credential identities.
package officialversion

import (
	"regexp"
	"strings"
	"time"
)

const (
	Source         = "official_stable"
	RepositoryURL  = "https://github.com/openai/codex"
	tagPrefix      = "rust-v"
	SyncInterval   = 6 * time.Hour
	RetryInterval  = 15 * time.Minute
	PollInterval   = time.Minute
	RequestTimeout = 30 * time.Second
)

var stablePattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type Release struct {
	Version     string    `json:"version"`
	Tag         string    `json:"tag"`
	URL         string    `json:"url"`
	PublishedAt time.Time `json:"published_at"`
}

type State struct {
	ID        uint      `json:"-" gorm:"primaryKey"`
	Release   Release   `json:"release" gorm:"serializer:json"`
	CheckedAt time.Time `json:"checked_at"`
	SyncedAt  time.Time `json:"synced_at"`
	LastError string    `json:"last_error"`
}

func (State) TableName() string { return "client_disguise_official_version" }

func ValidVersion(version string) bool { return stablePattern.MatchString(version) }

// Stable release components are compared without machine-sized integer limits.
func Compare(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return -1
	}
	if b == "" {
		return 1
	}
	av, bv := strings.Split(a, "."), strings.Split(b, ".")
	for i := range av {
		if len(av[i]) < len(bv[i]) {
			return -1
		}
		if len(av[i]) > len(bv[i]) {
			return 1
		}
		if c := strings.Compare(av[i], bv[i]); c != 0 {
			return c
		}
	}
	return 0
}
