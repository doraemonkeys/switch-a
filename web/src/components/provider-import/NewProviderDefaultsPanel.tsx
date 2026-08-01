import { useId, useState } from "react";
import { Check, Settings2 } from "lucide-react";
import type { BackoffPolicy, Group } from "../../api";
import { BackoffPolicyEditor } from "../provider-settings/BackoffPolicyEditor";
import { PROVIDER_DEFAULTS } from "../../config/constants";
import {
  PROVIDER_IMPORT_MAX_RETRIES,
  PROVIDER_IMPORT_MAX_SCHEDULING_VALUE,
  getProviderImportNewProviderDefaultsError,
  type ProviderImportNewProviderDefaults,
} from "../../lib/providerImportSettings";

interface NewProviderDefaultsPanelProps {
  defaults: ProviderImportNewProviderDefaults;
  groupId: string | null;
  groups: Group[];
  newProviderCount: number;
  disabled: boolean;
  onSetGroup: (groupId: string | null) => void;
  onApply: (defaults: ProviderImportNewProviderDefaults) => void;
}

function completeBackoff(backoff: BackoffPolicy): BackoffPolicy {
  return {
    initial_delay: backoff.initial_delay,
    max_delay: backoff.max_delay,
    multiplier: backoff.multiplier ?? PROVIDER_DEFAULTS.BACKOFF.MULTIPLIER,
    jitter: backoff.jitter ?? PROVIDER_DEFAULTS.BACKOFF.JITTER,
  };
}

function cloneDefaults(
  defaults: ProviderImportNewProviderDefaults,
): ProviderImportNewProviderDefaults {
  return { ...defaults, backoff: completeBackoff(defaults.backoff) };
}

function defaultsMatch(
  left: ProviderImportNewProviderDefaults,
  right: ProviderImportNewProviderDefaults,
): boolean {
  const leftBackoff = completeBackoff(left.backoff);
  const rightBackoff = completeBackoff(right.backoff);
  return (
    left.weight === right.weight &&
    left.maxRetries === right.maxRetries &&
    leftBackoff.initial_delay === rightBackoff.initial_delay &&
    leftBackoff.max_delay === rightBackoff.max_delay &&
    leftBackoff.multiplier === rightBackoff.multiplier &&
    leftBackoff.jitter === rightBackoff.jitter
  );
}

export function NewProviderDefaultsPanel({
  defaults,
  groupId,
  groups,
  newProviderCount,
  disabled,
  onSetGroup,
  onApply,
}: NewProviderDefaultsPanelProps) {
  const headingId = useId();
  const groupHintId = useId();
  const weightHintId = useId();
  const retriesHintId = useId();
  const [form, setForm] = useState(() => cloneDefaults(defaults));
  const [backoffExpanded, setBackoffExpanded] = useState(false);
  const error = getProviderImportNewProviderDefaultsError(form);
  const dirty = !defaultsMatch(form, defaults);
  const accountLabel = newProviderCount === 1 ? "provider" : "providers";

  return (
    <section
      aria-labelledby={headingId}
      className="space-y-4 rounded-xl border border-primary/20 bg-primary/5 p-4"
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex items-start gap-3">
          <span className="rounded-lg bg-primary-light p-2 text-primary">
            <Settings2 className="h-5 w-5" aria-hidden="true" />
          </span>
          <div>
            <h3 id={headingId} className="font-semibold text-text-primary">
              Defaults for new providers
            </h3>
            <p className="mt-1 max-w-2xl text-xs leading-relaxed text-text-secondary">
              Weight and retry behavior are Switch-A settings, so they cannot be
              inferred from sub2api. Apply them once here; priority, weight,
              concurrency, and retry count can still be adjusted per account.
            </p>
          </div>
        </div>
        <span className="rounded-full bg-white px-2.5 py-1 text-xs font-medium text-text-secondary shadow-sm">
          {newProviderCount} new {accountLabel}
        </span>
      </div>

      <div className="grid gap-3 sm:grid-cols-3">
        <div>
          <label className="text-xs font-medium text-text-secondary">
            Group
            <select
              value={groupId ?? ""}
              disabled={disabled}
              aria-describedby={groupHintId}
              onChange={(event) => onSetGroup(event.target.value || null)}
              className="input mt-1"
            >
              <option value="">No group</option>
              {groups.map((group) => (
                <option key={group.id} value={group.id}>
                  {group.enabled ? group.name : `${group.name} (disabled)`}
                </option>
              ))}
            </select>
          </label>
          <span id={groupHintId} className="mt-1 block text-xs text-text-muted">
            Shared by every new provider
          </span>
        </div>

        <div>
          <label className="text-xs font-medium text-text-secondary">
            Weight
            <input
              type="number"
              min={1}
              max={PROVIDER_IMPORT_MAX_SCHEDULING_VALUE}
              value={Number.isNaN(form.weight) ? "" : form.weight}
              disabled={disabled}
              aria-invalid={error?.field === "weight" || undefined}
              aria-describedby={weightHintId}
              onChange={(event) =>
                setForm((current) => ({
                  ...current,
                  weight:
                    event.target.value === ""
                      ? Number.NaN
                      : event.target.valueAsNumber,
                }))
              }
              className={`input mt-1 ${error?.field === "weight" ? "border-danger" : ""}`}
            />
          </label>
          <span
            id={weightHintId}
            className="mt-1 block text-xs text-text-muted"
          >
            Used by weighted routing
          </span>
        </div>

        <div>
          <label className="text-xs font-medium text-text-secondary">
            Max retries
            <input
              type="number"
              min={0}
              max={PROVIDER_IMPORT_MAX_RETRIES}
              value={Number.isNaN(form.maxRetries) ? "" : form.maxRetries}
              disabled={disabled}
              aria-invalid={error?.field === "maxRetries" || undefined}
              aria-describedby={retriesHintId}
              onChange={(event) =>
                setForm((current) => ({
                  ...current,
                  maxRetries:
                    event.target.value === ""
                      ? Number.NaN
                      : event.target.valueAsNumber,
                }))
              }
              className={`input mt-1 ${error?.field === "maxRetries" ? "border-danger" : ""}`}
            />
          </label>
          <span
            id={retriesHintId}
            className="mt-1 block text-xs text-text-muted"
          >
            0 switches provider immediately
          </span>
        </div>
      </div>

      <BackoffPolicyEditor
        backoff={form.backoff}
        maxRetries={form.maxRetries}
        expanded={backoffExpanded}
        disabled={disabled}
        onToggle={() => setBackoffExpanded((expanded) => !expanded)}
        onChange={(backoff) => setForm((current) => ({ ...current, backoff }))}
      />

      <div className="flex flex-wrap items-center justify-between gap-3">
        <p
          role={error ? "alert" : "status"}
          className={`text-xs ${error ? "text-danger" : "text-text-muted"}`}
        >
          {error?.message ??
            "Priority and concurrency remain prefilled from each source account."}
        </p>
        <button
          type="button"
          disabled={disabled || Boolean(error) || !dirty}
          onClick={() => onApply(cloneDefaults(form))}
          className="btn btn-secondary disabled:cursor-not-allowed disabled:opacity-50"
        >
          {!dirty && <Check className="h-4 w-4" aria-hidden="true" />}
          {dirty
            ? `Apply to ${newProviderCount} new ${accountLabel}`
            : "Defaults applied"}
        </button>
      </div>
    </section>
  );
}
