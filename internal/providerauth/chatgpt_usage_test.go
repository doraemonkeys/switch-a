package providerauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"switch-a/internal/model"
)

func TestShouldRefreshChatGPTUsageSnapshot(t *testing.T) {
	now := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)
	staleFetchedAt := now.Add(-chatGPTUsageSnapshotTTL - time.Second)
	freshFetchedAt := now.Add(-time.Minute)

	testCases := []struct {
		name     string
		snapshot *model.ProviderUsageSnapshot
		want     bool
	}{
		{
			name:     "missing snapshot",
			snapshot: nil,
			want:     true,
		},
		{
			name:     "missing fetched_at",
			snapshot: &model.ProviderUsageSnapshot{},
			want:     true,
		},
		{
			name: "stale snapshot",
			snapshot: &model.ProviderUsageSnapshot{
				FetchedAt: &staleFetchedAt,
			},
			want: true,
		},
		{
			name: "fresh snapshot",
			snapshot: &model.ProviderUsageSnapshot{
				FetchedAt: &freshFetchedAt,
			},
			want: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldRefreshChatGPTUsageSnapshot(tc.snapshot, now); got != tc.want {
				t.Fatalf("shouldRefreshChatGPTUsageSnapshot() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestApplyChatGPTUsageSnapshot_ClonesAndOverridesPlanType(t *testing.T) {
	fetchedAt := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	resetAt := fetchedAt.Add(5 * time.Hour)
	credential := &model.ChatGPTProviderCredential{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		IDToken:      "id-token",
		AccountID:    "acct_test",
		PlanType:     "plus",
	}
	snapshot := &model.ProviderUsageSnapshot{
		FetchedAt: &fetchedAt,
		PlanType:  "team",
		FiveHour: &model.ProviderUsageWindow{
			UsedPercent:   25,
			WindowSeconds: 5 * 60 * 60,
			ResetAt:       &resetAt,
		},
	}

	updated := applyChatGPTUsageSnapshot(credential, snapshot)
	if updated == credential {
		t.Fatal("applyChatGPTUsageSnapshot should clone the credential")
	}
	if updated.Usage == snapshot {
		t.Fatal("applyChatGPTUsageSnapshot should clone the usage snapshot")
	}
	if updated.PlanType != "team" {
		t.Fatalf("PlanType = %q, want %q", updated.PlanType, "team")
	}
	if updated.Usage == nil || updated.Usage.FetchedAt == nil {
		t.Fatal("Usage.FetchedAt = nil, want cloned timestamp")
	}
	if updated.Usage.FetchedAt.Location() != time.UTC {
		t.Fatalf("FetchedAt location = %s, want UTC", updated.Usage.FetchedAt.Location())
	}

	snapshot.PlanType = "enterprise"
	snapshot.FiveHour.UsedPercent = 99
	if updated.PlanType != "team" {
		t.Fatalf("PlanType changed to %q after source mutation", updated.PlanType)
	}
	if updated.Usage.FiveHour == nil || updated.Usage.FiveHour.UsedPercent != 25 {
		t.Fatalf("FiveHour = %#v, want preserved snapshot data", updated.Usage.FiveHour)
	}

	if got := applyChatGPTUsageSnapshot(nil, snapshot); got != nil {
		t.Fatalf("applyChatGPTUsageSnapshot(nil, snapshot) = %#v, want nil", got)
	}
}

func TestResolveChatGPTUsageCandidateURLs_ReturnsExpectedOrigins(t *testing.T) {
	got := resolveChatGPTUsageCandidateURLs()
	want := []string{
		"https://chatgpt.com/backend-api/wham/usage",
		"https://chatgpt.com/wham/usage",
		"https://chatgpt.com/api/codex/usage",
	}

	if len(got) != len(want) {
		t.Fatalf("len(resolveChatGPTUsageCandidateURLs()) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidate[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestReadChatGPTUsageSnapshotResponse_ErrorsAndSuccess(t *testing.T) {
	fetchedAt := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)

	t.Run("missing response", func(t *testing.T) {
		_, err := readChatGPTUsageSnapshotResponse(nil, fetchedAt)
		if err == nil || !strings.Contains(err.Error(), "missing response") {
			t.Fatalf("error = %v, want missing response", err)
		}
	})

	t.Run("unexpected status without body", func(t *testing.T) {
		_, err := readChatGPTUsageSnapshotResponse(&http.Response{
			StatusCode: http.StatusBadGateway,
			Body:       io.NopCloser(strings.NewReader("   ")),
		}, fetchedAt)
		if err == nil || !strings.Contains(err.Error(), "unexpected status 502") {
			t.Fatalf("error = %v, want unexpected status", err)
		}
	})

	t.Run("unexpected status with body", func(t *testing.T) {
		_, err := readChatGPTUsageSnapshotResponse(&http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader("quota denied")),
		}, fetchedAt)
		if err == nil || !strings.Contains(err.Error(), "unexpected status 403: quota denied") {
			t.Fatalf("error = %v, want body preview", err)
		}
	})

	t.Run("token invalidation remains a typed auth failure", func(t *testing.T) {
		_, err := readChatGPTUsageSnapshotResponse(&http.Response{
			StatusCode: http.StatusUnauthorized,
			Body: io.NopCloser(strings.NewReader(`{
				"error": {
					"message": "Your authentication token has been invalidated. Please try signing in again.",
					"code": "token_invalidated"
				}
			}`)),
		}, fetchedAt)

		var responseErr *chatGPTUsageResponseError
		if !errors.As(err, &responseErr) {
			t.Fatalf("error = %T, want chatGPTUsageResponseError", err)
		}
		if responseErr.Code != ProviderAuthReasonTokenInvalidated {
			t.Fatalf("Code = %q, want %q", responseErr.Code, ProviderAuthReasonTokenInvalidated)
		}
		if reason, terminal := classifyChatGPTUsageAuthFailure(err); !terminal || reason != ProviderAuthReasonTokenInvalidated {
			t.Fatalf("classification = (%q, %t), want (%q, true)", reason, terminal, ProviderAuthReasonTokenInvalidated)
		}
	})

	t.Run("invalid payload", func(t *testing.T) {
		_, err := readChatGPTUsageSnapshotResponse(&http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("{")),
		}, fetchedAt)
		if err == nil || !strings.Contains(err.Error(), "decode usage payload") {
			t.Fatalf("error = %v, want decode failure", err)
		}
	})

	t.Run("successful payload", func(t *testing.T) {
		snapshot, err := readChatGPTUsageSnapshotResponse(&http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{
				"plan_type":"team",
				"rate_limit":{
					"primary_window":{"used_percent":17,"limit_window_seconds":18000,"reset_at":1770000000},
					"secondary_window":{"used_percent":44,"limit_window_seconds":604800,"reset_at":1770500000}
				}
			}`)),
		}, fetchedAt)
		if err != nil {
			t.Fatalf("readChatGPTUsageSnapshotResponse returned error: %v", err)
		}
		if snapshot.PlanType != "team" {
			t.Fatalf("PlanType = %q, want %q", snapshot.PlanType, "team")
		}
		if snapshot.FiveHour == nil || snapshot.FiveHour.UsedPercent != 17 {
			t.Fatalf("FiveHour = %#v, want used_percent 17", snapshot.FiveHour)
		}
		if snapshot.OneWeek == nil || snapshot.OneWeek.UsedPercent != 44 {
			t.Fatalf("OneWeek = %#v, want used_percent 44", snapshot.OneWeek)
		}
	})
}

func TestMapChatGPTUsageSnapshot_PicksNearestWindowsAcrossAllSources(t *testing.T) {
	fetchedAt := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	snapshot := mapChatGPTUsageSnapshot(chatGPTUsageAPIResponse{
		PlanType: " team ",
		RateLimit: &chatGPTUsageRateLimitDetails{
			PrimaryWindow: &chatGPTUsageWindowRaw{
				UsedPercent:        10,
				LimitWindowSeconds: 17950,
				ResetAt:            1770000000,
			},
			SecondaryWindow: &chatGPTUsageWindowRaw{
				UsedPercent:        20,
				LimitWindowSeconds: 604000,
				ResetAt:            1770500000,
			},
		},
		AdditionalRateLimits: []chatGPTAdditionalRateLimitItem{
			{
				RateLimit: &chatGPTUsageRateLimitDetails{
					PrimaryWindow: &chatGPTUsageWindowRaw{
						UsedPercent:        30,
						LimitWindowSeconds: 18010,
						ResetAt:            1770100000,
					},
					SecondaryWindow: &chatGPTUsageWindowRaw{
						UsedPercent:        40,
						LimitWindowSeconds: 605000,
						ResetAt:            1770600000,
					},
				},
			},
		},
	}, fetchedAt)

	if snapshot.PlanType != "team" {
		t.Fatalf("PlanType = %q, want %q", snapshot.PlanType, "team")
	}
	if snapshot.FetchedAt == nil || snapshot.FetchedAt.Location() != time.UTC {
		t.Fatalf("FetchedAt = %#v, want UTC timestamp", snapshot.FetchedAt)
	}
	if snapshot.FiveHour == nil || snapshot.FiveHour.UsedPercent != 30 {
		t.Fatalf("FiveHour = %#v, want nearest 5h window", snapshot.FiveHour)
	}
	if snapshot.OneWeek == nil || snapshot.OneWeek.UsedPercent != 40 {
		t.Fatalf("OneWeek = %#v, want nearest 1w window", snapshot.OneWeek)
	}
}

func TestFetchChatGPTUsageSnapshot_RequiresCredentialFields(t *testing.T) {
	service := NewService(Config{})

	_, err := service.fetchChatGPTUsageSnapshot(context.Background(), &model.ChatGPTProviderCredential{})
	if err == nil || !strings.Contains(err.Error(), "requires access token and account id") {
		t.Fatalf("error = %v, want missing credential fields", err)
	}
}

func TestFetchChatGPTUsageSnapshot_RetriesCandidatesUntilSuccess(t *testing.T) {
	candidates := resolveChatGPTUsageCandidateURLs()
	callCount := 0
	service := NewService(Config{
		Clock: fixedClock{now: time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)},
		HTTPClient: stubHTTPDoer{
			do: func(req *http.Request) (*http.Response, error) {
				if got := req.Header.Get("Authorization"); got != "Bearer access-token" {
					t.Fatalf("Authorization = %q, want %q", got, "Bearer access-token")
				}
				if got := req.Header.Get("ChatGPT-Account-Id"); got != "acct_test" {
					t.Fatalf("ChatGPT-Account-Id = %q, want %q", got, "acct_test")
				}
				if got := req.Header.Get("User-Agent"); got != chatGPTOAuthUserAgent {
					t.Fatalf("User-Agent = %q, want %q", got, chatGPTOAuthUserAgent)
				}
				if req.URL.String() != candidates[callCount] {
					t.Fatalf("request URL = %q, want %q", req.URL.String(), candidates[callCount])
				}

				callCount++
				switch callCount {
				case 1:
					return nil, errors.New("dial tcp timeout")
				case 2:
					return &http.Response{
						StatusCode: http.StatusBadGateway,
						Body:       io.NopCloser(strings.NewReader("upstream unavailable")),
					}, nil
				default:
					return &http.Response{
						StatusCode: http.StatusOK,
						Body: io.NopCloser(strings.NewReader(`{
							"plan_type":"pro",
							"rate_limit":{
								"primary_window":{"used_percent":11,"limit_window_seconds":18000,"reset_at":1770000000},
								"secondary_window":{"used_percent":22,"limit_window_seconds":604800,"reset_at":1770500000}
							}
						}`)),
					}, nil
				}
			},
		},
	})

	snapshot, err := service.fetchChatGPTUsageSnapshot(context.Background(), &model.ChatGPTProviderCredential{
		AccessToken: "access-token",
		AccountID:   "acct_test",
	})
	if err != nil {
		t.Fatalf("fetchChatGPTUsageSnapshot returned error: %v", err)
	}
	if callCount != len(candidates) {
		t.Fatalf("callCount = %d, want %d", callCount, len(candidates))
	}
	if snapshot.PlanType != "pro" {
		t.Fatalf("PlanType = %q, want %q", snapshot.PlanType, "pro")
	}
}

func TestFetchChatGPTUsageSnapshot_StopsAfterTerminalAuthFailure(t *testing.T) {
	callCount := 0
	service := NewService(Config{
		HTTPClient: stubHTTPDoer{
			do: func(*http.Request) (*http.Response, error) {
				callCount++
				return &http.Response{
					StatusCode: http.StatusUnauthorized,
					Body: io.NopCloser(strings.NewReader(`{
						"error": {
							"message": "Your authentication token has been invalidated. Please try signing in again.",
							"code": "token_invalidated"
						}
					}`)),
				}, nil
			},
		},
	})

	_, err := service.fetchChatGPTUsageSnapshot(context.Background(), &model.ChatGPTProviderCredential{
		AccessToken: "access-token",
		AccountID:   "acct_test",
	})
	if err == nil {
		t.Fatal("fetchChatGPTUsageSnapshot returned nil error")
	}
	if callCount != 1 {
		t.Fatalf("callCount = %d, want 1", callCount)
	}
	if reason, terminal := classifyChatGPTUsageAuthFailure(err); !terminal || reason != ProviderAuthReasonTokenInvalidated {
		t.Fatalf("classification = (%q, %t), want (%q, true)", reason, terminal, ProviderAuthReasonTokenInvalidated)
	}
}

func TestFetchChatGPTUsageSnapshot_ReturnsJoinedErrorsWhenAllCandidatesFail(t *testing.T) {
	service := NewService(Config{
		HTTPClient: stubHTTPDoer{
			do: func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusForbidden,
					Body:       io.NopCloser(strings.NewReader("forbidden")),
				}, nil
			},
		},
	})

	_, err := service.fetchChatGPTUsageSnapshot(context.Background(), &model.ChatGPTProviderCredential{
		AccessToken: "access-token",
		AccountID:   "acct_test",
	})
	if err == nil {
		t.Fatal("fetchChatGPTUsageSnapshot returned nil error")
	}
	for _, want := range []string{
		"fetch chatgpt usage snapshot",
		"https://chatgpt.com/backend-api/wham/usage",
		"https://chatgpt.com/wham/usage",
		"https://chatgpt.com/api/codex/usage",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}
