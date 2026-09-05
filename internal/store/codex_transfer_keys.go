package store

import (
	"context"
	"fmt"

	codexkeyring "github.com/doraemonkeys/switch-a/internal/codex/keyring"
	"gorm.io/gorm"
)

func validateCodexTransferKeys(state *CodexState, keyring *codexkeyring.Keyring) error {
	versions := []string{}
	for _, client := range state.ClientIdentity.Clients {
		versions = append(versions, client.PrimaryVersion)
	}
	for _, alias := range state.ClientIdentity.Aliases {
		versions = append(versions, alias.Version)
	}
	for _, login := range state.Disguise.Logins {
		if login.AccountBasis.KeyVersion != "" {
			versions = append(versions, login.AccountBasis.KeyVersion)
		}
	}
	for _, history := range state.Disguise.LoginHistory {
		if history.Identity.AccountBasis.KeyVersion != "" {
			versions = append(versions, history.Identity.AccountBasis.KeyVersion)
		}
	}
	for _, owner := range state.Continuity {
		versions = append(versions, owner.OpaqueKeyVersion, owner.ClientKeyVersion)
		if owner.ProtocolSubjectKeyVersion != nil {
			versions = append(versions, *owner.ProtocolSubjectKeyVersion)
		}
	}
	return codexkeyring.ValidateCapabilities(keyring, codexkeyring.Requirements{HMACVersions: versions})
}
func exportDurableCodexKeys(ctx context.Context, tx *gorm.DB, current []codexkeyring.HMACMaterial) ([]codexkeyring.HMACMaterial, error) {
	var durable []codexkeyring.HMACMaterial
	if err := tx.WithContext(ctx).Order("version, purpose").Find(&durable).Error; err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	result := append([]codexkeyring.HMACMaterial(nil), current...)
	for _, item := range current {
		seen[fmt.Sprintf("%s/%s", item.Version, item.Purpose)] = true
	}
	for _, item := range durable {
		key := fmt.Sprintf("%s/%s", item.Version, item.Purpose)
		if !seen[key] {
			result = append(result, item)
			seen[key] = true
		}
	}
	return result, nil
}
