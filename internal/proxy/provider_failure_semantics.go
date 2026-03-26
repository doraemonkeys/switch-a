package proxy

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	codexUsageLimitErrorType            = "usage_limit_reached"
	usageLimitAutoDisableReason         = "usage limit reached"
	headerCodexPrimaryUsedPercent       = "X-Codex-Primary-Used-Percent"
	headerCodexSecondaryUsedPercent     = "X-Codex-Secondary-Used-Percent"
	headerCodexPrimaryResetAt           = "X-Codex-Primary-Reset-At"
	headerCodexSecondaryResetAt         = "X-Codex-Secondary-Reset-At"
	headerCodexPrimaryResetAfterSeconds = "X-Codex-Primary-Reset-After-Seconds"
	headerCodexSecondaryResetAfterSecs  = "X-Codex-Secondary-Reset-After-Seconds"
)

type providerFailureDisposition struct {
	switchReason      string
	autoDisableUntil  *time.Time
	autoDisableReason string
}

func (d providerFailureDisposition) forcesProviderSwitch() bool {
	return d.switchReason != ""
}

type codexUsageLimitPayload struct {
	Error struct {
		Type     string `json:"type"`
		ResetsAt int64  `json:"resets_at"`
	} `json:"error"`
}

func classifyProviderFailure(
	statusCode int,
	header http.Header,
	bodySnippet string,
	observedAt time.Time,
) providerFailureDisposition {
	if shouldForceProviderSwitch(statusCode) {
		return providerFailureDisposition{
			switchReason: formatPermanentErrorReason(statusCode),
		}
	}
	if statusCode != http.StatusTooManyRequests {
		return providerFailureDisposition{}
	}

	disableUntil := resolveUsageLimitDisableUntil(header, bodySnippet, observedAt)
	if disableUntil == nil {
		return providerFailureDisposition{}
	}
	return providerFailureDisposition{
		switchReason:      SwitchReasonUsageLimitReached,
		autoDisableUntil:  disableUntil,
		autoDisableReason: usageLimitAutoDisableReason,
	}
}

func classifyWebSocketHandshakeFailure(result *WebSocketResult) providerFailureDisposition {
	if result == nil || result.HandshakeAccepted || result.HandshakeStatusCode == 0 {
		return providerFailureDisposition{}
	}

	observedAt := result.HandshakeObservedAt
	if observedAt.IsZero() {
		observedAt = time.Now()
	}

	return classifyProviderFailure(
		result.HandshakeStatusCode,
		result.HandshakeHeaders,
		result.HandshakeBodySnippet,
		observedAt,
	)
}

func resolveUsageLimitDisableUntil(header http.Header, bodySnippet string, observedAt time.Time) *time.Time {
	bodyPayload, bodyIndicatesUsageLimit := parseUsageLimitPayload(bodySnippet)

	var candidates []time.Time
	headerReset, headerConfirmsUsageLimit := usageLimitResetFromHeaders(header, observedAt)
	if headerConfirmsUsageLimit && headerReset != nil {
		candidates = append(candidates, *headerReset)
	}
	if bodyIndicatesUsageLimit {
		if headerReset != nil {
			candidates = append(candidates, *headerReset)
		}
		if resetAt := unixSecondsToFutureTime(bodyPayload.Error.ResetsAt, observedAt); resetAt != nil {
			candidates = append(candidates, *resetAt)
		}
	}
	if len(candidates) == 0 {
		return nil
	}

	disableUntil := latestTime(candidates)
	// Current deployment keeps one OAuth account per provider. Provider-scoped
	// suspension is therefore the least surprising behavior until credential
	// sharing is introduced and the suspension scope expands accordingly.
	return &disableUntil
}

func usageLimitResetFromHeaders(header http.Header, observedAt time.Time) (*time.Time, bool) {
	if header == nil {
		return nil, false
	}

	primaryUsed, primaryUsedOK := parseHeaderFloat(header, headerCodexPrimaryUsedPercent)
	secondaryUsed, secondaryUsedOK := parseHeaderFloat(header, headerCodexSecondaryUsedPercent)
	primaryReset := parseResetTimeFromHeaders(header, headerCodexPrimaryResetAt, headerCodexPrimaryResetAfterSeconds, observedAt)
	secondaryReset := parseResetTimeFromHeaders(header, headerCodexSecondaryResetAt, headerCodexSecondaryResetAfterSecs, observedAt)

	candidates := make([]time.Time, 0, 2)
	if primaryUsedOK && primaryUsed >= 100 && primaryReset != nil {
		candidates = append(candidates, *primaryReset)
	}
	if secondaryUsedOK && secondaryUsed >= 100 && secondaryReset != nil {
		candidates = append(candidates, *secondaryReset)
	}
	if len(candidates) > 0 {
		latest := latestTime(candidates)
		return &latest, true
	}

	if primaryReset != nil {
		return primaryReset, false
	}
	return secondaryReset, false
}

func parseUsageLimitPayload(bodySnippet string) (codexUsageLimitPayload, bool) {
	var payload codexUsageLimitPayload
	if strings.TrimSpace(bodySnippet) == "" {
		return payload, false
	}
	if err := json.Unmarshal([]byte(bodySnippet), &payload); err != nil {
		return payload, false
	}
	return payload, payload.Error.Type == codexUsageLimitErrorType
}

func parseHeaderFloat(header http.Header, key string) (float64, bool) {
	value := strings.TrimSpace(header.Get(key))
	if value == "" {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func parseResetTimeFromHeaders(header http.Header, epochKey, afterKey string, observedAt time.Time) *time.Time {
	if resetAt := unixSecondsToFutureTime(parseHeaderInt64(header, epochKey), observedAt); resetAt != nil {
		return resetAt
	}

	afterSeconds := parseHeaderInt64(header, afterKey)
	if afterSeconds <= 0 {
		return nil
	}
	resetAt := observedAt.UTC().Add(time.Duration(afterSeconds) * time.Second)
	return &resetAt
}

func parseHeaderInt64(header http.Header, key string) int64 {
	value := strings.TrimSpace(header.Get(key))
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func unixSecondsToFutureTime(epochSeconds int64, observedAt time.Time) *time.Time {
	if epochSeconds <= 0 {
		return nil
	}
	resetAt := time.Unix(epochSeconds, 0).UTC()
	if !resetAt.After(observedAt.UTC()) {
		return nil
	}
	return &resetAt
}

func latestTime(times []time.Time) time.Time {
	latest := times[0]
	for _, candidate := range times[1:] {
		if candidate.After(latest) {
			latest = candidate
		}
	}
	return latest
}
