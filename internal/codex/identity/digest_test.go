package codexidentity

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/clientcredential"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/keyring"
)

func TestDigesterDomainSeparationAndRotation(t *testing.T) {
	old := mustDigester(t, "h1", map[string]byte{"h1": 1})
	rotated := mustDigester(t, "h2", map[string]byte{"h1": 1, "h2": 2})
	raw := []byte("same-secret-input")

	oldScope, err := old.ClientScope(raw)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := rotated.ClientScopeCandidates(raw)
	if err != nil || len(candidates) != 2 {
		t.Fatalf("ClientScopeCandidates() = %#v, %v", candidates, err)
	}
	if candidates[0].KeyVersion() != "h2" || candidates[1].KeyVersion() != "h1" || !candidates[1].Equal(oldScope) {
		t.Fatalf("rotated candidates = %#v, old = %#v", candidates, oldScope)
	}

	newClientScope, err := rotated.ClientScope([]byte("rotated-client-key"))
	if err != nil {
		t.Fatal(err)
	}
	if newClientScope.Equal(candidates[0]) {
		t.Fatal("raw client key rotation preserved ClientScope")
	}

	staticSubject, err := rotated.StaticCredentialSubject("openai", credentialsession.KindAPIKey, string(raw))
	if err != nil {
		t.Fatal(err)
	}
	_, staticDigest, ok := staticSubject.KeyedDigest()
	if !ok {
		t.Fatalf("static subject = %#v", staticSubject)
	}
	opaque, err := rotated.OpaqueDigest(OpaqueTurnState, raw)
	if err != nil {
		t.Fatal(err)
	}
	if candidates[0].Digest() == staticDigest || candidates[0].Digest() == opaque.Digest() || staticDigest == opaque.Digest() {
		t.Fatal("HMAC domains produced an equal digest")
	}
	otherNamespace, err := rotated.OpaqueDigest(OpaqueTurnMetadata, raw)
	if err != nil || otherNamespace.Digest() == opaque.Digest() {
		t.Fatalf("opaque namespace separation = %#v, %v", otherNamespace, err)
	}
	lookup, err := rotated.OpaqueDigestCandidates(OpaqueTurnState, raw)
	if err != nil || len(lookup) != 2 || !lookup[0].Equal(opaque) || lookup[1].KeyVersion() != "h1" {
		t.Fatalf("OpaqueDigestCandidates() = %#v, %v", lookup, err)
	}
}

func TestDigestContractsValidateInputsAndEncodeVersions(t *testing.T) {
	digester := mustDigester(t, "h1", map[string]byte{"h1": 1})
	for _, raw := range [][]byte{nil, {}, bytes.Repeat([]byte{'x'}, clientcredential.MaxClientCredentialBytes+1)} {
		if _, err := digester.ClientScope(raw); !IsError(err, ErrorInvalidInput) {
			t.Fatalf("ClientScope(len=%d) error = %v", len(raw), err)
		}
		if _, err := digester.ClientScopeCandidates(raw); !IsError(err, ErrorInvalidInput) {
			t.Fatalf("ClientScopeCandidates(len=%d) error = %v", len(raw), err)
		}
	}
	if _, err := NewDigester(nil); !IsError(err, ErrorInvalidInput) {
		t.Fatalf("NewDigester(nil) error = %v", err)
	}
	var nilKeyring *codexkeyring.Keyring
	if _, err := NewDigester(nilKeyring); !IsError(err, ErrorInvalidInput) {
		t.Fatalf("NewDigester(typed nil) error = %v", err)
	}
	for _, version := range []string{"", "with space", strings.Repeat("x", 33)} {
		if _, err := ClientScopeFromDigest(version, [DigestSize]byte{}); !IsError(err, ErrorInvalidInput) {
			t.Fatalf("ClientScopeFromDigest(%q) error = %v", version, err)
		}
		if _, err := NewKeyedCredentialSubject(version, [DigestSize]byte{}); !IsError(err, ErrorInvalidInput) {
			t.Fatalf("NewKeyedCredentialSubject(%q) error = %v", version, err)
		}
	}
	for _, namespace := range []string{"", " whitespace", "line\nbreak", strings.Repeat("x", MaxOpaqueNamespaceBytes+1)} {
		if _, err := NewOpaqueNamespace(namespace); !IsError(err, ErrorInvalidInput) {
			t.Fatalf("NewOpaqueNamespace(%q) error = %v", namespace, err)
		}
	}
	custom, err := NewOpaqueNamespace("session-identity/thread-id")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := digester.OpaqueDigest(custom, nil); !IsError(err, ErrorInvalidInput) {
		t.Fatalf("OpaqueDigest(empty) error = %v", err)
	}
	if _, err := OpaqueDigestFromParts("bad namespace", "h1", [DigestSize]byte{}); !IsError(err, ErrorInvalidInput) {
		t.Fatalf("OpaqueDigestFromParts(bad namespace) error = %v", err)
	}
	if _, err := OpaqueDigestFromParts(custom, "bad version", [DigestSize]byte{}); !IsError(err, ErrorInvalidInput) {
		t.Fatalf("OpaqueDigestFromParts(bad version) error = %v", err)
	}

	scope, err := digester.ClientScope([]byte("client"))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := scope.MarshalBinary()
	if err != nil || !bytes.Contains(encoded, []byte(clientScopeCodec)) || !bytes.Contains(encoded, []byte("h1")) {
		t.Fatalf("ClientScope.MarshalBinary() = %x, %v", encoded, err)
	}
	opaque, err := digester.OpaqueDigest(custom, []byte("opaque"))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = opaque.MarshalBinary()
	if err != nil || !bytes.Contains(encoded, []byte(opaqueBindingCodec)) || opaque.Namespace() != custom {
		t.Fatalf("OpaqueDigest.MarshalBinary() = %x, %v", encoded, err)
	}
	if _, err := (ClientScope{}).MarshalBinary(); !IsError(err, ErrorInvalidInput) {
		t.Fatalf("zero ClientScope MarshalBinary error = %v", err)
	}
	if _, err := (OpaqueDigest{}).MarshalBinary(); !IsError(err, ErrorInvalidInput) {
		t.Fatalf("zero OpaqueDigest MarshalBinary error = %v", err)
	}
}

