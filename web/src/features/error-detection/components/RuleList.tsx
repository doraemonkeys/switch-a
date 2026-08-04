import {
  ArrowDown,
  ArrowUp,
  Pencil,
  Plus,
  Power,
  RefreshCw,
  Trash2,
} from "lucide-react";
import {
  findBuiltInAPIType,
  isValidCustomAPIType,
  type APICatalog,
} from "@/api/api-catalog";
import type { Provider } from "@/api/types";
import type {
  InternalErrorRule,
  InternalErrorRuleStat,
  InternalErrorRuleStatsResponse,
} from "../contracts";
import { moveRuleIDs } from "../model";

function formatHitCount(value: string): string {
  try {
    return BigInt(value).toLocaleString();
  } catch {
    return value;
  }
}

function actionLabel(rule: InternalErrorRule): string {
  if (rule.action.type === "passthrough") return "Pass through";
  return rule.action.type === "retry_only"
    ? `Retry only · ${rule.action.max_retries}`
    : `Retry then switch · ${rule.action.max_retries}`;
}

function APITypeLabel({
  catalog,
  apiType,
}: {
  catalog: APICatalog;
  apiType: string | null;
}) {
  if (apiType === null) return <>All supported built-ins</>;
  const entry = findBuiltInAPIType(catalog, apiType);
  if (entry) {
    return (
      <>
        {entry.label}
        {!entry.semantic_error_supported && " · Unsupported"}
      </>
    );
  }
  return (
    <>
      {apiType} ·{" "}
      {isValidCustomAPIType(catalog, apiType)
        ? "Custom unsupported"
        : "Unknown"}
    </>
  );
}

function ScopeLabel({
  rule,
  providerByID,
}: {
  rule: InternalErrorRule;
  providerByID: ReadonlyMap<string, Provider>;
}) {
  if (rule.target.kind === "global") return <>Global</>;
  const provider = providerByID.get(rule.target.provider_id);
  return provider ? (
    <>{provider.name}</>
  ) : (
    <span className="text-warning-dark">
      Deleted provider · {rule.target.provider_id}
    </span>
  );
}

function RuleStats({
  stat,
  loading,
}: {
  stat: InternalErrorRuleStat | undefined;
  loading: boolean;
}) {
  if (!stat && loading)
    return <span className="text-text-muted">Loading…</span>;
  return (
    <div>
      <span className="font-mono text-sm text-text-primary">
        {formatHitCount(stat?.hit_count ?? "0")}
      </span>
      <span className="mt-1 block text-xs text-text-muted">
        {stat?.last_hit_at
          ? `Last hit ${new Date(stat.last_hit_at).toLocaleString()}`
          : "Never hit"}
      </span>
    </div>
  );
}

interface RuleRowProps {
  readonly rule: InternalErrorRule;
  readonly index: number;
  readonly rules: readonly InternalErrorRule[];
  readonly catalog: APICatalog;
  readonly providerByID: ReadonlyMap<string, Provider>;
  readonly stat?: InternalErrorRuleStat;
  readonly statsLoading: boolean;
  readonly busy: boolean;
  readonly onEdit: (rule: InternalErrorRule) => void;
  readonly onDelete: (rule: InternalErrorRule) => void;
  readonly onToggle: (rule: InternalErrorRule) => Promise<void>;
  readonly onReorder: (orderedRuleIDs: readonly string[]) => Promise<void>;
}

