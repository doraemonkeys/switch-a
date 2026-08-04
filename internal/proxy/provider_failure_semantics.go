package proxy

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth/codexquota"
)

const (
	codexUsageLimitErrorType            = "usage_limit_reached"
	usageLimitAutoDisableReason         = "usage limit reached"
	headerCodexPrimaryUsedPercent       = codexquota.HeaderPrimaryUsedPercent
	headerCodexSecondaryUsedPercent     = codexquota.HeaderSecondaryUsedPercent
	headerCodexPrimaryResetAt           = codexquota.HeaderPrimaryResetAt
	headerCodexSecondaryResetAt         = codexquota.HeaderSecondaryResetAt
	headerCodexPrimaryResetAfterSeconds = codexquota.HeaderPrimaryResetAfterSeconds
	headerCodexSecondaryResetAfterSecs  = codexquota.HeaderSecondaryResetAfterSeconds
)

type providerFailureScope uint8

const (
	providerFailureScopeUnknown providerFailureScope = iota
	providerFailureScopeClient
	providerFailureScopeProvider
)

type providerFailureDisposition struct {
	switchReason      string
	autoDisableUntil  *time.Time
	autoDisableReason string
	scope             providerFailureScope
}

func (d providerFailureDisposition) forcesProviderSwitch() bool {
	return d.switchReason != ""
}

func (d providerFailureDisposition) isProviderScoped() bool {
	return d.scope == providerFailureScopeProvider
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
	return classifyProviderFailureForProvider(nil, statusCode, header, bodySnippet, observedAt)
}

func classifyProviderFailureForProvider(
	provider *model.Provider,
	statusCode int,
	header http.Header,
	bodySnippet string,
	observedAt time.Time,
) providerFailureDisposition {
	return classifyProviderFailureEvidence(
		provider.UsageLimitPolicyOrDefault(),
		providerFailureEvidenceFromHTTP(statusCode, header, bodySnippet, observedAt),
	)
}

type providerFailureEvidence struct {
	observedAt      time.Time
	statusCode      int
	errorKeys       []string
	resetCandidates []time.Time
}

func providerFailureEvidenceFromHTTP(
	statusCode int,
	header http.Header,
	bodySnippet string,
	observedAt time.Time,
) providerFailureEvidence {
	evidence := providerFailureEvidence{
		observedAt: normalizeObservedAt(observedAt),
		statusCode: statusCode,
	}

	bodyPayload, bodyIndicatesUsageLimit := parseUsageLimitPayload(bodySnippet)
	if bodyIndicatesUsageLimit {
		evidence.errorKeys = append(evidence.errorKeys, codexUsageLimitErrorType)
		if resetAt := unixSecondsToFutureTime(bodyPayload.Error.ResetsAt, evidence.observedAt); resetAt != nil {
			evidence.resetCandidates = append(evidence.resetCandidates, *resetAt)
		}
	}

	headerResets, headerConfirmsUsageLimit := usageLimitResetCandidatesFromHeaders(header, evidence.observedAt)
	evidence.resetCandidates = append(evidence.resetCandidates, headerResets...)
	if headerConfirmsUsageLimit {
		evidence.errorKeys = append(evidence.errorKeys, codexUsageLimitErrorType)
	}
	return evidence
}

func classifyProviderFailureEvidence(
	usageLimitPolicy model.ProviderUsageLimitPolicy,
	evidence providerFailureEvidence,
) providerFailureDisposition {
	if shouldForceProviderSwitch(evidence.statusCode) {
		return providerFailureDisposition{
			switchReason: formatPermanentErrorReason(evidence.statusCode),
			scope:        providerFailureScopeProvider,
		}
	}

	if isUsageLimitEvidence(evidence.errorKeys) {
		disposition := providerFailureDisposition{
			switchReason: SwitchReasonUsageLimitReached,
			scope:        providerFailureScopeProvider,
		}
		if usageLimitPolicy == model.ProviderUsageLimitPolicySuspend {
			disableUntil := latestFutureResetCandidate(evidence.resetCandidates, evidence.observedAt)
			if disableUntil != nil {
				disposition.autoDisableUntil = disableUntil
				disposition.autoDisableReason = usageLimitAutoDisableReason
			}
		}
		return disposition
	}

	statusScope, statusMatched := classifyProviderFailureScopeFromStatus(evidence.statusCode)
	identifierScope, identifierMatched := classifyProviderFailureScopeFromIdentifiers(evidence.errorKeys)
	if statusMatched && identifierMatched && statusScope != identifierScope {
		return providerFailureDisposition{}
	}
	if identifierMatched {
		return providerFailureDisposition{scope: identifierScope}
	}
	if statusMatched {
		return providerFailureDisposition{scope: statusScope}
	}
	return providerFailureDisposition{}
}

func usageLimitResetCandidatesFromHeaders(header http.Header, observedAt time.Time) ([]time.Time, bool) {
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
	return candidates, len(candidates) > 0
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

func normalizeObservedAt(observedAt time.Time) time.Time {
	if observedAt.IsZero() {
		return time.Now().UTC()
	}
	return observedAt.UTC()
}

func latestFutureResetCandidate(candidates []time.Time, observedAt time.Time) *time.Time {
	filtered := make([]time.Time, 0, len(candidates))
	normalizedObservedAt := normalizeObservedAt(observedAt)
	for _, candidate := range candidates {
		if candidate.After(normalizedObservedAt) {
			filtered = append(filtered, candidate.UTC())
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	latest := latestTime(filtered)
	return &latest
}

func isUsageLimitEvidence(errorKeys []string) bool {
	for _, key := range errorKeys {
		if normalizeProviderFailureKey(key) == codexUsageLimitErrorType {
			return true
		}
	}
	return false
}

func normalizeProviderFailureKey(key string) string {
	normalized := strings.ToLower(strings.TrimSpace(key))
	return strings.ReplaceAll(normalized, " ", "_")
}

func classifyProviderFailureScopeFromStatus(statusCode int) (providerFailureScope, bool) {
	switch {
	case statusCode <= 0:
		return providerFailureScopeUnknown, false
	case statusCode >= 500 ||
		statusCode == http.StatusPaymentRequired ||
		statusCode == http.StatusTooManyRequests ||
		statusCode == http.StatusUnauthorized ||
		statusCode == http.StatusForbidden:
		return providerFailureScopeProvider, true
	case statusCode >= 400:
		return providerFailureScopeClient, true
	default:
		return providerFailureScopeUnknown, false
	}
}

func classifyProviderFailureScopeFromIdentifiers(errorKeys []string) (providerFailureScope, bool) {
	classification := providerFailureScopeUnknown
	matched := false
	for _, key := range errorKeys {
		normalized := normalizeProviderFailureKey(key)
		if normalized == "" {
			continue
		}
		nextClassification, ok := classifyProviderFailureScopeFromIdentifier(normalized)
		if !ok {
			continue
		}
		if matched && classification != nextClassification {
			return providerFailureScopeUnknown, true
		}
		classification = nextClassification
		matched = true
	}
	return classification, matched
}

func classifyProviderFailureScopeFromIdentifier(key string) (providerFailureScope, bool) {
	if key == codexUsageLimitErrorType {
		return providerFailureScopeProvider, true
	}
	return providerFailureScopeUnknown, false
}
