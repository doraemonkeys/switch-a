package codexkeyring

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestKeyringCurrentLegacyRotationAndRestart(t *testing.T) {
	oldDocument := testDocument(t, "h1", "a1")
	oldKeyring := parseTestKeyring(t, oldDocument, bytes.NewReader(bytes.Repeat([]byte{1}, 256)))
	oldDigest, err := oldKeyring.Sign(HMACCredentialSubject, []byte("static credential"))
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	oldCiphertext, err := oldKeyring.Seal(AEADCookieValue, []byte("jar/authority/cookie"), []byte("secret cookie"))
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}

	restarted := parseTestKeyring(t, oldDocument, bytes.NewReader(bytes.Repeat([]byte{2}, 256)))
	restartedDigest, err := restarted.Sign(HMACCredentialSubject, []byte("static credential"))
	if err != nil {
		t.Fatalf("restarted Sign() error = %v", err)
	}
	if restartedDigest != oldDigest {
		t.Fatalf("digest changed across restart: got %x, want %x", restartedDigest.Sum, oldDigest.Sum)
	}
	plaintext, err := restarted.Open(AEADCookieValue, []byte("jar/authority/cookie"), oldCiphertext)
	if err != nil || string(plaintext) != "secret cookie" {
		t.Fatalf("restarted Open() = %q, %v", plaintext, err)
	}

	rotatedDocument := testDocument(t, "h2", "a2")
	rotated := parseTestKeyring(t, rotatedDocument, bytes.NewReader(bytes.Repeat([]byte{3}, 256)))
	newDigest, err := rotated.Sign(HMACCredentialSubject, []byte("static credential"))
	if err != nil {
		t.Fatalf("rotated Sign() error = %v", err)
	}
	if newDigest.Version != "h2" || newDigest == oldDigest {
		t.Fatalf("rotated digest = %+v, want a distinct h2 digest", newDigest)
	}
	if err := rotated.Verify(HMACCredentialSubject, []byte("static credential"), oldDigest); err != nil {
		t.Fatalf("legacy Verify() error = %v", err)
	}
	candidates, err := rotated.LookupDigests(HMACCredentialSubject, []byte("static credential"))
	if err != nil {
		t.Fatalf("LookupDigests() error = %v", err)
	}
	if got := versionsOf(candidates); !slices.Equal(got, []string{"h2", "h1"}) {
		t.Fatalf("candidate versions = %v, want current then legacy", got)
	}
	legacyPlaintext, err := rotated.Open(AEADCookieValue, []byte("jar/authority/cookie"), oldCiphertext)
	if err != nil || string(legacyPlaintext) != "secret cookie" {
		t.Fatalf("legacy Open() = %q, %v", legacyPlaintext, err)
	}
	newCiphertext, err := rotated.Seal(AEADCookieValue, []byte("jar/authority/cookie"), []byte("new cookie"))
	if err != nil {
		t.Fatalf("rotated Seal() error = %v", err)
	}
	if newCiphertext.Version != "a2" {
		t.Fatalf("new ciphertext version = %q, want a2", newCiphertext.Version)
	}
}

func TestHMACPurposesAreDomainSeparated(t *testing.T) {
	keyring := parseTestKeyring(t, testDocument(t, "h2", "a2"), bytes.NewReader(bytes.Repeat([]byte{4}, 256)))
	seen := make(map[[digestBytes]byte]HMACPurpose)
	for _, purpose := range hmacPurposes {
		digest, err := keyring.Sign(purpose, []byte("same input"))
		if err != nil {
			t.Fatalf("Sign(%q) error = %v", purpose, err)
		}
		if previous, duplicate := seen[digest.Sum]; duplicate {
			t.Fatalf("purposes %q and %q produced the same digest", previous, purpose)
		}
		seen[digest.Sum] = purpose
	}
}

