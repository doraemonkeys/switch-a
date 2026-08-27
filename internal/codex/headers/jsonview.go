package codexheaders

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

const (
	eventResponseCreate        = "response.create"
	eventResponseInject        = "response.inject"
	eventResponseAppend        = "response.append"
	eventResponseCreated       = "response.created"
	eventResponseInProgress    = "response.in_progress"
	eventResponseCompleted     = "response.completed"
	eventResponseMetadata      = "response.metadata"
	eventCodexResponseMetadata = "codex.response.metadata"
)

type messageDirection uint8

const (
	directionClient messageDirection = iota + 1
	directionServer
)

type parseIssue struct {
	reason Reason
	field  Field
}

// MessageView is an immutable semantic observation paired with the untouched
// wire buffer. Semantic input may be decompressed bytes supplied by an outer body
// decoder; it is never used for replay.
type MessageView struct {
	present   bool
	wire      []byte
	eventType string
	values    map[Field]OpaqueValue
	issue     *parseIssue
	direction messageDirection
}

func (v MessageView) EventType() string   { return v.eventType }
func (v MessageView) ReplayBytes() []byte { return v.wire }

// InspectClientFrame observes a text-frame payload whose semantic and wire
// bytes are identical.
func InspectClientFrame(version FixtureVersion, raw []byte) MessageView {
	return inspectMessage(version, raw, raw, directionClient)
}

// InspectClientPayload separates semantic JSON from compressed/framed wire
// bytes, allowing the caller to replay the latter byte-for-byte.
func InspectClientPayload(version FixtureVersion, wire, semantic []byte) MessageView {
	return inspectMessage(version, wire, semantic, directionClient)
}

func InspectServerFrame(version FixtureVersion, raw []byte) MessageView {
	return inspectMessage(version, raw, raw, directionServer)
}

func inspectMessage(version FixtureVersion, wire, semantic []byte, direction messageDirection) MessageView {
	view := MessageView{present: true, wire: wire, direction: direction}
	if version != FixtureCodexDesktop0150Alpha8 {
		view.issue = &parseIssue{reason: ReasonUnsupportedFixture, field: FieldEnvelope}
		return view
	}
	root, err := decodeObject(semantic)
	if err != nil {
		reason := ReasonMalformedJSON
		if errors.Is(err, errExpectedObject) {
			reason = ReasonInvalidEnvelope
		}
		view.issue = &parseIssue{reason: reason, field: FieldEnvelope}
		return view
	}
	typeField := root.exact("type")
	if typeField.duplicate {
		view.issue = &parseIssue{reason: ReasonDuplicateSecurityKey, field: FieldEnvelope}
		return view
	}
	if !typeField.present {
		return view
	}
	eventType, valid := decodeRequiredString(typeField.raw)
	if !valid {
		view.issue = &parseIssue{reason: ReasonInvalidEnvelope, field: FieldEnvelope}
		return view
	}
	view.eventType = eventType
	if direction == directionClient {
		inspectClientRoot(&view, root)
		return view
	}
	inspectServerRoot(&view, root)
	return view
}

func inspectClientRoot(view *MessageView, root objectView) {
	switch view.eventType {
	case eventResponseCreate:
		inspectResponseCreate(view, root)
	case eventResponseInject:
		// Public documentation establishes only the event envelope. The target
		// fixture has no response_id shape, so forwarding would bypass the
		// generation check while parsing a guessed path would forge evidence.
		view.issue = &parseIssue{reason: ReasonEvidenceUnavailable, field: FieldResponseReference}
	case eventResponseAppend:
		view.issue = &parseIssue{reason: ReasonUnsupportedEvent, field: FieldResponseReference}
	}
}

func inspectResponseCreate(view *MessageView, root objectView) {
	metadataField := root.exact("client_metadata")
	if metadataField.duplicate {
		view.issue = &parseIssue{reason: ReasonDuplicateSecurityKey, field: FieldEnvelope}
		return
	}
	if metadataField.present {
		metadata, err := decodeObject(metadataField.raw)
		if err != nil {
			view.issue = &parseIssue{reason: ReasonInvalidProjection, field: FieldEnvelope}
			return
		}
		confirmed := []struct {
			key   string
			field Field
		}{
			{key: "thread_id", field: FieldThreadID},
			{key: "session_id", field: FieldSessionID},
			{key: "x-codex-window-id", field: FieldWindowID},
			{key: "x-codex-turn-metadata", field: FieldTurnMetadata},
		}
		for _, projection := range confirmed {
			field := metadata.exact(projection.key)
			if field.duplicate {
				view.issue = &parseIssue{reason: ReasonDuplicateSecurityKey, field: projection.field}
				return
			}
			if !field.present {
				continue
			}
			value, valid := decodeRequiredString(field.raw)
			if !valid {
				view.issue = &parseIssue{reason: ReasonInvalidProjection, field: projection.field}
				return
			}
			view.setValue(projection.field, value)
		}
	}
	previous := root.exact("previous_response_id")
	if previous.duplicate {
		view.issue = &parseIssue{reason: ReasonDuplicateSecurityKey, field: FieldResponseReference}
		return
	}
	if !previous.present {
		return
	}
	value, valid := decodeRequiredString(previous.raw)
	if !valid {
		view.issue = &parseIssue{reason: ReasonInvalidProjection, field: FieldResponseReference}
		return
	}
	view.setValue(FieldResponseReference, value)
}

