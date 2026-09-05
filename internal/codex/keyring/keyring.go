// Package codexkeyring provides the process-scoped cryptographic boundary for
// Codex ownership identifiers and persisted provider cookies.
package codexkeyring

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"sync"
	"sync/atomic"
)

const (
	digestBytes        = sha256.Size
	aeadNonceBytes     = 12
	aeadCodecContext   = "switch-a/codex-aead/v1"
	aeadAssociatedData = "associated-data"
)

// HMACPurpose is closed so a caller cannot accidentally create an undomained
// digest for a new protocol surface.
type HMACPurpose string

const (
	HMACClientScope       HMACPurpose = "client-scope/v1"
	HMACCredentialSubject HMACPurpose = "credential-subject/v1"
	HMACOpaqueBinding     HMACPurpose = "opaque-binding/v1"
	HMACJarHandle         HMACPurpose = "jar-handle/v1"
)

var hmacPurposes = [...]HMACPurpose{
	HMACClientScope,
	HMACCredentialSubject,
	HMACOpaqueBinding,
	HMACJarHandle,
}

// AEADPurpose is distinct from HMACPurpose so authentication and encryption
// domains cannot be mixed through a shared string parameter.
type AEADPurpose string

const AEADCookieValue AEADPurpose = "cookie-value/v1"

var aeadPurposes = [...]AEADPurpose{AEADCookieValue}

// Digest includes the version needed to validate persisted ownership after a
// restart or rotation. Sum is fixed-width to prevent aliasing caller buffers.
type Digest struct {
	Version string
	Sum     [digestBytes]byte
}

// SealedValue contains storage-safe metadata, never the derived AEAD key.
type SealedValue struct {
	Version    string
	Nonce      []byte
	Ciphertext []byte
}

// Keyring stores derived purpose keys rather than the root material. Its API
// intentionally has no way to request issuance with a legacy version.
type Keyring struct {
	transferMu   sync.Mutex
	imported     atomic.Pointer[hmacImportState]
	hmacCurrent  string
	hmacVersions []string
	hmacKeys     map[HMACPurpose]map[string][digestBytes]byte
	aeadCurrent  string
	aeadVersions []string
	aeadKeys     map[AEADPurpose]map[string]cipher.AEAD
	random       io.Reader
}

// Parse validates a complete keyring document and constructs an immutable
// process-scoped keyring. random is injected so nonce failures are testable.
func Parse(data []byte, random io.Reader) (*Keyring, error) {
	if random == nil {
		return nil, errorOf(ErrorInvalidInput, "random", "", "random source is required", nil)
	}
	hmacRing, aeadRing, err := parseDocument(data)
	if err != nil {
		return nil, err
	}
	defer clearParsedRing(hmacRing)
	defer clearParsedRing(aeadRing)
	keyring := &Keyring{
		hmacCurrent:  hmacRing.current,
		hmacVersions: append([]string(nil), hmacRing.versions...),
		hmacKeys:     make(map[HMACPurpose]map[string][digestBytes]byte, len(hmacPurposes)),
		aeadCurrent:  aeadRing.current,
		aeadVersions: append([]string(nil), aeadRing.versions...),
		aeadKeys:     make(map[AEADPurpose]map[string]cipher.AEAD, len(aeadPurposes)),
		random:       random,
	}
	if err := keyring.deriveHMACKeys(hmacRing); err != nil {
		return nil, err
	}
	if err := keyring.deriveAEADKeys(aeadRing); err != nil {
		return nil, err
	}
	return keyring, nil
}

func (k *Keyring) deriveHMACKeys(ring parsedRing) error {
	for _, purpose := range hmacPurposes {
		versions := make(map[string][digestBytes]byte, len(ring.keys))
		for version, root := range ring.keys {
			derived, err := hkdf.Key(sha256.New, root[:], nil, string(purpose), digestBytes)
			if err != nil {
				return errorOf(ErrorInvalidDocument, "hmac", version, "could not derive purpose key", err)
			}
			var key [digestBytes]byte
			copy(key[:], derived)
			versions[version] = key
		}
		k.hmacKeys[purpose] = versions
	}
	return nil
}

