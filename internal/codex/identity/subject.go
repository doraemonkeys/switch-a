package codexidentity

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
)

const credentialSubjectCodec = "codex-credential-subject/v1"

type CredentialSubjectKind string

const (
	CredentialSubjectAccount     CredentialSubjectKind = "account"
	CredentialSubjectKeyedDigest CredentialSubjectKind = "keyed-digest"
)

type CredentialSubject struct {
	kind       CredentialSubjectKind
	account    string
	keyVersion string
	digest     [DigestSize]byte
}

func NewAccountCredentialSubject(accountID string) (CredentialSubject, error) {
	subject, err := credentialsession.AccountSubject(accountID)
	if err != nil {
		return CredentialSubject{}, errorOf(ErrorInvalidInput, "credential_subject", "account subject is invalid", err)
	}
	return CredentialSubjectFromSession(subject)
}

func NewKeyedCredentialSubject(version string, digest [DigestSize]byte) (CredentialSubject, error) {
	if !keyVersionPattern.MatchString(version) {
		return CredentialSubject{}, errorOf(ErrorInvalidInput, "key_version", "version is invalid", nil)
	}
	return CredentialSubject{kind: CredentialSubjectKeyedDigest, keyVersion: version, digest: digest}, nil
}

func CredentialSubjectFromSession(subject credentialsession.Subject) (CredentialSubject, error) {
	if err := subject.Validate(); err != nil {
		return CredentialSubject{}, errorOf(ErrorInvalidInput, "credential_subject", "session subject is invalid", err)
	}
	switch subject.Kind {
	case credentialsession.SubjectPending:
		return CredentialSubject{}, errorOf(ErrorSubjectPending, "credential_subject", "credential subject is unresolved", credentialsession.ErrSubjectPending)
	case credentialsession.SubjectAccount:
		accountID := string(subject.Value)
		if accountID != strings.TrimSpace(accountID) {
			return CredentialSubject{}, errorOf(ErrorInvalidInput, "credential_subject", "account subject is non-canonical", nil)
		}
		return CredentialSubject{kind: CredentialSubjectAccount, account: accountID}, nil
	case credentialsession.SubjectKeyedDigest:
		var digest [DigestSize]byte
		copy(digest[:], subject.Value)
		return NewKeyedCredentialSubject(subject.KeyVersion, digest)
	default:
		return CredentialSubject{}, errorOf(ErrorInvalidInput, "credential_subject", "subject kind is unsupported", nil)
	}
}

func (s CredentialSubject) Kind() CredentialSubjectKind { return s.kind }
func (s CredentialSubject) AccountID() (string, bool) {
	return s.account, s.kind == CredentialSubjectAccount
}
func (s CredentialSubject) KeyedDigest() (version string, digest [DigestSize]byte, ok bool) {
	return s.keyVersion, s.digest, s.kind == CredentialSubjectKeyedDigest
}
func (s CredentialSubject) Equal(other CredentialSubject) bool { return s == other }

func (s CredentialSubject) validate() error {
	switch s.kind {
	case CredentialSubjectAccount:
		if s.account == "" || s.keyVersion != "" || s.digest != [DigestSize]byte{} {
			return errorOf(ErrorInvalidInput, "credential_subject", "account subject is invalid", nil)
		}
	case CredentialSubjectKeyedDigest:
		if s.account != "" || !keyVersionPattern.MatchString(s.keyVersion) {
			return errorOf(ErrorInvalidInput, "credential_subject", "keyed subject is invalid", nil)
		}
	default:
		return errorOf(ErrorInvalidInput, "credential_subject", "subject is uninitialized", nil)
	}
	return nil
}

func (s CredentialSubject) MarshalBinary() ([]byte, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if s.kind == CredentialSubjectAccount {
		return encodeFields(credentialSubjectCodec, []byte(s.kind), []byte(s.account))
	}
	return encodeFields(credentialSubjectCodec, []byte(s.kind), []byte(s.keyVersion), s.digest[:])
}

func (s CredentialSubject) String() string {
	return fmt.Sprintf("credential-subject(kind=%s,value=redacted)", safeSubjectKind(s.kind))
}

func (s CredentialSubject) GoString() string { return s.String() }

func (s CredentialSubject) MarshalJSON() ([]byte, error) {
	keyVersion := ""
	if s.kind == CredentialSubjectKeyedDigest {
		keyVersion = safeVersion(s.keyVersion)
	}
	return json.Marshal(struct {
		Kind       string `json:"kind"`
		KeyVersion string `json:"key_version,omitempty"`
		Value      string `json:"value"`
	}{Kind: safeSubjectKind(s.kind), KeyVersion: keyVersion, Value: "redacted"})
}

func safeSubjectKind(kind CredentialSubjectKind) string {
	if kind != CredentialSubjectAccount && kind != CredentialSubjectKeyedDigest {
		return "invalid"
	}
	return string(kind)
}