function RuleRow({
  rule,
  index,
  rules,
  catalog,
  providerByID,
  stat,
  statsLoading,
  busy,
  onEdit,
  onDelete,
  onToggle,
  onReorder,
}: RuleRowProps) {
  async function move(
    direction: -1 | 1,
    button: HTMLButtonElement,
  ): Promise<void> {
    try {
      await onReorder(moveRuleIDs(rules, rule.id, direction));
    } finally {
      // The keyed row is moved rather than recreated; explicitly restoring
      // focus makes that invariant visible to keyboard and assistive-tech users.
      button.focus();
    }
  }

  return (
    <tr className="align-top transition-colors hover:bg-bg-secondary/50">
      <td className="px-4 py-3">
        <span className="text-xs font-semibold text-text-muted">
          #{index + 1}
        </span>
      </td>
      <td className="px-4 py-3">
        <span
          className={`inline-flex rounded-full px-2.5 py-1 text-xs font-semibold ${
            rule.enabled
              ? "bg-success-light text-success-dark"
              : "bg-bg-secondary text-text-secondary"
          }`}
        >
          {rule.enabled ? "Enabled" : "Disabled"}
        </span>
      </td>
      <td className="px-4 py-3">
        <strong className="block text-sm text-text-primary">{rule.name}</strong>
        <span className="mt-1 block text-xs text-text-muted">
          <ScopeLabel rule={rule} providerByID={providerByID} />
        </span>
      </td>
      <td className="px-4 py-3 text-sm text-text-secondary">
        <APITypeLabel catalog={catalog} apiType={rule.api_type} />
      </td>
      <td className="max-w-72 px-4 py-3">
        <div className="flex flex-wrap gap-1.5">
          {rule.keywords.map((keyword) => (
            <span
              key={keyword}
              className="rounded-md border border-border bg-bg-tertiary px-2 py-0.5 font-mono text-xs text-text-primary"
            >
              {keyword}
            </span>
          ))}
        </div>
        <span className="mt-1 block text-xs uppercase tracking-wide text-text-muted">
          Match {rule.match_mode}
        </span>
      </td>
      <td className="px-4 py-3 text-sm text-text-secondary">
        {actionLabel(rule)}
      </td>
      <td className="px-4 py-3 text-sm">
        <RuleStats stat={stat} loading={statsLoading} />
      </td>
      <td className="px-4 py-3">
        <div className="flex items-center justify-end gap-1">
          <button
            type="button"
            disabled={busy || index === 0}
            onClick={(event) => void move(-1, event.currentTarget)}
            className="rounded-md p-2 text-text-muted hover:bg-bg-hover hover:text-primary disabled:opacity-30"
            aria-label={`Move ${rule.name} up`}
          >
            <ArrowUp className="h-4 w-4" aria-hidden="true" />
          </button>
          <button
            type="button"
            disabled={busy || index === rules.length - 1}
            onClick={(event) => void move(1, event.currentTarget)}
            className="rounded-md p-2 text-text-muted hover:bg-bg-hover hover:text-primary disabled:opacity-30"
            aria-label={`Move ${rule.name} down`}
          >
            <ArrowDown className="h-4 w-4" aria-hidden="true" />
          </button>
          <button
            type="button"
            disabled={busy}
            onClick={() => void onToggle(rule)}
            className="rounded-md p-2 text-text-muted hover:bg-bg-hover hover:text-warning-dark disabled:opacity-50"
            aria-label={`${rule.enabled ? "Disable" : "Enable"} ${rule.name}`}
          >
            <Power className="h-4 w-4" aria-hidden="true" />
          </button>
          <button
            type="button"
            disabled={busy}
            onClick={() => onEdit(rule)}
            className="rounded-md p-2 text-text-muted hover:bg-primary-light hover:text-primary disabled:opacity-50"
            aria-label={`Edit ${rule.name}`}
          >
            <Pencil className="h-4 w-4" aria-hidden="true" />
          </button>
          <button
            type="button"
            disabled={busy}
            onClick={() => onDelete(rule)}
            className="rounded-md p-2 text-text-muted hover:bg-danger-light hover:text-danger disabled:opacity-50"
            aria-label={`Delete ${rule.name}`}
          >
            <Trash2 className="h-4 w-4" aria-hidden="true" />
          </button>
        </div>
      </td>
    </tr>
  );
}

export interface RuleListProps {
  readonly rules: readonly InternalErrorRule[];
  readonly ruleRevision: string | null;
  readonly catalog: APICatalog;
  readonly providers: readonly Provider[];
  readonly stats: InternalErrorRuleStatsResponse | null;
  readonly statsLoading: boolean;
  readonly statsError: Error | null;
  readonly loading: boolean;
  readonly error: Error | null;
  readonly busy: boolean;
  readonly canCreate: boolean;
  readonly onCreate: () => void;
  readonly onEdit: (rule: InternalErrorRule) => void;
  readonly onDelete: (rule: InternalErrorRule) => void;
  readonly onToggle: (rule: InternalErrorRule) => Promise<void>;
  readonly onReorder: (orderedRuleIDs: readonly string[]) => Promise<void>;
}

type RuleListContentProps = Pick<
  RuleListProps,
  | "catalog"
  | "providers"
  | "statsLoading"
  | "loading"
  | "busy"
  | "onEdit"
  | "onDelete"
  | "onToggle"
  | "onReorder"
> & {
  readonly orderedRules: readonly InternalErrorRule[];
  readonly stats: InternalErrorRuleStatsResponse | null;
};

