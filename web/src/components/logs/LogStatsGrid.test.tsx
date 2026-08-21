import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { StatsResponse } from "../../api/types";
import { LogStatsGrid } from "./LogStatsGrid";

const mockStats: StatsResponse = {
  total_requests: 140,
  avg_latency_ms: 180,
  outcome_counts: {
    completed: 100,
    interrupted: 15,
    never_started: 10,
    abandoned_by_client: 5,
    unknown: 10,
  },
  providers: {
    total: 5,
    healthy: 3,
    unhealthy: 1,
    disabled: 1,
  },
  requests_by_api_type: { claude: 100, codex: 40 },
  requests_by_provider_outcome: [],
  time_range: {
    start: "2026-04-01T00:00:00Z",
    end: "2026-04-02T00:00:00Z",
  },
  outcome_timeseries: [
    {
      time: "2026-04-01T00:00:00Z",
      total_requests: 30,
      avg_latency_ms: 160,
      outcome_counts: {
        completed: 20,
        interrupted: 5,
        never_started: 2,
        abandoned_by_client: 1,
        unknown: 2,
      },
    },
    {
      time: "2026-04-01T01:00:00Z",
      total_requests: 50,
      avg_latency_ms: 190,
      outcome_counts: {
        completed: 40,
        interrupted: 5,
        abandoned_by_client: 2,
        unknown: 3,
      },
    },
  ],
};

describe("LogStatsGrid", () => {
  it("renders normalized stats cards and the outcome time series", () => {
    render(
      <LogStatsGrid
        stats={mockStats}
        statsLoading={false}
        window={{
          period: "24h",
          granularity: "1h",
          as_of: "2026-04-02T00:00:00.000Z",
        }}
        onWindowIntent={vi.fn()}
        hasActiveFilters={false}
      />,
    );

    expect(
      screen.getByRole("heading", { name: /normalized outcome stats/i }),
    ).toBeInTheDocument();
    expect(screen.getByText("Requests")).toBeInTheDocument();
    expect(screen.getByText("Healthy Providers")).toBeInTheDocument();
    expect(screen.getAllByText("Completed")).not.toHaveLength(0);
    expect(screen.getAllByText("Interrupted")).not.toHaveLength(0);
    expect(screen.getAllByText("Never Started")).not.toHaveLength(0);
    expect(screen.getAllByText("Abandoned")).not.toHaveLength(0);
    expect(screen.getAllByText("Unknown")).not.toHaveLength(0);
    expect(screen.getByText("140")).toBeInTheDocument();
    expect(screen.getByText("3/5")).toBeInTheDocument();
    expect(
      screen.getByRole("img", {
        name: /outcome trend for last 24 hours at 1 hour granularity/i,
      }),
    ).toBeInTheDocument();
  });

  it("warns when table filters are active", () => {
    render(
      <LogStatsGrid
        stats={mockStats}
        statsLoading={false}
        window={{
          period: "24h",
          granularity: "1h",
          as_of: "2026-04-02T00:00:00.000Z",
        }}
        onWindowIntent={vi.fn()}
        hasActiveFilters
      />,
    );

    expect(
      screen.getByText(
        /active table filters do not scope this stats panel yet/i,
      ),
    ).toBeInTheDocument();
  });

  it("updates the stats window control with a compatible default bucket size", () => {
    const handleParamsChange = vi.fn();

    render(
      <LogStatsGrid
        stats={mockStats}
        statsLoading={false}
        window={{
          period: "24h",
          granularity: "1h",
          as_of: "2026-04-02T00:00:00.000Z",
        }}
        onWindowIntent={handleParamsChange}
        hasActiveFilters={false}
      />,
    );

    fireEvent.change(screen.getByLabelText(/stats window/i), {
      target: { value: "7d" },
    });

    expect(handleParamsChange).toHaveBeenCalledWith({
      type: "period-selected",
      period: "7d",
    });
  });

  it("updates the bucket size control within the selected window", () => {
    const handleParamsChange = vi.fn();

    render(
      <LogStatsGrid
        stats={mockStats}
        statsLoading={false}
        window={{
          period: "7d",
          granularity: "6h",
          as_of: "2026-04-02T00:00:00.000Z",
        }}
        onWindowIntent={handleParamsChange}
        hasActiveFilters={false}
      />,
    );

    fireEvent.change(screen.getByLabelText(/bucket size/i), {
      target: { value: "1d" },
    });

    expect(handleParamsChange).toHaveBeenCalledWith({
      type: "granularity-selected",
      granularity: "1d",
    });
  });
});
