package semantic

import (
	"bufio"
	"errors"
	"io"
	"strings"

	codexheaders "github.com/doraemonkeys/switch-a/internal/codex/headers"
)

const (
	maxJSONDepth         = 10000
	maxKeyRawBytes       = 1024
	maxReasoningRawBytes = 1024
	maxNumberBytes       = 32
	// Every recognized event name is ASCII; each character may use a six-byte Unicode escape.
	maxEventTypeRawBytes = 2 + 6*codexheaders.MaxClientEventTypeBytes
)

var errJSON = errors.New("invalid JSON")

type captureMode uint8

const (
	skipValue captureMode = iota
	captureString
	captureReasoning
	captureReasoningScalar
	captureEventType
	captureMetadata
)

type jsonValue struct {
	kind        byte
	text        string
	validString bool
	fields      map[string]jsonField
}
type jsonField struct {
	first     jsonValue
	last      jsonValue
	duplicate bool
	invalid   bool
	captured  bool
}
type scanner struct {
	reader     *bufio.Reader
	depth      int
	rootMember func(string, jsonValue)
}

func (s *scanner) peek() (byte, error) {
	value, err := s.reader.Peek(1)
	if err != nil {
		return 0, err
	}
	return value[0], nil
}
func (s *scanner) space() error {
	for {
		b, err := s.peek()
		if err != nil {
			return err
		}
		switch b {
		case ' ', '\t', '\r', '\n':
			_, _ = s.reader.ReadByte()
		default:
			return nil
		}
	}
}
func (s *scanner) take(want byte) error {
	b, err := s.reader.ReadByte()
	if err != nil {
		return err
	}
	if b != want {
		return errJSON
	}
	return nil
}
func (s *scanner) document() error {
	if err := s.space(); err != nil {
		return err
	}
	b, _ := s.peek()
	if b != '{' {
		return errJSON
	}
	if _, err := s.object(skipValue, true); err != nil {
		return err
	}
	err := s.space()
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	return errJSON
}
func (s *scanner) value(mode captureMode) (jsonValue, error) {
	if err := s.space(); err != nil {
		return jsonValue{}, err
	}
	b, _ := s.peek()
	switch b {
	case '{':
		// A wrong-type scalar still needs syntax validation; none of its descendants
		// are facts. Only container contracts carry projection into another object.
		objectMode := skipValue
		if mode == captureReasoning || mode == captureMetadata {
			objectMode = mode
		}
		return s.object(objectMode, false)
	case '[':
		return s.array()
	case '"':
		return s.stringValue(mode)
	case 't', 'f', 'n':
		return s.literal(b)
	default:
		return s.number(mode)
	}
}
func (s *scanner) enter() error {
	s.depth++
	if s.depth > maxJSONDepth {
		s.depth--
		return errJSON
	}
	return nil
}
func (s *scanner) array() (jsonValue, error) {
	if err := s.enter(); err != nil {
		return jsonValue{}, err
	}
	defer func() { s.depth-- }()
	_, _ = s.reader.ReadByte()
	value := jsonValue{kind: '['}
	if err := s.space(); err != nil {
		return jsonValue{}, err
	}
	if b, _ := s.peek(); b == ']' {
		_, _ = s.reader.ReadByte()
		return value, nil
	}
	for {
		if _, err := s.value(skipValue); err != nil {
			return jsonValue{}, err
		}
		closed, err := s.containerNext(']')
		if err != nil {
			return jsonValue{}, err
		}
		if closed {
			return value, nil
		}
	}
}
func (s *scanner) containerNext(closing byte) (bool, error) {
	if err := s.space(); err != nil {
		return false, err
	}
	b, _ := s.reader.ReadByte()
	if b == closing {
		return true, nil
	}
	if b != ',' {
		return false, errJSON
	}
	return false, nil
}
func (s *scanner) object(mode captureMode, root bool) (jsonValue, error) {
	if err := s.enter(); err != nil {
		return jsonValue{}, err
	}
	defer func() { s.depth-- }()
	_, _ = s.reader.ReadByte()
	value := jsonValue{kind: '{'}
	if err := s.space(); err != nil {
		return value, err
	}
	if b, _ := s.peek(); b == '}' {
		_, _ = s.reader.ReadByte()
		return value, nil
	}
	for {
		key, child, capture, err := s.objectMember(mode, root)
		if err != nil {
			return value, err
		}
		if root {
			s.rootMember(key, child)
		} else if capture {
			value.captureField(key, child, mode)
		}
		closed, err := s.containerNext('}')
		if err != nil {
			return value, err
		}
		if closed {
			return value, nil
		}
	}
}
func (s *scanner) objectMember(mode captureMode, root bool) (string, jsonValue, bool, error) {
	if err := s.space(); err != nil {
		return "", jsonValue{}, false, err
	}
	keyLimit := 0
	if root || mode == captureReasoning || mode == captureMetadata {
		keyLimit = maxKeyRawBytes
	}
	key, _, err := s.string(keyLimit)
	if err != nil {
		return "", jsonValue{}, false, err
	}
	if err = s.space(); err != nil {
		return "", jsonValue{}, false, err
	}
	if err = s.take(':'); err != nil {
		return "", jsonValue{}, false, err
	}
	childMode := memberProjectionMode(key, mode, root)
	child, err := s.value(childMode)
	return key, child, childMode != skipValue, err
}
func memberProjectionMode(key string, mode captureMode, root bool) captureMode {
	switch {
	case root:
		return rootProjectionMode(key)
	case mode == captureReasoning:
		switch key {
		case "effort", "type", "budget_tokens":
			return captureReasoningScalar
		}
	case mode == captureMetadata:
		switch key {
		case "thread_id", "session_id", "x-codex-window-id", "x-codex-turn-metadata":
			return captureString
		}
	}
	return skipValue
}
func rootProjectionMode(key string) captureMode {
	switch {
	case strings.EqualFold(key, "model"), key == "previous_response_id":
		return captureString
	case key == "reasoning", key == "output_config", key == "thinking":
		return captureReasoning
	case key == "reasoning_effort":
		return captureReasoningScalar
	case key == "type":
		return captureEventType
	case key == "client_metadata":
		return captureMetadata
	default:
		return skipValue
	}
}
func (v *jsonValue) captureField(key string, child jsonValue, mode captureMode) {
	if v.fields == nil {
		v.fields = make(map[string]jsonField)
	}
	field, exists := v.fields[key]
	if !exists {
		field.first = child
	}
	field.last, field.duplicate = child, exists
	// Reasoning's invalid/captured flags survive replacement of a duplicate value.
	if mode == captureReasoning {
		valid := validReasoningScalar(key, child)
		field.invalid = field.invalid || !valid
		field.captured = field.captured || valid
	}
	v.fields[key] = field
}
