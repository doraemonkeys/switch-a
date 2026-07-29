package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/doraemonkeys/switch-a/internal/model"
)

const (
	reasoningObjectField       = "reasoning"
	outputConfigObjectField    = "output_config"
	thinkingObjectField        = "thinking"
	reasoningEffortMemberField = "reasoning_effort"
	effortMemberField          = "effort"
	thinkingTypeMemberField    = "type"
	budgetTokensMemberField    = "budget_tokens"
)

// reasoningMemberKind classifies top-level request members that carry
// reasoning configuration. Claude and Codex nest the controls inside an
// object, while Chat Completions (grok) uses a top-level scalar member.
type reasoningMemberKind uint8

const (
	reasoningObject reasoningMemberKind = iota
	outputConfigObject
	thinkingObject
	reasoningEffortMember
)

type requestedReasoningBuilder struct {
	observation model.RequestedReasoningObservation
	captured    bool
	invalid     bool
	ambiguous   bool
}

// ExtractRequestedReasoning observes only request shapes whose meaning is
// stable for the selected API and endpoint. Everything else stays explicit as
// unsupported instead of being mistaken for an omitted configuration.
func ExtractRequestedReasoning(apiType, path string, body []byte) model.RequestedReasoningObservation {
	// Observation support is defined over the native contract path; an
	// explicit namespace is routing metadata and must not mask the endpoint.
	if namespaceType, contractPath, ok := SplitAPINamespace(path); ok && namespaceType == apiType {
		path = contractPath
	}
	if !supportsReasoningObservation(apiType, path) {
		return reasoningObservationWithState(model.ReasoningObservationUnsupported)
	}

	builder := &requestedReasoningBuilder{}
	if err := scanRequestedReasoning(apiType, body, builder); err != nil {
		builder.invalid = true
	}
	return builder.build()
}

func supportsReasoningObservation(apiType, path string) bool {
	return apiType == APITypeClaude && path == RouteClaudeMessages ||
		apiType == APITypeCodex && (path == RouteCodexResponses ||
			path == RouteCodexResponsesV1 ||
			path == RouteCodexWebSearch ||
			path == RouteCodexWebSearchV1) ||
		apiType == APITypeGrok && (path == RouteGrokChatCompletions || path == RouteGrokChatCompletionsV1)
}

func scanRequestedReasoning(apiType string, body []byte, builder *requestedReasoningBuilder) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	opening, err := decoder.Token()
	if err != nil {
		return err
	}
	if opening != json.Delim('{') {
		return fmt.Errorf("reasoning request must be a JSON object")
	}

	seenMembers := make(map[string]bool)
	for decoder.More() {
		memberToken, tokenErr := decoder.Token()
		if tokenErr != nil {
			return tokenErr
		}
		member, ok := memberToken.(string)
		if !ok {
			return fmt.Errorf("reasoning request member name must be a string")
		}

		kind, relevant := reasoningMemberFor(apiType, member)
		if !relevant {
			if skipErr := skipJSONValue(decoder); skipErr != nil {
				return skipErr
			}
			continue
		}

		if seenMembers[member] {
			builder.ambiguous = true
		}
		seenMembers[member] = true

		var raw json.RawMessage
		if decodeErr := decoder.Decode(&raw); decodeErr != nil {
			return decodeErr
		}
		builder.clearMember(kind)
		if kind == reasoningEffortMember {
			builder.captureString(raw, &builder.observation.Effort)
			continue
		}
		if objectErr := scanReasoningObject(raw, kind, builder); objectErr != nil {
			builder.invalid = true
		}
	}

	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	if closing != json.Delim('}') {
		return fmt.Errorf("reasoning request object is not closed")
	}
	return requireJSONEOF(decoder)
}

func reasoningMemberFor(apiType, member string) (reasoningMemberKind, bool) {
	if apiType == APITypeCodex && member == reasoningObjectField {
		return reasoningObject, true
	}
	if apiType == APITypeGrok && member == reasoningEffortMemberField {
		return reasoningEffortMember, true
	}
	if apiType == APITypeClaude {
		switch member {
		case outputConfigObjectField:
			return outputConfigObject, true
		case thinkingObjectField:
			return thinkingObject, true
		}
	}
	return reasoningObject, false
}

