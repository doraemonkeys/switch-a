package responseanalysis

import (
	"io"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/framing"
)

// Registry has no mutable state: every resolution is derived from the frozen
// API catalog plus response metadata, so routing and analysis cannot drift.
type Registry struct{}

func NewRegistry() Registry {
	return Registry{}
}

func (Registry) Resolve(apiType, contentType, contentEncoding string) (Protocol, AnalysisFailureReason) {
	definition, ok := apicontract.Lookup(apiType)
	if !ok || !definition.SemanticErrorSupported {
		return Protocol{}, FailureUnsupportedProtocol
	}
	kind, ok := parseMediaKind(contentType)
	if !ok {
		return Protocol{}, FailureUnsupportedProtocol
	}
	coding, ok := ParseContentCoding(contentEncoding)
	if !ok {
		return Protocol{}, FailureUnsupportedEncoding
	}
	protocolID, ok := selectProtocolID(definition.ResponseProtocolIDs, kind)
	if !ok {
		return Protocol{}, FailureUnsupportedProtocol
	}
	return Protocol{
		id:     protocolID,
		family: definition.ErrorFamily,
		kind:   kind,
		coding: coding,
	}, ""
}

// Analyze keeps selection failures in the same observation vocabulary as
// decoder, framing, and adapter failures. This is primarily useful to bounded
// callers such as Test Message; the runtime uses Resolve directly.
func (r Registry) Analyze(
	apiType,
	contentType,
	contentEncoding string,
	source io.Reader,
	reserver allocation.Reserver,
) []Observation {
	return r.AnalyzeBounded(apiType, contentType, contentEncoding, source, reserver, DefaultTestMessageAnalysisLimits())
}

func (r Registry) AnalyzeBounded(
	apiType,
	contentType,
	contentEncoding string,
	source io.Reader,
	reserver allocation.Reserver,
	limits AnalysisLimits,
) []Observation {
	protocol, failure := r.Resolve(apiType, contentType, contentEncoding)
	if failure != "" {
		return []Observation{failureObservation("", failure)}
	}
	return protocol.AnalyzeBounded(source, reserver, limits)
}

func selectProtocolID(ids []apicontract.ResponseProtocolID, kind framing.Kind) (apicontract.ResponseProtocolID, bool) {
	for _, id := range ids {
		if protocolKind, known := kindForProtocolID(id); known && protocolKind == kind {
			return id, true
		}
	}
	return "", false
}

// The protocol ID is a frozen semantic identifier, not a naming convention.
// An explicit mapping prevents a future unrelated ID containing ".json." or
// ".sse." from being silently selected as a supported protocol.
func kindForProtocolID(id apicontract.ResponseProtocolID) (framing.Kind, bool) {
	switch id {
	case apicontract.ProtocolAnthropicMessagesJSON,
		apicontract.ProtocolOpenAIResponsesJSON,
		apicontract.ProtocolOpenAIChatCompletionsJSON,
		apicontract.ProtocolGoogleGenerateContentJSON:
		return framing.KindJSON, true
	case apicontract.ProtocolAnthropicMessagesSSE,
		apicontract.ProtocolOpenAIResponsesSSE,
		apicontract.ProtocolOpenAIChatCompletionsSSE,
		apicontract.ProtocolGoogleGenerateContentSSE:
		return framing.KindSSE, true
	default:
		return 0, false
	}
}
