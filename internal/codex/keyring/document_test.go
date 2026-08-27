package codexkeyring

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestParseRejectsInvalidDocuments(t *testing.T) {
	valid := testDocument(t, "h2", "a2")
	tests := []struct {
		name     string
		document string
	}{
		{name: "empty", document: ""},
		{name: "invalid json", document: "{"},
		{name: "trailing json", document: valid + `{}`},
		{name: "unknown schema", document: strings.Replace(valid, `"schema_version":1`, `"schema_version":2`, 1)},
		{name: "unknown field", document: strings.Replace(valid, `"schema_version":1`, `"schema_version":1,"secret":"no"`, 1)},
		{name: "duplicate top-level field", document: strings.Replace(valid, `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1)},
		{name: "duplicate nested key", document: strings.Replace(valid, `"h1":"`+testMaterial(1)+`"`, `"h1":"`+testMaterial(1)+`","h1":"`+testMaterial(2)+`"`, 1)},
		{name: "secret-looking duplicate field", document: strings.Replace(valid, `"schema_version":1`, `"schema_version":1,"`+testMaterial(1)+`":{},"`+testMaterial(1)+`":{}`, 1)},
		{name: "array in schema", document: `{"schema_version":[],"hmac":{},"aead":{}}`},
		{name: "missing hmac current", document: strings.Replace(valid, `"hmac":{"current":"h2",`, `"hmac":{"current":"",`, 1)},
		{name: "missing aead keys", document: strings.Replace(valid, `"aead":{"current":"a2","keys":{`, `"aead":{"current":"a2","keys":{"ignored":"","remove":`, 1)},
		{name: "unknown current", document: strings.Replace(valid, `"current":"h2"`, `"current":"h9"`, 1)},
		{name: "invalid current id", document: strings.Replace(valid, `"current":"h2"`, `"current":"`+testMaterial(1)+`"`, 1)},
		{name: "invalid key id", document: strings.Replace(valid, `"h1"`, `"bad key"`, 1)},
		{name: "too long key id", document: strings.Replace(valid, `"h1"`, `"abcdefghijklmnopqrstuvwxyz1234567"`, 1)},
		{name: "padded material", document: strings.Replace(valid, testMaterial(1), testMaterial(1)+"=", 1)},
		{name: "short material", document: strings.Replace(valid, testMaterial(1), base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 31)), 1)},
		{name: "invalid base64", document: strings.Replace(valid, testMaterial(1), "not+base64", 1)},
		{name: "duplicate id across rings", document: strings.Replace(valid, `"a1"`, `"h1"`, 1)},
		{name: "duplicate material across rings", document: strings.Replace(valid, testMaterial(11), testMaterial(1), 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse([]byte(test.document), bytes.NewReader(bytes.Repeat([]byte{1}, 64)))
			if !IsError(err, ErrorInvalidDocument) {
				t.Fatalf("Parse() error = %v, want invalid document", err)
			}
			for _, material := range []string{testMaterial(1), testMaterial(2), testMaterial(11)} {
				if strings.Contains(err.Error(), material) {
					t.Fatalf("error contains key material: %v", err)
				}
			}
		})
	}

	invalidUTF8 := append([]byte(`{"schema_version":1,"hmac":{"current":"`), 0xff)
	if _, err := Parse(invalidUTF8, bytes.NewReader(nil)); !IsError(err, ErrorInvalidDocument) {
		t.Fatalf("Parse(invalid UTF-8) error = %v", err)
	}
}

func TestCapabilitiesAreImmutableAndValidateRequirements(t *testing.T) {
	keyring := parseTestKeyring(t, testDocument(t, "h2", "a2"), bytes.NewReader(bytes.Repeat([]byte{2}, 64)))
	capabilities := keyring.Capabilities()
	if capabilities.HMACCurrent != "h2" || capabilities.AEADCurrent != "a2" {
		t.Fatalf("Capabilities() = %+v", capabilities)
	}
	capabilities.HMACVersions[0] = "modified"
	if keyring.Capabilities().HMACVersions[0] == "modified" {
		t.Fatal("Capabilities() exposed mutable internal version storage")
	}

	tests := []struct {
		name     string
		keyring  *Keyring
		required Requirements
		wantKind ErrorKind
	}{
		{name: "disabled no keyring"},
		{name: "enabled hmac no keyring", required: Requirements{NeedHMAC: true}, wantKind: ErrorCapabilityMissing},
		{name: "enabled aead no keyring", required: Requirements{NeedAEAD: true}, wantKind: ErrorCapabilityMissing},
		{name: "unknown hmac", keyring: keyring, required: Requirements{HMACVersions: []string{"h9"}}, wantKind: ErrorCapabilityMissing},
		{name: "invalid hmac", keyring: keyring, required: Requirements{HMACVersions: []string{testMaterial(1)}}, wantKind: ErrorCapabilityMissing},
		{name: "unknown aead", keyring: keyring, required: Requirements{AEADVersions: []string{"a9"}}, wantKind: ErrorCapabilityMissing},
		{name: "invalid aead", keyring: keyring, required: Requirements{AEADVersions: []string{testMaterial(1)}}, wantKind: ErrorCapabilityMissing},
		{name: "empty hmac version", keyring: keyring, required: Requirements{HMACVersions: []string{""}}, wantKind: ErrorMissingVersion},
		{name: "empty aead version", keyring: keyring, required: Requirements{AEADVersions: []string{""}}, wantKind: ErrorMissingVersion},
		{name: "all available", keyring: keyring, required: Requirements{NeedHMAC: true, NeedAEAD: true, HMACVersions: []string{"h1", "h2"}, AEADVersions: []string{"a1", "a2"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateCapabilities(test.keyring, test.required)
			if test.wantKind == "" && err != nil {
				t.Fatalf("ValidateCapabilities() error = %v", err)
			}
			if test.wantKind != "" && !IsError(err, test.wantKind) {
				t.Fatalf("ValidateCapabilities() error = %v, want kind %q", err, test.wantKind)
			}
		})
	}
}

func TestErrorClassificationDoesNotDependOnText(t *testing.T) {
	cause := invalidDocument("hmac", "h1", "reason")
	wrapped := errorOf(ErrorInvalidDocument, "document", "", "outer", cause)
	if !IsError(wrapped, ErrorInvalidDocument) {
		t.Fatalf("IsError() = false for %v", wrapped)
	}
	var typed *Error
	if !errors.As(wrapped, &typed) || typed.Kind != ErrorInvalidDocument {
		t.Fatalf("errors.As() = %+v", typed)
	}
	if (&Error{}).Error() != "codex keyring: " || (*Error)(nil).Error() != "<nil>" {
		t.Fatal("Error.Error() edge cases changed")
	}
}

func testDocument(t *testing.T, hmacCurrent, aeadCurrent string) string {
	t.Helper()
	return `{"schema_version":1,` +
		`"hmac":{"current":"` + hmacCurrent + `","keys":{"h1":"` + testMaterial(1) + `","h2":"` + testMaterial(2) + `"}},` +
		`"aead":{"current":"` + aeadCurrent + `","keys":{"a1":"` + testMaterial(11) + `","a2":"` + testMaterial(12) + `"}}}`
}

func testMaterial(value byte) string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{value}, keyMaterialBytes))
}

func parseTestKeyring(t *testing.T, document string, random io.Reader) *Keyring {
	t.Helper()
	keyring, err := Parse([]byte(document), random)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	return keyring
}
