import { useId } from "react";
import { ChevronDown, Clock3 } from "lucide-react";
import type { BackoffPolicy } from "../../api";
import { FORM_CONSTRAINTS, PROVIDER_DEFAULTS } from "../../config/constants";
import { calculateBackoffBaseDelays } from "../../features/error-detection/domain";
import { formatDuration } from "../../lib/utils";

const DURATION_PRESETS = {
  initialDelay: ["100ms", "500ms", "1s", "2s"] as const,
  maxDelay: ["0s", "1s", "2s", "5s", "10s"] as const,
};
const MAX_BACKOFF_PREVIEW_RETRIES = FORM_CONSTRAINTS.MAX_PROVIDER_RETRIES;

function effectiveBackoff(backoff: BackoffPolicy): Required<BackoffPolicy> {
  return {
    initial_delay: backoff.initial_delay,
    max_delay: backoff.max_delay,
    multiplier: backoff.multiplier ?? PROVIDER_DEFAULTS.BACKOFF.MULTIPLIER,
    jitter: backoff.jitter ?? PROVIDER_DEFAULTS.BACKOFF.JITTER,
  };
}

function hasCustomBackoff(backoff: BackoffPolicy): boolean {
  const policy = effectiveBackoff(backoff);
  const effectiveMultiplier =
    policy.multiplier === 0
      ? PROVIDER_DEFAULTS.BACKOFF.MULTIPLIER
      : policy.multiplier;
  return (
    policy.initial_delay !== PROVIDER_DEFAULTS.BACKOFF.INITIAL_DELAY ||
    policy.max_delay !== PROVIDER_DEFAULTS.BACKOFF.MAX_DELAY ||
    effectiveMultiplier !== PROVIDER_DEFAULTS.BACKOFF.MULTIPLIER ||
    policy.jitter !== PROVIDER_DEFAULTS.BACKOFF.JITTER
  );
}

interface DurationInputProps {
  label: string;
  value: string;
  presets: readonly string[];
  hint: string;
  disabled: boolean;
  onChange: (value: string) => void;
}

function DurationInput({
  label,
  value,
  presets,
  hint,
  disabled,
  onChange,
}: DurationInputProps) {
  const id = useId();
  return (
    <div className="space-y-2">
      <label htmlFor={id} className="text-sm font-medium text-text-secondary">
        {label}
      </label>
      <input
        id={id}
        type="text"
        className="input font-mono"
        value={value}
        disabled={disabled}
        onChange={(event) => onChange(event.target.value)}
        placeholder="e.g., 100ms, 1s, 2.5s"
      />
      <div className="flex flex-wrap gap-1.5" aria-label={`${label} presets`}>
        {presets.map((preset) => (
          <button
            key={preset}
            type="button"
            aria-pressed={value === preset}
            disabled={disabled}
            onClick={() => onChange(preset)}
            className={`cursor-pointer rounded-full border px-2 py-0.5 text-xs transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${
              value === preset
                ? "border-primary bg-primary text-white"
                : "border-border bg-bg-secondary text-text-secondary hover:border-primary"
            }`}
          >
            {preset}
          </button>
        ))}
      </div>
      <p className="text-xs text-text-muted">{hint}</p>
    </div>
  );
}

function BackoffPreview({
  backoff,
  maxRetries,
}: {
  backoff: Required<BackoffPolicy>;
  maxRetries: number;
}) {
  const previewRetryCount = Number.isSafeInteger(maxRetries)
    ? Math.min(Math.max(maxRetries, 0), MAX_BACKOFF_PREVIEW_RETRIES)
    : 0;
  const calculation = calculateBackoffBaseDelays(backoff, previewRetryCount);
  if (!calculation.valid) {
    return (
      <p
        role="status"
        className="rounded-lg bg-bg-tertiary p-3 text-xs text-text-muted"
      >
        Enter valid retry timing values to see the delay preview.
      </p>
    );
  }

  const attempts = calculation.base_delays_ms.map((delay, retryIndex) => ({
    attempt: retryIndex + 1,
    delay,
  }));
  const longestDelay = Math.max(...attempts.map(({ delay }) => delay), 1);

  return (
    <div className="rounded-lg border border-border bg-bg-tertiary/50 p-3">
      <div className="mb-2 flex items-center justify-between">
        <span className="text-xs font-medium text-text-secondary">
          Delay preview
        </span>
        {backoff.jitter && (
          <span className="rounded bg-info-light px-1.5 py-0.5 text-xs text-info">
            Jitter enabled
          </span>
        )}
      </div>
      <ul className="space-y-1.5" aria-label="Retry delay preview">
        {attempts.map(({ attempt, delay }) => (
          <li key={attempt} className="flex items-center gap-2">
            <span className="w-16 shrink-0 text-xs text-text-muted">
              Retry {attempt}
            </span>
            <div className="h-4 flex-1 overflow-hidden rounded bg-bg-secondary">
              <div
                className="h-full rounded bg-primary/60 transition-all"
                style={{ width: `${(delay / longestDelay) * 100}%` }}
              />
            </div>
            <span className="w-16 text-right font-mono text-xs text-text-secondary">
              {backoff.jitter ? "~" : ""}
              {formatDuration(delay, { smallestUnit: "ms" })}
            </span>
          </li>
        ))}
      </ul>
      {backoff.jitter && (
        <p className="mt-2 text-xs text-text-muted">
          Actual delays vary between 50% and 100% of the preview to avoid
          synchronized retry spikes.
        </p>
      )}
    </div>
  );
}

