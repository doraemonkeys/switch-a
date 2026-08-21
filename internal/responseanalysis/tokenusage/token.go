package tokenusage

import (
	"bytes"
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
)

// Constants for token parsing buffer sizes.
const (
	defaultTokenParseBytes     = 4 * 1024  // 4KB, tail buffer default size
	fullCaptureThreshold       = 32 * 1024 // 32KB, below this we capture full response
	MaxSSEBuffer               = 64 * 1024 // 64KB, SSE buffer max limit
	MinBufferReallocCapacity   = 8 * 1024  // 8KB, minimum capacity threshold for buffer reallocation
	maxUsageIntegerLexemeBytes = 64
	maxUsageIntegerExponent    = 64
)

// ObservedCount keeps the provider value separate from whether the provider
// actually supplied it. Analytics needs that distinction because an explicit
// zero is evidence while an omitted or invalid value is unknown.
type ObservedCount struct {
	Value   int64
	Present bool
}

func observedCount(value int64) ObservedCount {
	return ObservedCount{Value: value, Present: true}
}

func (c *ObservedCount) UnmarshalJSON(data []byte) error {
	if c == nil {
		return nil
	}
	*c = ObservedCount{}
	value, ok := parseJSONInteger(data)
	if ok {
		*c = observedCount(value)
	}
	// A malformed counter is one absent fact, not a reason to discard other
	// independent counters from the same usage envelope.
	return nil
}

func parseJSONInteger(data []byte) (int64, bool) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || (data[0] != '-' && (data[0] < '0' || data[0] > '9')) {
		return 0, false
	}
	var number json.Number
	if err := json.Unmarshal(data, &number); err != nil {
		return 0, false
	}
	return parseIntegerString(number.String())
}

func parseIntegerString(value string) (int64, bool) {
	if len(value) > maxUsageIntegerLexemeBytes || !usageExponentWithinBound(value) {
		return 0, false
	}
	rational, ok := new(big.Rat).SetString(value)
	if !ok || !rational.IsInt() || !rational.Num().IsInt64() {
		return 0, false
	}
	return rational.Num().Int64(), true
}

func usageExponentWithinBound(value string) bool {
	index := strings.IndexAny(value, "eE")
	if index < 0 {
		return true
	}
	exponentText := value[index+1:]
	if exponentText == "" {
		return false
	}
	exponent, err := strconv.ParseInt(exponentText, 10, 16)
	return err == nil && exponent >= -maxUsageIntegerExponent && exponent <= maxUsageIntegerExponent
}

func overlayCount(current *ObservedCount, later ObservedCount) {
	if later.Present {
		*current = later
	}
}

func accumulateCount(current *ObservedCount, addition ObservedCount) {
	if !addition.Present {
		return
	}
	current.Value += addition.Value
	current.Present = true
}

func countPointer(count ObservedCount) *int64 {
	if !count.Present {
		return nil
	}
	value := count.Value
	return &value
}

// TokenUsage represents the raw usage facts observed in one provider payload.
type TokenUsage struct {
	PromptTokens         ObservedCount
	CompletionTokens     ObservedCount
	TotalTokens          ObservedCount
	ReasoningTokens      ObservedCount
	CacheReadInputTokens ObservedCount
	CacheCreation        *CacheCreation
	ServiceTier          string
}

// CacheCreation holds provider-reported cache write facts and optional TTL
// breakdowns without assigning protocol-specific accounting semantics.
type CacheCreation struct {
	InputTokens            ObservedCount
	Ephemeral1hInputTokens ObservedCount
	Ephemeral5mInputTokens ObservedCount
}

// Clone returns a deep copy so callers can snapshot accumulated usage safely.
// WebSocket sessions update usage incrementally from relay goroutines, so log writers
// need an isolated copy instead of sharing mutable state.
func (u *TokenUsage) Clone() *TokenUsage {
	if u == nil {
		return nil
	}
	clone := *u
	if u.CacheCreation != nil {
		cacheCreation := *u.CacheCreation
		clone.CacheCreation = &cacheCreation
	}
	return &clone
}

