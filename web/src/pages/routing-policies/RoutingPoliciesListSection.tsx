import { Pencil, Power, RefreshCw, Trash2 } from "lucide-react";
import type { Provider, RoutingPolicy } from "../../api";
import { GroupBadge, ProviderBadge, VendorBadge } from "./badges";
import {
  formatModelMatch,
  formatPolicyCount,
  formatTargetMode,
} from "./shared";

function RoutingPolicyScope({
  policy,
  providerById,
  groupNameById,
}: {
  policy: RoutingPolicy;
  providerById: Map<string, Provider>;
  groupNameById: Map<string, string>;
}) {
  if (policy.target_provider_id) {
    return (
      <div className="space-y-2">
        <p className="text-xs uppercase tracking-wide text-text-muted">
          Exact provider
        </p>
        <ProviderBadge
          providerId={policy.target_provider_id}
          providerById={providerById}
          groupNameById={groupNameById}
        />
      </div>
    );
  }

  return (
    <div className="space-y-3">
      <div className="space-y-1">
        <p className="text-xs uppercase tracking-wide text-text-muted">
          Groups
        </p>
        <div className="flex flex-wrap gap-1.5">
          {policy.allowed_group_ids.length === 0 ? (
            <span className="text-sm text-text-muted">—</span>
          ) : (
            policy.allowed_group_ids.map((groupId) => (
              <GroupBadge
                key={groupId}
                groupId={groupId}
                label={groupNameById.get(groupId) ?? groupId}
              />
            ))
          )}
        </div>
      </div>
      <div className="space-y-1">
        <p className="text-xs uppercase tracking-wide text-text-muted">
          Vendors
        </p>
        <div className="flex flex-wrap gap-1.5">
          {policy.allowed_vendors.length === 0 ? (
            <span className="text-sm text-text-muted">—</span>
          ) : (
            policy.allowed_vendors.map((vendor) => (
              <VendorBadge key={vendor} vendor={vendor} />
            ))
          )}
        </div>
      </div>
    </div>
  );
}

interface RoutingPoliciesListSectionProps {
  available: boolean;
  error: Error | null;
  loading: boolean;
  policies: RoutingPolicy[];
  groupNameById: Map<string, string>;
  providerById: Map<string, Provider>;
  togglingPolicyID: string | null;
  onEditPolicy: (policy: RoutingPolicy) => void;
  onDeletePolicy: (policy: RoutingPolicy) => void;
  onTogglePolicy: (policy: RoutingPolicy) => void;
}