export interface BackoffPolicyEditorProps {
  backoff: BackoffPolicy;
  maxRetries: number;
  expanded: boolean;
  disabled?: boolean;
  onToggle: () => void;
  onChange: (backoff: BackoffPolicy) => void;
}

export function BackoffPolicyEditor({
  backoff,
  maxRetries,
  expanded,
  disabled = false,
  onToggle,
  onChange,
}: BackoffPolicyEditorProps) {
  const contentId = useId();
  const multiplierId = useId();
  const jitterId = useId();
  const policy = effectiveBackoff(backoff);
  const inactive = !Number.isSafeInteger(maxRetries) || maxRetries <= 0;
  const editorDisabled = disabled || inactive;

  function updateBackoff(patch: Partial<BackoffPolicy>) {
    onChange({ ...policy, ...patch });
  }

  return (
    <div
      className={`overflow-hidden rounded-lg border transition-colors ${
        editorDisabled ? "border-border/50 opacity-60" : "border-border"
      }`}
    >
      <button
        type="button"
        onClick={onToggle}
        disabled={editorDisabled}
        aria-expanded={!editorDisabled && expanded}
        aria-controls={contentId}
        className={`flex w-full items-center justify-between p-3 transition-colors ${
          editorDisabled
            ? "cursor-not-allowed bg-bg-secondary/50"
            : "cursor-pointer bg-bg-secondary hover:bg-bg-tertiary"
        }`}
      >
        <span className="flex items-center gap-2">
          <Clock3 className="h-4 w-4" aria-hidden="true" />
          <span
            className={`font-medium ${inactive ? "text-text-muted" : "text-text-primary"}`}
          >
            Retry Backoff
          </span>
          {!inactive && hasCustomBackoff(policy) && (
            <span className="rounded bg-info-light px-1.5 py-0.5 text-xs text-info-dark">
              Custom
            </span>
          )}
          {inactive && (
            <span className="rounded bg-bg-tertiary px-1.5 py-0.5 text-xs text-text-muted">
              Requires retries
            </span>
          )}
        </span>
        {!editorDisabled && (
          <ChevronDown
            className={`h-4 w-4 text-text-muted transition-transform ${expanded ? "rotate-180" : ""}`}
            aria-hidden="true"
          />
        )}
      </button>

      {expanded && !inactive && (
        <div
          id={contentId}
          className="space-y-4 border-t border-border bg-bg-primary p-4"
        >
          <p className="rounded-lg border border-info-light/50 bg-info-light/30 p-3 text-xs text-text-secondary">
            Exponential backoff spaces retries on the same provider so a
            temporary upstream problem is not amplified.
          </p>

          <div className="grid gap-4 sm:grid-cols-2">
            <DurationInput
              label="Initial Delay"
              value={policy.initial_delay}
              presets={DURATION_PRESETS.initialDelay}
              hint="Wait before the first retry"
              disabled={disabled}
              onChange={(value) => updateBackoff({ initial_delay: value })}
            />
            <DurationInput
              label="Max Delay"
              value={policy.max_delay}
              presets={DURATION_PRESETS.maxDelay}
              hint="0s keeps the delay uncapped"
              disabled={disabled}
              onChange={(value) => updateBackoff({ max_delay: value })}
            />
          </div>

          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <label
                htmlFor={multiplierId}
                className="text-sm font-medium text-text-secondary"
              >
                Multiplier
              </label>
              <input
                id={multiplierId}
                type="number"
                className="input"
                value={policy.multiplier}
                min={0}
                step={0.1}
                disabled={disabled}
                onChange={(event) =>
                  updateBackoff({ multiplier: event.target.valueAsNumber })
                }
              />
              <p className="text-xs text-text-muted">
                Growth after each retry; 0 uses the default multiplier
              </p>
            </div>
            <div className="space-y-2">
              <span className="text-sm font-medium text-text-secondary">
                Jitter
              </span>
              <label
                htmlFor={jitterId}
                className="flex cursor-pointer items-center gap-3 rounded-lg border border-border bg-bg-secondary p-3 transition-colors hover:bg-bg-tertiary"
              >
                <input
                  id={jitterId}
                  type="checkbox"
                  checked={policy.jitter}
                  disabled={disabled}
                  onChange={(event) =>
                    updateBackoff({ jitter: event.target.checked })
                  }
                  className="h-4 w-4 rounded border-border text-primary focus:ring-primary"
                />
                <span>
                  <span className="block text-sm font-medium text-text-primary">
                    Enable Jitter
                  </span>
                  <span className="block text-xs text-text-muted">
                    Prevent synchronized retry spikes
                  </span>
                </span>
              </label>
            </div>
          </div>

          <BackoffPreview backoff={policy} maxRetries={maxRetries} />
        </div>
      )}
    </div>
  );
}
