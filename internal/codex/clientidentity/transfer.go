package clientidentity

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"gorm.io/gorm"
)

type Snapshot struct {
	Clients []Client   `json:"clients"`
	Aliases []KeyAlias `json:"aliases"`
}

func Export(ctx context.Context, db *gorm.DB) (Snapshot, error) {
	result := Snapshot{Clients: []Client{}, Aliases: []KeyAlias{}}
	if err := db.WithContext(ctx).Order("id").Find(&result.Clients).Error; err != nil {
		return Snapshot{}, err
	}
	err := db.WithContext(ctx).Order("version, digest").Find(&result.Aliases).Error
	return result, err
}
func Import(ctx context.Context, db *gorm.DB, snapshot Snapshot) error {
	tx := db.WithContext(ctx)
	for _, client := range snapshot.Clients {
		if client.ID == "" || client.CreatedAt.IsZero() {
			return fmt.Errorf("invalid client identity")
		}
		if _, err := decodeScope(client.PrimaryVersion, client.PrimaryDigest); err != nil {
			return err
		}
		var current Client
		err := tx.Take(&current, "id = ?", client.ID).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			if err := tx.Create(&client).Error; err != nil {
				return err
			}
		case err != nil:
			return err
		case !reflect.DeepEqual(current, client):
			return fmt.Errorf("%w: identity %s", ErrConflict, client.ID)
		}
	}
	for _, alias := range snapshot.Aliases {
		if _, err := decodeScope(alias.Version, alias.Digest); err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&Client{}).Where("id = ?", alias.ClientID).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("client alias references missing identity %s", alias.ClientID)
		}
		var current KeyAlias
		err := tx.Where("version = ? AND digest = ?", alias.Version, alias.Digest).Take(&current).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			if err := tx.Create(&alias).Error; err != nil {
				return err
			}
		case err != nil:
			return err
		case current.ClientID != alias.ClientID:
			return ErrConflict
		}
	}
	return validateCanonicalAliases(tx, snapshot.Clients)
}
func validateCanonicalAliases(tx *gorm.DB, clients []Client) error {
	for _, client := range clients {
		var alias KeyAlias
		if err := tx.Where("version = ? AND digest = ?", client.PrimaryVersion, client.PrimaryDigest).Take(&alias).Error; err != nil {
			return fmt.Errorf("canonical client scope has no key alias: %w", err)
		}
		if alias.ClientID != client.ID {
			return ErrConflict
		}
	}
	return nil
}
func RequiredHMACVersions(ctx context.Context, db *gorm.DB) ([]string, error) {
	var versions []string
	err := db.WithContext(ctx).Raw("SELECT primary_version FROM codex_client_identities UNION SELECT version FROM codex_client_key_aliases ORDER BY 1").Scan(&versions).Error
	return versions, err
}
