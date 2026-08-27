package codexkeyring

import "slices"

// Capabilities is a non-secret projection suitable for startup validation and
// structured logging. Versions identify key generations, never key material.
type Capabilities struct {
	HMACVersions []string
	AEADVersions []string
	HMACCurrent  string
	AEADCurrent  string
}

// Requirements describes the key generations referenced by enabled features
// and durable rows. Empty requirements permit a missing keyring, which preserves
// the ADR's disabled-feature startup behavior.
type Requirements struct {
	NeedHMAC     bool
	NeedAEAD     bool
	HMACVersions []string
	AEADVersions []string
}

func (k *Keyring) Capabilities() Capabilities {
	if k == nil {
		return Capabilities{}
	}
	return Capabilities{
		HMACVersions: append([]string(nil), k.hmacVersions...),
		AEADVersions: append([]string(nil), k.aeadVersions...),
		HMACCurrent:  k.hmacCurrent,
		AEADCurrent:  k.aeadCurrent,
	}
}

// ValidateCapabilities fails closed before listeners start when an enabled
// feature or persisted row requires unavailable key material.
func ValidateCapabilities(keyring *Keyring, required Requirements) error {
	capabilities := keyring.Capabilities()
	if required.NeedHMAC && capabilities.HMACCurrent == "" {
		return errorOf(ErrorCapabilityMissing, "hmac", "", "enabled feature requires a current HMAC key", nil)
	}
	if required.NeedAEAD && capabilities.AEADCurrent == "" {
		return errorOf(ErrorCapabilityMissing, "aead", "", "enabled feature requires a current AEAD key", nil)
	}
	for _, version := range required.HMACVersions {
		if version == "" {
			return errorOf(ErrorMissingVersion, "hmac", "", "required HMAC version is empty", nil)
		}
		if !keyIDPattern.MatchString(version) {
			return errorOf(ErrorCapabilityMissing, "hmac", "", "required HMAC key version is invalid", nil)
		}
		if !slices.Contains(capabilities.HMACVersions, version) {
			return errorOf(ErrorCapabilityMissing, "hmac", version, "required HMAC key version is unavailable", nil)
		}
	}
	for _, version := range required.AEADVersions {
		if version == "" {
			return errorOf(ErrorMissingVersion, "aead", "", "required AEAD version is empty", nil)
		}
		if !keyIDPattern.MatchString(version) {
			return errorOf(ErrorCapabilityMissing, "aead", "", "required AEAD key version is invalid", nil)
		}
		if !slices.Contains(capabilities.AEADVersions, version) {
			return errorOf(ErrorCapabilityMissing, "aead", version, "required AEAD key version is unavailable", nil)
		}
	}
	return nil
}
