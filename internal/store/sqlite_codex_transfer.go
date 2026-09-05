package store

import (
	"context"
	"fmt"
	"reflect"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/codex/clientidentity"
	continuitysqlite "github.com/doraemonkeys/switch-a/internal/codex/continuity/sqlite"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	codexkeyring "github.com/doraemonkeys/switch-a/internal/codex/keyring"
	"github.com/doraemonkeys/switch-a/internal/model"
	"gorm.io/gorm"
)

const CodexStateVersion = 1

type CodexState struct {
	Sticky         []model.StickyEntry                `json:"sticky"`
	Version        int                                `json:"version"`
	Disguise       clientdisguise.Snapshot            `json:"disguise"`
	ClientIdentity clientidentity.Snapshot            `json:"client_identity"`
	Continuity     []continuitysqlite.TransferBinding `json:"continuity"`
	HMAC           []codexkeyring.HMACMaterial        `json:"keyring_hmac"`
}

func (s *SQLiteStore) ClientIdentityResolver(digester clientidentity.ScopeDigester) (*clientidentity.Resolver, error) {
	return clientidentity.NewWithConfig(clientidentity.Config{DB: s.db, Digester: digester, Now: s.clock.Now})
}
func (s *SQLiteStore) LoadCodexHMAC(ctx context.Context) ([]codexkeyring.HMACMaterial, error) {
	rows := []codexkeyring.HMACMaterial{}
	err := s.db.WithContext(ctx).Order("version, purpose").Find(&rows).Error
	return rows, err
}
func (s *SQLiteStore) InstallCodexKeyring(ctx context.Context, keyring *codexkeyring.Keyring) error {
	material, err := s.LoadCodexHMAC(ctx)
	if err != nil {
		return err
	}
	if err := keyring.WithHMACImport(material, nil); err != nil {
		return err
	}
	s.codexKeyring = keyring
	return nil
}
func (s *SQLiteStore) ExportCodexState(ctx context.Context) (*CodexState, error) {
	result := &CodexState{Version: CodexStateVersion}
	if s.codexKeyring != nil {
		result.HMAC = s.codexKeyring.ExportHMAC()
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		result.Disguise, err = s.ClientDisguiseRepository().WithDB(tx).Export(ctx)
		if err != nil {
			return err
		}
		result.ClientIdentity, err = clientidentity.Export(ctx, tx)
		if err != nil {
			return err
		}
		result.Continuity, err = continuitysqlite.Export(ctx, tx)
		if err != nil {
			return err
		}
		result.Sticky, err = (&SQLiteStore{db: tx, clock: s.clock}).LoadStickyEntries(ctx, s.clock.Now())
		if err != nil {
			return err
		}
		result.Sticky = codexStickyEntries(result.Sticky)
		result.HMAC, err = exportDurableCodexKeys(ctx, tx, result.HMAC)
		return err
	})
	if err != nil {
		return nil, err
	}
	if s.codexKeyring == nil {
		if len(result.ClientIdentity.Clients)+len(result.Disguise.Logins)+len(result.Disguise.LoginHistory)+len(result.Continuity) > 0 {
			return nil, fmt.Errorf("portable Codex state requires initialized keyring")
		}
		return nil, nil
	}
	return result, nil
}
func (s *CachedStore) ExportCodexState(ctx context.Context) (*CodexState, error) {
	if source, ok := s.Store.(interface {
		ExportCodexState(context.Context) (*CodexState, error)
	}); ok {
		return source.ExportCodexState(ctx)
	}
	return nil, nil
}
func importCodexState(ctx context.Context, tx *gorm.DB, state *CodexState) error {
	if state == nil {
		return nil
	}
	if state.Version != CodexStateVersion {
		return fmt.Errorf("unsupported Codex state version %d", state.Version)
	}
	if err := clientidentity.Import(ctx, tx, state.ClientIdentity); err != nil {
		return err
	}
	if err := clientdisguise.NewRepository(tx).Import(ctx, state.Disguise); err != nil {
		return err
	}
	if err := continuitysqlite.Import(ctx, tx, state.Continuity); err != nil {
		return err
	}
	for _, material := range state.HMAC {
		var current codexkeyring.HMACMaterial
		err := tx.Where("version = ? AND purpose = ?", material.Version, material.Purpose).FirstOrCreate(&current, material).Error
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(current, material) {
			return fmt.Errorf("portable key material conflict")
		}
	}
	return nil
}
func validateCodexStateReferences(ctx context.Context, tx *gorm.DB, state *CodexState) error {
	if state == nil {
		return nil
	}
	if err := validateImportedLogins(tx, state.Disguise.Logins); err != nil {
		return err
	}
	for _, mapping := range state.Disguise.Mappings {
		var count int64
		if err := tx.Model(&clientidentity.Client{}).Where("id = ?", mapping.ClientIdentityID).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("mapping references missing client %s", mapping.ClientIdentityID)
		}
		if err := tx.Model(&clientdisguise.LoginIdentity{}).Where("generation_id = ?", mapping.GenerationID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			if err := tx.Model(&clientdisguise.LoginHistory{}).Where("generation_id = ?", mapping.GenerationID).Count(&count).Error; err != nil {
				return err
			}
		}
		if count != 1 {
			return fmt.Errorf("mapping references missing generation %s", mapping.GenerationID)
		}
	}
	for _, reference := range state.Disguise.References {
		var count int64
		if err := tx.Model(&clientidentity.Client{}).Where("id = ?", reference.ClientIdentityID).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("reference source refers to missing client %s", reference.ClientIdentityID)
		}
	}
	return nil
}

func validateImportedLogins(tx *gorm.DB, logins []clientdisguise.LoginIdentity) error {
	for _, login := range logins {
		var count int64
		if err := tx.Table("credential_sessions").Where("id = ?", login.CredentialSessionID).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("login identity references missing credential %s", login.CredentialSessionID)
		}
		var session credentialsession.Session
		if err := tx.First(&session, "id = ?", login.CredentialSessionID).Error; err != nil {
			return err
		}
		subject := session.Subject()
		basis := clientdisguise.AccountBasis{Kind: string(subject.Kind), Value: subject.Value, KeyVersion: subject.KeyVersion}
		if subject.Resolved() && !basis.Equal(login.AccountBasis) {
			return fmt.Errorf("restored login account conflicts with verified credential %s", login.CredentialSessionID)
		}
		var stored clientdisguise.LoginIdentity
		if err := tx.First(&stored, "credential_session_id = ?", login.CredentialSessionID).Error; err != nil {
			return err
		}
		if stored.GenerationID != login.GenerationID || !stored.AccountBasis.Equal(login.AccountBasis) {
			return fmt.Errorf("restored login identity changed during credential import")
		}
	}
	return nil
}