export function RoutingPoliciesListSection({
  available,
  error,
  loading,
  policies,
  groupNameById,
  providerById,
  togglingPolicyID,
  onEditPolicy,
  onDeletePolicy,
  onTogglePolicy,
}: RoutingPoliciesListSectionProps) {
  const policySummary = formatPolicyCount(policies);
  const showEmptyState = !loading && policies.length === 0;

  if (!available && !error && !loading) {
    return (
      <section className="rounded-2xl border border-warning/20 bg-warning-light/30 px-5 py-4 shadow-sm">
        <h3 className="text-sm font-semibold text-warning-dark">
          Routing policy API unavailable
        </h3>
        <p className="text-sm text-text-secondary mt-1">
          The frontend is aligned to the lifecycle-aware routing policy
          resource, but the current backend build does not expose
          `/routing-policies` yet.
        </p>
      </section>
    );
  }

  if (loading && policies.length === 0) {
    return (
      <section className="bg-white rounded-2xl border border-border shadow-sm overflow-hidden">
        <div className="flex items-center justify-between gap-4 px-5 py-4 border-b border-border/60">
          <div>
            <h3 className="text-lg font-semibold text-text-primary">
              Policies
            </h3>
            <p className="text-sm text-text-secondary mt-1">{policySummary}</p>
          </div>
        </div>
        <div className="flex items-center justify-center py-20 text-text-muted">
          <RefreshCw className="w-6 h-6 animate-spin text-primary mr-3" />
          Loading routing policies...
        </div>
      </section>
    );
  }

  if (showEmptyState) {
    return (
      <section className="bg-white rounded-2xl border border-border shadow-sm overflow-hidden">
        <div className="flex items-center justify-between gap-4 px-5 py-4 border-b border-border/60">
          <div>
            <h3 className="text-lg font-semibold text-text-primary">
              Policies
            </h3>
            <p className="text-sm text-text-secondary mt-1">{policySummary}</p>
          </div>
        </div>
        <div className="px-5 py-16 text-center text-text-secondary">
          <p className="text-lg font-semibold text-text-primary">
            No routing policies configured
          </p>
          <p className="text-sm mt-2">
            Requests will continue using the default selector behavior until a
            rule is created for a specific api_type.
          </p>
        </div>
      </section>
    );
  }

  return (
    <section className="bg-white rounded-2xl border border-border shadow-sm overflow-hidden">
      <div className="flex items-center justify-between gap-4 px-5 py-4 border-b border-border/60">
        <div>
          <h3 className="text-lg font-semibold text-text-primary">Policies</h3>
          <p className="text-sm text-text-secondary mt-1">{policySummary}</p>
        </div>
      </div>

      <div className="overflow-x-auto">
        <table className="w-full min-w-[1120px] table-auto">
          <thead className="bg-bg-secondary border-b border-border/60">
            <tr>
              {[
                "Status",
                "API Type",
                "Model Match",
                "Target Mode",
                "Target Scope",
                "Updated",
                "",
              ].map((label) => (
                <th
                  key={label || "actions"}
                  className="px-4 py-3 text-left text-[11px] font-semibold uppercase tracking-wider text-text-secondary"
                >
                  {label}
                </th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y divide-border/60 bg-white">
            {policies.map((policy) => (
              <tr
                key={policy.id}
                className="hover:bg-bg-secondary/50 transition-colors"
              >
                <td className="px-4 py-3 align-top">
                  <span
                    className={`inline-flex rounded-full px-2.5 py-1 text-xs font-semibold ${
                      policy.enabled
                        ? "bg-success-light text-success-dark"
                        : "bg-bg-secondary text-text-secondary"
                    }`}
                  >
                    {policy.enabled ? "Enabled" : "Disabled"}
                  </span>
                </td>
                <td className="px-4 py-3 align-top">
                  <span className="inline-flex rounded-md bg-bg-tertiary border border-border/60 px-2 py-0.5 text-xs font-semibold uppercase tracking-wide text-text-primary">
                    {policy.api_type}
                  </span>
                </td>
                <td className="px-4 py-3 align-top text-sm text-text-primary">
                  {formatModelMatch(policy)}
                </td>
                <td className="px-4 py-3 align-top text-sm text-text-primary">
                  {formatTargetMode(policy)}
                </td>
                <td className="px-4 py-3 align-top">
                  <RoutingPolicyScope
                    policy={policy}
                    providerById={providerById}
                    groupNameById={groupNameById}
                  />
                </td>
                <td className="px-4 py-3 align-top text-sm text-text-secondary">
                  {policy.updated_at
                    ? new Date(policy.updated_at).toLocaleString()
                    : "—"}
                </td>
                <td className="px-4 py-3 align-top text-right">
                  <div className="flex items-center justify-end gap-1">
                    <button
                      type="button"
                      onClick={() => onTogglePolicy(policy)}
                      disabled={togglingPolicyID === policy.id}
                      className={`p-2 rounded-md text-text-muted hover:bg-bg-hover transition-colors ${
                        policy.enabled
                          ? "hover:text-warning"
                          : "hover:text-success"
                      }`}
                      aria-label={`${policy.enabled ? "Disable" : "Enable"} routing policy for ${policy.api_type}`}
                      title={policy.enabled ? "Disable" : "Enable"}
                    >
                      <Power className="w-4 h-4" />
                    </button>
                    <button
                      type="button"
                      onClick={() => onEditPolicy(policy)}
                      className="p-2 rounded-md text-text-muted hover:text-primary hover:bg-primary-light transition-colors"
                      aria-label={`Edit routing policy for ${policy.api_type}`}
                    >
                      <Pencil className="w-4 h-4" />
                    </button>
                    <button
                      type="button"
                      onClick={() => onDeletePolicy(policy)}
                      className="p-2 rounded-md text-text-muted hover:text-danger hover:bg-danger-light transition-colors"
                      aria-label={`Delete routing policy for ${policy.api_type}`}
                    >
                      <Trash2 className="w-4 h-4" />
                    </button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}
