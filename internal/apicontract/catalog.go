// Package apicontract defines the request API and response-protocol contracts
// shared by routing, validation, response analysis, and admin projections.
package apicontract

import "strings"

// APIType identifies the request-routing contract selected by the gateway.
type APIType string

const (
	APITypeClaude         APIType = "claude"
	APITypeDeepSeekClaude APIType = "deepseek-claude"
	APITypeCodex          APIType = "codex"
	APITypeGemini         APIType = "gemini"
	APITypeGrok           APIType = "grok"
	APITypeDeepSeekOpenAI APIType = "deepseek-openai"
)

// CustomAPITypePrefix distinguishes user-defined provider contracts from the
// built-in contracts whose response semantics are known by the gateway.
const CustomAPITypePrefix = "custom:"

// ErrorFamily selects the bounded, root-relative semantic-error predicate.
type ErrorFamily string

const (
	ErrorFamilyAnthropicMessages     ErrorFamily = "anthropic_messages"
	ErrorFamilyOpenAIResponses       ErrorFamily = "openai_responses"
	ErrorFamilyGoogleGenerateContent ErrorFamily = "google_generate_content"
	ErrorFamilyOpenAIChatCompletions ErrorFamily = "openai_chat_completions"
)

// RequestDialect identifies request-shape behavior. It is intentionally
// independent from ErrorFamily because request rewriting must not change when
// response analysis gains a different adapter family.
type RequestDialect string

const (
	RequestDialectAnthropicMessages     RequestDialect = "anthropic_messages"
	RequestDialectOpenAIResponses       RequestDialect = "openai_responses"
	RequestDialectGoogleGenerateContent RequestDialect = "google_generate_content"
	RequestDialectOpenAIChatCompletions RequestDialect = "openai_chat_completions"
)

// UpstreamPathPolicy defines how a gateway request path is mapped to the
// provider base URL after an explicit namespace has been removed.
type UpstreamPathPolicy string

const (
	UpstreamPathPreserve        UpstreamPathPolicy = "preserve"
	UpstreamPathStripOptionalV1 UpstreamPathPolicy = "strip_optional_v1"
)

// ResponseProtocolID is an observation fact selected from the request contract
// and response metadata. It never participates in rule scope.
type ResponseProtocolID string

const (
	ProtocolAnthropicMessagesJSON     ResponseProtocolID = "anthropic.messages.json.v1"
	ProtocolAnthropicMessagesSSE      ResponseProtocolID = "anthropic.messages.sse.v1"
	ProtocolOpenAIResponsesJSON       ResponseProtocolID = "openai.responses.json.v1"
	ProtocolOpenAIResponsesSSE        ResponseProtocolID = "openai.responses.sse.v1"
	ProtocolOpenAIChatCompletionsJSON ResponseProtocolID = "openai.chat_completions.json.v1"
	ProtocolOpenAIChatCompletionsSSE  ResponseProtocolID = "openai.chat_completions.sse.v1"
	ProtocolGoogleGenerateContentJSON ResponseProtocolID = "google.generate_content.json.v1beta"
	ProtocolGoogleGenerateContentSSE  ResponseProtocolID = "google.generate_content.sse.v1beta"
)

// Definition contains the complete immutable contract for one built-in API.
// Slices returned by this package are cloned so callers cannot change routing
// or analysis behavior for other requests.
type Definition struct {
	APIType                APIType              `json:"api_type"`
	Label                  string               `json:"label"`
	Description            string               `json:"description"`
	DisplayOrder           int                  `json:"display_order"`
	SemanticErrorSupported bool                 `json:"semantic_error_supported"`
	RequestDialect         RequestDialect       `json:"request_dialect"`
	UpstreamPathPolicy     UpstreamPathPolicy   `json:"upstream_path_policy"`
	ErrorFamily            ErrorFamily          `json:"error_family"`
	ResponseProtocolIDs    []ResponseProtocolID `json:"response_protocol_ids"`
	NamespacePattern       string               `json:"namespace_pattern"`
	UnnamespacedRoutes     []Route              `json:"unnamespaced_routes"`
}

