package codexquota

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
)

const (
	HeaderPrimaryUsedPercent         = "X-Codex-Primary-Used-Percent"
	HeaderPrimaryWindowMinutes       = "X-Codex-Primary-Window-Minutes"
	HeaderPrimaryResetAt             = "X-Codex-Primary-Reset-At"
	HeaderPrimaryResetAfterSeconds   = "X-Codex-Primary-Reset-After-Seconds"
	HeaderSecondaryUsedPercent       = "X-Codex-Secondary-Used-Percent"
	HeaderSecondaryWindowMinutes     = "X-Codex-Secondary-Window-Minutes"
	HeaderSecondaryResetAt           = "X-Codex-Secondary-Reset-At"
	HeaderSecondaryResetAfterSeconds = "X-Codex-Secondary-Reset-After-Seconds"
	HeaderPlanType                   = "X-Codex-Plan-Type"

	headerBengalfoxPrimaryUsedPercent         = "X-Codex-Bengalfox-Primary-Used-Percent"
	headerBengalfoxPrimaryWindowMinutes       = "X-Codex-Bengalfox-Primary-Window-Minutes"
	headerBengalfoxPrimaryResetAt             = "X-Codex-Bengalfox-Primary-Reset-At"
	headerBengalfoxPrimaryResetAfterSeconds   = "X-Codex-Bengalfox-Primary-Reset-After-Seconds"
	headerBengalfoxSecondaryUsedPercent       = "X-Codex-Bengalfox-Secondary-Used-Percent"
	headerBengalfoxSecondaryWindowMinutes     = "X-Codex-Bengalfox-Secondary-Window-Minutes"
	headerBengalfoxSecondaryResetAt           = "X-Codex-Bengalfox-Secondary-Reset-At"
	headerBengalfoxSecondaryResetAfterSeconds = "X-Codex-Bengalfox-Secondary-Reset-After-Seconds"
	headerBengalfoxPlanType                   = "X-Codex-Bengalfox-Plan-Type"

	secondsPerMinute              = int64(time.Minute / time.Second)
	defaultPrimaryWindowSeconds   = int64((5 * time.Hour) / time.Second)
	defaultSecondaryWindowSeconds = int64((7 * 24 * time.Hour) / time.Second)
)

type windowHeaders struct {
	usedPercent       headerNames
	windowMinutes     headerNames
	resetAt           headerNames
	resetAfterSeconds headerNames
	defaultSeconds    int64
}

type headerNames struct {
	canonical string
	fallback  string
}

var (
	primaryWindowHeaders = windowHeaders{
		usedPercent:       headerNames{HeaderPrimaryUsedPercent, headerBengalfoxPrimaryUsedPercent},
		windowMinutes:     headerNames{HeaderPrimaryWindowMinutes, headerBengalfoxPrimaryWindowMinutes},
		resetAt:           headerNames{HeaderPrimaryResetAt, headerBengalfoxPrimaryResetAt},
		resetAfterSeconds: headerNames{HeaderPrimaryResetAfterSeconds, headerBengalfoxPrimaryResetAfterSeconds},
		defaultSeconds:    defaultPrimaryWindowSeconds,
	}
	secondaryWindowHeaders = windowHeaders{
		usedPercent:       headerNames{HeaderSecondaryUsedPercent, headerBengalfoxSecondaryUsedPercent},
		windowMinutes:     headerNames{HeaderSecondaryWindowMinutes, headerBengalfoxSecondaryWindowMinutes},
		resetAt:           headerNames{HeaderSecondaryResetAt, headerBengalfoxSecondaryResetAt},
		resetAfterSeconds: headerNames{HeaderSecondaryResetAfterSeconds, headerBengalfoxSecondaryResetAfterSeconds},
		defaultSeconds:    defaultSecondaryWindowSeconds,
	}
)

// ParseResponseHeaders translates the account-quota headers returned by Codex
// into the same snapshot shape used by the explicit usage endpoint. Individual
// malformed fields are rejected without discarding other trustworthy windows.
func ParseResponseHeaders(header http.Header, observedAt time.Time) (*model.ProviderUsageSnapshot, []string) {
	if header == nil || observedAt.IsZero() {
		return nil, nil
	}

	primary, primaryRejected := parseWindow(header, primaryWindowHeaders, observedAt)
	secondary, secondaryRejected := parseWindow(header, secondaryWindowHeaders, observedAt)
	rejected := make([]string, 0, len(primaryRejected)+len(secondaryRejected))
	rejected = append(rejected, primaryRejected...)
	rejected = append(rejected, secondaryRejected...)
	if primary == nil && secondary == nil {
		return nil, rejected
	}

	planType, _ := firstHeader(header, headerNames{HeaderPlanType, headerBengalfoxPlanType})
	fetchedAt := observedAt.UTC()
	return &model.ProviderUsageSnapshot{
		FetchedAt: &fetchedAt,
		PlanType:  strings.TrimSpace(planType),
		FiveHour:  primary,
		OneWeek:   secondary,
	}, rejected
}

func parseWindow(header http.Header, names windowHeaders, observedAt time.Time) (*model.ProviderUsageWindow, []string) {
	usedValue, usedHeader := firstHeader(header, names.usedPercent)
	if usedHeader == "" {
		return nil, nil
	}
	usedPercent, err := strconv.ParseFloat(strings.TrimSpace(usedValue), 64)
	if err != nil || math.IsNaN(usedPercent) || math.IsInf(usedPercent, 0) || usedPercent < 0 || usedPercent > 100 {
		return nil, []string{usedHeader}
	}

	rejected := make([]string, 0, 3)
	windowSeconds := names.defaultSeconds
	if windowValue, windowHeader := firstHeader(header, names.windowMinutes); windowHeader != "" {
		minutes, parseErr := strconv.ParseInt(strings.TrimSpace(windowValue), 10, 64)
		if parseErr != nil || minutes <= 0 || minutes > math.MaxInt64/secondsPerMinute {
			rejected = append(rejected, windowHeader)
		} else {
			windowSeconds = minutes * secondsPerMinute
		}
	}

	resetAt, resetRejected := parseResetAt(header, names, observedAt)
	rejected = append(rejected, resetRejected...)
	return &model.ProviderUsageWindow{
		UsedPercent:   usedPercent,
		WindowSeconds: windowSeconds,
		ResetAt:       resetAt,
	}, rejected
}

func parseResetAt(header http.Header, names windowHeaders, observedAt time.Time) (*time.Time, []string) {
	rejected := make([]string, 0, 2)
	if value, name := firstHeader(header, names.resetAt); name != "" {
		if unixSeconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil && unixSeconds > 0 {
			resetAt := time.Unix(unixSeconds, 0).UTC()
			return &resetAt, rejected
		}
		if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value)); err == nil {
			resetAt := parsed.UTC()
			return &resetAt, rejected
		}
		rejected = append(rejected, name)
	}

	if value, name := firstHeader(header, names.resetAfterSeconds); name != "" {
		seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err == nil && seconds >= 0 && seconds <= math.MaxInt64/int64(time.Second) {
			resetAt := observedAt.Add(time.Duration(seconds) * time.Second).UTC()
			return &resetAt, rejected
		}
		rejected = append(rejected, name)
	}
	return nil, rejected
}

func firstHeader(header http.Header, names headerNames) (string, string) {
	for _, name := range []string{names.canonical, names.fallback} {
		if value := strings.TrimSpace(header.Get(name)); value != "" {
			return value, name
		}
	}
	return "", ""
}
