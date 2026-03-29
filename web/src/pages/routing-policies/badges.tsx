import type { Provider } from "../../api";
import { stringToColor } from "../../lib/utils";
import { getProviderMeta } from "./providerLabels";

export function GroupBadge({
  groupId,
  label,
}: {
  groupId: string;
  label: string;
}) {
  const colors = stringToColor(groupId);
  return (
    <span
      className="inline-flex items-center rounded-md border px-2 py-0.5 text-xs font-medium"
      style={{
        backgroundColor: colors.bg,
        color: colors.text,
        borderColor: colors.border,
      }}
    >
      {label}
    </span>
  );
}

export function ProviderBadge({
  providerId,
  providerById,
  groupNameById,
}: {
  providerId: string;
  providerById: Map<string, Provider>;
  groupNameById: Map<string, string>;
}) {
  const provider = providerById.get(providerId);

  if (!provider) {
    return (
      <span className="inline-flex rounded-md border border-warning/20 bg-warning-light px-2 py-0.5 text-xs font-medium text-warning-dark">
        {providerId} (unavailable)
      </span>
    );
  }

  const meta = getProviderMeta(provider, groupNameById);

  return (
    <div className="space-y-1">
      <span className="inline-flex rounded-md border border-primary/15 bg-primary-light/40 px-2 py-0.5 text-xs font-semibold text-text-primary">
        {provider.name}
      </span>
      {meta && <p className="text-xs text-text-muted">{meta}</p>}
    </div>
  );
}

export function VendorBadge({
  vendor,
  stale = false,
}: {
  vendor: string;
  stale?: boolean;
}) {
  return (
    <span
      className={`inline-flex rounded-md border px-2 py-0.5 text-xs font-medium ${
        stale
          ? "border-warning/20 bg-warning-light text-warning-dark"
          : "border-border/60 bg-bg-secondary text-text-secondary"
      }`}
    >
      {stale ? `${vendor} (stale)` : vendor}
    </span>
  );
}
