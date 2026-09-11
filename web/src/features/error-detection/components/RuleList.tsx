import {
  ArrowDown,
  ArrowUp,
  ArrowRight,
  Repeat2,
  Plus,
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

function RuleActionSummary({ rule }: { rule: InternalErrorRule }) {
  const action = rule.action;
  if (action.type === "passthrough") {
    return (
      <div className="detection-rule-action">
        <strong>
          <ArrowRight size={14} aria-hidden="true" />
          Pass through
        </strong>
        <p>Return the upstream error</p>
      </div>
    );
  }
  return (
    <div className="detection-rule-action">
      <strong>
        <Repeat2 size={14} aria-hidden="true" />
        {action.type === "retry_only"
          ? "Retry same provider"
          : "Retry, then switch"}
      </strong>
      <p>
        Up to {action.max_retries}{" "}
        {action.max_retries === 1 ? "retry" : "retries"}
        {action.type === "retry_then_switch" && (
          <>
            <br />
            Then try another provider
          </>
        )}
      </p>
    </div>
  );
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
  available,
}: {
  stat: InternalErrorRuleStat | undefined;
  loading: boolean;
  available: boolean;
}) {
  if (!stat && loading)
    return <span className="text-text-muted">Loading…</span>;
  if (!available)
    return <span className="text-xs text-text-muted">Unavailable</span>;
  return (
    <div>
      <span className="font-mono text-sm text-text-primary">
        {formatHitCount(stat?.hit_count ?? "0")}
      </span>
      <span className="ml-1 text-xs text-text-muted">hits</span>
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
  readonly statsAvailable: boolean;
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
  statsAvailable,
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
    <li className="detection-rule">
      <div className="detection-rule-order">
        <span>#{index + 1}</span>
        <div className="detection-order-buttons">
          <button
            type="button"
            disabled={busy || index === 0}
            onClick={(event) => void move(-1, event.currentTarget)}
            className="detection-icon-button"
            aria-label={`Move ${rule.name} up`}
            title="Move up"
          >
            <ArrowUp size={12} aria-hidden="true" />
          </button>
          <button
            type="button"
            disabled={busy || index === rules.length - 1}
            onClick={(event) => void move(1, event.currentTarget)}
            className="detection-icon-button"
            aria-label={`Move ${rule.name} down`}
            title="Move down"
          >
            <ArrowDown size={12} aria-hidden="true" />
          </button>
        </div>
      </div>
      <div className="min-w-0">
        <div className="detection-rule-title">
          <strong>{rule.name}</strong>
          <button
            type="button"
            disabled={busy}
            onClick={() => void onToggle(rule)}
            className="detection-state"
            data-enabled={rule.enabled}
            aria-label={`${rule.enabled ? "Disable" : "Enable"} ${rule.name}`}
            title={rule.enabled ? "Disable rule" : "Enable rule"}
          >
            <span aria-hidden="true" />
            {rule.enabled ? "Enabled" : "Disabled"}
          </button>
        </div>
        <div className="detection-rule-meta">
          <APITypeLabel catalog={catalog} apiType={rule.api_type} />
          <span aria-hidden="true">·</span>
          <ScopeLabel rule={rule} providerByID={providerByID} />
        </div>
        <div className="detection-rule-keywords">
          <span>{rule.match_mode === "any" ? "ANY OF" : "ALL OF"}</span>
          {rule.keywords.map((keyword) => (
            <code key={keyword}>{keyword}</code>
          ))}
        </div>
      </div>
      <RuleActionSummary rule={rule} />
      <div className="detection-rule-stats">
        <RuleStats
          stat={stat}
          loading={statsLoading}
          available={statsAvailable}
        />
      </div>
      <div className="detection-rule-actions">
        <button
          type="button"
          disabled={busy}
          onClick={() => onEdit(rule)}
          className="detection-edit-button"
          aria-label={`Edit ${rule.name}`}
        >
          Edit
        </button>
        <button
          type="button"
          disabled={busy}
          onClick={() => onDelete(rule)}
          className="detection-icon-button hover:text-danger"
          aria-label={`Delete ${rule.name}`}
          title="Delete rule"
        >
          <Trash2 size={14} aria-hidden="true" />
        </button>
      </div>
    </li>
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
  return (
    <>
      <div className="detection-columns" aria-hidden="true">
        <span>Order</span>
        <span>Rule / match conditions</span>
        <span>On match</span>
        <span>Total hits</span>
        <span />
      </div>
      <ol aria-label="Ordered detection rules">
        {orderedRules.map((rule, index) => (
          <RuleRow
            key={rule.id}
            rule={rule}
            index={index}
            rules={orderedRules}
            catalog={catalog}
            providerByID={providerByID}
            stat={statByRuleID.get(rule.id)}
            statsAvailable={stats !== null}
            statsLoading={statsLoading}
            busy={busy}
            onEdit={onEdit}
            onDelete={onDelete}
            onToggle={onToggle}
            onReorder={onReorder}
          />
        ))}
      </ol>
    </>
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
      className="detection-rules"
    >
      <div className="detection-toolbar">
        <div>
          <h2
            id="internal-error-rules-heading"
            className="text-lg font-semibold text-text-primary"
          >
            Detection rules
          </h2>
          <p className="mt-1 text-sm text-text-secondary">
            Match an upstream error, then choose what happens next.
          </p>
        </div>
        <button
          type="button"
          disabled={!canCreate || busy}
          onClick={onCreate}
          className="btn btn-primary btn-sm"
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
      <footer className="detection-rules-footer">
        <span>Priority: provider scope → exact API type → rule order.</span>
        {ruleRevision !== null && <span>Revision {ruleRevision}</span>}
      </footer>
    </section>
  );
}
