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

// Requirements describes explicit current-key and historical-generation checks.
// The file lifecycle uses HistoricalVersions and always requires both current
// capabilities; this lower-level shape remains useful for component preflight.
type Requirements struct {
	NeedHMAC     bool
	NeedAEAD     bool
	HMACVersions []string
	AEADVersions []string
}

// HistoricalVersions is the durable key history discovered before the
// keyring file is opened. Keeping it separate from capability switches makes
// the no-history prerequisite for first-run generation explicit.
type HistoricalVersions struct {
	HMAC []string
	AEAD []string
}

func (versions HistoricalVersions) hasReferences() bool {
	return len(versions.HMAC) != 0 || len(versions.AEAD) != 0
}

func (k *Keyring) Capabilities() Capabilities {
	if k == nil {
		return Capabilities{}
	}
	state := k.hmacState()
	return Capabilities{
		HMACVersions: append([]string(nil), state.versions...),
		AEADVersions: append([]string(nil), k.aeadVersions...),
		HMACCurrent:  k.hmacCurrent,
		AEADCurrent:  k.aeadCurrent,
	}
}

// ValidateCapabilities reports unavailable current or historical key material.
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

func validateCompleteCapabilities(keyring *Keyring, historical HistoricalVersions) error {
	return ValidateCapabilities(keyring, Requirements{
		NeedHMAC:     true,
		NeedAEAD:     true,
		HMACVersions: historical.HMAC,
		AEADVersions: historical.AEAD,
	})
}
