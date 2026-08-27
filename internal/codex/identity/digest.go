package codexidentity

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/codex/clientcredential"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/keyring"
)

const (
	DigestSize              = sha256.Size
	MaxOpaqueNamespaceBytes = 128
	clientScopeCodec        = "codex-client-scope/v1"
	opaqueBindingCodec      = "codex-opaque-binding/v1"
)

var keyVersionPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)

// HMACDigester is the consumer-owned boundary implemented by codexkeyring.
// Keeping the purpose parameter in this interface makes cross-domain reuse
// visible in reviews and tests.
type HMACDigester interface {
	Sign(purpose codexkeyring.HMACPurpose, input []byte) (codexkeyring.Digest, error)
	LookupDigests(purpose codexkeyring.HMACPurpose, input []byte) ([]codexkeyring.Digest, error)
}

// Digester creates current identifiers and current-plus-legacy lookup
// candidates. It never retains any raw client credential or opaque value.
type Digester struct {
	hmac HMACDigester
}

func NewDigester(hmac HMACDigester) (Digester, error) {
	if isNilInterface(hmac) {
		return Digester{}, errorOf(ErrorInvalidInput, "hmac", "digester is required", nil)
	}
	return Digester{hmac: hmac}, nil
}

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

type ClientScope struct {
	version string
	sum     [DigestSize]byte
}

func ClientScopeFromDigest(version string, sum [DigestSize]byte) (ClientScope, error) {
	if !keyVersionPattern.MatchString(version) {
		return ClientScope{}, errorOf(ErrorInvalidInput, "key_version", "version is invalid", nil)
	}
	return ClientScope{version: version, sum: sum}, nil
}

func (s ClientScope) KeyVersion() string           { return s.version }
func (s ClientScope) Digest() [DigestSize]byte     { return s.sum }
func (s ClientScope) Equal(other ClientScope) bool { return s == other }

func (s ClientScope) MarshalBinary() ([]byte, error) {
	if s.version == "" {
		return nil, errorOf(ErrorInvalidInput, "client_scope", "scope is uninitialized", nil)
	}
	return encodeFields(clientScopeCodec, []byte(s.version), s.sum[:])
}

func (s ClientScope) String() string {
	return fmt.Sprintf("client-scope(version=%s,digest=redacted)", safeVersion(s.version))
}

func (s ClientScope) GoString() string { return s.String() }

func (s ClientScope) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		KeyVersion string `json:"key_version"`
		Digest     string `json:"digest"`
	}{KeyVersion: safeVersion(s.version), Digest: "redacted"})
}

func (d Digester) ClientScope(rawCredential []byte) (ClientScope, error) {
	if err := validateClientCredential(rawCredential); err != nil {
		return ClientScope{}, err
	}
	digest, err := d.hmac.Sign(codexkeyring.HMACClientScope, rawCredential)
	if err != nil {
		return ClientScope{}, errorOf(ErrorDigestUnavailable, "client_scope", "could not sign client scope", err)
	}
	return clientScopeFromKeyring(digest)
}

func (d Digester) ClientScopeCandidates(rawCredential []byte) ([]ClientScope, error) {
	if err := validateClientCredential(rawCredential); err != nil {
		return nil, err
	}
	digests, err := d.hmac.LookupDigests(codexkeyring.HMACClientScope, rawCredential)
	if err != nil {
		return nil, errorOf(ErrorDigestUnavailable, "client_scope", "could not derive lookup candidates", err)
	}
	if len(digests) == 0 {
		return nil, errorOf(ErrorDigestUnavailable, "client_scope", "no lookup candidate was produced", nil)
	}
	result := make([]ClientScope, 0, len(digests))
	for _, digest := range digests {
		scope, convertErr := clientScopeFromKeyring(digest)
		if convertErr != nil {
			return nil, convertErr
		}
		result = append(result, scope)
	}
	return result, nil
}

func validateClientCredential(raw []byte) error {
	if len(raw) == 0 {
		return errorOf(ErrorInvalidInput, "client_credential", "credential is empty", nil)
	}
	if len(raw) > clientcredential.MaxClientCredentialBytes {
		return errorOf(ErrorInvalidInput, "client_credential", "credential exceeds the size limit", nil)
	}
	return nil
}

func clientScopeFromKeyring(digest codexkeyring.Digest) (ClientScope, error) {
	return ClientScopeFromDigest(digest.Version, digest.Sum)
}

// OpaqueNamespace distinguishes field categories before applying the already
// separate opaque-binding HMAC purpose.
type OpaqueNamespace string

const (
	OpaqueTurnState         OpaqueNamespace = "turn-state"
	OpaqueTurnMetadata      OpaqueNamespace = "turn-metadata"
	OpaqueSessionIdentity   OpaqueNamespace = "session-identity"
	OpaqueResponseReference OpaqueNamespace = "response-reference"
)

