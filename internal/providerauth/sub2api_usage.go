package providerauth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
)

const (
	secondsPerMinute            = 60
	minimumImportedUsagePercent = 0
	maximumImportedUsagePercent = 100
	// sub2api exports five-hour and seven-day windows. A one-month ceiling leaves
	// migration headroom while making reset-time arithmetic and UI state bounded.
	maximumImportedUsageWindowMinutes = 31 * 24 * 60
	maximumImportedUsageFutureSkew    = 5 * time.Minute
)

type sub2APIUsageFields struct {
	FiveHourUsedPercent   json.RawMessage `json:"codex_5h_used_percent"`
	FiveHourWindowMinutes json.RawMessage `json:"codex_5h_window_minutes"`
	FiveHourResetAt       json.RawMessage `json:"codex_5h_reset_at"`
	OneWeekUsedPercent    json.RawMessage `json:"codex_7d_used_percent"`
	OneWeekWindowMinutes  json.RawMessage `json:"codex_7d_window_minutes"`
	OneWeekResetAt        json.RawMessage `json:"codex_7d_reset_at"`
	UsageUpdatedAt        json.RawMessage `json:"codex_usage_updated_at"`
}

func parseSub2APIUsageSnapshot(
	raw json.RawMessage,
	planType string,
	now time.Time,
) (*model.ProviderUsageSnapshot, []ChatGPTProviderImportWarning) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var fields sub2APIUsageFields
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, []ChatGPTProviderImportWarning{{
			Code:    ChatGPTProviderImportWarningUsageMetadataInvalid,
			Message: "Usage metadata is invalid and was ignored.",
		}}
	}

	warnings := make([]ChatGPTProviderImportWarning, 0)
	fiveHour, fiveHourWarnings := parseSub2APIUsageWindow(
		"codex_5h",
		fields.FiveHourUsedPercent,
		fields.FiveHourWindowMinutes,
		fields.FiveHourResetAt,
		now,
	)
	oneWeek, oneWeekWarnings := parseSub2APIUsageWindow(
		"codex_7d",
		fields.OneWeekUsedPercent,
		fields.OneWeekWindowMinutes,
		fields.OneWeekResetAt,
		now,
	)
	warnings = append(warnings, fiveHourWarnings...)
	warnings = append(warnings, oneWeekWarnings...)

	fetchedAt, fetchedAtPresent, err := optionalJSONTime(fields.UsageUpdatedAt)
	if err != nil || (fetchedAtPresent && fetchedAt.After(now.Add(maximumImportedUsageFutureSkew))) {
		warnings = append(warnings, ChatGPTProviderImportWarning{
			Code:    ChatGPTProviderImportWarningUsageMetadataInvalid,
			Message: "codex_usage_updated_at is invalid and was ignored.",
		})
		fetchedAtPresent = false
	}
	// Freshness is meaningful only when it qualifies real usage data; an isolated
	// timestamp must not suppress the live usage fetch that can populate windows.
	if fiveHour == nil && oneWeek == nil {
		return nil, warnings
	}

	snapshot := &model.ProviderUsageSnapshot{
		PlanType: strings.TrimSpace(planType),
		FiveHour: fiveHour,
		OneWeek:  oneWeek,
	}
	if fetchedAtPresent {
		value := fetchedAt.UTC()
		snapshot.FetchedAt = &value
	}
	return snapshot, warnings
}

func parseSub2APIUsageWindow(
	prefix string,
	usedRaw json.RawMessage,
	windowRaw json.RawMessage,
	resetRaw json.RawMessage,
	now time.Time,
) (*model.ProviderUsageWindow, []ChatGPTProviderImportWarning) {
	if len(usedRaw) == 0 && len(windowRaw) == 0 && len(resetRaw) == 0 {
		return nil, nil
	}

	warnings := make([]ChatGPTProviderImportWarning, 0)
	usedPresent := hasOptionalJSONValue(usedRaw)
	usedPercent, err := optionalJSONFloat(usedRaw)
	usedValid := usedPresent && err == nil && !math.IsNaN(usedPercent) && !math.IsInf(usedPercent, 0) &&
		usedPercent >= minimumImportedUsagePercent && usedPercent <= maximumImportedUsagePercent
	if !usedValid {
		warnings = append(warnings, invalidUsageFieldWarning(prefix+"_used_percent"))
	}
	windowPresent := hasOptionalJSONValue(windowRaw)
	windowMinutes, err := optionalJSONInt64(windowRaw)
	windowValid := windowPresent && err == nil && windowMinutes > 0 &&
		windowMinutes <= maximumImportedUsageWindowMinutes
	if !windowValid {
		warnings = append(warnings, invalidUsageFieldWarning(prefix+"_window_minutes"))
	}

	resetAt, resetPresent, err := optionalJSONTime(resetRaw)
	if err != nil {
		warnings = append(warnings, invalidUsageFieldWarning(prefix+"_reset_at"))
		resetPresent = false
	}
	if !usedValid || !windowValid {
		return nil, warnings
	}
	windowSeconds := windowMinutes * secondsPerMinute
	if resetPresent && resetAt.After(now.Add(time.Duration(windowSeconds)*time.Second+maximumImportedUsageFutureSkew)) {
		warnings = append(warnings, invalidUsageFieldWarning(prefix+"_reset_at"))
		resetPresent = false
	}

	window := &model.ProviderUsageWindow{
		UsedPercent:   usedPercent,
		WindowSeconds: windowSeconds,
	}
	if resetPresent {
		value := resetAt.UTC()
		window.ResetAt = &value
	}
	return window, warnings
}

func hasOptionalJSONValue(raw json.RawMessage) bool {
	return len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func invalidUsageFieldWarning(field string) ChatGPTProviderImportWarning {
	return ChatGPTProviderImportWarning{
		Code:    ChatGPTProviderImportWarningUsageMetadataInvalid,
		Message: field + " is invalid and was ignored.",
	}
}

func optionalJSONFloat(raw json.RawMessage) (float64, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var number json.Number
	if err := decoder.Decode(&number); err != nil {
		return 0, err
	}
	value, err := number.Float64()
	if err != nil {
		return 0, err
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("number must be finite")
	}
	return value, nil
}

func optionalJSONInt64(raw json.RawMessage) (int64, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var number json.Number
	if err := decoder.Decode(&number); err != nil {
		return 0, err
	}
	value, err := number.Int64()
	if err != nil {
		return 0, err
	}
	if value < minimumJSONSafeInteger || value > maximumJSONSafeInteger {
		return 0, fmt.Errorf("integer exceeds JSON safe range")
	}
	return value, nil
}

func optionalJSONTime(raw json.RawMessage) (time.Time, bool, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return time.Time{}, false, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		parsed, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(text))
		if parseErr != nil {
			return time.Time{}, true, parseErr
		}
		value, rangeErr := jsonRepresentableTime(parsed)
		return value, true, rangeErr
	}
	seconds, err := optionalJSONInt64(raw)
	if err != nil {
		return time.Time{}, true, err
	}
	value, rangeErr := jsonRepresentableUnixTime(seconds)
	return value, true, rangeErr
}
