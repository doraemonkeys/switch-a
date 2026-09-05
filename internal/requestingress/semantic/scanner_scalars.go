package semantic

import "encoding/json"

func (s *scanner) stringValue(mode captureMode) (jsonValue, error) {
	limit := 0
	switch mode {
	case captureString:
		limit = -1
	case captureReasoningScalar:
		limit = maxReasoningRawBytes
	case captureEventType:
		limit = maxEventTypeRawBytes
	}
	value, valid, err := s.string(limit)
	return jsonValue{kind: '"', text: value, validString: valid}, err
}
func (s *scanner) literal(first byte) (jsonValue, error) {
	literal := "null"
	switch first {
	case 't':
		literal = "true"
	case 'f':
		literal = "false"
	}
	for i := range literal {
		if err := s.take(literal[i]); err != nil {
			return jsonValue{}, err
		}
	}
	return jsonValue{kind: first}, nil
}

type stringToken struct {
	raw              []byte
	limit            int
	truncated        bool
	escaped          bool
	unicodeRemaining int
}

func (t *stringToken) append(b byte) {
	if t.limit == 0 {
		return
	}
	if t.limit < 0 || len(t.raw) < t.limit {
		t.raw = append(t.raw, b)
		return
	}
	t.truncated = true
}
func (t *stringToken) accept(b byte) (bool, error) {
	t.append(b)
	if t.unicodeRemaining > 0 {
		if !isHexDigit(b) {
			return false, errJSON
		}
		t.unicodeRemaining--
		return false, nil
	}
	if t.escaped {
		return false, t.acceptEscape(b)
	}
	switch {
	case b == '"':
		return true, nil
	case b < ' ':
		return false, errJSON
	case b == '\\':
		t.escaped = true
	}
	return false, nil
}
func isHexDigit(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F'
}
func (t *stringToken) acceptEscape(b byte) error {
	t.escaped = false
	switch b {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		return nil
	case 'u':
		t.unicodeRemaining = 4
		return nil
	default:
		return errJSON
	}
}
func (t *stringToken) value() (string, bool, error) {
	if t.limit == 0 || t.truncated {
		return "", false, nil
	}
	var value string
	if err := json.Unmarshal(t.raw, &value); err != nil {
		return "", false, err
	}
	return value, true, nil
}

// Strings are validated byte by byte, retaining only consumer-requested text.
// Skipped payloads never become tokens, even for a single giant string.
func (s *scanner) string(limit int) (string, bool, error) {
	if err := s.take('"'); err != nil {
		return "", false, err
	}
	token := stringToken{limit: limit}
	token.append('"')
	for {
		b, err := s.reader.ReadByte()
		if err != nil {
			return "", false, err
		}
		done, err := token.accept(b)
		if err != nil {
			return "", false, err
		}
		if done {
			return token.value()
		}
	}
}

type numberToken struct {
	raw     []byte
	capture bool
}

func (n *numberToken) read(s *scanner) {
	b, _ := s.reader.ReadByte()
	if n.capture && len(n.raw) < maxNumberBytes {
		n.raw = append(n.raw, b)
	}
}
func (n *numberToken) digits(s *scanner, required bool) error {
	consumed := false
	for {
		b, err := s.peek()
		if err != nil || b < '0' || b > '9' {
			break
		}
		n.read(s)
		consumed = true
	}
	if required && !consumed {
		return errJSON
	}
	return nil
}
func (n *numberToken) integer(s *scanner) error {
	b, err := s.peek()
	if err != nil {
		return err
	}
	if b == '-' {
		n.read(s)
		b, err = s.peek()
		if err != nil {
			return err
		}
	}
	switch {
	case b == '0':
		n.read(s)
		return nil
	case b >= '1' && b <= '9':
		return n.digits(s, false)
	default:
		return errJSON
	}
}
func (n *numberToken) fraction(s *scanner) error {
	b, err := s.peek()
	if err != nil || b != '.' {
		return nil
	}
	n.read(s)
	return n.digits(s, true)
}
func (n *numberToken) exponent(s *scanner) error {
	b, err := s.peek()
	if err != nil || b != 'e' && b != 'E' {
		return nil
	}
	n.read(s)
	b, err = s.peek()
	if err == nil && (b == '+' || b == '-') {
		n.read(s)
	}
	return n.digits(s, true)
}
func (s *scanner) number(mode captureMode) (jsonValue, error) {
	token := numberToken{capture: mode != skipValue}
	if err := token.integer(s); err != nil {
		return jsonValue{}, err
	}
	if err := token.fraction(s); err != nil {
		return jsonValue{}, err
	}
	if err := token.exponent(s); err != nil {
		return jsonValue{}, err
	}
	return jsonValue{kind: '0', text: string(token.raw)}, nil
}
