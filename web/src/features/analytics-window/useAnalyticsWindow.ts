import { useReducer } from "react";
import {
  analyticsWindowReducer,
  createAnalyticsWindow,
  type AnalyticsWindowIntent,
} from "./analytics-window";

export interface AnalyticsWindowClock {
  now: () => Date;
}

const SYSTEM_CLOCK: AnalyticsWindowClock = {
  now: () => new Date(),
};

/**
 * Owns the complete query window so both analytics endpoints always receive the
 * same exclusive end, including on the first request.
 */
export function useAnalyticsWindow(clock: AnalyticsWindowClock = SYSTEM_CLOCK) {
  const [window, dispatch] = useReducer(
    analyticsWindowReducer,
    clock,
    (initialClock) => createAnalyticsWindow(initialClock.now().toISOString()),
  );

  const applyIntent = (intent: AnalyticsWindowIntent) => {
    if (intent.type === "refresh-requested") {
      dispatch({ type: "refreshed", as_of: clock.now().toISOString() });
      return;
    }
    dispatch(intent);
  };

  return { window, applyIntent };
}
