package codexkeyring

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"testing"
)

func TestGenerateDocumentUsesCanonicalCompleteSchema(t *testing.T) {
	randomBytes := make([]byte, generatedRootCount*keyMaterialBytes)
	for index := range randomBytes {
		randomBytes[index] = byte(index + 1)
	}

	serialized, err := GenerateDocument(bytes.NewReader(randomBytes))
	if err != nil {
		t.Fatalf("GenerateDocument() error = %v", err)
	}
	want := "{\n" +
		"  \"schema_version\": 1,\n" +
		"  \"hmac\": {\n" +
		"    \"current\": \"hmac-1\",\n" +
		"    \"keys\": {\n" +
		"      \"hmac-1\": \"" + base64.RawURLEncoding.EncodeToString(randomBytes[:keyMaterialBytes]) + "\"\n" +
		"    }\n" +
		"  },\n" +
		"  \"aead\": {\n" +
		"    \"current\": \"aead-1\",\n" +
		"    \"keys\": {\n" +
		"      \"aead-1\": \"" + base64.RawURLEncoding.EncodeToString(randomBytes[keyMaterialBytes:]) + "\"\n" +
		"    }\n" +
		"  }\n" +
		"}\n"
	if string(serialized) != want {
		t.Fatalf("GenerateDocument() =\n%s\nwant canonical bytes =\n%s", serialized, want)
	}

	keyring, err := Parse(serialized, bytes.NewReader(nil))
	if err != nil {
		t.Fatalf("Parse(generated) error = %v", err)
	}
	capabilities := keyring.Capabilities()
	if capabilities.HMACCurrent != initialHMACVersion || capabilities.AEADCurrent != initialAEADVersion {
		t.Fatalf("generated capabilities = %+v", capabilities)
	}
}

func TestGenerateDocumentRejectsInvalidRandomSources(t *testing.T) {
	tests := []struct {
		name   string
		random io.Reader
		kind   ErrorKind
	}{
		{name: "nil", kind: ErrorInvalidInput},
		{name: "short", random: bytes.NewReader(make([]byte, keyMaterialBytes)), kind: ErrorRandomSource},
		{name: "duplicate roots", random: bytes.NewReader(bytes.Repeat([]byte{7}, generatedRootCount*keyMaterialBytes)), kind: ErrorInvalidDocument},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serialized, err := GenerateDocument(test.random)
			if !IsError(err, test.kind) {
				t.Fatalf("GenerateDocument() error = %v, want kind %q", err, test.kind)
			}
			if serialized != nil {
				t.Fatalf("GenerateDocument() returned secret bytes after failure: %d bytes", len(serialized))
			}
		})
	}
}

func TestGenerateDocumentPreservesRandomFailureCause(t *testing.T) {
	want := errors.New("entropy unavailable")
	_, err := GenerateDocument(failingReader{err: want})
	if !errors.Is(err, want) {
		t.Fatalf("GenerateDocument() error = %v, want wrapped cause", err)
	}
}

type failingReader struct{ err error }

func (reader failingReader) Read([]byte) (int, error) { return 0, reader.err }
