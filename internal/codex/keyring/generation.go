package codexkeyring

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
)

const (
	keyVersionFingerprintBytes = 12
	generatedRootCount         = 2
)

// GenerateDocument creates a complete first-generation keyring document.
// The returned bytes contain secret material and are intended for immediate
// durable publication, not logging or general configuration transport.
func GenerateDocument(random io.Reader) ([]byte, error) {
	if random == nil {
		return nil, errorOf(ErrorInvalidInput, "random", "", "random source is required", nil)
	}

	material := make([]byte, generatedRootCount*keyMaterialBytes)
	defer clear(material)
	if _, err := io.ReadFull(random, material); err != nil {
		return nil, errorOf(ErrorRandomSource, "document", "", "could not generate root key material", err)
	}

	initialHMACVersion := generatedVersion("hmac", material[:keyMaterialBytes])
	initialAEADVersion := generatedVersion("aead", material[keyMaterialBytes:])
	generated := document{
		SchemaVersion: documentSchemaVersion,
		HMAC: documentRing{
			Current: initialHMACVersion,
			Keys: map[string]string{
				initialHMACVersion: base64.RawURLEncoding.EncodeToString(material[:keyMaterialBytes]),
			},
		},
		AEAD: documentRing{
			Current: initialAEADVersion,
			Keys: map[string]string{
				initialAEADVersion: base64.RawURLEncoding.EncodeToString(material[keyMaterialBytes:]),
			},
		},
	}
	serialized, err := serializeDocument(generated)
	if err != nil {
		return nil, err
	}

	// Validation here makes the generator uphold the same strict document
	// contract as an operator-supplied file, including cross-ring uniqueness.
	hmacRing, aeadRing, err := parseDocument(serialized)
	clearParsedRing(hmacRing)
	clearParsedRing(aeadRing)
	if err != nil {
		clear(serialized)
		return nil, err
	}
	return serialized, nil
}

// Independent installations must not assign the same version to different roots:
// those versions become durable references when ownership is transferred.
func generatedVersion(family string, root []byte) string {
	digest := sha256.Sum256(root)
	return family + "-" + hex.EncodeToString(digest[:keyVersionFingerprintBytes])
}