func inspectServerRoot(view *MessageView, root objectView) {
	switch view.eventType {
	case eventCodexResponseMetadata:
		inspectServerMetadata(view, root)
	case eventResponseCreated, eventResponseInProgress, eventResponseCompleted:
		inspectServerResponseReference(view, root)
	}
}

func inspectServerMetadata(view *MessageView, root objectView) {
	headersField := root.exact("headers")
	if headersField.duplicate {
		view.issue = &parseIssue{reason: ReasonDuplicateSecurityKey, field: FieldEnvelope}
		return
	}
	if !headersField.present {
		return
	}
	headers, err := decodeObject(headersField.raw)
	if err != nil {
		view.issue = &parseIssue{reason: ReasonInvalidProjection, field: FieldEnvelope}
		return
	}
	state := headers.folded("x-codex-turn-state")
	if state.duplicate {
		view.issue = &parseIssue{reason: ReasonDuplicateSecurityKey, field: FieldTurnState}
		return
	}
	if !state.present {
		return
	}
	value, valid := decodeRequiredString(state.raw)
	if !valid {
		view.issue = &parseIssue{reason: ReasonInvalidProjection, field: FieldTurnState}
		return
	}
	view.setValue(FieldTurnState, value)
}

func inspectServerResponseReference(view *MessageView, root objectView) {
	responseField := root.exact("response")
	if responseField.duplicate {
		view.issue = &parseIssue{reason: ReasonDuplicateSecurityKey, field: FieldResponseReference}
		return
	}
	if !responseField.present {
		return
	}
	response, err := decodeObject(responseField.raw)
	if err != nil {
		view.issue = &parseIssue{reason: ReasonInvalidProjection, field: FieldResponseReference}
		return
	}
	id := response.exact("id")
	if id.duplicate {
		view.issue = &parseIssue{reason: ReasonDuplicateSecurityKey, field: FieldResponseReference}
		return
	}
	if !id.present {
		return
	}
	value, valid := decodeRequiredString(id.raw)
	if !valid {
		view.issue = &parseIssue{reason: ReasonInvalidProjection, field: FieldResponseReference}
		return
	}
	view.setValue(FieldResponseReference, value)
}

func (v *MessageView) setValue(field Field, value string) {
	if v.values == nil {
		v.values = make(map[Field]OpaqueValue)
	}
	v.values[field] = newOpaqueValue(value)
}

type rawField struct {
	present   bool
	duplicate bool
	raw       json.RawMessage
}

type objectMember struct {
	key string
	raw json.RawMessage
}

type objectView struct {
	members []objectMember
}

func (v objectView) exact(key string) rawField {
	return v.match(func(candidate string) bool { return candidate == key })
}

func (v objectView) folded(key string) rawField {
	return v.match(func(candidate string) bool { return strings.EqualFold(candidate, key) })
}

func (v objectView) match(matches func(string) bool) rawField {
	var field rawField
	for _, member := range v.members {
		if !matches(member.key) {
			continue
		}
		if field.present {
			field.duplicate = true
			continue
		}
		field.present = true
		field.raw = member.raw
	}
	return field
}

func decodeObject(raw []byte) (objectView, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return objectView{}, err
	}
	opening, object := token.(json.Delim)
	if !object || opening != '{' {
		return objectView{}, errExpectedObject
	}
	var view objectView
	for decoder.More() {
		keyToken, tokenErr := decoder.Token()
		if tokenErr != nil {
			return objectView{}, tokenErr
		}
		key, stringKey := keyToken.(string)
		if !stringKey {
			return objectView{}, errExpectedObjectKey
		}
		var value json.RawMessage
		if decodeErr := decoder.Decode(&value); decodeErr != nil {
			return objectView{}, decodeErr
		}
		view.members = append(view.members, objectMember{key: key, raw: value})
	}
	closingToken, err := decoder.Token()
	if err != nil {
		return objectView{}, err
	}
	closing, object := closingToken.(json.Delim)
	if !object || closing != '}' {
		return objectView{}, errExpectedObject
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return objectView{}, errTrailingJSON
		}
		return objectView{}, err
	}
	return view, nil
}

func decodeRequiredString(raw json.RawMessage) (string, bool) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || value == "" {
		return "", false
	}
	return value, true
}

var (
	errExpectedObject    = errors.New("expected JSON object")
	errExpectedObjectKey = errors.New("expected JSON object key")
	errTrailingJSON      = errors.New("trailing JSON value")
)