func scanReasoningObject(raw json.RawMessage, kind reasoningMemberKind, builder *requestedReasoningBuilder) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil {
		return err
	}
	if opening != json.Delim('{') {
		return fmt.Errorf("reasoning configuration must be a JSON object")
	}

	seenMembers := make(map[string]bool)
	for decoder.More() {
		memberToken, tokenErr := decoder.Token()
		if tokenErr != nil {
			return tokenErr
		}
		member, ok := memberToken.(string)
		if !ok {
			return fmt.Errorf("reasoning configuration member name must be a string")
		}
		if !isRelevantReasoningMember(kind, member) {
			if skipErr := skipJSONValue(decoder); skipErr != nil {
				return skipErr
			}
			continue
		}

		if seenMembers[member] {
			builder.ambiguous = true
		}
		seenMembers[member] = true

		var memberRaw json.RawMessage
		if decodeErr := decoder.Decode(&memberRaw); decodeErr != nil {
			return decodeErr
		}
		builder.captureMember(kind, member, memberRaw)
	}

	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	if closing != json.Delim('}') {
		return fmt.Errorf("reasoning configuration object is not closed")
	}
	return requireJSONEOF(decoder)
}

func isRelevantReasoningMember(kind reasoningMemberKind, member string) bool {
	switch kind {
	case reasoningObject, outputConfigObject:
		return member == effortMemberField
	case thinkingObject:
		return member == thinkingTypeMemberField || member == budgetTokensMemberField
	default:
		return false
	}
}

func (builder *requestedReasoningBuilder) captureMember(kind reasoningMemberKind, member string, raw json.RawMessage) {
	switch {
	case (kind == reasoningObject || kind == outputConfigObject) && member == effortMemberField:
		builder.observation.Effort = nil
		builder.captureString(raw, &builder.observation.Effort)
	case kind == thinkingObject && member == thinkingTypeMemberField:
		builder.observation.Mode = nil
		builder.captureString(raw, &builder.observation.Mode)
	case kind == thinkingObject && member == budgetTokensMemberField:
		builder.observation.BudgetTokens = nil
		builder.captureBudget(raw)
	}
}

func (builder *requestedReasoningBuilder) clearMember(kind reasoningMemberKind) {
	switch kind {
	case reasoningObject, outputConfigObject, reasoningEffortMember:
		builder.observation.Effort = nil
	case thinkingObject:
		builder.observation.Mode = nil
		builder.observation.BudgetTokens = nil
	}
}

func (builder *requestedReasoningBuilder) captureString(raw json.RawMessage, destination **string) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		builder.invalid = true
		return
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || utf8.RuneCountInString(value) > model.MaxReasoningValueRunes {
		builder.invalid = true
		return
	}
	*destination = &value
	builder.captured = true
}

func (builder *requestedReasoningBuilder) captureBudget(raw json.RawMessage) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		builder.invalid = true
		return
	}
	var value int64
	if err := json.Unmarshal(raw, &value); err != nil {
		builder.invalid = true
		return
	}
	builder.observation.BudgetTokens = &value
	builder.captured = true
}

func (builder *requestedReasoningBuilder) build() model.RequestedReasoningObservation {
	state := model.ReasoningObservationAbsent
	switch {
	case builder.invalid:
		state = model.ReasoningObservationInvalid
	case builder.ambiguous:
		state = model.ReasoningObservationAmbiguous
	case builder.captured:
		state = model.ReasoningObservationCaptured
	}
	builder.observation.State = &state
	return builder.observation
}

func reasoningObservationWithState(state model.ReasoningObservationState) model.RequestedReasoningObservation {
	return model.RequestedReasoningObservation{State: &state}
}

// skipJSONValue advances through arbitrary values without materializing large
// message or input fields as RawMessage copies.
func skipJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter || delimiter != json.Delim('{') && delimiter != json.Delim('[') {
		return nil
	}

	depth := 1
	for depth > 0 {
		token, err = decoder.Token()
		if err != nil {
			return err
		}
		if delimiter, isDelimiter = token.(json.Delim); !isDelimiter {
			continue
		}
		switch delimiter {
		case json.Delim('{'), json.Delim('['):
			depth++
		case json.Delim('}'), json.Delim(']'):
			depth--
		}
	}
	return nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	if _, err := decoder.Token(); err == io.EOF {
		return nil
	} else if err != nil {
		return err
	}
	return fmt.Errorf("reasoning request contains trailing JSON data")
}