var definitions = []Definition{
	{
		APIType:                APITypeClaude,
		Label:                  "Claude",
		Description:            "Anthropic Messages API",
		DisplayOrder:           0,
		SemanticErrorSupported: true,
		RequestDialect:         RequestDialectAnthropicMessages,
		UpstreamPathPolicy:     UpstreamPathPreserve,
		ErrorFamily:            ErrorFamilyAnthropicMessages,
		ResponseProtocolIDs:    []ResponseProtocolID{ProtocolAnthropicMessagesJSON, ProtocolAnthropicMessagesSSE},
		NamespacePattern:       "/claude/",
		UnnamespacedRoutes: []Route{
			{Method: MethodPost, Pattern: RouteClaudeMessages, Match: RouteMatchExact},
			{Method: MethodPost, Pattern: RouteClaudeCountTokens, Match: RouteMatchExact},
			{Method: MethodGet, Pattern: RouteClaudeModels, Match: RouteMatchExact},
		},
	},
	{
		APIType:                APITypeDeepSeekClaude,
		Label:                  "DeepSeek Claude",
		Description:            "DeepSeek Anthropic Messages-compatible API",
		DisplayOrder:           1,
		SemanticErrorSupported: true,
		RequestDialect:         RequestDialectAnthropicMessages,
		UpstreamPathPolicy:     UpstreamPathPreserve,
		ErrorFamily:            ErrorFamilyAnthropicMessages,
		ResponseProtocolIDs:    []ResponseProtocolID{ProtocolAnthropicMessagesJSON, ProtocolAnthropicMessagesSSE},
		NamespacePattern:       "/deepseek-claude/",
	},
	{
		APIType:                APITypeCodex,
		Label:                  "Codex",
		Description:            "OpenAI Responses API",
		DisplayOrder:           2,
		SemanticErrorSupported: true,
		RequestDialect:         RequestDialectOpenAIResponses,
		UpstreamPathPolicy:     UpstreamPathStripOptionalV1,
		ErrorFamily:            ErrorFamilyOpenAIResponses,
		ResponseProtocolIDs:    []ResponseProtocolID{ProtocolOpenAIResponsesJSON, ProtocolOpenAIResponsesSSE},
		NamespacePattern:       "/codex/",
		UnnamespacedRoutes: []Route{
			{Method: MethodGet, Pattern: RouteCodexResponses, Match: RouteMatchExact},
			{Method: MethodPost, Pattern: RouteCodexResponses, Match: RouteMatchExact},
			{Method: MethodGet, Pattern: RouteCodexResponsesV1, Match: RouteMatchExact},
			{Method: MethodPost, Pattern: RouteCodexResponsesV1, Match: RouteMatchExact},
			{Method: MethodPost, Pattern: RouteCodexResponsesSubtree, Match: RouteMatchPrefix},
			{Method: MethodPost, Pattern: RouteCodexResponsesSubtreeV1, Match: RouteMatchPrefix},
			{Method: MethodPost, Pattern: RouteCodexWebSearch, Match: RouteMatchExact},
			{Method: MethodPost, Pattern: RouteCodexWebSearchV1, Match: RouteMatchExact},
		},
	},
	{
		APIType:                APITypeGemini,
		Label:                  "Gemini",
		Description:            "Google GenerateContent API",
		DisplayOrder:           3,
		SemanticErrorSupported: true,
		RequestDialect:         RequestDialectGoogleGenerateContent,
		UpstreamPathPolicy:     UpstreamPathPreserve,
		ErrorFamily:            ErrorFamilyGoogleGenerateContent,
		ResponseProtocolIDs:    []ResponseProtocolID{ProtocolGoogleGenerateContentJSON, ProtocolGoogleGenerateContentSSE},
		NamespacePattern:       "/gemini/",
		UnnamespacedRoutes: []Route{
			{Method: MethodPost, Pattern: RouteGeminiV1Beta, Match: RouteMatchPrefix},
		},
	},
	{
		APIType:                APITypeGrok,
		Label:                  "Grok",
		Description:            "xAI OpenAI Chat Completions-compatible API",
		DisplayOrder:           4,
		SemanticErrorSupported: true,
		RequestDialect:         RequestDialectOpenAIChatCompletions,
		UpstreamPathPolicy:     UpstreamPathStripOptionalV1,
		ErrorFamily:            ErrorFamilyOpenAIChatCompletions,
		ResponseProtocolIDs:    []ResponseProtocolID{ProtocolOpenAIChatCompletionsJSON, ProtocolOpenAIChatCompletionsSSE},
		NamespacePattern:       "/grok/",
		UnnamespacedRoutes: []Route{
			{Method: MethodPost, Pattern: RouteChatCompletions, Match: RouteMatchExact},
			{Method: MethodPost, Pattern: RouteChatCompletionsV1, Match: RouteMatchExact},
		},
	},
	{
		APIType:                APITypeDeepSeekOpenAI,
		Label:                  "DeepSeek OpenAI",
		Description:            "DeepSeek OpenAI Chat Completions-compatible API",
		DisplayOrder:           5,
		SemanticErrorSupported: true,
		RequestDialect:         RequestDialectOpenAIChatCompletions,
		UpstreamPathPolicy:     UpstreamPathStripOptionalV1,
		ErrorFamily:            ErrorFamilyOpenAIChatCompletions,
		ResponseProtocolIDs:    []ResponseProtocolID{ProtocolOpenAIChatCompletionsJSON, ProtocolOpenAIChatCompletionsSSE},
		NamespacePattern:       "/deepseek-openai/",
	},
}

