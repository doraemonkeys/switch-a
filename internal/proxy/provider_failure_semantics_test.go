package proxy

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestClassifyProviderFailure_UsageLimitReachedUsesPrimaryReset(t *testing.T) {
	observedAt := time.Date(2026, time.March, 26, 10, 0, 0, 0, time.UTC)
	resetAt := observedAt.Add(15 * time.Minute).Truncate(time.Second)
	header := make(http.Header)
	header.Set(headerCodexPrimaryUsedPercent, "100")
	header.Set(headerCodexSecondaryUsedPercent, "67")
	header.Set(headerCodexPrimaryResetAt, strconv.FormatInt(resetAt.Unix(), 10))

	body := `{"type":"error","error":{"type":"usage_limit_reached","resets_at":` + strconv.FormatInt(resetAt.Unix(), 10) + `}}`
	disposition := classifyProviderFailure(http.StatusTooManyRequests, header, body, observedAt)

	if disposition.switchReason != SwitchReasonUsageLimitReached {
		t.Fatalf("switchReason = %q, want %q", disposition.switchReason, SwitchReasonUsageLimitReached)
	}
	if disposition.autoDisableUntil == nil || !disposition.autoDisableUntil.Equal(resetAt) {
		t.Fatalf("autoDisableUntil = %v, want %v", disposition.autoDisableUntil, resetAt)
	}
	if disposition.autoDisableReason != usageLimitAutoDisableReason {
		t.Fatalf("autoDisableReason = %q, want %q", disposition.autoDisableReason, usageLimitAutoDisableReason)
	}
}

func TestClassifyProviderFailure_UsageLimitReachedUsesLaterResetWhenBothWindowsAreExhausted(t *testing.T) {
	observedAt := time.Date(2026, time.March, 26, 10, 0, 0, 0, time.UTC)
	primaryReset := observedAt.Add(15 * time.Minute).Truncate(time.Second)
	secondaryReset := observedAt.Add(3 * time.Hour).Truncate(time.Second)
	header := make(http.Header)
	header.Set(headerCodexPrimaryUsedPercent, "100")
	header.Set(headerCodexSecondaryUsedPercent, "100")
	header.Set(headerCodexPrimaryResetAt, strconv.FormatInt(primaryReset.Unix(), 10))
	header.Set(headerCodexSecondaryResetAt, strconv.FormatInt(secondaryReset.Unix(), 10))

	body := `{"type":"error","error":{"type":"usage_limit_reached","resets_at":` + strconv.FormatInt(primaryReset.Unix(), 10) + `}}`
	disposition := classifyProviderFailure(http.StatusTooManyRequests, header, body, observedAt)

	if disposition.autoDisableUntil == nil || !disposition.autoDisableUntil.Equal(secondaryReset) {
		t.Fatalf("autoDisableUntil = %v, want %v", disposition.autoDisableUntil, secondaryReset)
	}
}

func TestClassifyProviderFailure_Generic429DoesNotSuspendProvider(t *testing.T) {
	observedAt := time.Date(2026, time.March, 26, 10, 0, 0, 0, time.UTC)
	header := make(http.Header)
	header.Set(headerCodexPrimaryResetAt, strconv.FormatInt(observedAt.Add(5*time.Minute).Unix(), 10))

	disposition := classifyProviderFailure(http.StatusTooManyRequests, header, `{"error":{"type":"rate_limit"}}`, observedAt)
	if disposition.switchReason != "" {
		t.Fatalf("switchReason = %q, want empty", disposition.switchReason)
	}
	if disposition.autoDisableUntil != nil {
		t.Fatalf("autoDisableUntil = %v, want nil", disposition.autoDisableUntil)
	}
}

func TestResolveUsageLimitDisableUntil_FallsBackToBodyReset(t *testing.T) {
	observedAt := time.Date(2026, time.March, 26, 10, 0, 0, 0, time.UTC)
	bodyReset := observedAt.Add(45 * time.Minute).Truncate(time.Second)
	body := `{"type":"error","error":{"type":"usage_limit_reached","resets_at":` + strconv.FormatInt(bodyReset.Unix(), 10) + `}}`

	disableUntil := resolveUsageLimitDisableUntil(nil, body, observedAt)
	if disableUntil == nil || !disableUntil.Equal(bodyReset) {
		t.Fatalf("disableUntil = %v, want %v", disableUntil, bodyReset)
	}
}
