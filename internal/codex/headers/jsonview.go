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
	eventResponseIncomplete    = "response.incomplete"
	eventResponseFailed        = "response.failed"
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
	present           bool
	recognized        bool
	wire              []byte
	eventType         string
	responseLifecycle ResponseLifecycle
	values            map[Field]OpaqueValue
	issue             *parseIssue
	direction         messageDirection
}

func (v MessageView) Recognized() bool                     { return v.recognized }
func (v MessageView) EventType() string                    { return v.eventType }
func (v MessageView) ResponseLifecycle() ResponseLifecycle { return v.responseLifecycle }
func (v MessageView) ReplayBytes() []byte                  { return v.wire }

// InspectClientFrame observes a text-frame payload whose semantic and wire
// bytes are identical.
func InspectClientFrame(raw []byte) MessageView {
	return inspectMessage(raw, raw, directionClient, false)
}

// InspectClientPayload separates semantic JSON from compressed/framed wire
// bytes, allowing the caller to replay the latter byte-for-byte.
func InspectClientPayload(wire, semantic []byte) MessageView {
	return inspectMessage(wire, semantic, directionClient, false)
}

func InspectServerFrame(raw []byte) MessageView {
	return inspectMessage(raw, raw, directionServer, false)
}

func inspectMessage(wire, semantic []byte, direction messageDirection, serverSSE bool) MessageView {
	view := MessageView{present: true, wire: wire, direction: direction}
	root, err := decodeObject(semantic)
	if err != nil {
		return view
	}
	typeField := root.exact("type")
	if typeField.duplicate || !typeField.present {
		return view
	}
	eventType, valid := decodeRequiredString(typeField.raw)
	if !valid {
		return view
	}
	var recognized bool
	if direction == directionClient {
		recognized = inspectClientRoot(&view, eventType, root)
	} else {
		recognized = inspectServerRoot(&view, eventType, root, serverSSE)
	}
	if recognized {
		view.recognized = true
		view.eventType = eventType
	}
	return view
}

func inspectClientRoot(view *MessageView, eventType string, root objectView) bool {
	switch eventType {
	case eventResponseCreate:
		inspectResponseCreate(view, root)
		return true
	case eventResponseInject, eventResponseAppend:
		// These controls are connection-bound transport contracts. Their payload
		// shape is intentionally opaque because no stable target field is evidenced.
		return true
	default:
		return false
	}
}

func inspectResponseCreate(view *MessageView, root objectView) {
	applyClientProjection(view, projectRawClient(root))
}

func inspectServerRoot(view *MessageView, eventType string, root objectView, sse bool) bool {
	switch eventType {
	case eventCodexResponseMetadata:
		inspectServerMetadata(view, root)
		return true
	case eventResponseMetadata:
		if !sse {
			return false
		}
		inspectSSEResponseMetadata(view, root)
		return true
	case eventResponseCreated, eventResponseInProgress:
		view.responseLifecycle = ResponseLifecycleActive
		inspectServerResponseReference(view, root)
		return true
	case eventResponseCompleted, eventResponseIncomplete, eventResponseFailed:
		view.responseLifecycle = ResponseLifecycleTerminal
		inspectServerResponseReference(view, root)
		return true
	default:
		return false
	}
}

func inspectSSEResponseMetadata(view *MessageView, root objectView) {
	responseID := root.exact("response_id")
	if responseID.duplicate {
		view.issue = &parseIssue{reason: ReasonDuplicateSecurityKey, field: FieldResponseReference}
		return
	}
	if !responseID.present {
		return
	}
	value, valid := decodeRequiredString(responseID.raw)
	if !valid {
		view.issue = &parseIssue{reason: ReasonInvalidProjection, field: FieldResponseReference}
		return
	}
	view.setValue(FieldResponseReference, value)
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
