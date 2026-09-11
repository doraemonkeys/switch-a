import { useState } from "react";
import { useApi } from "../api";
import { TokenUsageAnalyticsPanel } from "../components";
import { TokenAPIKeyFilter } from "../components/token-analytics/TokenAPIKeyFilter";
import { tokenAPIKeyLabel } from "../components/token-analytics/token-format";
import type {
  AnalyticsWindow,
  AnalyticsWindowIntent,
} from "../features/analytics-window/analytics-window";
import {
  type AnalyticsWindowClock,
  useAnalyticsWindow,
} from "../features/analytics-window/useAnalyticsWindow";
import { useQuery } from "../hooks/useQuery";
import { useTokenUsage } from "../hooks/useTokenUsage";

interface TokenUsageProps {
  clock?: AnalyticsWindowClock;
}

export function TokenUsage({ clock }: TokenUsageProps = {}) {
  const api = useApi();
  const { window, applyIntent } = useAnalyticsWindow(clock);
  const [fingerprint, setFingerprint] = useState<string>();
  const directory = useQuery(() => api.tokenUsage.clientAPIKeys(), {
    queryKey: window.as_of,
    errorMessage: "Failed to fetch token usage API keys",
  });
  const keys = directory.data ?? [];
  const selectedKey = keys.find((key) => key.fingerprint === fingerprint);
  let scopeLabel = "Global";
  if (fingerprint === "") {
    scopeLabel = "Unattributed requests";
  } else if (fingerprint !== undefined) {
    scopeLabel = selectedKey ? tokenAPIKeyLabel(selectedKey) : fingerprint;
  }

  return (
    <div className="space-y-6">
      <TokenAPIKeyFilter
        keys={keys}
        selected={fingerprint}
        onSelect={setFingerprint}
        loading={directory.loading}
        error={directory.error}
        onRetry={() => void directory.refetch()}
      />
      <TokenUsageReport
        key={fingerprint ?? "all"}
        fingerprint={fingerprint}
        scopeLabel={scopeLabel}
        window={window}
        onWindowIntent={applyIntent}
      />
    </div>
  );
}

// A different key owns a different report instance. A failed switch must not
// relabel the previous key's cached totals, while refreshes retain their snapshot.
function TokenUsageReport({
  fingerprint,
  scopeLabel,
  window,
  onWindowIntent,
}: {
  fingerprint: string | undefined;
  scopeLabel: string;
  window: AnalyticsWindow;
  onWindowIntent: (intent: AnalyticsWindowIntent) => void;
}) {
  const params =
    fingerprint === undefined
      ? window
      : { ...window, client_api_key_fingerprint: fingerprint };
  const { data, loading, error } = useTokenUsage(params);

  return (
    <TokenUsageAnalyticsPanel
      data={data}
      loading={loading}
      error={error}
      scopeLabel={scopeLabel}
      window={window}
      onWindowIntent={onWindowIntent}
    />
  );
}
