package downloadcapability

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
)

const (
	EntropyBytes           = 32
	exportIDPrefix         = "ce_"
	exportIDDigestBytes    = 18
	exportIDEncodedBytes   = (exportIDDigestBytes*8 + 5) / 6
	CanonicalExportIDBytes = len(exportIDPrefix) + exportIDEncodedBytes
)

var (
	strictExportIDEncoding = base64.RawURLEncoding.Strict()
	errZeroExportID        = errors.New("request capture export ID is zero")
	errEntropyUnavailable  = errors.New("request capture export entropy source is unavailable")
	errEntropyRead         = errors.New("request capture export entropy read failed")
)

type Hash [sha256.Size]byte

func MakeExportID(generated [16]byte) (string, error) {
	if generated == ([16]byte{}) {
		return "", errZeroExportID
	}
	digest := sha256.Sum256(generated[:])
	return exportIDPrefix + base64.RawURLEncoding.EncodeToString(digest[:exportIDDigestBytes]), nil
}

func IsCanonicalExportID(value string) bool {
	if len(value) != CanonicalExportIDBytes ||
		value[:len(exportIDPrefix)] != exportIDPrefix {
		return false
	}
	encoded := value[len(exportIDPrefix):]
	for index := range len(encoded) {
		character := encoded[index]
		if character >= 'A' && character <= 'Z' ||
			character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			character == '-' || character == '_' {
			continue
		}
		return false
	}

	var digest [exportIDDigestBytes]byte
	decodedBytes, err := strictExportIDEncoding.Decode(digest[:], []byte(encoded))
	if err != nil || decodedBytes != len(digest) {
		return false
	}
	var canonical [exportIDEncodedBytes]byte
	base64.RawURLEncoding.Encode(canonical[:], digest[:])
	for index := range canonical {
		if canonical[index] != encoded[index] {
			return false
		}
	}
	return true
}

func NewToken(entropy io.Reader) (string, Hash, error) {
	if entropy == nil {
		return "", Hash{}, errEntropyUnavailable
	}
	random := make([]byte, EntropyBytes)
	if _, err := io.ReadFull(entropy, random); err != nil {
		// Dependency errors remain opaque because Error and Is are executable
		// behavior and may disclose credentials.
		return "", Hash{}, errEntropyRead
	}
	rawToken := base64.RawURLEncoding.EncodeToString(random)
	return rawToken, HashToken(rawToken), nil
}

func HashToken(rawToken string) Hash {
	return sha256.Sum256([]byte(rawToken))
}

func Matches(expected Hash, rawToken string) bool {
	if len(rawToken) != base64.RawURLEncoding.EncodedLen(EntropyBytes) {
		return false
	}
	actual := HashToken(rawToken)
	return subtle.ConstantTimeCompare(expected[:], actual[:]) == 1
}
