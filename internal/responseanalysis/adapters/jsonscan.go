package adapters

import (
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

const maxJSONNestingDepth = 256

type jsonKind uint8

const (
	jsonInvalid jsonKind = iota
	jsonObject
	jsonArray
	jsonString
	jsonNumber
	jsonLiteral
)

type jsonValue struct {
	kind       jsonKind
	start, end int
}

type jsonDocument struct {
	data []byte
	root jsonValue
}

type jsonParser struct{ data []byte }

func decodeDocument(data []byte) (jsonDocument, bool) {
	if !utf8.Valid(data) {
		return jsonDocument{}, false
	}
	parser := jsonParser{data: data}
	start := skipJSONSpace(data, 0)
	root, next, ok := parser.value(start, 0)
	if !ok || skipJSONSpace(data, next) != len(data) {
		return jsonDocument{}, false
	}
	return jsonDocument{data: data, root: root}, true
}

func (p jsonParser) value(index, depth int) (jsonValue, int, bool) {
	index = skipJSONSpace(p.data, index)
	if index >= len(p.data) {
		return jsonValue{}, index, false
	}
	switch p.data[index] {
	case '{':
		return p.object(index, depth)
	case '[':
		return p.array(index, depth)
	case '"':
		end, ok := scanJSONString(p.data, index)
		return jsonValue{kind: jsonString, start: index, end: end}, end, ok
	case 't':
		return p.literal(index, "true")
	case 'f':
		return p.literal(index, "false")
	case 'n':
		return p.literal(index, "null")
	default:
		end, ok := scanJSONNumber(p.data, index)
		return jsonValue{kind: jsonNumber, start: index, end: end}, end, ok
	}
}

func (p jsonParser) object(index, depth int) (jsonValue, int, bool) {
	if depth >= maxJSONNestingDepth {
		return jsonValue{}, index, false
	}
	start := index
	index = skipJSONSpace(p.data, index+1)
	if index < len(p.data) && p.data[index] == '}' {
		index++
		return jsonValue{kind: jsonObject, start: start, end: index}, index, true
	}
	for {
		if index >= len(p.data) || p.data[index] != '"' {
			return jsonValue{}, index, false
		}
		var ok bool
		if index, ok = scanJSONString(p.data, index); !ok {
			return jsonValue{}, index, false
		}
		index = skipJSONSpace(p.data, index)
		if index >= len(p.data) || p.data[index] != ':' {
			return jsonValue{}, index, false
		}
		_, index, ok = p.value(index+1, depth+1)
		if !ok {
			return jsonValue{}, index, false
		}
		index = skipJSONSpace(p.data, index)
		if index >= len(p.data) {
			return jsonValue{}, index, false
		}
		switch p.data[index] {
		case '}':
			index++
			return jsonValue{kind: jsonObject, start: start, end: index}, index, true
		case ',':
			index = skipJSONSpace(p.data, index+1)
		default:
			return jsonValue{}, index, false
		}
	}
}

func (p jsonParser) array(index, depth int) (jsonValue, int, bool) {
	if depth >= maxJSONNestingDepth {
		return jsonValue{}, index, false
	}
	start := index
	index = skipJSONSpace(p.data, index+1)
	if index < len(p.data) && p.data[index] == ']' {
		index++
		return jsonValue{kind: jsonArray, start: start, end: index}, index, true
	}
	for {
		_, next, ok := p.value(index, depth+1)
		if !ok {
			return jsonValue{}, index, false
		}
		index = skipJSONSpace(p.data, next)
		if index >= len(p.data) {
			return jsonValue{}, index, false
		}
		switch p.data[index] {
		case ']':
			index++
			return jsonValue{kind: jsonArray, start: start, end: index}, index, true
		case ',':
			index = skipJSONSpace(p.data, index+1)
		default:
			return jsonValue{}, index, false
		}
	}
}

func (p jsonParser) literal(index int, literal string) (jsonValue, int, bool) {
	end := index + len(literal)
	if end > len(p.data) || !bytesEqualString(p.data[index:end], literal) {
		return jsonValue{}, index, false
	}
	return jsonValue{kind: jsonLiteral, start: index, end: end}, end, true
}

func bytesEqualString(data []byte, expected string) bool {
	if len(data) != len(expected) {
		return false
	}
	for index := range data {
		if data[index] != expected[index] {
			return false
		}
	}
	return true
}

func skipJSONSpace(data []byte, index int) int {
	for index < len(data) {
		switch data[index] {
		case ' ', '\t', '\r', '\n':
			index++
		default:
			return index
		}
	}
	return index
}

func scanJSONString(data []byte, index int) (int, bool) {
	if index >= len(data) || data[index] != '"' {
		return index, false
	}
	for index++; index < len(data); {
		current := data[index]
		switch {
		case current == '"':
			return index + 1, true
		case current == '\\':
			index++
			if index >= len(data) {
				return index, false
			}
			switch data[index] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				index++
			case 'u':
				if index+5 > len(data) || !validHex4(data[index+1:index+5]) {
					return index, false
				}
				index += 5
			default:
				return index, false
			}
		case current < 0x20:
			return index, false
		case current < utf8.RuneSelf:
			index++
		default:
			_, size := utf8.DecodeRune(data[index:])
			if size == 1 {
				return index, false
			}
			index += size
		}
	}
	return index, false
}

