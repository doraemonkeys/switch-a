// Package adapters owns provider-family envelope predicates, event
// classification, bounded semantic extraction, and usage observation.
package adapters

import (
	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/framing"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/tokenusage"
)

// EventClass is a mutually exclusive visibility classification. Usage may also
// be attached to control or visible events, so accounting never has to infer
// visibility from the presence of token fields.
type EventClass string

const (
	EventControl       EventClass = "control"
	EventUsage         EventClass = "usage"
	EventError         EventClass = "error"
	EventClientVisible EventClass = "client_visible"
	EventFailOpen      EventClass = "fail_open"
)

// SemanticFields retain provider casing for diagnostics; rule matching owns
// its normalization so extraction does not irreversibly erase source meaning.
type SemanticFields struct {
	Type    string `json:"type,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Reason  string `json:"reason,omitempty"`

	emptyPresence optionalFieldPresence
}

type optionalFieldPresence uint8

const (
	presenceType optionalFieldPresence = 1 << iota
	presenceCode
	presenceMessage
	presenceReason
)

// Presence methods preserve the provider's distinction between an absent
// optional field and a present empty string. Non-empty values imply presence,
// so the compact metadata records only the otherwise ambiguous case.
func (f SemanticFields) HasType() bool {
	return f.Type != "" || f.emptyPresence&presenceType != 0
}

func (f SemanticFields) HasCode() bool {
	return f.Code != "" || f.emptyPresence&presenceCode != 0
}

func (f SemanticFields) HasMessage() bool {
	return f.Message != "" || f.emptyPresence&presenceMessage != 0
}

func (f SemanticFields) HasReason() bool {
	return f.Reason != "" || f.emptyPresence&presenceReason != 0
}

func (f *SemanticFields) preserveEmptyPresence(value string, status fieldStatus, presence optionalFieldPresence) {
	if f != nil && status == fieldValid && value == "" {
		f.emptyPresence |= presence
	}
}

type Result struct {
	Class           EventClass
	Fields          *SemanticFields
	Usage           *tokenusage.TokenUsage
	Failure         framing.FailureReason
	AllocationError error
	resources       allocation.Bundle
}

func (r *Result) Release() {
	if r == nil {
		return
	}
	r.resources.Release()
	*r = Result{}
}

// TakeResources transfers allocations retained by semantic values to the root
// observation. The values remain valid until that owner calls Release.
func (r *Result) TakeResources() allocation.Bundle {
	if r == nil {
		return allocation.Bundle{}
	}
	return r.resources.Take()
}

type Limits struct {
	TypeBytes    int
	CodeBytes    int
	MessageBytes int
	ReasonBytes  int
}

// Dispatcher is event-local. Response-level deduplication and rule decisions
// belong to the coordinator, while this concrete dispatcher keeps provider
// variants and allocation ownership behind one deep-module boundary.
type Dispatcher struct {
	family apicontract.ErrorFamily
	base   baseAdapter
}

func New(family apicontract.ErrorFamily, kind framing.Kind, limits Limits) *Dispatcher {
	dispatcher, _ := NewWithReserver(family, kind, limits, allocation.NoopReserver{})
	return dispatcher
}

func NewWithReserver(family apicontract.ErrorFamily, kind framing.Kind, limits Limits, reserver allocation.Reserver) (*Dispatcher, error) {
	if reserver == nil {
		return nil, allocation.ErrNilReserver
	}
	return &Dispatcher{family: family, base: baseAdapter{kind: kind, limits: limits, reserver: reserver}}, nil
}

func (d *Dispatcher) Observe(frame framing.Frame) Result {
	if d == nil {
		return Result{Class: EventFailOpen, Failure: framing.FailureInternal}
	}
	switch d.family {
	case apicontract.ErrorFamilyAnthropicMessages:
		return (anthropicAdapter{baseAdapter: d.base}).Observe(frame)
	case apicontract.ErrorFamilyOpenAIResponses:
		return (responsesAdapter{baseAdapter: d.base}).Observe(frame)
	case apicontract.ErrorFamilyOpenAIChatCompletions:
		return (chatAdapter{baseAdapter: d.base}).Observe(frame)
	case apicontract.ErrorFamilyGoogleGenerateContent:
		return (googleAdapter{baseAdapter: d.base}).Observe(frame)
	default:
		return Result{Class: EventFailOpen, Failure: framing.FailureInternal}
	}
}

type baseAdapter struct {
	kind     framing.Kind
	limits   Limits
	reserver allocation.Reserver
}

func (a baseAdapter) begin(frame framing.Frame) (jsonDocument, *Result) {
	document, ok := decodeDocument(frame.Data)
	if !ok {
		result := Result{Class: EventFailOpen, Failure: framing.FailureMalformedFrame}
		return jsonDocument{}, &result
	}
	if document.root.kind != jsonObject {
		result := Result{Class: EventClientVisible}
		return jsonDocument{}, &result
	}
	return document, nil
}

func (a baseAdapter) resources() resourceContext {
	return resourceContext{reserver: a.reserver}
}

func (a baseAdapter) usage(document jsonDocument, nestedParent string, resources *resourceContext) *tokenusage.TokenUsage {
	if usage := standardRootUsage(document, resources); usage != nil {
		return usage
	}
	if nestedParent != "" {
		return nestedUsage(document, nestedParent, resources)
	}
	return nil
}

// ExtractUsage keeps HTTP and SSE accounting on the same reviewed parser seam
// as semantic adapters, even while the pending-response runtime is introduced
// in a later wave.
func ExtractUsage(data []byte, logger tokenusage.Logger) *tokenusage.TokenUsage {
	document, ok := decodeDocument(data)
	if !ok || document.root.kind != jsonObject {
		return nil
	}
	resources := resourceContext{reserver: allocation.NoopReserver{}}
	defer resources.release()
	if usage := standardRootUsage(document, &resources); usage != nil {
		return usage
	}
	if usage := googleRootUsage(document, &resources); usage != nil {
		return usage
	}
	if usage := nestedUsage(document, "message", &resources); usage != nil {
		return usage
	}
	return nestedUsage(document, "response", &resources)
}

func (a baseAdapter) nonError(usage *tokenusage.TokenUsage, usageOnly bool, resources *resourceContext) Result {
	if a.kind == framing.KindSSE && usage != nil && usageOnly {
		return resources.finish(Result{Class: EventUsage, Usage: usage})
	}
	return resources.finish(Result{Class: EventClientVisible, Usage: usage})
}