func TestHMACVerificationFailuresAreTyped(t *testing.T) {
	keyring := parseTestKeyring(t, testDocument(t, "h2", "a2"), bytes.NewReader(bytes.Repeat([]byte{5}, 256)))
	digest, err := keyring.Sign(HMACOpaqueBinding, []byte("binding"))
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}

	tests := []struct {
		name     string
		purpose  HMACPurpose
		input    []byte
		digest   Digest
		wantKind ErrorKind
	}{
		{name: "invalid purpose", purpose: HMACPurpose("other/v1"), digest: digest, wantKind: ErrorInvalidPurpose},
		{name: "missing version", purpose: HMACOpaqueBinding, digest: Digest{Sum: digest.Sum}, wantKind: ErrorMissingVersion},
		{name: "unknown version", purpose: HMACOpaqueBinding, digest: Digest{Version: "h9", Sum: digest.Sum}, wantKind: ErrorUnknownVersion},
		{name: "invalid version", purpose: HMACOpaqueBinding, digest: Digest{Version: testMaterial(1), Sum: digest.Sum}, wantKind: ErrorUnknownVersion},
		{name: "wrong input", purpose: HMACOpaqueBinding, input: []byte("other"), digest: digest, wantKind: ErrorAuthenticationFailed},
		{name: "modified digest", purpose: HMACOpaqueBinding, input: []byte("binding"), digest: modifiedDigest(digest), wantKind: ErrorAuthenticationFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := keyring.Verify(test.purpose, test.input, test.digest)
			if !IsError(err, test.wantKind) {
				t.Fatalf("Verify() error = %v, want kind %q", err, test.wantKind)
			}
		})
	}

	if _, err := keyring.Sign(HMACPurpose("other/v1"), nil); !IsError(err, ErrorInvalidPurpose) {
		t.Fatalf("Sign(invalid) error = %v", err)
	}
	if _, err := keyring.LookupDigests(HMACPurpose("other/v1"), nil); !IsError(err, ErrorInvalidPurpose) {
		t.Fatalf("LookupDigests(invalid) error = %v", err)
	}
}

func TestAEADAuthenticatesVersionedPurposeAndContext(t *testing.T) {
	keyring := parseTestKeyring(t, testDocument(t, "h2", "a2"), bytes.NewReader(bytes.Repeat([]byte{6}, 256)))
	context := []byte("jar|vendor|origin|subject|cookie-key")
	sealed, err := keyring.Seal(AEADCookieValue, context, []byte("cookie value"))
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	plaintext, err := keyring.Open(AEADCookieValue, context, sealed)
	if err != nil || string(plaintext) != "cookie value" {
		t.Fatalf("Open() = %q, %v", plaintext, err)
	}

	modifiedCiphertext := cloneSealed(sealed)
	modifiedCiphertext.Ciphertext[0] ^= 1
	modifiedNonce := cloneSealed(sealed)
	modifiedNonce.Nonce[0] ^= 1
	tests := []struct {
		name     string
		purpose  AEADPurpose
		context  []byte
		sealed   SealedValue
		wantKind ErrorKind
	}{
		{name: "invalid purpose", purpose: AEADPurpose("other/v1"), context: context, sealed: sealed, wantKind: ErrorInvalidPurpose},
		{name: "missing version", purpose: AEADCookieValue, context: context, sealed: SealedValue{Nonce: sealed.Nonce, Ciphertext: sealed.Ciphertext}, wantKind: ErrorMissingVersion},
		{name: "unknown version", purpose: AEADCookieValue, context: context, sealed: SealedValue{Version: "a9", Nonce: sealed.Nonce, Ciphertext: sealed.Ciphertext}, wantKind: ErrorUnknownVersion},
		{name: "invalid version", purpose: AEADCookieValue, context: context, sealed: SealedValue{Version: testMaterial(1), Nonce: sealed.Nonce, Ciphertext: sealed.Ciphertext}, wantKind: ErrorUnknownVersion},
		{name: "bad nonce length", purpose: AEADCookieValue, context: context, sealed: SealedValue{Version: sealed.Version, Nonce: sealed.Nonce[:1], Ciphertext: sealed.Ciphertext}, wantKind: ErrorInvalidInput},
		{name: "changed context", purpose: AEADCookieValue, context: []byte("other authority"), sealed: sealed, wantKind: ErrorAuthenticationFailed},
		{name: "changed nonce", purpose: AEADCookieValue, context: context, sealed: modifiedNonce, wantKind: ErrorAuthenticationFailed},
		{name: "changed ciphertext", purpose: AEADCookieValue, context: context, sealed: modifiedCiphertext, wantKind: ErrorAuthenticationFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := keyring.Open(test.purpose, test.context, test.sealed)
			if !IsError(err, test.wantKind) {
				t.Fatalf("Open() error = %v, want kind %q", err, test.wantKind)
			}
		})
	}
	if _, err := keyring.Seal(AEADPurpose("other/v1"), nil, nil); !IsError(err, ErrorInvalidPurpose) {
		t.Fatalf("Seal(invalid) error = %v", err)
	}
}

