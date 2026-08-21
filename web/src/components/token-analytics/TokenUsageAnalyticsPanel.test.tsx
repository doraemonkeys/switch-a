import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { TokenUsageResponse } from "../../api/types";
import type {
  AnalyticsWindow,
  AnalyticsWindowIntent,
} from "../../features/analytics-window/analytics-window";
import { TokenUsageAnalyticsPanel } from "./TokenUsageAnalyticsPanel";

const mockWindow: AnalyticsWindow = {
  period: "24h",
  granularity: "1h",
  as_of: "2026-08-21T16:00:00.000Z",
};

const mockTokenUsageResponse: TokenUsageResponse = {
  summary: {
    total_tokens: "12451200",
    input_tokens: "8204110",
    output_tokens: "4247090",
    fresh_input_tokens: "4663710",
    cache_read_input_tokens: "3120400",
    cache_creation_input_tokens: "420000",
    unclassified_input_tokens: "0",
    standard_output_tokens: "3426690",
    reasoning_tokens: "820400",
    unclassified_output_tokens: "0",
    cache_hit_rate: 0.3803,
    reasoning_ratio: 0.1932,
  },
  timeseries: [
    {
      start: "2026-08-21T00:00:00Z",
      end: "2026-08-21T01:00:00Z",
      total_tokens: "1200000",
      input_tokens: "800000",
      output_tokens: "400000",
      fresh_input_tokens: "400000",
      cache_read_input_tokens: "350000",
      cache_creation_input_tokens: "50000",
      unclassified_input_tokens: "0",
      standard_output_tokens: "320000",
      reasoning_tokens: "80000",
      unclassified_output_tokens: "0",
      total_requests: 120,
      observed_requests: 118,
      comparable_requests: 118,
    },
  ],
  by_provider: [
    {
      provider_id: "p1",
      provider_name: "Anthropic Direct",
      total_tokens: "7420000",
      input_tokens: "5000000",
      output_tokens: "2420000",
      fresh_input_tokens: "2000000",
      cache_read_input_tokens: "2800000",
      cache_creation_input_tokens: "200000",
      unclassified_input_tokens: "0",
      standard_output_tokens: "2420000",
      reasoning_tokens: "0",
      unclassified_output_tokens: "0",
      request_count: 840,
      share: 0.596,
    },
  ],
  by_model: [
    {
      model: "claude-3-7-sonnet",
      total_tokens: "6120000",
      input_tokens: "4000000",
      output_tokens: "2120000",
      fresh_input_tokens: "1800000",
      cache_read_input_tokens: "2000000",
      cache_creation_input_tokens: "200000",
      unclassified_input_tokens: "0",
      standard_output_tokens: "2120000",
      reasoning_tokens: "0",
      unclassified_output_tokens: "0",
      request_count: 620,
      share: 0.492,
    },
  ],
  time_range: {
    start: "2026-08-20T16:00:00Z",
    end: "2026-08-21T16:00:00Z",
    granularity: "1h",
  },
  coverage: {
    total_requests: 1456,
    observed_requests: 1430,
    comparable_requests: 1430,
    without_usage_requests: 26,
    rate: 0.9821,
  },
  data_quality: {
    quality_rate: 1,
    partial_requests: 0,
    invalid_requests: 0,
    unknown_semantics_requests: 0,
  },
};

interface RenderPanelOptions {
  data?: TokenUsageResponse | null;
  loading?: boolean;
  error?: Error | null;
  window?: AnalyticsWindow;
  onWindowIntent?: (intent: AnalyticsWindowIntent) => void;
  hasActiveFilters?: boolean;
}

function renderPanel({
  data = mockTokenUsageResponse,
  loading = false,
  error = null,
  window = mockWindow,
  onWindowIntent = vi.fn(),
  hasActiveFilters = false,
}: RenderPanelOptions = {}) {
  const view = render(
    <TokenUsageAnalyticsPanel
      data={data}
      loading={loading}
      error={error}
      window={window}
      onWindowIntent={onWindowIntent}
      hasActiveFilters={hasActiveFilters}
    />,
  );
  return { ...view, onWindowIntent };
}

