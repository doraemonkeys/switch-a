package tokenusage

import (
	"bytes"
	"encoding/json"
)

// Constants for token parsing buffer sizes.
const (
	defaultTokenParseBytes   = 4 * 1024  // 4KB, tail buffer default size
	fullCaptureThreshold     = 32 * 1024 // 32KB, below this we capture full response
	MaxSSEBuffer             = 64 * 1024 // 64KB, SSE buffer max limit
	MinBufferReallocCapacity = 8 * 1024  // 8KB, minimum capacity threshold for buffer reallocation
)

// TokenUsage represents complete token usage statistics.
type TokenUsage struct {
	// === Common fields (unified semantics across providers) ===
	PromptTokens     int64 // Total input tokens (OpenAI: prompt_tokens, Claude: input_tokens)
	CompletionTokens int64 // Output tokens (OpenAI: completion_tokens, Claude: output_tokens)
	TotalTokens      int64 // Total
	ReasoningTokens  int64 // OpenAI reasoning tokens included in output/completion tokens

	// === Claude cache ===
	CacheReadInputTokens int64          // Tokens read from cache (billed at 10% of standard input)
	CacheCreation        *CacheCreation // Tokens written to cache (billed at 125% of standard, with TTL breakdown)

	// === Metadata ===
	ServiceTier string // "standard" / "scale" etc.
}

