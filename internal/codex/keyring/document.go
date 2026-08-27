package codexkeyring

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"unicode/utf8"
)

const (
	documentSchemaVersion = 1
	keyMaterialBytes      = 32
)

var keyIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)

type document struct {
	SchemaVersion int          `json:"schema_version"`
	HMAC          documentRing `json:"hmac"`
	AEAD          documentRing `json:"aead"`
}

type documentRing struct {
	Current string            `json:"current"`
	Keys    map[string]string `json:"keys"`
}

type parsedRing struct {
	current  string
	versions []string
	keys     map[string][keyMaterialBytes]byte
}

func parseDocument(data []byte) (parsedRing, parsedRing, error) {
	if !utf8.Valid(data) {
		return parsedRing{}, parsedRing{}, invalidDocument("document", "", "must be valid UTF-8")
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return parsedRing{}, parsedRing{}, err
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var decoded document
	if err := decoder.Decode(&decoded); err != nil {
		return parsedRing{}, parsedRing{}, errorOf(ErrorInvalidDocument, "document", "", "invalid JSON schema", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return parsedRing{}, parsedRing{}, err
	}
	if decoded.SchemaVersion != documentSchemaVersion {
		return parsedRing{}, parsedRing{}, invalidDocument(
			"document",
			"",
			"schema_version must be %d",
			documentSchemaVersion,
		)
	}

	seenIDs := make(map[string]string)
	seenMaterial := make(map[[keyMaterialBytes]byte]string)
	hmacRing, err := parseDocumentRing("hmac", decoded.HMAC, seenIDs, seenMaterial)
	if err != nil {
		return parsedRing{}, parsedRing{}, err
	}
	aeadRing, err := parseDocumentRing("aead", decoded.AEAD, seenIDs, seenMaterial)
	if err != nil {
		clearParsedRing(hmacRing)
		return parsedRing{}, parsedRing{}, err
	}
	return hmacRing, aeadRing, nil
}

func parseDocumentRing(
	component string,
	document documentRing,
	seenIDs map[string]string,
	seenMaterial map[[keyMaterialBytes]byte]string,
) (parsedRing, error) {
	if document.Current == "" {
		return parsedRing{}, invalidDocument(component, "", "current key version is required")
	}
	if !keyIDPattern.MatchString(document.Current) {
		return parsedRing{}, invalidDocument(component, "", "current key version does not match the required format")
	}
	if len(document.Keys) == 0 {
		return parsedRing{}, invalidDocument(component, "", "at least one key is required")
	}
	if _, ok := document.Keys[document.Current]; !ok {
		return parsedRing{}, invalidDocument(component, document.Current, "current key version is not present")
	}

	versions := make([]string, 0, len(document.Keys))
	keys := make(map[string][keyMaterialBytes]byte, len(document.Keys))
	valid := false
	defer func() {
		if !valid {
			clearParsedRing(parsedRing{keys: keys})
		}
	}()
	for version, encoded := range document.Keys {
		if !keyIDPattern.MatchString(version) {
			return parsedRing{}, invalidDocument(component, "", "key version does not match the required format")
		}
		if firstComponent, exists := seenIDs[version]; exists {
			return parsedRing{}, invalidDocument(
				component,
				version,
				"key version is already used by %s",
				firstComponent,
			)
		}
		material, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
		if err != nil || len(material) != keyMaterialBytes {
			clear(material)
			return parsedRing{}, invalidDocument(
				component,
				version,
				"key material must be unpadded base64url encoding of exactly %d bytes",
				keyMaterialBytes,
			)
		}
		var key [keyMaterialBytes]byte
		copy(key[:], material)
		clear(material)
		if firstComponent, exists := seenMaterial[key]; exists {
			clear(key[:])
			return parsedRing{}, invalidDocument(
				component,
				version,
				"key material duplicates a key in %s",
				firstComponent,
			)
		}
		seenIDs[version] = component
		seenMaterial[key] = component
		versions = append(versions, version)
		keys[version] = key
	}
	sort.Strings(versions)
	valid = true
	return parsedRing{current: document.Current, versions: versions, keys: keys}, nil
}

func clearParsedRing(ring parsedRing) {
	for version, key := range ring.keys {
		clear(key[:])
		ring.keys[version] = key
	}
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err == io.EOF {
		return nil
	} else if err != nil {
		return errorOf(ErrorInvalidDocument, "document", "", "invalid trailing JSON", err)
	}
	return invalidDocument("document", "", "must contain exactly one JSON value")
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := walkJSONValue(decoder); err != nil {
		return err
	}
	return ensureJSONEOF(decoder)
}

func walkJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return errorOf(ErrorInvalidDocument, "document", "", "invalid JSON", err)
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}

	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return errorOf(ErrorInvalidDocument, "document", "", "invalid object key", err)
			}
			key, ok := keyToken.(string)
			if !ok {
				return invalidDocument("document", "", "object key must be a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return invalidDocument("document", "", "duplicate JSON field")
			}
			seen[key] = struct{}{}
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
	default:
		return invalidDocument("document", "", "unexpected JSON delimiter %q", delimiter)
	}
	if _, err := decoder.Token(); err != nil {
		return errorOf(ErrorInvalidDocument, "document", "", fmt.Sprintf("unterminated %q", delimiter), err)
	}
	return nil
}
