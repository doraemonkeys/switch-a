package store

import (
	"context"
	"fmt"
	"slices"

	continuitysqlite "github.com/doraemonkeys/switch-a/internal/codex/continuity/sqlite"
	providercookiesqlite "github.com/doraemonkeys/switch-a/internal/codex/cookie/sqlite"
)

// CodexKeyVersions is the complete key-generation footprint of the two
// independent Codex persistence families. Credential-subject generations stay
// separate because they belong to the CredentialSession aggregate.
type CodexKeyVersions struct {
	HMAC []string
	AEAD []string
}

// CodexRepositories exposes only the two domain repositories. The shared GORM
// handle remains owned by SQLiteStore and never leaks into protocol runtimes.
type CodexRepositories struct {
	Continuity      *continuitysqlite.Repository
	ProviderCookies *providercookiesqlite.Repository
}

// InspectCodexPersistence is the read-only startup capability boundary. Both
// schemas are validated before any listener can rely on their key references.
func (s *SQLiteStore) InspectCodexPersistence(ctx context.Context) (CodexKeyVersions, error) {
	if s == nil || s.db == nil {
		return CodexKeyVersions{}, fmt.Errorf("inspect Codex persistence: SQLite store is unavailable")
	}
	continuity, err := continuitysqlite.Open(ctx, s.db)
	if err != nil {
		return CodexKeyVersions{}, fmt.Errorf("inspect Codex continuity schema: %w", err)
	}
	continuityHMAC, err := continuity.RequiredHMACVersions(ctx)
	if err != nil {
		return CodexKeyVersions{}, fmt.Errorf("inspect Codex continuity key versions: %w", err)
	}
	cookieHMAC, err := providercookiesqlite.RequiredHMACVersions(ctx, s.db)
	if err != nil {
		return CodexKeyVersions{}, fmt.Errorf("inspect provider-Cookie HMAC versions: %w", err)
	}
	cookieAEAD, err := providercookiesqlite.RequiredAEADVersions(ctx, s.db)
	if err != nil {
		return CodexKeyVersions{}, fmt.Errorf("inspect provider-Cookie AEAD versions: %w", err)
	}
	return CodexKeyVersions{
		HMAC: mergeKeyVersions(continuityHMAC, cookieHMAC),
		AEAD: mergeKeyVersions(cookieAEAD),
	}, nil
}

// OpenCodexRepositories composes repositories only after startup preflight has
// proved that every persisted key generation is available. Schema migration is
// deliberately absent from this method so construction cannot conceal ordering
// or partial-migration failures.
func (s *SQLiteStore) OpenCodexRepositories(
	ctx context.Context,
	cipher providercookiesqlite.ValueCipher,
) (CodexRepositories, error) {
	if s == nil || s.db == nil {
		return CodexRepositories{}, fmt.Errorf("open Codex repositories: SQLite store is unavailable")
	}
	continuity, err := continuitysqlite.Open(ctx, s.db)
	if err != nil {
		return CodexRepositories{}, fmt.Errorf("open Codex continuity repository: %w", err)
	}
	cookies, err := providercookiesqlite.Open(ctx, providercookiesqlite.Config{
		DB:     s.db,
		Cipher: cipher,
	})
	if err != nil {
		return CodexRepositories{}, fmt.Errorf("open provider-Cookie repository: %w", err)
	}
	return CodexRepositories{Continuity: continuity, ProviderCookies: cookies}, nil
}

func mergeKeyVersions(groups ...[]string) []string {
	unique := make(map[string]struct{})
	for _, group := range groups {
		for _, version := range group {
			// Empty persisted versions must reach keyring validation and fail closed;
			// aggregation must not sanitize corrupt storage into an empty requirement.
			unique[version] = struct{}{}
		}
	}
	versions := make([]string, 0, len(unique))
	for version := range unique {
		versions = append(versions, version)
	}
	slices.Sort(versions)
	return versions
}
