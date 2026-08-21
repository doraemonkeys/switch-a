import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { TokenBucketDTO } from "../../api/types";
import { TokenTrendChart } from "./TokenTrendChart";

const mockTimeseries: TokenBucketDTO[] = [
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
  {
    start: "2026-08-21T01:00:00Z",
    end: "2026-08-21T02:00:00Z",
    total_tokens: "800000",
    input_tokens: "500000",
    output_tokens: "300000",
    fresh_input_tokens: "300000",
    cache_read_input_tokens: "180000",
    cache_creation_input_tokens: "20000",
    unclassified_input_tokens: "0",
    standard_output_tokens: "250000",
    reasoning_tokens: "50000",
    unclassified_output_tokens: "0",
    total_requests: 80,
    observed_requests: 80,
    comparable_requests: 80,
  },
];

describe("TokenTrendChart", () => {
  it("renders trend chart with legend series buttons", () => {
    render(
      <TokenTrendChart
        timeseries={mockTimeseries}
        period="24h"
        granularity="1h"
      />,
    );

    expect(
      screen.getByText("Token Consumption Trend Over Time"),
    ).toBeInTheDocument();
    expect(screen.getByText("Fresh Input")).toBeInTheDocument();
    expect(screen.getByText("Cache Read")).toBeInTheDocument();
    expect(screen.getByText("Cache Creation")).toBeInTheDocument();
    expect(screen.getByText("Standard Output")).toBeInTheDocument();
    expect(screen.getByText("Reasoning CoT")).toBeInTheDocument();
  });

  it("toggles legend filter series", () => {
    render(
      <TokenTrendChart
        timeseries={mockTimeseries}
        period="24h"
        granularity="1h"
      />,
    );

    const freshBtn = screen.getByRole("button", { name: /Fresh Input/i });
    expect(freshBtn).toHaveAttribute("aria-pressed", "true");

    fireEvent.click(freshBtn);
    expect(freshBtn).toHaveAttribute("aria-pressed", "false");

    fireEvent.click(freshBtn);
    expect(freshBtn).toHaveAttribute("aria-pressed", "true");
  });

  it("renders rich tooltip on bucket hover", () => {
    render(
      <TokenTrendChart
        timeseries={mockTimeseries}
        period="24h"
        granularity="1h"
      />,
    );

    const bucketBars = screen.getAllByLabelText(/Bucket/i);
    expect(bucketBars).toHaveLength(2);

    fireEvent.mouseEnter(bucketBars[0]);

    const tooltip = screen.getByRole("tooltip");
    expect(tooltip).toBeInTheDocument();
    expect(tooltip).toHaveTextContent("1,200,000");
    expect(tooltip).toHaveTextContent("118 observed / 120 total");
    expect(tooltip).toHaveTextContent("800,000");
    expect(tooltip).toHaveTextContent("350,000");
    expect(tooltip).toHaveTextContent("320,000");
    expect(tooltip).toHaveTextContent("80,000");
  });

  it("exposes every bucket as a focusable tooltip trigger", () => {
    render(
      <TokenTrendChart
        timeseries={mockTimeseries}
        period="24h"
        granularity="1h"
      />,
    );

    const bucketButtons = screen.getAllByRole("button", { name: /Bucket/i });
    expect(bucketButtons).toHaveLength(2);

    fireEvent.focus(bucketButtons[0]);

    const tooltip = screen.getByRole("tooltip");
    expect(bucketButtons[0]).toHaveAttribute("aria-describedby", tooltip.id);
    expect(bucketButtons[0]).toHaveAttribute("aria-expanded", "true");
    expect(tooltip).toHaveTextContent("1,200,000");

    fireEvent.blur(bucketButtons[0]);
    expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();
  });

  it("renders empty state message when timeseries is empty", () => {
    render(<TokenTrendChart timeseries={[]} period="24h" granularity="1h" />);

    expect(
      screen.getByText("No token time series recorded for this time window."),
    ).toBeInTheDocument();
  });
});
