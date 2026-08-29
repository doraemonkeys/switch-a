package store

import (
	"context"
	"fmt"
	"slices"
	"strings"

	continuitysqlite "github.com/doraemonkeys/switch-a/internal/codex/continuity/sqlite"
	providercookiesqlite "github.com/doraemonkeys/switch-a/internal/codex/cookie/sqlite"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
)

// CredentialSubjectRecord is the non-secret credential identity evidence used
// during bootstrap. Keeping the aggregate kind and auth state beside the
// subject prevents a syntactically valid subject from being interpreted under
// the wrong credential lifecycle.
type CredentialSubjectRecord struct {
	SessionID      string
	CredentialKind credentialsession.Kind
	Subject        credentialsession.Subject
	AuthStatus     credentialsession.AuthStatus
}

// CodexPersistenceInventory preserves each durable key family independently so
// keyring validation can report the exact family and missing generation.
type CodexPersistenceInventory struct {
	CredentialSubjects                []CredentialSubjectRecord
	CredentialHMACVersions            []string
	ContinuityHMACVersions            []string
	ProviderCookieHMACVersions        []string
	ProviderCookieAEADVersions        []string
	PendingStaticCredentialSessionIDs []string
	PendingChatGPTReauthSessionIDs    []string
}

func (inventory CodexPersistenceInventory) PendingStaticCredentialSubjectCount() int {
	return len(inventory.PendingStaticCredentialSessionIDs)
}

func (inventory CodexPersistenceInventory) PendingChatGPTReauthSubjectCount() int {
	return len(inventory.PendingChatGPTReauthSessionIDs)
}

// CodexRepositories exposes only the two domain repositories. The shared GORM
// handle remains owned by SQLiteStore and never leaks into protocol runtimes.
type CodexRepositories struct {
	Continuity      *continuitysqlite.Repository
	ProviderCookies *providercookiesqlite.Repository
}

// InspectCodexPersistence is the read-only startup capability boundary. Every
// credential subject is validated before an empty history can authorize
// keyring generation, then the independent continuity and Cookie schemas are
// inspected without merging their key requirements.
func (s *SQLiteStore) InspectCodexPersistence(ctx context.Context) (CodexPersistenceInventory, error) {
	if s == nil || s.db == nil {
		return CodexPersistenceInventory{}, fmt.Errorf("inspect Codex persistence: SQLite store is unavailable")
	}
	inventory, err := s.inspectCredentialSubjects(ctx)
	if err != nil {
		return CodexPersistenceInventory{}, err
	}
	continuity, err := continuitysqlite.Open(ctx, s.db)
	if err != nil {
		return CodexPersistenceInventory{}, fmt.Errorf("inspect Codex continuity schema: %w", err)
	}
	continuityHMAC, err := continuity.RequiredHMACVersions(ctx)
	if err != nil {
		return CodexPersistenceInventory{}, fmt.Errorf("inspect Codex continuity key versions: %w", err)
	}
	cookieHMAC, err := providercookiesqlite.RequiredHMACVersions(ctx, s.db)
	if err != nil {
		return CodexPersistenceInventory{}, fmt.Errorf("inspect provider-Cookie HMAC versions: %w", err)
	}
	cookieAEAD, err := providercookiesqlite.RequiredAEADVersions(ctx, s.db)
	if err != nil {
		return CodexPersistenceInventory{}, fmt.Errorf("inspect provider-Cookie AEAD versions: %w", err)
	}
	inventory.ContinuityHMACVersions = slices.Clone(continuityHMAC)
	inventory.ProviderCookieHMACVersions = slices.Clone(cookieHMAC)
	inventory.ProviderCookieAEADVersions = slices.Clone(cookieAEAD)
	return inventory, nil
}

func (s *SQLiteStore) inspectCredentialSubjects(ctx context.Context) (CodexPersistenceInventory, error) {
	var sessions []credentialsession.Session
	if err := s.db.WithContext(ctx).Order("id ASC").Find(&sessions).Error; err != nil {
		return CodexPersistenceInventory{}, fmt.Errorf("inspect credential subjects: %w", err)
	}

	inventory := CodexPersistenceInventory{
		CredentialSubjects: make([]CredentialSubjectRecord, 0, len(sessions)),
	}
	credentialVersions := make(map[string]struct{})
	for index := range sessions {
		session := &sessions[index]
		if err := session.Validate(); err != nil {
			return CodexPersistenceInventory{}, fmt.Errorf("inspect credential subject for session %q: %w", session.ID, err)
		}
		subject := session.Subject()
		if subject.Kind == credentialsession.SubjectKeyedDigest && strings.TrimSpace(subject.KeyVersion) != subject.KeyVersion {
			return CodexPersistenceInventory{}, fmt.Errorf(
				"inspect credential subject for session %q: %w: keyed subject version is not canonical",
				session.ID,
				credentialsession.ErrInvalidSession,
			)
		}
		inventory.CredentialSubjects = append(inventory.CredentialSubjects, CredentialSubjectRecord{
			SessionID:      session.ID,
			CredentialKind: session.Kind,
			Subject:        subject,
			AuthStatus:     session.AuthState.Status,
		})
		switch subject.Kind {
		case credentialsession.SubjectKeyedDigest:
			credentialVersions[subject.KeyVersion] = struct{}{}
		case credentialsession.SubjectPending:
			switch session.Kind {
			case credentialsession.KindAPIKey:
				inventory.PendingStaticCredentialSessionIDs = append(inventory.PendingStaticCredentialSessionIDs, session.ID)
			case credentialsession.KindChatGPT:
				inventory.PendingChatGPTReauthSessionIDs = append(inventory.PendingChatGPTReauthSessionIDs, session.ID)
			}
		}
	}
	for version := range credentialVersions {
		inventory.CredentialHMACVersions = append(inventory.CredentialHMACVersions, version)
	}
	slices.Sort(inventory.CredentialHMACVersions)
	return inventory, nil
}

// FinalizeStaticCredentialSubjects installs a signer only after all pending
// API-key subjects commit and a second complete inspection proves none remain.
// Pending ChatGPT rows are reauthentication state and deliberately survive.
func (s *SQLiteStore) FinalizeStaticCredentialSubjects(ctx context.Context, signer StaticCredentialSubjectSigner) error {
	if s == nil || s.db == nil || s.credentialSigning == nil {
		return fmt.Errorf("finalize static credential subjects: SQLite store is unavailable")
	}
	if signer == nil {
		return fmt.Errorf("finalize static credential subjects: signer is required")
	}

	s.credentialSigning.mu.Lock()
	defer s.credentialSigning.mu.Unlock()
	if _, err := s.InspectCodexPersistence(ctx); err != nil {
		return fmt.Errorf("finalize static credential subjects preflight: %w", err)
	}
	if err := finalizePendingStaticSubjects(s.db.WithContext(ctx), signer); err != nil {
		return fmt.Errorf("finalize static credential subjects: %w", err)
	}
	inventory, err := s.InspectCodexPersistence(ctx)
	if err != nil {
		return fmt.Errorf("finalize static credential subjects postcondition: %w", err)
	}
	if inventory.PendingStaticCredentialSubjectCount() != 0 {
		return fmt.Errorf(
			"finalize static credential subjects postcondition: %d pending static subjects remain",
			inventory.PendingStaticCredentialSubjectCount(),
		)
	}
	s.credentialSigning.signer = signer
	return nil
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