// OverlayObserved combines cumulative or partial samples from one HTTP/SSE
// response. A later field replaces an earlier field only when it was observed.
func (u *TokenUsage) OverlayObserved(other *TokenUsage) *TokenUsage {
	if other == nil {
		return u
	}
	if u == nil {
		return other.Clone()
	}

	overlayCount(&u.PromptTokens, other.PromptTokens)
	overlayCount(&u.CompletionTokens, other.CompletionTokens)
	overlayCount(&u.TotalTokens, other.TotalTokens)
	overlayCount(&u.ReasoningTokens, other.ReasoningTokens)
	overlayCount(&u.CacheReadInputTokens, other.CacheReadInputTokens)

	if other.CacheCreation != nil {
		if u.CacheCreation == nil {
			cacheCreation := *other.CacheCreation
			u.CacheCreation = &cacheCreation
		} else {
			overlayCount(&u.CacheCreation.InputTokens, other.CacheCreation.InputTokens)
			overlayCount(&u.CacheCreation.Ephemeral1hInputTokens, other.CacheCreation.Ephemeral1hInputTokens)
			overlayCount(&u.CacheCreation.Ephemeral5mInputTokens, other.CacheCreation.Ephemeral5mInputTokens)
		}
	}
	if other.ServiceTier != "" {
		u.ServiceTier = other.ServiceTier
	}
	return u
}

// Accumulate combines distinct completed billable responses in one WebSocket
// session. Presence unions even when the added value is explicitly zero.
func (u *TokenUsage) Accumulate(other *TokenUsage) *TokenUsage {
	if other == nil {
		return u
	}
	if u == nil {
		return other.Clone()
	}

	accumulateCount(&u.PromptTokens, other.PromptTokens)
	accumulateCount(&u.CompletionTokens, other.CompletionTokens)
	accumulateCount(&u.TotalTokens, other.TotalTokens)
	accumulateCount(&u.ReasoningTokens, other.ReasoningTokens)
	accumulateCount(&u.CacheReadInputTokens, other.CacheReadInputTokens)
	if other.CacheCreation != nil {
		if u.CacheCreation == nil {
			u.CacheCreation = &CacheCreation{}
		}
		accumulateCount(&u.CacheCreation.InputTokens, other.CacheCreation.InputTokens)
		accumulateCount(&u.CacheCreation.Ephemeral1hInputTokens, other.CacheCreation.Ephemeral1hInputTokens)
		accumulateCount(&u.CacheCreation.Ephemeral5mInputTokens, other.CacheCreation.Ephemeral5mInputTokens)
	}

	if u.ServiceTier == "" {
		u.ServiceTier = other.ServiceTier
	} else if other.ServiceTier != "" && other.ServiceTier != u.ServiceTier {
		u.ServiceTier = ""
	}

	return u
}

// BillableInputTokens returns the legacy cache-adjusted approximation for
// protocols whose read/write multipliers are 0.1 and 1.25. Raw capture and
// analytics deliberately do not use this provider-pricing helper.
func (u *TokenUsage) BillableInputTokens() float64 {
	if u == nil {
		return 0
	}
	uncached := u.PromptTokens.Value - u.CacheReadInputTokens.Value
	uncached = max(uncached, 0) // Protect against anomalous provider counters.
	var cacheCreation int64
	if u.CacheCreation != nil {
		cacheCreation = u.CacheCreation.InputTokens.Value
	}
	return float64(uncached) + float64(u.CacheReadInputTokens.Value)*0.1 + float64(cacheCreation)*1.25
}

// CacheHitRatio returns the cache hit ratio.
func (u *TokenUsage) CacheHitRatio() float64 {
	if u == nil || u.PromptTokens.Value == 0 {
		return 0
	}
	return float64(u.CacheReadInputTokens.Value) / float64(u.PromptTokens.Value)
}

// cacheCreationField holds nested cache creation details with TTL breakdown.
type cacheCreationField struct {
	Ephemeral1hInputTokens ObservedCount `json:"ephemeral_1h_input_tokens"`
	Ephemeral5mInputTokens ObservedCount `json:"ephemeral_5m_input_tokens"`
}