describe("TokenUsageAnalyticsPanel", () => {
  it("renders data view with hero cards, chart, and top breakdowns", () => {
    renderPanel();

    expect(screen.getByText("Token Usage Analytics")).toBeInTheDocument();
    expect(screen.getByText(/Coverage: 98.2%/)).toBeInTheDocument();
    expect(
      screen.getByText(/Observed-data quality: 100.0%/),
    ).toBeInTheDocument();
    expect(screen.getByText("Total Tokens")).toBeInTheDocument();
    expect(screen.getByText("12.45M")).toBeInTheDocument();
    expect(
      screen.getByText("Token Consumption Trend Over Time"),
    ).toBeInTheDocument();
    expect(screen.getByText("Top Providers")).toBeInTheDocument();
    expect(screen.getByText("Top Models")).toBeInTheDocument();
  });

  it("emits semantic period and granularity intents", () => {
    const onWindowIntent = vi.fn();
    renderPanel({ onWindowIntent });

    fireEvent.change(screen.getByLabelText("Time Range"), {
      target: { value: "7d" },
    });
    fireEvent.change(screen.getByLabelText("Bucket Size"), {
      target: { value: "15m" },
    });

    expect(onWindowIntent).toHaveBeenNthCalledWith(1, {
      type: "period-selected",
      period: "7d",
    });
    expect(onWindowIntent).toHaveBeenNthCalledWith(2, {
      type: "granularity-selected",
      granularity: "15m",
    });
  });

  it("emits the shared refresh intent", () => {
    const onWindowIntent = vi.fn();
    renderPanel({ onWindowIntent });

    fireEvent.click(
      screen.getByRole("button", { name: /Refresh token analytics/i }),
    );

    expect(onWindowIntent).toHaveBeenCalledWith({ type: "refresh-requested" });
  });

  it("displays active table filter notice", () => {
    renderPanel({ hasActiveFilters: true });

    expect(
      screen.getByText(
        /Active table filters do not scope this token analytics panel/i,
      ),
    ).toBeInTheDocument();
  });

  it("displays data quality warning when quality is below 100%", () => {
    renderPanel({
      data: {
        ...mockTokenUsageResponse,
        coverage: {
          ...mockTokenUsageResponse.coverage,
          comparable_requests: 1410,
          rate: 1410 / 1456,
        },
        data_quality: {
          quality_rate: 1410 / 1430,
          partial_requests: 12,
          invalid_requests: 3,
          unknown_semantics_requests: 5,
        },
      },
    });

    expect(screen.getByRole("alert")).toHaveTextContent(
      "Observed Data Quality Notice (98.6% quality rate)",
    );
    expect(screen.getByRole("alert")).toHaveTextContent(
      "12 partial, 3 invalid, and 5 unknown semantics",
    );
  });

  it("renders loading skeleton when loading without data", () => {
    renderPanel({ data: null, loading: true });

    expect(
      screen.getByLabelText("Loading token usage analytics"),
    ).toBeInTheDocument();
  });

  it("renders an initial error state with retry", () => {
    const onWindowIntent = vi.fn();
    renderPanel({
      data: null,
      error: new Error("Network failed"),
      onWindowIntent,
    });

    expect(
      screen.getByText("Failed to load token usage analytics"),
    ).toBeInTheDocument();
    expect(screen.getByText("Network failed")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /Retry/i }));
    expect(onWindowIntent).toHaveBeenCalledWith({ type: "refresh-requested" });
  });

  it("marks retained data stale when a refresh fails", () => {
    const onWindowIntent = vi.fn();
    const { container } = renderPanel({
      error: new Error("Gateway unavailable"),
      onWindowIntent,
    });

    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent(
      "Refresh failed ? showing the last successful snapshot",
    );
    expect(alert).toHaveTextContent("Gateway unavailable");
    expect(
      container.querySelector('time[datetime="2026-08-21T16:00:00Z"]'),
    ).toBeInTheDocument();
    expect(screen.getByText("12.45M")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /Retry/i }));
    expect(onWindowIntent).toHaveBeenCalledWith({ type: "refresh-requested" });
  });

  it("treats bounded zero-filled buckets as an empty report", () => {
    const zeroBucket = {
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
      total_requests: 0,
      observed_requests: 0,
      comparable_requests: 0,
    };
    const emptyData: TokenUsageResponse = {
      ...mockTokenUsageResponse,
      summary: {
        ...zeroBucket,
        cache_hit_rate: 0,
        reasoning_ratio: 0,
      },
      coverage: {
        total_requests: 0,
        observed_requests: 0,
        comparable_requests: 0,
        without_usage_requests: 0,
        rate: 0,
      },
      timeseries: [
        {
          ...zeroBucket,
          start: "2026-08-20T16:00:00Z",
          end: "2026-08-20T17:00:00Z",
        },
        {
          ...zeroBucket,
          start: "2026-08-20T17:00:00Z",
          end: "2026-08-20T18:00:00Z",
        },
      ],
      by_provider: [],
      by_model: [],
    };

    renderPanel({ data: emptyData });

    expect(screen.getByText("No Token Telemetry Recorded")).toBeInTheDocument();
    expect(
      screen.queryByText("Token Consumption Trend Over Time"),
    ).not.toBeInTheDocument();
  });

  it("opens and closes the information modal", () => {
    renderPanel();

    fireEvent.click(screen.getByLabelText("Token usage analytics information"));
    expect(screen.getByText("Token Analytics Guide")).toBeInTheDocument();
    expect(
      screen.getByText("Canonical Token Conservation"),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /Got it/i }));
    expect(screen.queryByText("Token Analytics Guide")).not.toBeInTheDocument();
  });
});
