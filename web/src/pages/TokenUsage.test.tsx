import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
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
  const registeredKey = {
    fingerprint: "a".repeat(64),
    name: "Laptop",
    masked_key: "prefix…same",
  };
  const observedKey = {
    fingerprint: "b".repeat(64),
    name: "",
    masked_key: "prefix…same",
  };

  it("filters every report request by the original key and preserves the selection across window changes", async () => {
    const apiClient = createTokenUsageApi();
    vi.mocked(apiClient.tokenUsage.clientAPIKeys).mockResolvedValue([
      registeredKey,
      observedKey,
    ]);
    renderPage({
      apiClient,
      clock: { now: () => new Date("2026-08-21T08:00:00.000Z") },
    });
    await screen.findByRole("option", { name: /Laptop/ });
    expect(
      screen.getByRole("option", { name: "prefix…same · bbbbbbbbbbbb" }),
    ).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Client API Key"), {
      target: { value: observedKey.fingerprint },
    });
    await waitFor(() =>
      expect(apiClient.tokenUsage.get).toHaveBeenLastCalledWith({
        period: "24h",
        granularity: "1h",
        as_of: "2026-08-21T08:00:00.000Z",
        client_api_key_fingerprint: observedKey.fingerprint,
      }),
    );
    expect(screen.queryByText("Global")).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Time Range"), {
      target: { value: "7d" },
    });
    await waitFor(() =>
      expect(apiClient.tokenUsage.get).toHaveBeenLastCalledWith(
        expect.objectContaining({
          period: "7d",
          client_api_key_fingerprint: observedKey.fingerprint,
        }),
      ),
    );
    fireEvent.change(screen.getByLabelText("Client API Key"), {
      target: { value: "" },
    });
    await waitFor(() =>
      expect(apiClient.tokenUsage.get).toHaveBeenLastCalledWith(
        expect.objectContaining({ client_api_key_fingerprint: "" }),
      ),
    );
    fireEvent.change(screen.getByLabelText("Client API Key"), {
      target: { value: "all" },
    });
    await waitFor(() =>
      expect(apiClient.tokenUsage.get).toHaveBeenLastCalledWith({
        period: "7d",
        granularity: "6h",
        as_of: "2026-08-21T08:00:00.000Z",
      }),
    );
  });

  it("does not relabel old reports or publish an earlier key response after a failed switch", async () => {
    const apiClient = createTokenUsageApi();
    vi.mocked(apiClient.tokenUsage.clientAPIKeys).mockResolvedValue([
      registeredKey,
      observedKey,
    ]);
    renderPage({
      apiClient,
      clock: { now: () => new Date("2026-08-21T08:00:00.000Z") },
    });
    await screen.findByText("No Token Telemetry Recorded");
    let resolveEarlier!: (value: TokenUsageResponse) => void;
    vi.mocked(apiClient.tokenUsage.get)
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveEarlier = resolve;
          }),
      )
      .mockRejectedValueOnce(new Error("selected key unavailable"));
    fireEvent.change(screen.getByLabelText("Client API Key"), {
      target: { value: registeredKey.fingerprint },
    });
    await waitFor(() =>
      expect(apiClient.tokenUsage.get).toHaveBeenCalledTimes(2),
    );
    expect(
      screen.queryByText("No Token Telemetry Recorded"),
    ).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Client API Key"), {
      target: { value: observedKey.fingerprint },
    });
    await screen.findByText("selected key unavailable");
    await act(async () => {
      resolveEarlier(EMPTY_TOKEN_USAGE_RESPONSE);
    });
    expect(
      screen.queryByText("No Token Telemetry Recorded"),
    ).not.toBeInTheDocument();
    expect(screen.getByText("selected key unavailable")).toBeInTheDocument();
  });

  it("keeps global analytics usable when the key directory fails and retries the directory", async () => {
    const apiClient = createTokenUsageApi();
    vi.mocked(apiClient.tokenUsage.clientAPIKeys)
      .mockRejectedValueOnce(new Error("directory unavailable"))
      .mockResolvedValue([registeredKey]);
    renderPage({
      apiClient,
      clock: { now: () => new Date("2026-08-21T08:00:00.000Z") },
    });
    await screen.findByText(/Failed to load API keys/);
    expect(screen.getByText("No Token Telemetry Recorded")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Retry API keys" }));
    await screen.findByRole("option", { name: /Laptop/ });
    expect(
      screen.queryByText(/Failed to load API keys/),
    ).not.toBeInTheDocument();
    expect(apiClient.tokenUsage.get).toHaveBeenCalledTimes(1);
  });

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