// tokenDetailsField models nested token detail objects. Compatible APIs have
// shipped both singular and plural parent keys, so normalization accepts both.
type tokenDetailsField struct {
	CachedTokens     ObservedCount `json:"cached_tokens"`
	ReasoningTokens  ObservedCount `json:"reasoning_tokens"`
	CacheWriteTokens ObservedCount `json:"cache_write_tokens"`
}

// usageField represents the supported snake_case usage envelopes.
type usageField struct {
	// Prompt/completion aliases.
	PromptTokens     ObservedCount `json:"prompt_tokens"`
	CompletionTokens ObservedCount `json:"completion_tokens"`
	TotalTokens      ObservedCount `json:"total_tokens"`
	ReasoningTokens  ObservedCount `json:"reasoning_tokens"`
	// Nested token details use singular and plural compatibility aliases.
	PromptTokensDetails     *tokenDetailsField `json:"prompt_tokens_details"`
	InputTokensDetails      *tokenDetailsField `json:"input_tokens_details"`
	InputTokenDetails       *tokenDetailsField `json:"input_token_details"`
	CompletionTokensDetails *tokenDetailsField `json:"completion_tokens_details"`
	OutputTokensDetails     *tokenDetailsField `json:"output_tokens_details"`
	OutputTokenDetails      *tokenDetailsField `json:"output_token_details"`

	// Input/output aliases.
	InputTokens  ObservedCount `json:"input_tokens"`
	OutputTokens ObservedCount `json:"output_tokens"`

	// Flat cache facts.
	CacheReadInputTokens     ObservedCount `json:"cache_read_input_tokens"`
	CacheCreationInputTokens ObservedCount `json:"cache_creation_input_tokens"`

	// Nested cache TTL facts.
	CacheCreation *cacheCreationField `json:"cache_creation"`

	// Metadata
	ServiceTier string `json:"service_tier"`
}

// usageMetadataField represents the supported camelCase usage metadata envelope.
type usageMetadataField struct {
	PromptTokenCount        ObservedCount `json:"promptTokenCount"`
	CandidatesTokenCount    ObservedCount `json:"candidatesTokenCount"`
	TotalTokenCount         ObservedCount `json:"totalTokenCount"`
	CachedContentTokenCount ObservedCount `json:"cachedContentTokenCount"`
}

// usageResponse is a unified response structure compatible with multiple API formats.
// Note: Must handle two SSE formats:
// - message_start: may have nested cache_creation object
// - message_delta: usually has flat cache_creation_input_tokens
type usageResponse struct {
	// Snake-case usage envelope.
	Usage *usageField `json:"usage"`

	// Camel-case usage envelope.
	UsageMetadata *usageMetadataField `json:"usageMetadata"`
}

var usageKeys = [][]byte{
	[]byte(`"usage"`),
	[]byte(`"usageMetadata"`),
}

// Parse extracts usage fields from a provider response payload.
// It first tries direct JSON decoding, then falls back to usage-object extraction
// so truncated or prefixed payloads still yield observed usage facts when possible.
func Parse(data []byte) *TokenUsage {
	return ParseWithLogger(data, nil)
}

// ParseWithLogger behaves like Parse but emits debug diagnostics during recovery paths.
func ParseWithLogger(data []byte, logger Logger) *TokenUsage {
	if len(data) == 0 {
		return nil
	}

	// Strategy 1: Try parsing complete JSON
	if extracted := tryParseFullJSON(data); extracted != nil {
		return extracted
	}

	// Strategy 2: Bracket matching to extract usage sub-object (supports nesting)
	return extractUsageObject(data, logger)
}

// tryParseFullJSON attempts to parse the complete JSON response.
func tryParseFullJSON(data []byte) *TokenUsage {
	start := bytes.IndexByte(data, '{')
	if start < 0 {
		return nil
	}
	var resp usageResponse
	if json.Unmarshal(data[start:], &resp) == nil {
		return convertToTokenUsage(&resp)
	}
	return nil
}

