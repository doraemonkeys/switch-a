package jsonstream

import "unicode/utf8"

// Sink is the minimal bounded-output contract needed to encode JSON text without
// allocating an intermediate quoted string.
type Sink interface {
	WriteByte(byte) error
	WriteString(string) error
	WriteBytes([]byte) error
}

// WriteString emits one RFC 8259 JSON string. Invalid UTF-8 is normalized to the
// replacement rune so every successful stream remains valid JSON.
func WriteString(sink Sink, value string) error {
	if err := sink.WriteByte('"'); err != nil {
		return err
	}
	for len(value) > 0 {
		safeBytes := safePrefixBytes(value)
		if safeBytes > 0 {
			if err := sink.WriteString(value[:safeBytes]); err != nil {
				return err
			}
			value = value[safeBytes:]
			continue
		}
		consumed, err := writeEscapedPrefix(sink, value)
		if err != nil {
			return err
		}
		value = value[consumed:]
	}
	return sink.WriteByte('"')
}

func safePrefixBytes(value string) int {
	safeBytes := 0
	for safeBytes < len(value) {
		current := value[safeBytes]
		if current < utf8.RuneSelf {
			if current < 0x20 || current == '"' || current == '\\' {
				break
			}
			safeBytes++
			continue
		}
		runeValue, size := utf8.DecodeRuneInString(value[safeBytes:])
		if size == 1 || runeValue == '\u2028' || runeValue == '\u2029' {
			break
		}
		safeBytes += size
	}
	return safeBytes
}

func writeEscapedPrefix(sink Sink, value string) (int, error) {
	if value[0] < utf8.RuneSelf {
		return 1, writeEscapedASCII(sink, value[0])
	}
	runeValue, size := utf8.DecodeRuneInString(value)
	switch {
	case runeValue == utf8.RuneError && size == 1:
		return size, sink.WriteString(`\ufffd`)
	case runeValue == '\u2028':
		return size, sink.WriteString(`\u2028`)
	case runeValue == '\u2029':
		return size, sink.WriteString(`\u2029`)
	default:
		return size, sink.WriteString(value[:size])
	}
}

func writeEscapedASCII(sink Sink, value byte) error {
	var escaped string
	switch value {
	case '"':
		escaped = `\"`
	case '\\':
		escaped = `\\`
	case '\b':
		escaped = `\b`
	case '\f':
		escaped = `\f`
	case '\n':
		escaped = `\n`
	case '\r':
		escaped = `\r`
	case '\t':
		escaped = `\t`
	default:
		return writeControlEscape(sink, value)
	}
	return sink.WriteString(escaped)
}

func writeControlEscape(sink Sink, value byte) error {
	const hexDigits = "0123456789abcdef"
	control := [...]byte{'\\', 'u', '0', '0', hexDigits[value>>4], hexDigits[value&0x0f]}
	return sink.WriteBytes(control[:])
}
