import { TokenUsageAnalyticsPanel } from "../components";
import {
  type AnalyticsWindowClock,
  useAnalyticsWindow,
} from "../features/analytics-window/useAnalyticsWindow";
import { useTokenUsage } from "../hooks/useTokenUsage";

interface TokenUsageProps {
  clock?: AnalyticsWindowClock;
}

export function TokenUsage({ clock }: TokenUsageProps = {}) {
  const { window, applyIntent } = useAnalyticsWindow(clock);
  const { data, loading, error } = useTokenUsage(window);

  return (
    <div className="space-y-6">
      <TokenUsageAnalyticsPanel
        data={data}
        loading={loading}
        error={error}
        window={window}
        onWindowIntent={applyIntent}
      />
    </div>
  );
}