func (k *Keyring) deriveAEADKeys(ring parsedRing) error {
	for _, purpose := range aeadPurposes {
		versions := make(map[string]cipher.AEAD, len(ring.keys))
		for version, root := range ring.keys {
			derived, err := hkdf.Key(sha256.New, root[:], nil, string(purpose), keyMaterialBytes)
			if err != nil {
				return errorOf(ErrorInvalidDocument, "aead", version, "could not derive purpose key", err)
			}
			block, err := aes.NewCipher(derived)
			if err != nil {
				return errorOf(ErrorInvalidDocument, "aead", version, "could not construct AES key", err)
			}
			aead, err := cipher.NewGCM(block)
			if err != nil {
				return errorOf(ErrorInvalidDocument, "aead", version, "could not construct AES-GCM", err)
			}
			versions[version] = aead
		}
		k.aeadKeys[purpose] = versions
	}
	return nil
}

// Sign creates a new identifier with only the current HMAC version.
func (k *Keyring) Sign(purpose HMACPurpose, input []byte) (Digest, error) {
	versions, ok := k.hmacKeys[purpose]
	if !ok {
		return Digest{}, errorOf(ErrorInvalidPurpose, "hmac", "", "unsupported HMAC purpose", nil)
	}
	return digestWithKey(k.hmacCurrent, versions[k.hmacCurrent], input), nil
}

// LookupDigests returns current then legacy candidates for durable lookup. The
// ordering makes the active generation observable without allowing legacy
// issuance through Sign.
func (k *Keyring) LookupDigests(purpose HMACPurpose, input []byte) ([]Digest, error) {
	state := k.hmacState()
	versions, ok := k.hmacKeys[purpose]
	if !ok {
		return nil, errorOf(ErrorInvalidPurpose, "hmac", "", "unsupported HMAC purpose", nil)
	}
	result := make([]Digest, 0, len(state.versions))
	result = append(result, digestWithKey(k.hmacCurrent, versions[k.hmacCurrent], input))
	for _, version := range state.versions {
		if version == k.hmacCurrent {
			continue
		}
		key, found := versions[version]
		if !found {
			key = state.keys[purpose][version]
		}
		result = append(result, digestWithKey(version, key, input))
	}
	return result, nil
}

func digestWithKey(version string, key [digestBytes]byte, input []byte) Digest {
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write(input)
	var sum [digestBytes]byte
	copy(sum[:], mac.Sum(nil))
	return Digest{Version: version, Sum: sum}
}

// Verify validates a stored versioned digest with current or legacy material.
func (k *Keyring) Verify(purpose HMACPurpose, input []byte, expected Digest) error {
	state := k.hmacState()
	versions, ok := k.hmacKeys[purpose]
	if !ok {
		return errorOf(ErrorInvalidPurpose, "hmac", "", "unsupported HMAC purpose", nil)
	}
	if expected.Version == "" {
		return errorOf(ErrorMissingVersion, "hmac", "", "digest key version is required", nil)
	}
	if !keyIDPattern.MatchString(expected.Version) {
		return errorOf(ErrorUnknownVersion, "hmac", "", "digest key version is invalid", nil)
	}
	key, ok := versions[expected.Version]
	if !ok {
		key, ok = state.keys[purpose][expected.Version]
	}
	if !ok {
		return errorOf(ErrorUnknownVersion, "hmac", expected.Version, "digest key version is unavailable", nil)
	}
	actual := digestWithKey(expected.Version, key, input)
	if subtle.ConstantTimeCompare(actual.Sum[:], expected.Sum[:]) != 1 {
		return errorOf(ErrorAuthenticationFailed, "hmac", expected.Version, "digest verification failed", nil)
	}
	return nil
}

