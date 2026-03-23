package providerauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"switch-a/internal/model"
)

const (
	chatGPTUsageSnapshotTTL         = 2 * time.Minute
	chatGPTUsageBackendPath         = "/backend-api/wham/usage"
	chatGPTUsageLegacyPath          = "/wham/usage"
	chatGPTUsageCodexPath           = "/api/codex/usage"
	chatGPTUsageErrorPreviewLimit   = 240
	chatGPTUsageWindowFiveHoursSecs = 5 * 60 * 60
	chatGPTUsageWindowOneWeekSecs   = 7 * 24 * 60 * 60
)

type chatGPTUsageAPIResponse struct {
	PlanType             string                           `json:"plan_type"`
	RateLimit            *chatGPTUsageRateLimitDetails    `json:"rate_limit"`
	AdditionalRateLimits []chatGPTAdditionalRateLimitItem `json:"additional_rate_limits"`
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
		seen := false
		for _, existing := range deduped {
			if existing == candidate {
				seen = true
				break
			}
		}
		if !seen {
			deduped = append(deduped, candidate)
		}
	}
	return deduped
}

func (s *Service) fetchChatGPTUsageSnapshot(ctx context.Context, credential *model.ChatGPTProviderCredential) (*model.ProviderUsageSnapshot, error) {
	if credential == nil || credential.AccessToken == "" || credential.AccountID == "" {
		return nil, fmt.Errorf("chatgpt usage requires access token and account id")
	}

	var errors []string
	for _, usageURL := range resolveChatGPTUsageCandidateURLs() {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, usageURL, nil)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s -> build request: %v", usageURL, err))
			continue
		}

		request.Header.Set("Authorization", "Bearer "+credential.AccessToken)
		request.Header.Set("ChatGPT-Account-Id", credential.AccountID)
		request.Header.Set("Accept", "application/json")
		request.Header.Set("User-Agent", chatGPTOAuthUserAgent)

		response, err := s.httpClient.Do(request)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s -> %v", usageURL, err))
			continue
		}

		snapshot, readErr := readChatGPTUsageSnapshotResponse(response, s.clock.Now())
		if response.Body != nil {
			_ = response.Body.Close()
		}
		if readErr != nil {
			errors = append(errors, fmt.Sprintf("%s -> %v", usageURL, readErr))
			continue
		}
		return snapshot, nil
	}

	if len(errors) == 0 {
		return nil, fmt.Errorf("no chatgpt usage endpoint candidates")
	}
	return nil, fmt.Errorf("fetch chatgpt usage snapshot: %s", strings.Join(errors, " | "))
}

func readChatGPTUsageSnapshotResponse(response *http.Response, fetchedAt time.Time) (*model.ProviderUsageSnapshot, error) {
	if response == nil {
		return nil, fmt.Errorf("missing response")
	}
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, chatGPTUsageErrorPreviewLimit))
		snippet := strings.TrimSpace(string(body))
		if snippet == "" {
			return nil, fmt.Errorf("unexpected status %d", response.StatusCode)
		}
		return nil, fmt.Errorf("unexpected status %d: %s", response.StatusCode, snippet)
	}

	var payload chatGPTUsageAPIResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode usage payload: %w", err)
	}
	return mapChatGPTUsageSnapshot(payload, fetchedAt), nil
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
