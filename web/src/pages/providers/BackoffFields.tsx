import { useId } from "react";
import type { ProviderInput, BackoffPolicy } from "../../api";
import { PROVIDER_DEFAULTS, FORM_CONSTRAINTS } from "../../config/constants";
import { FormField } from "./FormField";

/** Common duration presets for quick selection */
const DURATION_PRESETS = {
  INITIAL_DELAY: ["50ms", "100ms", "200ms", "500ms"] as const,
  MAX_DELAY: ["1s", "2s", "5s", "10s"] as const,
};

/** Check if backoff policy has non-default configuration */
function hasBackoffConfig(backoff: BackoffPolicy | undefined): boolean {
  if (!backoff) return false;
  return (
    backoff.initial_delay !== PROVIDER_DEFAULTS.BACKOFF.INITIAL_DELAY ||
    backoff.max_delay !== PROVIDER_DEFAULTS.BACKOFF.MAX_DELAY ||
    (backoff.multiplier !== undefined &&
      backoff.multiplier !== PROVIDER_DEFAULTS.BACKOFF.MULTIPLIER) ||
    (backoff.jitter !== undefined &&
      backoff.jitter !== PROVIDER_DEFAULTS.BACKOFF.JITTER)
  );
}

interface DurationInputProps {
  label: string;
  value: string;
  onChange: (value: string) => void;
  presets: readonly string[];
  hint?: string;
}

function DurationInput({
  label,
  value,
  onChange,
  presets,
  hint,
}: DurationInputProps) {
  return (
    <FormField label={label}>
      {(id) => (
        <div className="space-y-2">
          <input
            id={id}
            type="text"
            className="input font-mono"
            value={value}
            onChange={(e) => onChange(e.target.value)}
            placeholder="e.g., 100ms, 1s, 2.5s"
          />
          <div className="flex flex-wrap gap-1.5">
            {presets.map((preset) => (
              <button
                key={preset}
                type="button"
                aria-pressed={value === preset}
                onClick={() => onChange(preset)}
                className={`px-2 py-0.5 text-xs rounded-full border transition-colors cursor-pointer ${
                  value === preset
                    ? "bg-primary text-white border-primary"
                    : "bg-bg-secondary text-text-secondary border-border hover:border-primary"
                }`}
              >
                {preset}
              </button>
            ))}
          </div>
          {hint && <p className="text-xs text-text-muted">{hint}</p>}
        </div>
      )}
    </FormField>
  );
}

interface BackoffPreviewProps {
  backoff: BackoffPolicy;
  maxRetries: number;
}

/**
 * Visual preview of backoff delays for each retry attempt.
 * Helps users understand the exponential backoff behavior.
 */
function BackoffPreview({ backoff, maxRetries }: BackoffPreviewProps) {
  if (maxRetries <= 0) return null;

  // Parse duration string to milliseconds (simplified parsing)
  const parseDuration = (str: string): number => {
    const match = str.match(/^([\d.]+)(ms|s)$/);
    if (!match) return 0;
    const [, num, unit] = match;
    return unit === "s" ? parseFloat(num) * 1000 : parseFloat(num);
  };

  const formatDuration = (ms: number): string => {
    if (ms >= 1000) return `${(ms / 1000).toFixed(1)}s`;
    return `${Math.round(ms)}ms`;
  };

  const initialMs = parseDuration(backoff.initial_delay);
  const maxMs = parseDuration(backoff.max_delay);
  const multiplier = backoff.multiplier ?? PROVIDER_DEFAULTS.BACKOFF.MULTIPLIER;

  // Calculate delays for each retry
  const delays: number[] = [];
  for (let i = 0; i < maxRetries; i++) {
    let delay = initialMs * Math.pow(multiplier, i);
    if (maxMs > 0 && delay > maxMs) delay = maxMs;
    delays.push(delay);
  }

  const maxDelay = Math.max(...delays, 1);

  return (
    <div className="mt-3 p-3 rounded-lg bg-bg-tertiary/50 border border-border">
      <div className="flex items-center justify-between mb-2">
        <span className="text-xs font-medium text-text-secondary">
          Delay Preview
        </span>
        {backoff.jitter && (
          <span className="text-xs text-info px-1.5 py-0.5 bg-info-light rounded">
            + Jitter
          </span>
        )}
      </div>
      <ul role="list" className="space-y-1.5">
        {delays.map((delay, idx) => (
          <li key={`retry-${idx}-${delay}`} className="flex items-center gap-2">
            <span className="text-xs text-text-muted w-16 shrink-0">
              Retry {idx + 1}:
            </span>
            <div className="flex-1 h-4 bg-bg-secondary rounded overflow-hidden">
              <div
                className="h-full bg-primary/60 rounded transition-all"
                style={{ width: `${(delay / maxDelay) * 100}%` }}
              />
            </div>
            <span className="text-xs font-mono text-text-secondary w-14 text-right">
              {backoff.jitter
                ? `~${formatDuration(delay)}`
                : formatDuration(delay)}
            </span>
          </li>
        ))}
      </ul>
      {backoff.jitter && (
        <p className="text-xs text-text-muted mt-2 italic">
          With jitter, actual delays are randomly chosen between 0 and the shown
          value
        </p>
      )}
    </div>
  );
}

interface BackoffSectionProps {
  formData: ProviderInput;
  setFormData: React.Dispatch<React.SetStateAction<ProviderInput>>;
  expanded: boolean;
  onToggle: () => void;
}

