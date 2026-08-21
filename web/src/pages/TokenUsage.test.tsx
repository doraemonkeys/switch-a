import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { describe, expect, it, vi } from "vitest";
import type { ApiClient, TokenUsageResponse } from "../api/client";
import { ApiContext } from "../api/context";
import type { AnalyticsWindowClock } from "../features/analytics-window/useAnalyticsWindow";
import { createMockApiClient } from "../hooks/test-utils";
import { TokenUsage } from "./TokenUsage";

const EMPTY_TOKEN_USAGE_RESPONSE: TokenUsageResponse = {
  summary: {
    total_tokens: "0",
    input_tokens: "0",
    output_tokens: "0",
    fresh_input_tokens: "0",
    cache_read_input_tokens: "0",
    cache_creation_input_tokens: "0",
    unclassified_input_tokens: "0",
    standard_output_tokens: "0",
    reasoning_tokens: "0",
    unclassified_output_tokens: "0",
    cache_hit_rate: 0,
    reasoning_ratio: 0,
  },
  timeseries: [],
  by_provider: [],
  by_model: [],
  time_range: {
    start: "2026-08-20T08:00:00Z",
    end: "2026-08-21T08:00:00Z",
    granularity: "1h",
  },
  coverage: {
    total_requests: 0,
    observed_requests: 0,
    comparable_requests: 0,
    without_usage_requests: 0,
    rate: 0,
  },
  data_quality: {
    quality_rate: 0,
    partial_requests: 0,
    invalid_requests: 0,
    unknown_semantics_requests: 0,
  },
};

function renderPage({
  apiClient,
  clock,
  initialEntry = "/token-usage",
}: {
  apiClient: ApiClient;
  clock: AnalyticsWindowClock;
  initialEntry?: string;
}) {
  return render(
    <ApiContext.Provider value={apiClient}>
      <MemoryRouter initialEntries={[initialEntry]}>
        <TokenUsage clock={clock} />
      </MemoryRouter>
    </ApiContext.Provider>,
  );
}

function createTokenUsageApi(): ApiClient {
  const apiClient = createMockApiClient();
  apiClient.tokenUsage.get = vi
    .fn()
    .mockResolvedValue(EMPTY_TOKEN_USAGE_RESPONSE);
  return apiClient;
}

describe("TokenUsage", () => {
  it("owns a default global analytics window without inheriting URL or Logs filters", async () => {
    const apiClient = createTokenUsageApi();
    const now = vi.fn(() => new Date("2026-08-21T08:00:00.000Z"));

    renderPage({
      apiClient,
      clock: { now },
      initialEntry:
        "/token-usage?provider_id=provider-a&model=hidden&api_type=codex&status=500&offset=40&sort_by=latency_ms",
    });

    expect(
      screen.getByRole("heading", { name: "Token Usage Analytics" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Global")).toBeInTheDocument();
    await waitFor(() => {
      expect(apiClient.tokenUsage.get).toHaveBeenCalledWith({
        period: "24h",
        granularity: "1h",
        as_of: "2026-08-21T08:00:00.000Z",
      });
    });

    const requestedParams = vi.mocked(apiClient.tokenUsage.get).mock
      .calls[0]?.[0];
    expect(Object.keys(requestedParams ?? {})).toEqual([
      "period",
      "granularity",
      "as_of",
    ]);
    expect(now).toHaveBeenCalledTimes(1);
  });

  it("turns selector and refresh intents into one semantic query each", async () => {
    const apiClient = createTokenUsageApi();
    const now = vi
      .fn<() => Date>()
      .mockReturnValueOnce(new Date("2026-08-21T08:00:00.000Z"))
      .mockReturnValueOnce(new Date("2026-08-21T09:30:00.000Z"));

    renderPage({ apiClient, clock: { now } });

    await waitFor(() => {
      expect(apiClient.tokenUsage.get).toHaveBeenCalledTimes(1);
    });

    fireEvent.change(screen.getByLabelText("Time Range"), {
      target: { value: "7d" },
    });
    await waitFor(() => {
      expect(apiClient.tokenUsage.get).toHaveBeenNthCalledWith(2, {
        period: "7d",
        granularity: "6h",
        as_of: "2026-08-21T08:00:00.000Z",
      });
    });

    fireEvent.change(screen.getByLabelText("Bucket Size"), {
      target: { value: "1d" },
    });
    await waitFor(() => {
      expect(apiClient.tokenUsage.get).toHaveBeenNthCalledWith(3, {
        period: "7d",
        granularity: "1d",
        as_of: "2026-08-21T08:00:00.000Z",
      });
    });
    await waitFor(() => {
      expect(
        screen.getByRole("button", { name: "Refresh token analytics" }),
      ).toBeEnabled();
    });

    fireEvent.click(
      screen.getByRole("button", { name: "Refresh token analytics" }),
    );
    await waitFor(() => {
      expect(apiClient.tokenUsage.get).toHaveBeenNthCalledWith(4, {
        period: "7d",
        granularity: "1d",
        as_of: "2026-08-21T09:30:00.000Z",
      });
    });

    expect(apiClient.tokenUsage.get).toHaveBeenCalledTimes(4);
    expect(now).toHaveBeenCalledTimes(2);
  });
});