func scanJSONNumber(data []byte, index int) (int, bool) {
	start := index
	if index < len(data) && data[index] == '-' {
		index++
	}
	if index >= len(data) {
		return start, false
	}
	if data[index] == '0' {
		index++
	} else {
		if data[index] < '1' || data[index] > '9' {
			return start, false
		}
		for index < len(data) && data[index] >= '0' && data[index] <= '9' {
			index++
		}
	}
	if index < len(data) && data[index] == '.' {
		index++
		fractionStart := index
		for index < len(data) && data[index] >= '0' && data[index] <= '9' {
			index++
		}
		if index == fractionStart {
			return start, false
		}
	}
	if index < len(data) && (data[index] == 'e' || data[index] == 'E') {
		index++
		if index < len(data) && (data[index] == '+' || data[index] == '-') {
			index++
		}
		exponentStart := index
		for index < len(data) && data[index] >= '0' && data[index] <= '9' {
			index++
		}
		if index == exponentStart {
			return start, false
		}
	}
	return index, index > start
}

func validHex4(data []byte) bool {
	if len(data) != 4 {
		return false
	}
	for _, value := range data {
		if !isHexDigit(value) {
			return false
		}
	}
	return true
}

func isHexDigit(value byte) bool {
	return value >= '0' && value <= '9' ||
		value >= 'a' && value <= 'f' ||
		value >= 'A' && value <= 'F'
}

func (d jsonDocument) objectField(parent jsonValue, name string) (jsonValue, bool) {
	if parent.kind != jsonObject {
		return jsonValue{}, false
	}
	parser := jsonParser{data: d.data}
	index := skipJSONSpace(d.data, parent.start+1)
	var result jsonValue
	var found bool
	for index < parent.end && d.data[index] != '}' {
		keyStart := index
		keyEnd, ok := scanJSONString(d.data, keyStart)
		if !ok {
			return jsonValue{}, false
		}
		index = skipJSONSpace(d.data, keyEnd)
		value, next, ok := parser.value(index+1, 1)
		if !ok {
			return jsonValue{}, false
		}
		if decodedStringEquals(d.data, jsonValue{kind: jsonString, start: keyStart, end: keyEnd}, name) {
			// Matching encoding/json's last-key behavior keeps duplicate handling
			// deterministic without making the scanner a second protocol dialect.
			result, found = value, true
		}
		index = skipJSONSpace(d.data, next)
		if index < parent.end && d.data[index] == ',' {
			index = skipJSONSpace(d.data, index+1)
		}
	}
	return result, found
}

func (d jsonDocument) objectFieldCount(parent jsonValue) int {
	if parent.kind != jsonObject {
		return 0
	}
	parser := jsonParser{data: d.data}
	index := skipJSONSpace(d.data, parent.start+1)
	count := 0
	for index < parent.end && d.data[index] != '}' {
		keyEnd, ok := scanJSONString(d.data, index)
		if !ok {
			return 0
		}
		index = skipJSONSpace(d.data, keyEnd)
		_, next, ok := parser.value(index+1, 1)
		if !ok {
			return 0
		}
		count++
		index = skipJSONSpace(d.data, next)
		if index < parent.end && d.data[index] == ',' {
			index = skipJSONSpace(d.data, index+1)
		}
	}
	return count
}

