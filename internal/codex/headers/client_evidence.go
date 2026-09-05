package codexheaders

import "net/http"

// MaxClientEventTypeBytes bounds recognition without retaining unknown event names.
const MaxClientEventTypeBytes = max(len(eventResponseCreate), len(eventResponseInject), len(eventResponseAppend))

// ClientEvidence contains only semantic claims. HTTP operations must not retain replay payloads.
type ClientEvidence struct {
	present    bool
	recognized bool
	eventType  string
	values     map[Field]OpaqueValue
	issue      *parseIssue
}

func (e ClientEvidence) Recognized() bool  { return e.recognized }
func (e ClientEvidence) EventType() string { return e.eventType }

// ClientEvidence detaches the observation from its transport-owned wire buffer.
func (v MessageView) ClientEvidence() ClientEvidence {
	issue := v.issue
	if v.present && v.direction != directionClient {
		issue = &parseIssue{reason: ReasonInvalidEnvelope, field: FieldEnvelope}
	}
	return ClientEvidence{present: v.present, recognized: v.recognized, eventType: v.eventType, values: v.values, issue: issue}
}

type ClientEvidenceInput struct {
	Headers         http.Header
	Evidence        ClientEvidence
	Owners          OwnerLookup
	AttestationLock OperationLockStatus
	StateAdmission  StateAdmission
}

func DecideClientEvidence(input ClientEvidenceInput) Result {
	e := input.Evidence
	return DecideClient(ClientInput{
		Headers: input.Headers,
		Message: MessageView{present: e.present, recognized: e.recognized, eventType: e.eventType, values: e.values, issue: e.issue, direction: directionClient},
		Owners:  input.Owners, AttestationLock: input.AttestationLock, StateAdmission: input.StateAdmission,
	})
}

// StringProjection preserves duplicate and type information before consumer interpretation.
type StringProjection struct {
	Present   bool
	Duplicate bool
	Valid     bool
	Value     string
}
type MetadataProjection struct {
	Present      bool
	Duplicate    bool
	ValidObject  bool
	ThreadID     StringProjection
	SessionID    StringProjection
	WindowID     StringProjection
	TurnMetadata StringProjection
}
type ClientProjection struct {
	Present            bool
	ValidObject        bool
	Type               StringProjection
	Metadata           MetadataProjection
	PreviousResponseID StringProjection
}

// ProjectClientEvidence preserves the existing client envelope's fixed field precedence.
func ProjectClientEvidence(p ClientProjection) ClientEvidence {
	view := MessageView{present: p.Present, direction: directionClient}
	if !p.ValidObject || !p.Type.Present || p.Type.Duplicate || !p.Type.Valid || p.Type.Value == "" {
		return view.ClientEvidence()
	}
	switch p.Type.Value {
	case eventResponseCreate:
		applyClientProjection(&view, p)
	case eventResponseInject, eventResponseAppend:
	default:
		return view.ClientEvidence()
	}
	view.recognized, view.eventType = true, p.Type.Value
	return view.ClientEvidence()
}
func applyClientProjection(view *MessageView, p ClientProjection) {
	m := p.Metadata
	if m.Duplicate {
		view.issue = &parseIssue{reason: ReasonDuplicateSecurityKey, field: FieldEnvelope}
		return
	}
	if m.Present {
		if !m.ValidObject {
			view.issue = &parseIssue{reason: ReasonInvalidProjection, field: FieldEnvelope}
			return
		}
		for _, entry := range []struct {
			value StringProjection
			field Field
		}{
			{m.ThreadID, FieldThreadID}, {m.SessionID, FieldSessionID}, {m.WindowID, FieldWindowID}, {m.TurnMetadata, FieldTurnMetadata},
		} {
			if !applyClientString(view, entry.value, entry.field) {
				return
			}
		}
	}
	applyClientString(view, p.PreviousResponseID, FieldResponseReference)
}
func applyClientString(view *MessageView, value StringProjection, field Field) bool {
	if value.Duplicate {
		view.issue = &parseIssue{reason: ReasonDuplicateSecurityKey, field: field}
		return false
	}
	if !value.Present {
		return true
	}
	if !value.Valid || value.Value == "" {
		view.issue = &parseIssue{reason: ReasonInvalidProjection, field: field}
		return false
	}
	view.setValue(field, value.Value)
	return true
}
func projectRawString(field rawField) StringProjection {
	value, valid := decodeRequiredString(field.raw)
	return StringProjection{Present: field.present, Duplicate: field.duplicate, Valid: valid, Value: value}
}
func projectRawClient(root objectView) ClientProjection {
	p := ClientProjection{Present: true, ValidObject: true, Type: projectRawString(root.exact("type")), PreviousResponseID: projectRawString(root.exact("previous_response_id"))}
	field := root.exact("client_metadata")
	p.Metadata.Present, p.Metadata.Duplicate = field.present, field.duplicate
	if field.present && !field.duplicate {
		object, err := decodeObject(field.raw)
		if err == nil {
			p.Metadata.ValidObject = true
			p.Metadata.ThreadID = projectRawString(object.exact("thread_id"))
			p.Metadata.SessionID = projectRawString(object.exact("session_id"))
			p.Metadata.WindowID = projectRawString(object.exact("x-codex-window-id"))
			p.Metadata.TurnMetadata = projectRawString(object.exact("x-codex-turn-metadata"))
		}
	}
	return p
}