// extractUsageObject uses bracket matching to extract usage object (supports nesting).
// Note: Bracket matching may be affected by {} inside strings (like {"note": "test{}"})
// but usage objects typically only have numeric fields, so practical impact is minimal.
func extractUsageObject(data []byte, logger Logger) *TokenUsage {
	for _, key := range usageKeys {
		idx := bytes.Index(data, key)
		if idx < 0 {
			continue
		}
		// Find first {
		start := bytes.IndexByte(data[idx:], '{')
		if start < 0 {
			continue
		}
		start += idx
		// Bracket matching to find end
		depth, end := 1, start+1
		for end < len(data) && depth > 0 {
			switch data[end] {
			case '{':
				depth++
			case '}':
				depth--
			}
			end++
		}
		if depth != 0 {
			if logger != nil {
				logger.Debug("bracket matching incomplete",
					"key", string(key),
					"depth", depth,
				)
			}
			continue
		}
		// Parse extracted object
		var usage map[string]any
		decoder := json.NewDecoder(bytes.NewReader(data[start:end]))
		decoder.UseNumber()
		if err := decoder.Decode(&usage); err != nil {
			if logger != nil {
				logger.Debug("bracket extracted JSON parse failed",
					"error", err,
					"data_preview", TruncateForLog(data[start:end], 100),
				)
			}
			continue
		}
		if result := normalizeUsageMap(usage); result != nil {
			return result
		}
	}
	return nil
}

// convertToTokenUsage converts usageResponse to TokenUsage.
func convertToTokenUsage(resp *usageResponse) *TokenUsage {
	if resp.Usage != nil {
		return convertUsageFieldToTokenUsage(resp.Usage)
	}
	if resp.UsageMetadata != nil {
		return convertUsageMetadataToTokenUsage(resp.UsageMetadata)
	}
	return nil
}

// convertUsageFieldToTokenUsage normalizes a snake-case usage envelope.
func convertUsageFieldToTokenUsage(u *usageField) *TokenUsage {
	prompt := firstObservedCount(u.PromptTokens, u.InputTokens)
	completion := firstObservedCount(u.CompletionTokens, u.OutputTokens)
	cacheRead := resolveCacheReadFromUsageField(u)
	reasoning := resolveReasoningTokensFromUsageField(u)
	result := &TokenUsage{
		PromptTokens:         prompt,
		CompletionTokens:     completion,
		TotalTokens:          u.TotalTokens,
		ReasoningTokens:      reasoning,
		CacheReadInputTokens: cacheRead,
		ServiceTier:          u.ServiceTier,
		CacheCreation:        resolveCacheCreationFromUsageField(u),
	}
	if !result.hasObservedCount() {
		return nil
	}
	return result
}

// convertUsageMetadataToTokenUsage normalizes a camel-case usage envelope.
func convertUsageMetadataToTokenUsage(m *usageMetadataField) *TokenUsage {
	result := &TokenUsage{
		PromptTokens:         m.PromptTokenCount,
		CompletionTokens:     m.CandidatesTokenCount,
		TotalTokens:          m.TotalTokenCount,
		CacheReadInputTokens: m.CachedContentTokenCount,
	}
	if !result.hasObservedCount() {
		return nil
	}
	return result
}

func firstObservedCount(counts ...ObservedCount) ObservedCount {
	for _, count := range counts {
		if count.Present {
			return count
		}
	}
	return ObservedCount{}
}

func (u *TokenUsage) hasObservedCount() bool {
	if u == nil {
		return false
	}
	if u.PromptTokens.Present || u.CompletionTokens.Present || u.TotalTokens.Present ||
		u.ReasoningTokens.Present || u.CacheReadInputTokens.Present {
		return true
	}
	return u.CacheCreation != nil && (u.CacheCreation.InputTokens.Present ||
		u.CacheCreation.Ephemeral1hInputTokens.Present || u.CacheCreation.Ephemeral5mInputTokens.Present)
}

func usageInt64(value any) ObservedCount {
	switch n := value.(type) {
	case json.Number:
		if value, ok := parseIntegerString(n.String()); ok {
			return observedCount(value)
		}
	case float64:
		if value, ok := parseIntegerString(strconv.FormatFloat(n, 'f', -1, 64)); ok {
			return observedCount(value)
		}
	case int64:
		return observedCount(n)
	case int:
		return observedCount(int64(n))
	}
	return ObservedCount{}
}