// All returns the built-in definitions in stable display order.
func All() []Definition {
	result := make([]Definition, len(definitions))
	for i, definition := range definitions {
		result[i] = cloneDefinition(definition)
	}
	return result
}

// Lookup returns a cloned built-in definition.
func Lookup(apiType string) (Definition, bool) {
	for _, definition := range definitions {
		if string(definition.APIType) == apiType {
			return cloneDefinition(definition), true
		}
	}
	return Definition{}, false
}

// IsBuiltIn reports whether apiType names a registered built-in contract.
func IsBuiltIn(apiType string) bool {
	_, ok := Lookup(apiType)
	return ok
}

// ParseCustomAPIType returns the single path segment that identifies a custom
// provider contract. A slash would make validation and /custom/{id}/ routing
// disagree about which provider API type a request addresses.
func ParseCustomAPIType(apiType string) (toolID string, ok bool) {
	toolID, prefixed := strings.CutPrefix(apiType, CustomAPITypePrefix)
	if !prefixed || toolID == "" || toolID == "." || toolID == ".." || strings.ContainsRune(toolID, '/') {
		return "", false
	}
	return toolID, true
}

// IsValidProviderAPIType accepts every built-in contract and the existing
// non-empty custom namespace. Custom contracts remain routable but do not gain
// structured semantic analysis merely by sharing a response shape.
func IsValidProviderAPIType(apiType string) bool {
	if IsBuiltIn(apiType) {
		return true
	}
	_, ok := ParseCustomAPIType(apiType)
	return ok
}

// SupportsSemanticErrors reports whether a rule may be enabled for apiType.
func SupportsSemanticErrors(apiType string) bool {
	definition, ok := Lookup(apiType)
	return ok && definition.SemanticErrorSupported
}

// UsesRequestDialect lets request transformations consume their own explicit
// catalog axis instead of borrowing response-error compatibility.
func UsesRequestDialect(apiType string, dialect RequestDialect) bool {
	definition, ok := Lookup(apiType)
	return ok && definition.RequestDialect == dialect
}

func cloneDefinition(definition Definition) Definition {
	definition.ResponseProtocolIDs = append([]ResponseProtocolID{}, definition.ResponseProtocolIDs...)
	definition.UnnamespacedRoutes = append([]Route{}, definition.UnnamespacedRoutes...)
	return definition
}
