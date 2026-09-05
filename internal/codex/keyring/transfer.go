package codexkeyring

import (
	"fmt"
	"maps"
	"slices"
)

// HMACMaterial transfers purpose-derived keys. Cookie encryption roots are
// intentionally outside this ownership snapshot.
type HMACMaterial struct {
	Version string      `json:"version" gorm:"primaryKey"`
	Purpose HMACPurpose `json:"purpose" gorm:"primaryKey"`
	Key     []byte      `json:"key"`
}

func (HMACMaterial) TableName() string { return "codex_portable_hmac_keys" }

func (k *Keyring) ExportHMAC() []HMACMaterial {
	state := k.hmacState()
	result := make([]HMACMaterial, 0, len(hmacPurposes)*len(state.versions))
	for _, version := range state.versions {
		for _, purpose := range hmacPurposes {
			key, ok := k.hmacKeys[purpose][version]
			if !ok {
				key = state.keys[purpose][version]
			}
			result = append(result, HMACMaterial{Version: version, Purpose: purpose, Key: append([]byte(nil), key[:]...)})
		}
	}
	return result
}

// WithHMACImport serializes lookup publication with durable commit. The current
// issuance keys remain immutable, so credential creation inside commit can Sign.
func (k *Keyring) WithHMACImport(material []HMACMaterial, commit func(*Keyring) error) error {
	k.transferMu.Lock()
	defer k.transferMu.Unlock()
	state := k.hmacState()
	candidate := make(map[HMACPurpose]map[string][digestBytes]byte, len(hmacPurposes))
	for _, purpose := range hmacPurposes {
		candidate[purpose] = make(map[string][digestBytes]byte)
		maps.Copy(candidate[purpose], state.keys[purpose])
	}
	versions := slices.Clone(state.versions)
	for _, item := range material {
		if !keyIDPattern.MatchString(item.Version) || !slices.Contains(hmacPurposes[:], item.Purpose) || len(item.Key) != digestBytes {
			return fmt.Errorf("invalid portable HMAC material")
		}
		var key [digestBytes]byte
		copy(key[:], item.Key)
		existing, found := k.hmacKeys[item.Purpose][item.Version]
		if !found {
			existing, found = candidate[item.Purpose][item.Version]
		}
		if found && existing != key {
			return fmt.Errorf("portable HMAC conflict for %s/%s", item.Version, item.Purpose)
		}
		if !found {
			candidate[item.Purpose][item.Version] = key
		}
		if !slices.Contains(versions, item.Version) {
			versions = append(versions, item.Version)
		}
	}
	for _, version := range versions {
		for _, purpose := range hmacPurposes {
			if _, ok := k.hmacKeys[purpose][version]; ok {
				continue
			}
			if _, ok := candidate[purpose][version]; !ok {
				return fmt.Errorf("portable HMAC version %s is incomplete", version)
			}
		}
	}
	slices.Sort(versions)
	next := &hmacImportState{keys: candidate, versions: versions}
	if commit != nil {
		view := &Keyring{hmacCurrent: k.hmacCurrent, hmacVersions: k.hmacVersions, hmacKeys: k.hmacKeys, aeadCurrent: k.aeadCurrent, aeadVersions: k.aeadVersions, aeadKeys: k.aeadKeys, random: k.random}
		view.imported.Store(next)
		if err := commit(view); err != nil {
			return err
		}
	}
	k.imported.Store(next)
	return nil
}

type hmacImportState struct {
	versions []string
	keys     map[HMACPurpose]map[string][digestBytes]byte
}

func (k *Keyring) hmacState() *hmacImportState {
	if state := k.imported.Load(); state != nil {
		return state
	}
	return &hmacImportState{versions: k.hmacVersions}
}
