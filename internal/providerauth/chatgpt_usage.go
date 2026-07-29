package providerauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
)

const (
	chatGPTUsageSnapshotTTL = 2 * time.Minute
	chatGPTUsageBackendPath = "/backend-api/wham/usage"
	chatGPTUsageLegacyPath  = "/wham/usage"
	chatGPTUsageCodexPath   = "/api/codex/usage"
	// Structured auth codes can occur after the short human-readable preview,
	// so parsing gets a larger bounded budget while logs stay compact.
	chatGPTUsageErrorBodyLimit      = 32 << 10
	chatGPTUsageErrorPreviewLimit   = 240
	chatGPTUsageWindowFiveHoursSecs = 5 * 60 * 60
	chatGPTUsageWindowOneWeekSecs   = 7 * 24 * 60 * 60
)

type chatGPTUsageAPIResponse struct {
	PlanType             string                           `json:"plan_type"`
	RateLimit            *chatGPTUsageRateLimitDetails    `json:"rate_limit"`
	AdditionalRateLimits []chatGPTAdditionalRateLimitItem `json:"additional_rate_limits"`
}

type chatGPTUsageAPIErrorResponse struct {
	Error struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error"`
}

type chatGPTUsageResponseError struct {
	StatusCode int
	Code       string
	Message    string
	Preview    string
}

func (e *chatGPTUsageResponseError) Error() string {
	switch {
	case e.Code != "" && e.Message != "":
		return fmt.Sprintf("unexpected status %d (%s): %s", e.StatusCode, e.Code, e.Message)
	case e.Code != "":
		return fmt.Sprintf("unexpected status %d (%s)", e.StatusCode, e.Code)
	case e.Preview != "":
		return fmt.Sprintf("unexpected status %d: %s", e.StatusCode, e.Preview)
	default:
		return fmt.Sprintf("unexpected status %d", e.StatusCode)
	}
}

func classifyChatGPTUsageAuthFailure(err error) (string, bool) {
	var responseErr *chatGPTUsageResponseError
	if !errors.As(err, &responseErr) || responseErr.StatusCode != http.StatusUnauthorized {
		return "", false
	}

	// Only explicit terminal OAuth codes invalidate the provider. A generic 401
	// can still be recoverable through the separately controlled token refresh flow.
	switch responseErr.Code {
	case ProviderAuthReasonTokenInvalidated,
		ProviderAuthReasonInvalidGrant,
		ProviderAuthReasonRefreshTokenReused,
		ProviderAuthReasonInteractionRequired,
		ProviderAuthReasonLoginRequired:
		return responseErr.Code, true
	default:
		return "", false
	}
}

type chatGPTUsageRateLimitDetails struct {
	PrimaryWindow   *chatGPTUsageWindowRaw `json:"primary_window"`
	SecondaryWindow *chatGPTUsageWindowRaw `json:"secondary_window"`
}

type chatGPTAdditionalRateLimitItem struct {
	RateLimit *chatGPTUsageRateLimitDetails `json:"rate_limit"`
}

type chatGPTUsageWindowRaw struct {
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int64   `json:"limit_window_seconds"`
	ResetAt            int64   `json:"reset_at"`
}

func shouldRefreshChatGPTUsageSnapshot(snapshot *model.ProviderUsageSnapshot, now time.Time) bool {
	if snapshot == nil || snapshot.FetchedAt == nil {
		return true
	}
	return snapshot.FetchedAt.Add(chatGPTUsageSnapshotTTL).Before(now)
}

func applyChatGPTUsageSnapshot(credential *model.ChatGPTProviderCredential, snapshot *model.ProviderUsageSnapshot) *model.ChatGPTProviderCredential {
	if credential == nil {
		return nil
	}
	cloned := *credential
	cloned.Usage = cloneProviderUsageSnapshot(snapshot)
	if snapshot != nil && strings.TrimSpace(snapshot.PlanType) != "" {
		cloned.PlanType = strings.TrimSpace(snapshot.PlanType)
	}
	return &cloned
}

func cloneProviderUsageSnapshot(snapshot *model.ProviderUsageSnapshot) *model.ProviderUsageSnapshot {
	if snapshot == nil {
		return nil
	}

	cloned := *snapshot
	if snapshot.FetchedAt != nil {
		value := snapshot.FetchedAt.UTC()
		cloned.FetchedAt = &value
	}
	cloned.FiveHour = cloneProviderUsageWindow(snapshot.FiveHour)
	cloned.OneWeek = cloneProviderUsageWindow(snapshot.OneWeek)
	return &cloned
}

func cloneProviderUsageWindow(window *model.ProviderUsageWindow) *model.ProviderUsageWindow {
	if window == nil {
		return nil
	}

	cloned := *window
	if window.ResetAt != nil {
		value := window.ResetAt.UTC()
		cloned.ResetAt = &value
	}
	return &cloned
}

func resolveChatGPTUsageCandidateURLs() []string {
	origin := "https://chatgpt.com"
	if parsed, err := url.Parse(chatGPTCodexBaseURL); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		origin = parsed.Scheme + "://" + parsed.Host
	}

	candidates := []string{
		origin + chatGPTUsageBackendPath,
		origin + chatGPTUsageLegacyPath,
		origin + chatGPTUsageCodexPath,
	}

	deduped := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if !slices.Contains(deduped, candidate) {
			deduped = append(deduped, candidate)
		}
	}
	return deduped
}

