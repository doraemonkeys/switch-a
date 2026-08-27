package model

import (
	"encoding/json"
	"strings"
	"time"
)

// CloneProviderUsageSnapshot returns a detached copy suitable for persistence or export.
func CloneProviderUsageSnapshot(snapshot *ProviderUsageSnapshot) *ProviderUsageSnapshot {
	if snapshot == nil {
		return nil
	}
	clone := *snapshot
	clone.FetchedAt = cloneTimePtr(snapshot.FetchedAt)
	clone.FiveHour = cloneProviderUsageWindow(snapshot.FiveHour)
	clone.OneWeek = cloneProviderUsageWindow(snapshot.OneWeek)
	return &clone
}

// DecodeChatGPTProviderSecret parses the single canonical secret wire format
// shared by credential-session lifecycle services and atomic import validation.
func DecodeChatGPTProviderSecret(raw string) (*ChatGPTProviderSecret, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var secret ChatGPTProviderSecret
	if err := json.Unmarshal([]byte(raw), &secret); err != nil {
		return nil, err
	}
	return &secret, nil
}

// EncodeChatGPTProviderSecret centralizes the persisted secret shape so summary
// fields cannot accidentally drift into refresh-capable storage.
func EncodeChatGPTProviderSecret(secret *ChatGPTProviderSecret) (string, error) {
	if secret == nil {
		return "", nil
	}
	payload, err := json.Marshal(secret)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func cloneProviderUsageWindow(window *ProviderUsageWindow) *ProviderUsageWindow {
	if window == nil {
		return nil
	}
	clone := *window
	clone.ResetAt = cloneTimePtr(window.ResetAt)
	return &clone
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