export function BackoffSection({
  formData,
  setFormData,
  expanded,
  onToggle,
}: BackoffSectionProps) {
  const contentId = useId();
  const maxRetries = formData.max_retries ?? 0;
  const isDisabled = maxRetries === 0;

  // Current backoff values with defaults
  const backoff: BackoffPolicy = {
    initial_delay:
      formData.backoff?.initial_delay ??
      PROVIDER_DEFAULTS.BACKOFF.INITIAL_DELAY,
    max_delay:
      formData.backoff?.max_delay ?? PROVIDER_DEFAULTS.BACKOFF.MAX_DELAY,
    multiplier:
      formData.backoff?.multiplier ?? PROVIDER_DEFAULTS.BACKOFF.MULTIPLIER,
    jitter: formData.backoff?.jitter ?? PROVIDER_DEFAULTS.BACKOFF.JITTER,
  };

  const updateBackoff = (patch: Partial<BackoffPolicy>) => {
    setFormData((prev) => ({
      ...prev,
      backoff: { ...backoff, ...patch },
    }));
  };

  const hasConfig = hasBackoffConfig(formData.backoff);

  return (
    <div
      className={`border rounded-lg overflow-hidden transition-colors ${
        isDisabled ? "border-border/50 opacity-60" : "border-border"
      }`}
    >
      <button
        type="button"
        onClick={onToggle}
        disabled={isDisabled}
        aria-expanded={!isDisabled && expanded}
        aria-controls={contentId}
        className={`w-full flex items-center justify-between p-3 transition-colors ${
          isDisabled
            ? "bg-bg-secondary/50 cursor-not-allowed"
            : "bg-bg-secondary hover:bg-bg-tertiary cursor-pointer"
        }`}
      >
        <div className="flex items-center gap-2">
          <span className="text-lg">⏱️</span>
          <span
            className={`font-medium ${isDisabled ? "text-text-muted" : "text-text-primary"}`}
          >
            Retry Backoff
          </span>
          {!isDisabled && hasConfig && (
            <span className="px-1.5 py-0.5 text-xs bg-info-light text-info-dark rounded">
              Custom
            </span>
          )}
          {isDisabled && (
            <span className="px-1.5 py-0.5 text-xs bg-bg-tertiary text-text-muted rounded">
              Requires max_retries &gt; 0
            </span>
          )}
        </div>
        {!isDisabled && (
          <span
            className={`text-text-muted transition-transform ${expanded ? "rotate-180" : ""}`}
          >
            ▼
          </span>
        )}
      </button>

      {expanded && !isDisabled && (
        <div
          id={contentId}
          className="p-4 space-y-4 border-t border-border bg-bg-primary"
        >
          <div className="p-3 rounded-lg bg-info-light/30 border border-info-light/50">
            <p className="text-xs text-text-secondary">
              <strong>Exponential Backoff</strong> adds increasing delays
              between retries on the same provider. This helps avoid
              overwhelming a temporarily overloaded service.
            </p>
          </div>

          <div className="grid grid-cols-2 gap-4">
            <DurationInput
              label="Initial Delay"
              value={backoff.initial_delay}
              onChange={(v) => updateBackoff({ initial_delay: v })}
              presets={DURATION_PRESETS.INITIAL_DELAY}
              hint="Delay before first retry"
            />
            <DurationInput
              label="Max Delay"
              value={backoff.max_delay}
              onChange={(v) => updateBackoff({ max_delay: v })}
              presets={DURATION_PRESETS.MAX_DELAY}
              hint="Upper limit for delay"
            />
          </div>

          <div className="grid grid-cols-2 gap-4">
            <FormField label="Multiplier">
              {(id) => (
                <div className="space-y-2">
                  <input
                    id={id}
                    type="number"
                    className="input"
                    value={backoff.multiplier}
                    onChange={(e) =>
                      updateBackoff({
                        multiplier:
                          parseFloat(e.target.value) ||
                          PROVIDER_DEFAULTS.BACKOFF.MULTIPLIER,
                      })
                    }
                    min={1}
                    max={FORM_CONSTRAINTS.BACKOFF_MAX_MULTIPLIER}
                    step={0.1}
                  />
                  <p className="text-xs text-text-muted">
                    Delay multiplied by this each retry (≥1)
                  </p>
                </div>
              )}
            </FormField>
            <FormField label="Jitter">
              {(id) => (
                <div className="space-y-2">
                  <label
                    htmlFor={id}
                    className="flex items-center gap-3 p-3 rounded-lg border border-border bg-bg-secondary hover:bg-bg-tertiary transition-colors cursor-pointer"
                  >
                    <input
                      id={id}
                      type="checkbox"
                      checked={backoff.jitter}
                      onChange={(e) =>
                        updateBackoff({ jitter: e.target.checked })
                      }
                      className="w-4 h-4 rounded border-border text-primary focus:ring-primary"
                    />
                    <div>
                      <span className="text-sm font-medium text-text-primary">
                        Enable Jitter
                      </span>
                      <p className="text-xs text-text-muted">
                        Randomize delays to prevent thundering herd
                      </p>
                    </div>
                  </label>
                </div>
              )}
            </FormField>
          </div>

          <BackoffPreview backoff={backoff} maxRetries={maxRetries} />
        </div>
      )}
    </div>
  );
}