// Seal encrypts with the current AEAD version and a fresh 96-bit nonce.
func (k *Keyring) Seal(purpose AEADPurpose, associatedContext, plaintext []byte) (SealedValue, error) {
	versions, ok := k.aeadKeys[purpose]
	if !ok {
		return SealedValue{}, errorOf(ErrorInvalidPurpose, "aead", "", "unsupported AEAD purpose", nil)
	}
	nonce := make([]byte, aeadNonceBytes)
	if _, err := io.ReadFull(k.random, nonce); err != nil {
		return SealedValue{}, errorOf(ErrorRandomSource, "aead", k.aeadCurrent, "could not generate nonce", err)
	}
	aad, err := encodeAAD(purpose, k.aeadCurrent, associatedContext)
	if err != nil {
		return SealedValue{}, err
	}
	ciphertext := versions[k.aeadCurrent].Seal(nil, nonce, plaintext, aad)
	return SealedValue{
		Version:    k.aeadCurrent,
		Nonce:      append([]byte(nil), nonce...),
		Ciphertext: append([]byte(nil), ciphertext...),
	}, nil
}

// Open decrypts current or legacy values. It never rewrites legacy ciphertext;
// the owning repository decides whether a successful merge warrants rotation.
func (k *Keyring) Open(purpose AEADPurpose, associatedContext []byte, sealed SealedValue) ([]byte, error) {
	versions, ok := k.aeadKeys[purpose]
	if !ok {
		return nil, errorOf(ErrorInvalidPurpose, "aead", "", "unsupported AEAD purpose", nil)
	}
	if sealed.Version == "" {
		return nil, errorOf(ErrorMissingVersion, "aead", "", "ciphertext key version is required", nil)
	}
	if !keyIDPattern.MatchString(sealed.Version) {
		return nil, errorOf(ErrorUnknownVersion, "aead", "", "ciphertext key version is invalid", nil)
	}
	aead, ok := versions[sealed.Version]
	if !ok {
		return nil, errorOf(ErrorUnknownVersion, "aead", sealed.Version, "ciphertext key version is unavailable", nil)
	}
	if len(sealed.Nonce) != aeadNonceBytes {
		return nil, errorOf(ErrorInvalidInput, "aead", sealed.Version, "nonce must be 96 bits", nil)
	}
	aad, err := encodeAAD(purpose, sealed.Version, associatedContext)
	if err != nil {
		return nil, err
	}
	plaintext, err := aead.Open(nil, sealed.Nonce, sealed.Ciphertext, aad)
	if err != nil {
		return nil, errorOf(ErrorAuthenticationFailed, "aead", sealed.Version, "ciphertext authentication failed", err)
	}
	return plaintext, nil
}

func encodeAAD(purpose AEADPurpose, version string, associatedContext []byte) ([]byte, error) {
	fields := [][]byte{
		[]byte(aeadCodecContext),
		[]byte(purpose),
		[]byte(version),
		[]byte(aeadAssociatedData),
		associatedContext,
	}
	total := 0
	for _, field := range fields {
		if uint64(len(field)) > math.MaxUint32 {
			return nil, errorOf(ErrorInvalidInput, "aead", version, "associated context is too large", nil)
		}
		total += 4 + len(field)
	}
	encoded := make([]byte, 0, total)
	var length [4]byte
	for _, field := range fields {
		binary.BigEndian.PutUint32(length[:], uint32(len(field)))
		encoded = append(encoded, length[:]...)
		encoded = append(encoded, field...)
	}
	return encoded, nil
}

// String and GoString prevent accidental general-purpose formatting from
// reflecting derived keys into logs or diagnostics.
func (*Keyring) String() string   { return "codex-keyring(redacted)" }
func (*Keyring) GoString() string { return "codex-keyring(redacted)" }

func (k *Keyring) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("codex keyring contains non-serializable secret material")
}
