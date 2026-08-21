import type { StatsGranularity, StatsPeriod } from "../../api/types";

export interface AnalyticsWindow {
  period: StatsPeriod;
  granularity: StatsGranularity;
  as_of: string;
}

export type AnalyticsWindowIntent =
  | { type: "period-selected"; period: StatsPeriod }
  | { type: "granularity-selected"; granularity: StatsGranularity }
  | { type: "refresh-requested" };

export const GRANULARITY_OPTIONS_BY_PERIOD: Record<
  StatsPeriod,
  StatsGranularity[]
> = {
  "24h": ["5m", "15m", "1h"],
  "7d": ["1h", "6h", "1d"],
  "30d": ["6h", "1d"],
  all: ["1d"],
};

export const DEFAULT_GRANULARITY_BY_PERIOD: Record<
  StatsPeriod,
  StatsGranularity
> = {
  "24h": "1h",
  "7d": "6h",
  "30d": "1d",
  all: "1d",
};

export function isGranularityAllowed(
  period: StatsPeriod,
  granularity: StatsGranularity | undefined,
): granularity is StatsGranularity {
  return (
    granularity !== undefined &&
    GRANULARITY_OPTIONS_BY_PERIOD[period].includes(granularity)
  );
}

export function createAnalyticsWindow(asOf: string): AnalyticsWindow {
  return {
    period: "24h",
    granularity: DEFAULT_GRANULARITY_BY_PERIOD["24h"],
    as_of: asOf,
  };
}

export type AnalyticsWindowEvent =
  | Exclude<AnalyticsWindowIntent, { type: "refresh-requested" }>
  | { type: "refreshed"; as_of: string };

export function analyticsWindowReducer(
  window: AnalyticsWindow,
  event: AnalyticsWindowEvent,
): AnalyticsWindow {
  switch (event.type) {
    case "period-selected":
      return {
        ...window,
        period: event.period,
        granularity: DEFAULT_GRANULARITY_BY_PERIOD[event.period],
      };
    case "granularity-selected":
      if (!isGranularityAllowed(window.period, event.granularity)) {
        return window;
      }
      return { ...window, granularity: event.granularity };
    case "refreshed":
      return { ...window, as_of: event.as_of };
  }
}
