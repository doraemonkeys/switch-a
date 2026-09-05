package store

import (
	"context"
	"errors"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	codexkeyring "github.com/doraemonkeys/switch-a/internal/codex/keyring"
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
