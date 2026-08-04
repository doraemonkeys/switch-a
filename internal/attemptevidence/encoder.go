package attemptevidence

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

var ErrEvidenceTooLarge = errors.New("attempt evidence exceeds the 32-KiB wire limit")

var envelopeSiblingKeys = [...]string{
	"gateway", "upstream_handshake", "transport", "upstream_event",
}

// Encode merges semantic evidence into an existing v2 envelope. Decoding and
// re-encoding the complete object is intentional: it preserves every unknown
// sibling while making HTML escaping and the byte ceiling one atomic policy.
func Encode(existing []byte, semantic *SemanticError) ([]byte, error) {
	if len(bytes.TrimSpace(existing)) == 0 && semantic == nil {
		return nil, nil
	}

	envelope := make(map[string]any, len(envelopeSiblingKeys)+2)
	if len(bytes.TrimSpace(existing)) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(existing))
		decoder.UseNumber()
		if err := decoder.Decode(&envelope); err != nil {
			return nil, fmt.Errorf("decode existing attempt evidence: %w", err)
		}
		if err := requireEOF(decoder); err != nil {
			return nil, err
		}
		version, ok := envelope["v"].(json.Number)
		if !ok || version.String() != "2" {
			return nil, fmt.Errorf("existing attempt evidence must use v2")
		}
	}
	envelope["v"] = EnvelopeVersion
	for _, key := range envelopeSiblingKeys {
		if _, present := envelope[key]; !present {
			envelope[key] = nil
		}
	}
	if semantic != nil {
		envelope["semantic_error"] = semantic
	} else if _, present := envelope["semantic_error"]; !present {
		envelope["semantic_error"] = nil
	}

	encoded, err := marshalWithoutHTMLEscaping(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode attempt evidence: %w", err)
	}
	if len(encoded) > MaxAttemptEvidenceBytes {
		return nil, fmt.Errorf("%w: got %d bytes", ErrEvidenceTooLarge, len(encoded))
	}
	return encoded, nil
}

func EncodeString(existing *string, semantic *SemanticError) (*string, error) {
	var raw []byte
	if existing != nil {
		raw = []byte(*existing)
	}
	encoded, err := Encode(raw, semantic)
	if err != nil || encoded == nil {
		return nil, err
	}
	value := string(encoded)
	return &value, nil
}

func marshalWithoutHTMLEscaping(value any) ([]byte, error) {
	var destination bytes.Buffer
	encoder := json.NewEncoder(&destination)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(destination.Bytes(), []byte{'\n'}), nil
}

func requireEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("attempt evidence contains multiple JSON values")
		}
		return fmt.Errorf("decode trailing attempt evidence: %w", err)
	}
	return nil
}