func NewOpaqueNamespace(value string) (OpaqueNamespace, error) {
	if value == "" || value != strings.TrimSpace(value) || len(value) > MaxOpaqueNamespaceBytes {
		return "", errorOf(ErrorInvalidInput, "opaque_namespace", "namespace is empty, non-canonical, or too large", nil)
	}
	for _, char := range value {
		if char < 0x21 || char > 0x7e {
			return "", errorOf(ErrorInvalidInput, "opaque_namespace", "namespace must contain printable ASCII", nil)
		}
	}
	return OpaqueNamespace(value), nil
}

type OpaqueDigest struct {
	namespace OpaqueNamespace
	version   string
	sum       [DigestSize]byte
}

func OpaqueDigestFromParts(namespace OpaqueNamespace, version string, sum [DigestSize]byte) (OpaqueDigest, error) {
	if _, err := NewOpaqueNamespace(string(namespace)); err != nil {
		return OpaqueDigest{}, err
	}
	if !keyVersionPattern.MatchString(version) {
		return OpaqueDigest{}, errorOf(ErrorInvalidInput, "key_version", "version is invalid", nil)
	}
	return OpaqueDigest{namespace: namespace, version: version, sum: sum}, nil
}

func (d OpaqueDigest) Namespace() OpaqueNamespace    { return d.namespace }
func (d OpaqueDigest) KeyVersion() string            { return d.version }
func (d OpaqueDigest) Digest() [DigestSize]byte      { return d.sum }
func (d OpaqueDigest) Equal(other OpaqueDigest) bool { return d == other }

func (d OpaqueDigest) MarshalBinary() ([]byte, error) {
	if d.namespace == "" || d.version == "" {
		return nil, errorOf(ErrorInvalidInput, "opaque_digest", "digest is uninitialized", nil)
	}
	return encodeFields(opaqueBindingCodec, []byte(d.namespace), []byte(d.version), d.sum[:])
}

func (d OpaqueDigest) String() string {
	return fmt.Sprintf(
		"opaque-digest(namespace=%s,version=%s,digest=redacted)",
		safeNamespace(d.namespace),
		safeVersion(d.version),
	)
}

func (d OpaqueDigest) GoString() string { return d.String() }

func (d OpaqueDigest) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Namespace  string `json:"namespace"`
		KeyVersion string `json:"key_version"`
		Digest     string `json:"digest"`
	}{Namespace: safeNamespace(d.namespace), KeyVersion: safeVersion(d.version), Digest: "redacted"})
}

func (d Digester) OpaqueDigest(namespace OpaqueNamespace, opaque []byte) (OpaqueDigest, error) {
	input, err := opaqueInput(namespace, opaque)
	if err != nil {
		return OpaqueDigest{}, err
	}
	digest, err := d.hmac.Sign(codexkeyring.HMACOpaqueBinding, input)
	if err != nil {
		return OpaqueDigest{}, errorOf(ErrorDigestUnavailable, "opaque", "could not sign opaque value", err)
	}
	return OpaqueDigestFromParts(namespace, digest.Version, digest.Sum)
}

func (d Digester) OpaqueDigestCandidates(namespace OpaqueNamespace, opaque []byte) ([]OpaqueDigest, error) {
	input, err := opaqueInput(namespace, opaque)
	if err != nil {
		return nil, err
	}
	digests, err := d.hmac.LookupDigests(codexkeyring.HMACOpaqueBinding, input)
	if err != nil {
		return nil, errorOf(ErrorDigestUnavailable, "opaque", "could not derive lookup candidates", err)
	}
	if len(digests) == 0 {
		return nil, errorOf(ErrorDigestUnavailable, "opaque", "no lookup candidate was produced", nil)
	}
	result := make([]OpaqueDigest, 0, len(digests))
	for _, digest := range digests {
		candidate, convertErr := OpaqueDigestFromParts(namespace, digest.Version, digest.Sum)
		if convertErr != nil {
			return nil, convertErr
		}
		result = append(result, candidate)
	}
	return result, nil
}

func opaqueInput(namespace OpaqueNamespace, opaque []byte) ([]byte, error) {
	if _, err := NewOpaqueNamespace(string(namespace)); err != nil {
		return nil, err
	}
	if len(opaque) == 0 {
		return nil, errorOf(ErrorInvalidInput, "opaque", "value is empty", nil)
	}
	return encodeFields(opaqueBindingCodec, []byte(namespace), opaque)
}

func (d Digester) StaticCredentialSubject(vendor string, kind credentialsession.Kind, secret string) (CredentialSubject, error) {
	input, err := credentialsession.StaticSubjectInput(vendor, kind, secret)
	if err != nil {
		return CredentialSubject{}, errorOf(ErrorInvalidInput, "static_credential", "canonical input is invalid", err)
	}
	digest, err := d.hmac.Sign(codexkeyring.HMACCredentialSubject, input)
	if err != nil {
		return CredentialSubject{}, errorOf(ErrorDigestUnavailable, "credential_subject", "could not sign static subject", err)
	}
	return NewKeyedCredentialSubject(digest.Version, digest.Sum)
}

func safeVersion(version string) string {
	if !keyVersionPattern.MatchString(version) {
		return "invalid"
	}
	return version
}

func safeNamespace(namespace OpaqueNamespace) string {
	if _, err := NewOpaqueNamespace(string(namespace)); err != nil {
		return "invalid"
	}
	return string(namespace)
}