function RuleListContent({
  orderedRules,
  catalog,
  providers,
  stats,
  statsLoading,
  loading,
  busy,
  onEdit,
  onDelete,
  onToggle,
  onReorder,
}: RuleListContentProps) {
  if (loading && orderedRules.length === 0) {
    return (
      <div className="flex items-center justify-center py-16 text-sm text-text-muted">
        <RefreshCw
          className="mr-2 h-5 w-5 animate-spin text-primary"
          aria-hidden="true"
        />
        Loading detection rules…
      </div>
    );
  }
  if (orderedRules.length === 0) {
    return (
      <div className="px-5 py-16 text-center">
        <h3 className="text-base font-semibold text-text-primary">
          No detection rules configured
        </h3>
        <p className="mx-auto mt-2 max-w-xl text-sm text-text-secondary">
          Structured upstream errors continue to pass through normally until a
          rule is created. Ordinary response content is never scanned.
        </p>
      </div>
    );
  }

  const providerByID = new Map(
    providers.map((provider) => [provider.id, provider]),
  );
  const statByRuleID = new Map(
    stats?.stats.map((stat) => [stat.rule_id, stat]) ?? [],
  );
  const headings = [
    "Position",
    "Status",
    "Rule / scope",
    "API type",
    "Keywords",
    "Action / retries",
    "Hits",
    "Actions",
  ];
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[1180px] table-auto">
        <thead className="border-b border-border bg-bg-secondary">
          <tr>
            {headings.map((heading) => (
              <th
                key={heading}
                scope="col"
                className="px-4 py-3 text-left text-[11px] font-semibold uppercase tracking-wider text-text-secondary"
              >
                {heading}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="divide-y divide-border">
          {orderedRules.map((rule, index) => (
            <RuleRow
              key={rule.id}
              rule={rule}
              index={index}
              rules={orderedRules}
              catalog={catalog}
              providerByID={providerByID}
              stat={statByRuleID.get(rule.id)}
              statsLoading={statsLoading}
              busy={busy}
              onEdit={onEdit}
              onDelete={onDelete}
              onToggle={onToggle}
              onReorder={onReorder}
            />
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function RuleList({
  rules,
  ruleRevision,
  catalog,
  providers,
  stats,
  statsLoading,
  statsError,
  loading,
  error,
  busy,
  canCreate,
  onCreate,
  onEdit,
  onDelete,
  onToggle,
  onReorder,
}: RuleListProps) {
  const orderedRules = [...rules].sort(
    (left, right) => left.position - right.position,
  );
  const statsRevisionStale =
    stats !== null &&
    ruleRevision !== null &&
    stats.rule_set_revision !== ruleRevision;

  return (
    <section
      aria-labelledby="internal-error-rules-heading"
      aria-busy={loading || busy}
      className="overflow-hidden rounded-2xl border border-border bg-white shadow-sm"
    >
      <div className="flex flex-wrap items-center justify-between gap-4 border-b border-border px-5 py-4">
        <div>
          <h2
            id="internal-error-rules-heading"
            className="text-lg font-semibold text-text-primary"
          >
            Detection rules
          </h2>
          <p className="mt-1 text-sm text-text-secondary">
            {rules.length} {rules.length === 1 ? "rule" : "rules"}
            {ruleRevision !== null ? ` · revision ${ruleRevision}` : ""}
          </p>
        </div>
        <button
          type="button"
          disabled={!canCreate || busy}
          onClick={onCreate}
          className="btn btn-primary"
        >
          <Plus className="h-4 w-4" aria-hidden="true" />
          Add rule
        </button>
      </div>

      {error && (
        <div
          role="alert"
          className="border-b border-danger/20 bg-danger/5 p-4 text-sm text-danger"
        >
          Rules could not be refreshed: {error.message}
        </div>
      )}
      {statsError && (
        <div
          role="status"
          className="border-b border-warning/20 bg-warning-light/30 p-4 text-sm text-warning-dark"
        >
          Hit statistics are unavailable: {statsError.message}
        </div>
      )}
      {statsRevisionStale && (
        <div
          role="status"
          className="border-b border-warning/20 bg-warning-light/30 p-4 text-sm text-warning-dark"
        >
          Statistics are from rule revision {stats?.rule_set_revision};
          refreshing for revision {ruleRevision}.
        </div>
      )}

      <RuleListContent
        orderedRules={orderedRules}
        catalog={catalog}
        providers={providers}
        stats={stats}
        statsLoading={statsLoading}
        loading={loading}
        busy={busy}
        onEdit={onEdit}
        onDelete={onDelete}
        onToggle={onToggle}
        onReorder={onReorder}
      />
    </section>
  );
}