func TestSealUsesFreshNonceAndDoesNotAliasResults(t *testing.T) {
	nonces := append(bytes.Repeat([]byte{7}, aeadNonceBytes), bytes.Repeat([]byte{8}, aeadNonceBytes)...)
	keyring := parseTestKeyring(t, testDocument(t, "h2", "a2"), bytes.NewReader(nonces))
	first, err := keyring.Seal(AEADCookieValue, []byte("context"), []byte("same"))
	if err != nil {
		t.Fatalf("first Seal() error = %v", err)
	}
	second, err := keyring.Seal(AEADCookieValue, []byte("context"), []byte("same"))
	if err != nil {
		t.Fatalf("second Seal() error = %v", err)
	}
	if bytes.Equal(first.Nonce, second.Nonce) || bytes.Equal(first.Ciphertext, second.Ciphertext) {
		t.Fatal("separate seals reused nonce or ciphertext")
	}
	first.Nonce[0] ^= 1
	first.Ciphertext[0] ^= 1
	plaintext, err := keyring.Open(AEADCookieValue, []byte("context"), second)
	if err != nil || string(plaintext) != "same" {
		t.Fatalf("mutating one result affected another: %q, %v", plaintext, err)
	}
}

func TestSealRandomFailureIsTyped(t *testing.T) {
	keyring := parseTestKeyring(t, testDocument(t, "h2", "a2"), errorReader{})
	_, err := keyring.Seal(AEADCookieValue, nil, nil)
	if !IsError(err, ErrorRandomSource) || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Seal() error = %v, want typed wrapped random failure", err)
	}
}

func TestKeyringOperationsAreConcurrentSafe(t *testing.T) {
	keyring := parseTestKeyring(t, testDocument(t, "h2", "a2"), &lockedReader{reader: bytes.NewReader(bytes.Repeat([]byte{9}, 4096))})
	const workers = 16
	var wait sync.WaitGroup
	errorsSeen := make(chan error, workers)
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			input := []byte(fmt.Sprintf("value-%d", index))
			digest, err := keyring.Sign(HMACJarHandle, input)
			if err == nil {
				err = keyring.Verify(HMACJarHandle, input, digest)
			}
			sealed := SealedValue{}
			if err == nil {
				sealed, err = keyring.Seal(AEADCookieValue, input, input)
			}
			if err == nil {
				_, err = keyring.Open(AEADCookieValue, input, sealed)
			}
			errorsSeen <- err
		}(worker)
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatalf("concurrent operation error = %v", err)
		}
	}
}

func TestKeyringFormattingAndSerializationCannotExposeMaterial(t *testing.T) {
	document := testDocument(t, "h2", "a2")
	keyring := parseTestKeyring(t, document, bytes.NewReader(bytes.Repeat([]byte{10}, 256)))
	for _, formatted := range []string{fmt.Sprint(keyring), fmt.Sprintf("%#v", keyring)} {
		if formatted != "codex-keyring(redacted)" {
			t.Fatalf("formatted keyring = %q", formatted)
		}
		for _, secret := range []string{testMaterial(1), testMaterial(11)} {
			if strings.Contains(formatted, secret) {
				t.Fatal("formatted keyring contains encoded root material")
			}
		}
	}
	if encoded, err := json.Marshal(keyring); err == nil || encoded != nil {
		t.Fatalf("json.Marshal() = %q, %v, want refusal", encoded, err)
	}
}

func TestParseRequiresRandomSource(t *testing.T) {
	_, err := Parse([]byte(testDocument(t, "h2", "a2")), nil)
	if !IsError(err, ErrorInvalidInput) {
		t.Fatalf("Parse(nil random) error = %v", err)
	}
}

func versionsOf(digests []Digest) []string {
	versions := make([]string, 0, len(digests))
	for _, digest := range digests {
		versions = append(versions, digest.Version)
	}
	return versions
}

func modifiedDigest(digest Digest) Digest {
	digest.Sum[0] ^= 1
	return digest
}

func cloneSealed(sealed SealedValue) SealedValue {
	return SealedValue{
		Version:    sealed.Version,
		Nonce:      append([]byte(nil), sealed.Nonce...),
		Ciphertext: append([]byte(nil), sealed.Ciphertext...),
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type lockedReader struct {
	mu     sync.Mutex
	reader io.Reader
}

func (r *lockedReader) Read(target []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reader.Read(target)
}