// CacheCreation holds cache write statistics (with TTL breakdown).
type CacheCreation struct {
	InputTokens            int64 // cache_creation_input_tokens total
	Ephemeral1hInputTokens int64 // 1-hour ephemeral cache
	Ephemeral5mInputTokens int64 // 5-minute ephemeral cache
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

// Merge adds another usage sample into the receiver.
// WebSocket sessions may emit multiple billable events over one connection, so the
// connection log needs an accumulated total rather than the last event only.
func (u *TokenUsage) Merge(other *TokenUsage) *TokenUsage {
	if other == nil {
		return u
	}
	if u == nil {
		return other.Clone()
	}

	u.PromptTokens += other.PromptTokens
	u.CompletionTokens += other.CompletionTokens
	u.TotalTokens += other.TotalTokens
	u.ReasoningTokens += other.ReasoningTokens
	u.CacheReadInputTokens += other.CacheReadInputTokens

	if other.CacheCreation != nil {
		if u.CacheCreation == nil {
			u.CacheCreation = &CacheCreation{}
		}
		u.CacheCreation.InputTokens += other.CacheCreation.InputTokens
		u.CacheCreation.Ephemeral1hInputTokens += other.CacheCreation.Ephemeral1hInputTokens
		u.CacheCreation.Ephemeral5mInputTokens += other.CacheCreation.Ephemeral5mInputTokens
	}

	if u.ServiceTier == "" {
		u.ServiceTier = other.ServiceTier
	} else if other.ServiceTier != "" && other.ServiceTier != u.ServiceTier {
		u.ServiceTier = ""
	}

	return u
}

// BillableInputTokens returns the billable equivalent input tokens.
// Claude billing: cache_read * 0.1 + cache_creation * 1.25 + uncached * 1.0
func (u *TokenUsage) BillableInputTokens() float64 {
	if u == nil {
		return 0
	}
	uncached := u.PromptTokens - u.CacheReadInputTokens
	uncached = max(uncached, 0) // Protect against anomalous provider counters.
	var cacheCreation int64
	if u.CacheCreation != nil {
		cacheCreation = u.CacheCreation.InputTokens
	}
	return float64(uncached) + float64(u.CacheReadInputTokens)*0.1 + float64(cacheCreation)*1.25
}

// CacheHitRatio returns the cache hit ratio.
func (u *TokenUsage) CacheHitRatio() float64 {
	if u == nil || u.PromptTokens == 0 {
		return 0
	}
	return float64(u.CacheReadInputTokens) / float64(u.PromptTokens)
}

// tailBuffer is a ring buffer that retains the last N bytes.
// Used to extract the `usage` field from response tail.
type tailBuffer struct {
	buf  []byte
	size int
	pos  int
	full bool
}

func newTailBuffer(size int) *tailBuffer {
	return &tailBuffer{buf: make([]byte, size), size: size}
}

func (tb *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if n >= tb.size {
		copy(tb.buf, p[n-tb.size:])
		tb.pos = 0
		tb.full = true
		return n, nil
	}
	// Check for wrap-around
	if tb.pos+n >= tb.size {
		tb.full = true
	}
	// Segmented copy
	firstPart := tb.size - tb.pos
	if firstPart >= n {
		copy(tb.buf[tb.pos:], p)
	} else {
		copy(tb.buf[tb.pos:], p[:firstPart])
		copy(tb.buf, p[firstPart:])
	}
	tb.pos = (tb.pos + n) % tb.size
	return n, nil
}

// Bytes returns buffer content in write order.
// Uses make + copy to avoid potential over-allocation from append.
func (tb *tailBuffer) Bytes() []byte {
	if !tb.full {
		return tb.buf[:tb.pos]
	}
	// When pos == 0, buf is already in correct order, return a copy
	if tb.pos == 0 {
		result := make([]byte, tb.size)
		copy(result, tb.buf)
		return result
	}
	result := make([]byte, tb.size)
	n := copy(result, tb.buf[tb.pos:])
	copy(result[n:], tb.buf[:tb.pos])
	return result
}

// CaptureBuffer captures response data before it is parsed into usage fields.
// The transport layer chooses the concrete strategy based on response size.
type CaptureBuffer interface {
	Write(p []byte) (int, error)
	Bytes() []byte
}

// fullCaptureBuffer captures the entire response (for small responses).
type fullCaptureBuffer struct {
	buf *bytes.Buffer
}

func (b *fullCaptureBuffer) Write(p []byte) (int, error) { return b.buf.Write(p) }
func (b *fullCaptureBuffer) Bytes() []byte               { return b.buf.Bytes() }

// NewCaptureBuffer selects the buffer strategy based on Content-Length.
func NewCaptureBuffer(contentLength int64) CaptureBuffer {
	if contentLength == 0 {
		return nil // Empty response, avoid meaningless allocation
	}
	if contentLength > 0 && contentLength <= fullCaptureThreshold {
		return &fullCaptureBuffer{buf: bytes.NewBuffer(make([]byte, 0, contentLength))}
	}
	return newTailBuffer(defaultTokenParseBytes)
}

func newCaptureBuffer(contentLength int64) CaptureBuffer {
	return NewCaptureBuffer(contentLength)
}

// cacheCreationField holds nested cache creation details with TTL breakdown.
type cacheCreationField struct {
	Ephemeral1hInputTokens int64 `json:"ephemeral_1h_input_tokens"`
	Ephemeral5mInputTokens int64 `json:"ephemeral_5m_input_tokens"`
}

// tokenDetailsField models provider-specific nested token detail objects.
// OpenAI has shipped both singular and plural input-token detail keys across APIs,
// so normalization must accept either alias before Request Logs are persisted.
type tokenDetailsField struct {
	CachedTokens    int64 `json:"cached_tokens"`
	ReasoningTokens int64 `json:"reasoning_tokens"`
}

// usageField represents OpenAI/Claude usage format.
type usageField struct {
	// OpenAI
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	// OpenAI nested token details.
	// `input_tokens_details` is the current Responses/Codex shape while
	// `input_token_details` still appears in some Realtime payloads.
	PromptTokensDetails     *tokenDetailsField `json:"prompt_tokens_details"`
	InputTokensDetails      *tokenDetailsField `json:"input_tokens_details"`
	InputTokenDetails       *tokenDetailsField `json:"input_token_details"`
	CompletionTokensDetails *tokenDetailsField `json:"completion_tokens_details"`
	OutputTokensDetails     *tokenDetailsField `json:"output_tokens_details"`
	OutputTokenDetails      *tokenDetailsField `json:"output_token_details"`

	// Claude basic
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`

	// Claude cache (flat fields - message_delta format)
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`

	// Claude cache (nested object - message_start format, with TTL breakdown)
	CacheCreation *cacheCreationField `json:"cache_creation"`

	// Metadata
	ServiceTier string `json:"service_tier"`
}

// usageMetadataField represents Gemini usage metadata format.
type usageMetadataField struct {
	PromptTokenCount        int64 `json:"promptTokenCount"`
	CandidatesTokenCount    int64 `json:"candidatesTokenCount"`
	TotalTokenCount         int64 `json:"totalTokenCount"`
	CachedContentTokenCount int64 `json:"cachedContentTokenCount"` // Gemini cache
}

// usageResponse is a unified response structure compatible with multiple API formats.
// Note: Must handle two SSE formats:
// - message_start: may have nested cache_creation object
// - message_delta: usually has flat cache_creation_input_tokens
type usageResponse struct {
	// OpenAI / Claude format
	Usage *usageField `json:"usage"`

	// Gemini format
	UsageMetadata *usageMetadataField `json:"usageMetadata"`
}

var usageKeys = [][]byte{
	[]byte(`"usage"`),
	[]byte(`"usageMetadata"`),
}

// Parse extracts usage fields from a provider response payload.
// It first tries direct JSON decoding, then falls back to usage-object extraction
// so truncated or prefixed payloads still yield billable accounting when possible.
func Parse(data []byte) *TokenUsage {
	return ParseWithLogger(data, nil)
}

// Logger interface for debug logging during parsing.
type Logger interface {
	Debug(msg string, keysAndValues ...any)
}

// ZapLoggerAdapter adapts *zap.SugaredLogger to the Logger interface.
// This allows the token parsing code to use zap for structured logging.
type ZapLoggerAdapter struct {
	logger ZapSugaredLogger
}

// ZapSugaredLogger defines the minimal interface needed from *zap.SugaredLogger.
// Using an interface allows for easier testing and decoupling.
type ZapSugaredLogger interface {
	// Debugw logs a message with key-value pairs at debug level.
	Debugw(msg string, keysAndValues ...any)
}

// NewZapLoggerAdapter creates a new adapter for a zap sugared logger.
func NewZapLoggerAdapter(logger ZapSugaredLogger) *ZapLoggerAdapter {
	if logger == nil {
		return nil
	}
	return &ZapLoggerAdapter{logger: logger}
}

// Debug implements the Logger interface for ZapLoggerAdapter.
func (a *ZapLoggerAdapter) Debug(msg string, keysAndValues ...any) {
	if a == nil || a.logger == nil {
		return
	}
	// Use Debugw to log message with key-value pairs
	a.logger.Debugw(msg, keysAndValues...)
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
		if err := json.Unmarshal(data[start:end], &usage); err != nil {
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

// convertUsageFieldToTokenUsage converts OpenAI/Claude usage field to TokenUsage.
func convertUsageFieldToTokenUsage(u *usageField) *TokenUsage {
	// Claude uses input_tokens/output_tokens
	// OpenAI uses prompt_tokens/completion_tokens
	prompt := u.PromptTokens
	if prompt == 0 {
		prompt = u.InputTokens
	}
	completion := u.CompletionTokens
	if completion == 0 {
		completion = u.OutputTokens
	}
	total := u.TotalTokens
	if total == 0 {
		total = prompt + completion
	}
	cacheRead := resolveCacheReadFromUsageField(u)
	reasoning := resolveReasoningTokensFromUsageField(u)
	// Return only if at least one field has a value
	if prompt == 0 && completion == 0 && total == 0 && cacheRead == 0 && reasoning == 0 {
		return nil
	}

	result := &TokenUsage{
		PromptTokens:         prompt,
		CompletionTokens:     completion,
		TotalTokens:          total,
		ReasoningTokens:      reasoning,
		CacheReadInputTokens: cacheRead,
		ServiceTier:          u.ServiceTier,
	}

	// Handle cache write statistics (compatible with both nested and flat formats)
	if u.CacheCreationInputTokens > 0 || u.CacheCreation != nil {
		result.CacheCreation = &CacheCreation{
			InputTokens: u.CacheCreationInputTokens,
		}
		// If there's a nested cache_creation object, extract TTL details
		if u.CacheCreation != nil {
			result.CacheCreation.Ephemeral1hInputTokens = u.CacheCreation.Ephemeral1hInputTokens
			result.CacheCreation.Ephemeral5mInputTokens = u.CacheCreation.Ephemeral5mInputTokens
		}
	}

	return result
}

// convertUsageMetadataToTokenUsage converts Gemini usage metadata to TokenUsage.
func convertUsageMetadataToTokenUsage(m *usageMetadataField) *TokenUsage {
	if m.PromptTokenCount == 0 && m.CandidatesTokenCount == 0 && m.TotalTokenCount == 0 {
		return nil
	}
	return &TokenUsage{
		PromptTokens:         m.PromptTokenCount,
		CompletionTokens:     m.CandidatesTokenCount,
		TotalTokens:          m.TotalTokenCount,
		CacheReadInputTokens: m.CachedContentTokenCount, // Gemini cache mapping
	}
}

func usageInt64(value any) (int64, bool) {
	switch n := value.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	default:
		return 0, false
	}
}

func lookupUsageInt64(m map[string]any, keys ...string) int64 {
	for _, key := range keys {
		value, ok := m[key]
		if !ok {
			continue
		}
		if number, ok := usageInt64(value); ok {
			return number
		}
	}
	return 0
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

func lookupNestedUsageInt64(m map[string]any, parentKey, childKey string) int64 {
	childMap := lookupNestedUsageMap(m, parentKey)
	if childMap == nil {
		return 0
	}
	return lookupUsageInt64(childMap, childKey)
}

func lookupFirstNestedUsageInt64(m map[string]any, childKey string, parentKeys ...string) int64 {
	for _, parentKey := range parentKeys {
		value := lookupNestedUsageInt64(m, parentKey, childKey)
		if value != 0 {
			return value
		}
	}
	return 0
}

func resolveCacheReadFromUsageField(u *usageField) int64 {
	if u == nil {
		return 0
	}
	if u.CacheReadInputTokens != 0 {
		return u.CacheReadInputTokens
	}
	for _, details := range []*tokenDetailsField{
		u.PromptTokensDetails,
		u.InputTokensDetails,
		u.InputTokenDetails,
	} {
		if details != nil && details.CachedTokens != 0 {
			return details.CachedTokens
		}
	}
	return 0
}

func resolveReasoningTokensFromUsageField(u *usageField) int64 {
	if u == nil {
		return 0
	}
	for _, details := range []*tokenDetailsField{
		u.CompletionTokensDetails,
		u.OutputTokensDetails,
		u.OutputTokenDetails,
	} {
		if details != nil && details.ReasoningTokens != 0 {
			return details.ReasoningTokens
		}
	}
	return 0
}

func resolveCacheReadTokens(m map[string]any) int64 {
	cacheRead := lookupUsageInt64(m, "cache_read_input_tokens", "cachedContentTokenCount")
	if cacheRead != 0 {
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

func resolveReasoningTokens(m map[string]any) int64 {
	reasoning := lookupUsageInt64(m, "reasoning_tokens")
	if reasoning != 0 {
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
	cacheCreationMap := lookupNestedUsageMap(m, "cache_creation")

	if cacheCreationTokens == 0 && cacheCreationMap == nil {
		return nil
	}

	cacheCreation := &CacheCreation{
		InputTokens: cacheCreationTokens,
	}

	if cacheCreationMap == nil {
		return cacheCreation
	}

	cacheCreation.Ephemeral1hInputTokens = lookupUsageInt64(cacheCreationMap, "ephemeral_1h_input_tokens")
	cacheCreation.Ephemeral5mInputTokens = lookupUsageInt64(cacheCreationMap, "ephemeral_5m_input_tokens")
	return cacheCreation
}

// normalizeUsageMap converts a map to TokenUsage.
func normalizeUsageMap(m map[string]any) *TokenUsage {
	prompt := lookupUsageInt64(m, "prompt_tokens", "input_tokens", "promptTokenCount")
	completion := lookupUsageInt64(m, "completion_tokens", "output_tokens", "candidatesTokenCount")
	total := lookupUsageInt64(m, "total_tokens", "totalTokenCount")
	cacheRead := resolveCacheReadTokens(m)
	reasoning := resolveReasoningTokens(m)

	if total == 0 {
		total = prompt + completion
	}

	if prompt == 0 && completion == 0 && total == 0 && cacheRead == 0 && reasoning == 0 {
		return nil
	}

	result := &TokenUsage{
		PromptTokens:         prompt,
		CompletionTokens:     completion,
		TotalTokens:          total,
		ReasoningTokens:      reasoning,
		CacheReadInputTokens: cacheRead,
		ServiceTier:          lookupUsageString(m, "service_tier"),
	}
	result.CacheCreation = buildCacheCreationFromUsageMap(m)

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
	Ephemeral1hInputTokens int64  `json:"ephemeral_1h_input_tokens,omitempty"`
	Ephemeral5mInputTokens int64  `json:"ephemeral_5m_input_tokens,omitempty"`
}

// ToModelFields converts TokenUsage to fields suitable for RequestLog.
// Returns individual token counts and a JSON string for extended details.
// Returns nil pointers if the usage is nil.
func (u *TokenUsage) ToModelFields() (promptTokens, completionTokens, totalTokens, reasoningTokens, cacheReadTokens, cacheCreationTokens *int64, usageDetails *string) {
	if u == nil {
		return nil, nil, nil, nil, nil, nil, nil
	}

	// Core token fields
	promptTokens = &u.PromptTokens
	completionTokens = &u.CompletionTokens
	totalTokens = &u.TotalTokens
	if u.ReasoningTokens > 0 {
		reasoningTokens = &u.ReasoningTokens
	}

	// Cache read tokens (if present)
	if u.CacheReadInputTokens > 0 {
		cacheReadTokens = &u.CacheReadInputTokens
	}

	// Cache creation tokens (if present)
	if u.CacheCreation != nil && u.CacheCreation.InputTokens > 0 {
		cacheCreationTokens = &u.CacheCreation.InputTokens
	}

	// Build extended details JSON (only if there's something to store)
	details := UsageDetailsJSON{
		ServiceTier: u.ServiceTier,
	}
	if u.CacheCreation != nil {
		details.Ephemeral1hInputTokens = u.CacheCreation.Ephemeral1hInputTokens
		details.Ephemeral5mInputTokens = u.CacheCreation.Ephemeral5mInputTokens
	}

	// Only serialize if there's meaningful data
	if details.ServiceTier != "" || details.Ephemeral1hInputTokens > 0 || details.Ephemeral5mInputTokens > 0 {
		if jsonBytes, err := json.Marshal(details); err == nil {
			jsonStr := string(jsonBytes)
			usageDetails = &jsonStr
		}
	}

	return
}