func (d jsonDocument) arrayValues(array jsonValue, visit func(jsonValue) bool) {
	if array.kind != jsonArray {
		return
	}
	parser := jsonParser{data: d.data}
	index := skipJSONSpace(d.data, array.start+1)
	for index < array.end && d.data[index] != ']' {
		value, next, ok := parser.value(index, 1)
		if !ok || !visit(value) {
			return
		}
		index = skipJSONSpace(d.data, next)
		if index < array.end && d.data[index] == ',' {
			index = skipJSONSpace(d.data, index+1)
		}
	}
}

func (d jsonDocument) arrayEmpty(value jsonValue) bool {
	return value.kind == jsonArray && skipJSONSpace(d.data, value.start+1) < value.end && d.data[skipJSONSpace(d.data, value.start+1)] == ']'
}

func decodedStringEquals(data []byte, value jsonValue, expected string) bool {
	index := 0
	matched := walkJSONString(data, value, func(current rune) {
		if index >= len(expected) || current >= utf8.RuneSelf || expected[index] != byte(current) {
			index = len(expected) + 1
			return
		}
		index++
	})
	return matched && index == len(expected)
}

func trimmedString(data []byte, value jsonValue, maxBytes int, resources *resourceContext) (string, fieldStatus) {
	return boundedString(data, value, maxBytes, resources, preserveRune)
}

func canonicalUsageString(data []byte, value jsonValue, maxBytes int, resources *resourceContext) (string, fieldStatus) {
	return boundedString(data, value, maxBytes, resources, unicode.ToLower)
}

func preserveRune(current rune) rune {
	return current
}

func boundedString(
	data []byte,
	value jsonValue,
	maxBytes int,
	resources *resourceContext,
	transform func(rune) rune,
) (string, fieldStatus) {
	if value.kind != jsonString {
		return "", fieldInvalid
	}
	started, length, pending, ordinal, lastNonSpace := false, 0, 0, 0, 0
	if !walkJSONString(data, value, func(current rune) {
		ordinal++
		transformed := transform(current)
		if unicode.IsSpace(current) {
			if started {
				pending += utf8.RuneLen(transformed)
			}
			return
		}
		started = true
		length += pending + utf8.RuneLen(transformed)
		pending = 0
		lastNonSpace = ordinal
	}) {
		return "", fieldInvalid
	}
	if length > maxBytes {
		return "", fieldTooLarge
	}
	if length == 0 {
		return "", fieldValid
	}
	if !resources.reserve(length) {
		return "", fieldInvalid
	}
	var builder strings.Builder
	builder.Grow(length)
	ordinal = 0
	started = false
	walkJSONString(data, value, func(current rune) {
		ordinal++
		if ordinal > lastNonSpace || !started && unicode.IsSpace(current) {
			return
		}
		started = true
		builder.WriteRune(transform(current))
	})
	return builder.String(), fieldValid
}

func walkJSONString(data []byte, value jsonValue, visit func(rune)) bool {
	if value.kind != jsonString || value.start < 0 || value.end > len(data) || value.end-value.start < 2 {
		return false
	}
	for index, limit := value.start+1, value.end-1; index < limit; {
		current := data[index]
		if current != '\\' {
			r, size := utf8.DecodeRune(data[index:limit])
			visit(r)
			index += size
			continue
		}
		index++
		switch data[index] {
		case '"', '\\', '/':
			visit(rune(data[index]))
			index++
		case 'b':
			visit('\b')
			index++
		case 'f':
			visit('\f')
			index++
		case 'n':
			visit('\n')
			index++
		case 'r':
			visit('\r')
			index++
		case 't':
			visit('\t')
			index++
		case 'u':
			first := rune(parseHex4(data[index+1 : index+5]))
			index += 5
			if utf16.IsSurrogate(first) && first >= 0xd800 && first <= 0xdbff && index+6 <= limit && data[index] == '\\' && data[index+1] == 'u' {
				second := rune(parseHex4(data[index+2 : index+6]))
				if second >= 0xdc00 && second <= 0xdfff {
					first = utf16.DecodeRune(first, second)
					index += 6
				} else {
					first = utf8.RuneError
				}
			} else if utf16.IsSurrogate(first) {
				first = utf8.RuneError
			}
			visit(first)
		default:
			return false
		}
	}
	return true
}

func parseHex4(data []byte) uint16 {
	var value uint16
	for _, current := range data {
		value <<= 4
		switch {
		case current >= '0' && current <= '9':
			value += uint16(current - '0')
		case current >= 'a' && current <= 'f':
			value += uint16(current-'a') + 10
		default:
			value += uint16(current-'A') + 10
		}
	}
	return value
}
