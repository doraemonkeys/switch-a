package semantic

import (
	"strconv"
	"strings"
	"unicode/utf8"

	codexheaders "github.com/doraemonkeys/switch-a/internal/codex/headers"
	"github.com/doraemonkeys/switch-a/internal/model"
)

type projection struct {
	contract     ReasoningContract
	model        string
	modelInvalid bool
	modelPresent bool
	reasoning    reasoningBuilder
	client       codexheaders.ClientProjection
}
type reasoningBuilder struct {
	observation model.RequestedReasoningObservation
	captured    bool
	invalid     bool
	ambiguous   bool
	seen        map[string]bool
}

func validReasoningScalar(key string, v jsonValue) bool {
	if key == "budget_tokens" {
		if v.kind != '0' {
			return false
		}
		_, err := strconv.ParseInt(v.text, 10, 64)
		return err == nil
	}
	return v.kind == '"' && v.validString && utf8.RuneCountInString(v.text) <= model.MaxReasoningValueRunes
}
func (p *projection) member(key string, v jsonValue) {
	if strings.EqualFold(key, "model") {
		p.modelPresent = true
		switch v.kind {
		case '"':
			p.model = v.text
		case 'n':
		default:
			p.modelInvalid = true
		}
	}
	p.reasoningMember(key, v)
	switch key {
	case "type":
		setClientString(&p.client.Type, v)
	case "previous_response_id":
		setClientString(&p.client.PreviousResponseID, v)
	case "client_metadata":
		if p.client.Metadata.Present {
			p.client.Metadata.Duplicate = true
			return
		}
		m := codexheaders.MetadataProjection{Present: true, ValidObject: v.kind == '{'}
		m.ThreadID = clientField(v, "thread_id")
		m.SessionID = clientField(v, "session_id")
		m.WindowID = clientField(v, "x-codex-window-id")
		m.TurnMetadata = clientField(v, "x-codex-turn-metadata")
		p.client.Metadata = m
	}
}
func setClientString(destination *codexheaders.StringProjection, v jsonValue) {
	if destination.Present {
		destination.Duplicate = true
		return
	}
	*destination = codexheaders.StringProjection{Present: true, Valid: v.kind == '"' && v.validString, Value: v.text}
}
func clientField(v jsonValue, key string) codexheaders.StringProjection {
	field, ok := v.fields[key]
	if !ok {
		return codexheaders.StringProjection{}
	}
	return codexheaders.StringProjection{Present: true, Duplicate: field.duplicate, Valid: field.first.kind == '"' && field.first.validString, Value: field.first.text}
}
func (p *projection) reasoningMember(key string, v jsonValue) {
	relevant := p.contract == ReasoningCodex && key == "reasoning" ||
		p.contract == ReasoningClaude && (key == "thinking" || key == "output_config") ||
		p.contract == ReasoningChat && key == "reasoning_effort"
	if !relevant {
		return
	}
	b := &p.reasoning
	if b.seen == nil {
		b.seen = make(map[string]bool)
	}
	b.ambiguous = b.ambiguous || b.seen[key]
	b.seen[key] = true
	if key == "thinking" {
		b.observation.Mode = nil
		b.observation.BudgetTokens = nil
	} else {
		b.observation.Effort = nil
	}
	if key == "reasoning_effort" {
		if !validReasoningScalar(key, v) {
			b.invalid = true
			return
		}
		value := v.text
		b.observation.Effort = &value
		b.captured = true
		return
	}
	if v.kind != '{' {
		b.invalid = true
		return
	}
	if key == "thinking" {
		b.field(v, "type", &b.observation.Mode)
		if field, ok := v.fields["budget_tokens"]; ok {
			b.flags(field)
			if validReasoningScalar("budget_tokens", field.last) {
				value, _ := strconv.ParseInt(field.last.text, 10, 64)
				b.observation.BudgetTokens = &value
			}
		}
	} else {
		b.field(v, "effort", &b.observation.Effort)
	}
}
func (b *reasoningBuilder) flags(field jsonField) {
	b.ambiguous = b.ambiguous || field.duplicate
	b.invalid = b.invalid || field.invalid
	b.captured = b.captured || field.captured
}
func (b *reasoningBuilder) field(v jsonValue, key string, destination **string) {
	field, ok := v.fields[key]
	if !ok {
		return
	}
	b.flags(field)
	if validReasoningScalar(key, field.last) {
		value := field.last.text
		*destination = &value
	}
}
func (b *reasoningBuilder) build() model.RequestedReasoningObservation {
	state := model.ReasoningObservationAbsent
	switch {
	case b.invalid:
		state = model.ReasoningObservationInvalid
	case b.ambiguous:
		state = model.ReasoningObservationAmbiguous
	case b.captured:
		state = model.ReasoningObservationCaptured
	}
	b.observation.State = &state
	return b.observation
}
func reasoningState(state model.ReasoningObservationState) model.RequestedReasoningObservation {
	return model.RequestedReasoningObservation{State: &state}
}
func (p *projection) finish(present bool, jsonErr error) Result {
	result := Result{
		Model:     Fact[string]{State: Unavailable, Value: unknownModel, Reason: ReasonMissing},
		Reasoning: Fact[model.RequestedReasoningObservation]{State: Known},
		Codex:     Fact[codexheaders.ClientEvidence]{State: Known},
	}
	if jsonErr == nil && !p.modelInvalid && p.model != "" {
		result.Model = Fact[string]{State: Known, Value: p.model}
	}
	if jsonErr != nil || p.modelInvalid {
		result.Model.Reason = ReasonInvalidJSON
	}
	if p.contract == ReasoningUnsupported {
		result.Reasoning.Value = reasoningState(model.ReasoningObservationUnsupported)
	} else {
		p.reasoning.invalid = p.reasoning.invalid || jsonErr != nil
		result.Reasoning.Value = p.reasoning.build()
		if jsonErr != nil {
			result.Reasoning.Reason = ReasonInvalidJSON
		}
	}
	p.client.Present = present
	p.client.ValidObject = jsonErr == nil
	result.Codex.Value = codexheaders.ProjectClientEvidence(p.client)
	if jsonErr != nil {
		result.Codex.Reason = ReasonInvalidJSON
	}
	return result
}