func (s *Service) fetchChatGPTUsageSnapshot(ctx context.Context, credential *model.ChatGPTProviderCredential) (*model.ProviderUsageSnapshot, error) {
	if credential == nil || credential.AccessToken == "" || credential.AccountID == "" {
		return nil, fmt.Errorf("chatgpt usage requires access token and account id")
	}

	var attemptErrors []string
	for _, usageURL := range resolveChatGPTUsageCandidateURLs() {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, usageURL, nil)
		if err != nil {
			attemptErrors = append(attemptErrors, fmt.Sprintf("%s -> build request: %v", usageURL, err))
			continue
		}

		request.Header.Set("Authorization", "Bearer "+credential.AccessToken)
		request.Header.Set("ChatGPT-Account-Id", credential.AccountID)
		request.Header.Set("Accept", "application/json")
		request.Header.Set("User-Agent", chatGPTOAuthUserAgent)

		response, err := s.httpClient.Do(request)
		if err != nil {
			attemptErrors = append(attemptErrors, fmt.Sprintf("%s -> %v", usageURL, err))
			continue
		}

		snapshot, readErr := readChatGPTUsageSnapshotResponse(response, s.clock.Now())
		if response.Body != nil {
			_ = response.Body.Close()
		}
		if readErr != nil {
			if _, terminal := classifyChatGPTUsageAuthFailure(readErr); terminal {
				return nil, fmt.Errorf("fetch chatgpt usage snapshot from %s: %w", usageURL, readErr)
			}
			attemptErrors = append(attemptErrors, fmt.Sprintf("%s -> %v", usageURL, readErr))
			continue
		}
		return snapshot, nil
	}

	if len(attemptErrors) == 0 {
		return nil, fmt.Errorf("no chatgpt usage endpoint candidates")
	}
	return nil, fmt.Errorf("fetch chatgpt usage snapshot: %s", strings.Join(attemptErrors, " | "))
}

func readChatGPTUsageSnapshotResponse(response *http.Response, fetchedAt time.Time) (*model.ProviderUsageSnapshot, error) {
	if response == nil {
		return nil, fmt.Errorf("missing response")
	}
	if response.StatusCode != http.StatusOK {
		var body []byte
		if response.Body != nil {
			body, _ = io.ReadAll(io.LimitReader(response.Body, chatGPTUsageErrorBodyLimit))
		}
		var payload chatGPTUsageAPIErrorResponse
		_ = json.Unmarshal(body, &payload)
		return nil, &chatGPTUsageResponseError{
			StatusCode: response.StatusCode,
			Code:       strings.TrimSpace(payload.Error.Code),
			Message:    strings.TrimSpace(payload.Error.Message),
			Preview:    chatGPTUsageErrorPreview(string(body)),
		}
	}

	var payload chatGPTUsageAPIResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode usage payload: %w", err)
	}
	return mapChatGPTUsageSnapshot(payload, fetchedAt), nil
}

func chatGPTUsageErrorPreview(body string) string {
	preview := []rune(strings.TrimSpace(body))
	if len(preview) > chatGPTUsageErrorPreviewLimit {
		preview = preview[:chatGPTUsageErrorPreviewLimit]
	}
	return string(preview)
}

func mapChatGPTUsageSnapshot(payload chatGPTUsageAPIResponse, fetchedAt time.Time) *model.ProviderUsageSnapshot {
	windows := make([]chatGPTUsageWindowRaw, 0, 6)
	if payload.RateLimit != nil {
		if payload.RateLimit.PrimaryWindow != nil {
			windows = append(windows, *payload.RateLimit.PrimaryWindow)
		}
		if payload.RateLimit.SecondaryWindow != nil {
			windows = append(windows, *payload.RateLimit.SecondaryWindow)
		}
	}
	for _, additional := range payload.AdditionalRateLimits {
		if additional.RateLimit == nil {
			continue
		}
		if additional.RateLimit.PrimaryWindow != nil {
			windows = append(windows, *additional.RateLimit.PrimaryWindow)
		}
		if additional.RateLimit.SecondaryWindow != nil {
			windows = append(windows, *additional.RateLimit.SecondaryWindow)
		}
	}

	fetchedAt = fetchedAt.UTC()
	return &model.ProviderUsageSnapshot{
		FetchedAt: &fetchedAt,
		PlanType:  strings.TrimSpace(payload.PlanType),
		FiveHour:  toProviderUsageWindow(pickNearestChatGPTUsageWindow(windows, chatGPTUsageWindowFiveHoursSecs)),
		OneWeek:   toProviderUsageWindow(pickNearestChatGPTUsageWindow(windows, chatGPTUsageWindowOneWeekSecs)),
	}
}

func pickNearestChatGPTUsageWindow(windows []chatGPTUsageWindowRaw, targetSeconds int64) *chatGPTUsageWindowRaw {
	if len(windows) == 0 {
		return nil
	}

	best := windows[0]
	bestDistance := absInt64(best.LimitWindowSeconds - targetSeconds)
	for _, candidate := range windows[1:] {
		distance := absInt64(candidate.LimitWindowSeconds - targetSeconds)
		if distance < bestDistance {
			best = candidate
			bestDistance = distance
		}
	}

	result := best
	return &result
}

func toProviderUsageWindow(window *chatGPTUsageWindowRaw) *model.ProviderUsageWindow {
	if window == nil {
		return nil
	}

	resetAt := time.Unix(window.ResetAt, 0).UTC()
	return &model.ProviderUsageWindow{
		UsedPercent:   window.UsedPercent,
		WindowSeconds: window.LimitWindowSeconds,
		ResetAt:       &resetAt,
	}
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
