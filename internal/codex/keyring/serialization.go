package codexkeyring

import (
	"encoding/json"
)

func serializeDocument(value document) ([]byte, error) {
	serialized, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, errorOf(ErrorInvalidDocument, "document", "", "could not serialize document", err)
	}
	// A terminal newline keeps the generated secret file friendly to standard
	// operational tools without creating multiple valid byte representations.
	return append(serialized, '\n'), nil
}