func TestDigesterFailsClosedOnSignerFailures(t *testing.T) {
	failing, err := NewDigester(stubHMAC{err: errors.New("keyring unavailable")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := failing.ClientScope([]byte("key")); !IsError(err, ErrorDigestUnavailable) {
		t.Fatalf("ClientScope signer error = %v", err)
	}
	if _, err := failing.ClientScopeCandidates([]byte("key")); !IsError(err, ErrorDigestUnavailable) {
		t.Fatalf("ClientScopeCandidates signer error = %v", err)
	}
	if _, err := failing.OpaqueDigest(OpaqueTurnState, []byte("state")); !IsError(err, ErrorDigestUnavailable) {
		t.Fatalf("OpaqueDigest signer error = %v", err)
	}
	if _, err := failing.OpaqueDigestCandidates(OpaqueTurnState, []byte("state")); !IsError(err, ErrorDigestUnavailable) {
		t.Fatalf("OpaqueDigestCandidates signer error = %v", err)
	}
	if _, err := failing.StaticCredentialSubject("openai", credentialsession.KindAPIKey, "key"); !IsError(err, ErrorDigestUnavailable) {
		t.Fatalf("StaticCredentialSubject signer error = %v", err)
	}

	empty, err := NewDigester(stubHMAC{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := empty.ClientScopeCandidates([]byte("key")); !IsError(err, ErrorDigestUnavailable) {
		t.Fatalf("empty ClientScopeCandidates error = %v", err)
	}
	if _, err := empty.OpaqueDigestCandidates(OpaqueTurnState, []byte("state")); !IsError(err, ErrorDigestUnavailable) {
		t.Fatalf("empty OpaqueDigestCandidates error = %v", err)
	}
	if _, err := empty.StaticCredentialSubject("openai", credentialsession.KindChatGPT, "key"); !IsError(err, ErrorInvalidInput) {
		t.Fatalf("invalid StaticCredentialSubject error = %v", err)
	}
}

func TestIdentityValuesAreLogSafe(t *testing.T) {
	digester := mustDigester(t, "h1", map[string]byte{"h1": 1})
	rawClient := "raw-client-secret-never-log"
	scope, err := digester.ClientScope([]byte(rawClient))
	if err != nil {
		t.Fatal(err)
	}
	opaqueValue := "opaque-state-never-log"
	opaque, err := digester.OpaqueDigest(OpaqueTurnState, []byte(opaqueValue))
	if err != nil {
		t.Fatal(err)
	}
	accountID := "account-never-log"
	subject, err := NewAccountCredentialSubject(accountID)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{scope, opaque, subject} {
		formatted := []string{fmt.Sprint(value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value)}
		encoded, jsonErr := json.Marshal(value)
		if jsonErr != nil {
			t.Fatal(jsonErr)
		}
		formatted = append(formatted, string(encoded))
		for _, output := range formatted {
			for _, forbidden := range []string{rawClient, opaqueValue, accountID, hex.EncodeToString(scope.sum[:]), hex.EncodeToString(opaque.sum[:])} {
				if strings.Contains(output, forbidden) {
					t.Fatalf("formatted identity leaked %q in %q", forbidden, output)
				}
			}
		}
	}
}

type stubHMAC struct {
	err error
}

func (s stubHMAC) Sign(codexkeyring.HMACPurpose, []byte) (codexkeyring.Digest, error) {
	return codexkeyring.Digest{}, s.err
}
func (s stubHMAC) LookupDigests(codexkeyring.HMACPurpose, []byte) ([]codexkeyring.Digest, error) {
	return nil, s.err
}

func mustDigester(t *testing.T, current string, versions map[string]byte) Digester {
	t.Helper()
	hmacKeys := make(map[string]string, len(versions))
	for version, marker := range versions {
		hmacKeys[version] = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{marker}, 32))
	}
	document := struct {
		SchemaVersion int `json:"schema_version"`
		HMAC          struct {
			Current string            `json:"current"`
			Keys    map[string]string `json:"keys"`
		} `json:"hmac"`
		AEAD struct {
			Current string            `json:"current"`
			Keys    map[string]string `json:"keys"`
		} `json:"aead"`
	}{SchemaVersion: 1}
	document.HMAC.Current = current
	document.HMAC.Keys = hmacKeys
	document.AEAD.Current = "a1"
	document.AEAD.Keys = map[string]string{"a1": base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{99}, 32))}
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := codexkeyring.Parse(data, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	digester, err := NewDigester(keyring)
	if err != nil {
		t.Fatal(err)
	}
	return digester
}