func lookupUsageInt64(m map[string]any, keys ...string) ObservedCount {
	for _, key := range keys {
		value, ok := m[key]
		if !ok {
			continue
		}
		if number := usageInt64(value); number.Present {
			return number
		}
	}
	return ObservedCount{}
}

func lookupUsageString(m map[string]any, key string) string {
	value, ok := m[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}

func lookupNestedUsageMap(m map[string]any, key string) map[string]any {
	value, ok := m[key]
	if !ok {
		return nil
	}
	childMap, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return childMap
}

func lookupNestedUsageInt64(m map[string]any, parentKey, childKey string) ObservedCount {
	childMap := lookupNestedUsageMap(m, parentKey)
	if childMap == nil {
		return ObservedCount{}
	}
	return lookupUsageInt64(childMap, childKey)
}

func lookupFirstNestedUsageInt64(m map[string]any, childKey string, parentKeys ...string) ObservedCount {
	for _, parentKey := range parentKeys {
		value := lookupNestedUsageInt64(m, parentKey, childKey)
		if value.Present {
			return value
		}
	}
	return ObservedCount{}
}

func resolveCacheReadFromUsageField(u *usageField) ObservedCount {
	if u == nil {
		return ObservedCount{}
	}
	if u.CacheReadInputTokens.Present {
		return u.CacheReadInputTokens
	}
	for _, details := range []*tokenDetailsField{
		u.PromptTokensDetails,
		u.InputTokensDetails,
		u.InputTokenDetails,
	} {
		if details != nil && details.CachedTokens.Present {
			return details.CachedTokens
		}
	}
	return ObservedCount{}
}

func resolveReasoningTokensFromUsageField(u *usageField) ObservedCount {
	if u == nil {
		return ObservedCount{}
	}
	if u.ReasoningTokens.Present {
		return u.ReasoningTokens
	}
	for _, details := range []*tokenDetailsField{
		u.CompletionTokensDetails,
		u.OutputTokensDetails,
		u.OutputTokenDetails,
	} {
		if details != nil && details.ReasoningTokens.Present {
			return details.ReasoningTokens
		}
	}
	return ObservedCount{}
}

func resolveCacheCreationFromUsageField(u *usageField) *CacheCreation {
	inputTokens := u.CacheCreationInputTokens
	if !inputTokens.Present {
		for _, details := range []*tokenDetailsField{u.PromptTokensDetails, u.InputTokensDetails, u.InputTokenDetails} {
			if details != nil && details.CacheWriteTokens.Present {
				inputTokens = details.CacheWriteTokens
				break
			}
		}
	}
	result := &CacheCreation{InputTokens: inputTokens}
	if u.CacheCreation != nil {
		result.Ephemeral1hInputTokens = u.CacheCreation.Ephemeral1hInputTokens
		result.Ephemeral5mInputTokens = u.CacheCreation.Ephemeral5mInputTokens
	}
	if !result.InputTokens.Present && !result.Ephemeral1hInputTokens.Present && !result.Ephemeral5mInputTokens.Present {
		return nil
	}
	return result
}

func resolveCacheReadTokens(m map[string]any) ObservedCount {
	cacheRead := lookupUsageInt64(m, "cache_read_input_tokens", "cachedContentTokenCount")
	if cacheRead.Present {
		return cacheRead
	}
	return lookupFirstNestedUsageInt64(
		m,
		"cached_tokens",
		"prompt_tokens_details",
		"input_tokens_details",
		"input_token_details",
	)
}

func resolveReasoningTokens(m map[string]any) ObservedCount {
	reasoning := lookupUsageInt64(m, "reasoning_tokens")
	if reasoning.Present {
		return reasoning
	}
	return lookupFirstNestedUsageInt64(
		m,
		"reasoning_tokens",
		"completion_tokens_details",
		"output_tokens_details",
		"output_token_details",
	)
}

func buildCacheCreationFromUsageMap(m map[string]any) *CacheCreation {
	cacheCreationTokens := lookupUsageInt64(m, "cache_creation_input_tokens")
	if !cacheCreationTokens.Present {
		cacheCreationTokens = lookupFirstNestedUsageInt64(
			m,
			"cache_write_tokens",
			"prompt_tokens_details",
			"input_tokens_details",
			"input_token_details",
		)
	}
	cacheCreationMap := lookupNestedUsageMap(m, "cache_creation")
	cacheCreation := &CacheCreation{
		InputTokens: cacheCreationTokens,
	}
	if cacheCreationMap != nil {
		cacheCreation.Ephemeral1hInputTokens = lookupUsageInt64(cacheCreationMap, "ephemeral_1h_input_tokens")
		cacheCreation.Ephemeral5mInputTokens = lookupUsageInt64(cacheCreationMap, "ephemeral_5m_input_tokens")
	}
	if !cacheCreation.InputTokens.Present && !cacheCreation.Ephemeral1hInputTokens.Present && !cacheCreation.Ephemeral5mInputTokens.Present {
		return nil
	}
	return cacheCreation
}

// normalizeUsageMap converts a map to TokenUsage.
func normalizeUsageMap(m map[string]any) *TokenUsage {
	prompt := lookupUsageInt64(m, "prompt_tokens", "input_tokens", "promptTokenCount")
	completion := lookupUsageInt64(m, "completion_tokens", "output_tokens", "candidatesTokenCount")
	total := lookupUsageInt64(m, "total_tokens", "totalTokenCount")
	cacheRead := resolveCacheReadTokens(m)
	reasoning := resolveReasoningTokens(m)

	result := &TokenUsage{
		PromptTokens:         prompt,
		CompletionTokens:     completion,
		TotalTokens:          total,
		ReasoningTokens:      reasoning,
		CacheReadInputTokens: cacheRead,
		ServiceTier:          lookupUsageString(m, "service_tier"),
	}
	result.CacheCreation = buildCacheCreationFromUsageMap(m)
	if !result.hasObservedCount() {
		return nil
	}
	return result
}

// TruncateForLog bounds payload previews so parse diagnostics stay readable.
func TruncateForLog(data []byte, maxLen int) string {
	if len(data) <= maxLen {
		return string(data)
	}
	return string(data[:maxLen]) + "..."
}

// UsageDetailsJSON represents the extended usage details stored as JSON.
// This contains fields that are less frequently queried but still useful.
type UsageDetailsJSON struct {
	ServiceTier            string `json:"service_tier,omitempty"`
	Ephemeral1hInputTokens *int64 `json:"ephemeral_1h_input_tokens,omitempty"`
	Ephemeral5mInputTokens *int64 `json:"ephemeral_5m_input_tokens,omitempty"`
}

// ToModelFields converts TokenUsage to fields suitable for RequestLog.
// Returns individual token counts and a JSON string for extended details.
// Returns nil pointers if the usage is nil.
func (u *TokenUsage) ToModelFields() (promptTokens, completionTokens, totalTokens, reasoningTokens, cacheReadTokens, cacheCreationTokens *int64, usageDetails *string) {
	if u == nil {
		return nil, nil, nil, nil, nil, nil, nil
	}

	// This is the single raw-fact persistence boundary. Mapping presence here
	// prevents transport writers from inventing completeness independently.
	promptTokens = countPointer(u.PromptTokens)
	completionTokens = countPointer(u.CompletionTokens)
	totalTokens = countPointer(u.TotalTokens)
	reasoningTokens = countPointer(u.ReasoningTokens)
	cacheReadTokens = countPointer(u.CacheReadInputTokens)
	if u.CacheCreation != nil {
		cacheCreationTokens = countPointer(u.CacheCreation.InputTokens)
	}

	// Build extended details JSON (only if there's something to store)
	details := UsageDetailsJSON{
		ServiceTier: u.ServiceTier,
	}
	if u.CacheCreation != nil {
		details.Ephemeral1hInputTokens = countPointer(u.CacheCreation.Ephemeral1hInputTokens)
		details.Ephemeral5mInputTokens = countPointer(u.CacheCreation.Ephemeral5mInputTokens)
	}

	if details.ServiceTier != "" || details.Ephemeral1hInputTokens != nil || details.Ephemeral5mInputTokens != nil {
		if jsonBytes, err := json.Marshal(details); err == nil {
			jsonStr := string(jsonBytes)
			usageDetails = &jsonStr
		}
	}

	return
}
