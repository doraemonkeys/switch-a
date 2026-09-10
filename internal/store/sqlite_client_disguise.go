package store

import (
	"context"
	"errors"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise/officialversion"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	codexkeyring "github.com/doraemonkeys/switch-a/internal/codex/keyring"
	"github.com/doraemonkeys/switch-a/internal/defaults"
	"github.com/doraemonkeys/switch-a/internal/providerauth/accountclient"
	"gorm.io/gorm"
)

func (s *SQLiteStore) ClientDisguiseRepository() *clientdisguise.Repository {
	return clientdisguise.NewRepository(s.db)
}
func (s *CachedStore) ClientDisguiseRepository() *clientdisguise.Repository {
	if source, ok := s.Store.(interface {
		ClientDisguiseRepository() *clientdisguise.Repository
	}); ok {
		return source.ClientDisguiseRepository()
	}
	return nil
}

// ResolveAccountClientPolicy reads the choice and release as one snapshot so a
// concurrent settings edit cannot change an account operation halfway through.
func (s *SQLiteStore) ResolveAccountClientPolicy(ctx context.Context) (accountclient.Policy, error) {
	var policy accountclient.Policy
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		mode, err := (&SQLiteStore{db: tx}).GetConfig(ctx, defaults.ConfigKeyGPTAccountFallbackClient)
		if err != nil {
			return err
		}
		policy.FallbackClient = accountclient.FallbackClient(mode)
		if policy.FallbackClient == accountclient.FallbackOfficialStable {
			state, err := clientdisguise.NewRepository(tx).OfficialVersion(ctx)
			policy.OfficialVersion = state.Release
			return err
		}
		return nil
	})
	return policy, err
}

// OfficialVersionRepository counts global account requests as release followers,
// even before the first credential has a client-disguise binding.
type OfficialVersionRepository struct {
	*clientdisguise.Repository
	settings *SQLiteStore
}

func (s *SQLiteStore) OfficialVersionRepository() *OfficialVersionRepository {
	return &OfficialVersionRepository{Repository: s.ClientDisguiseRepository(), settings: s}
}

func (r *OfficialVersionRepository) HasOfficialVersionFollowers(ctx context.Context) (bool, error) {
	mode, err := r.settings.GetConfig(ctx, defaults.ConfigKeyGPTAccountFallbackClient)
	if err != nil {
		return false, err
	}
	if accountclient.FallbackClient(mode) == accountclient.FallbackOfficialStable {
		return true, nil
	}
	return r.Repository.HasOfficialVersionFollowers(ctx)
}

var _ officialversion.Store = (*OfficialVersionRepository)(nil)

// A static credential imported alongside its identity keeps its verified source
// subject. Re-signing it with a destination key would change conversation authority.
func preserveRestoredStaticSubject(ctx context.Context, db *gorm.DB, session *credentialsession.Session, signer StaticCredentialSubjectSigner) error {
	if session.Kind != credentialsession.KindAPIKey {
		return nil
	}
	login, err := clientdisguise.NewRepository(db).GetLogin(ctx, session.ID)
	if errors.Is(err, clientdisguise.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if login.AccountBasis.Kind != string(credentialsession.SubjectKeyedDigest) {
		return nil
	}
	verifier, ok := signer.(interface {
		Verify(codexkeyring.HMACPurpose, []byte, codexkeyring.Digest) error
	})
	if !ok {
		return nil
	}
	input, err := credentialsession.StaticSubjectInput(session.Kind, session.SecretData)
	if err != nil {
		return err
	}
	var sum [32]byte
	copy(sum[:], login.AccountBasis.Value)
	if err := verifier.Verify(codexkeyring.HMACCredentialSubject, input, codexkeyring.Digest{Version: login.AccountBasis.KeyVersion, Sum: sum}); err != nil {
		return err
	}
	return session.SetSubject(credentialsession.Subject{Kind: credentialsession.SubjectKeyedDigest, KeyVersion: login.AccountBasis.KeyVersion, Value: append([]byte(nil), login.AccountBasis.Value...)})
}
func initializeDisguiseLogins(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var sessions []credentialsession.Session
		if err := tx.Find(&sessions).Error; err != nil {
			return err
		}
		for _, session := range sessions {
			if err := syncDisguiseLogin(ctx, tx, session.ID, session.Subject()); err != nil {
				return err
			}
		}
		return nil
	})
}
func syncDisguiseLogin(ctx context.Context, db *gorm.DB, sessionID string, subject credentialsession.Subject) error {
	basis := clientdisguise.AccountBasis{Kind: string(subject.Kind), Value: append([]byte(nil), subject.Value...), KeyVersion: subject.KeyVersion}
	_, err := clientdisguise.NewRepository(db).SyncLoginAccount(ctx, sessionID, basis)
	// A restored, unauthenticated placeholder keeps any historical account basis.
	if errors.Is(err, clientdisguise.ErrNotFound) && !subject.Resolved() {
		return nil
	}
	return err
}
