import { useId, useState, type FormEvent } from "react";
import { AlertTriangle, Sparkles } from "lucide-react";
import {
  findBuiltInAPIType,
  isValidCustomAPIType,
  type APICatalog,
} from "@/api/api-catalog";
import type { Provider } from "@/api/types";
import { BackoffPolicyEditor } from "@/components/provider-settings/BackoffPolicyEditor";
import type { InternalErrorRuleAction, RuleBackoffPolicy } from "../contracts";
import { MAX_RULE_RETRIES } from "../domain";
import {
  ENGLISH_INTERNAL_ERROR_PRESET_COPY,
  INTERNAL_ERROR_RULE_PRESETS,
  applyRulePreset,
  areRuleDraftsEqual,
  changeRuleDraftAction,
  type InternalErrorRulePreset,
  type InternalErrorRulePresetCopy,
  type InternalErrorRulePresetID,
  type RuleDraft,
  type RuleDraftErrors,
} from "../model";
import { ActionDialog } from "./ActionDialog";
import { RetryWaitEstimate } from "./RetryWaitEstimate";

function FieldError({ id, message }: { id: string; message?: string }) {
  if (!message) return null;
  return (
    <p id={id} role="alert" className="mt-1 text-xs text-danger">
      {message}
    </p>
  );
}

function draftActionForEstimate(
  draft: RuleDraft,
): InternalErrorRuleAction | null {
  if (draft.action.type === "passthrough") return draft.action;
  if (!/^(0|[1-9]\d*)$/u.test(draft.action.max_retries)) return null;
  return {
    type: draft.action.type,
    max_retries: Number(draft.action.max_retries),
    backoff: draft.action.backoff,
  };
}

function ScopeFields({
  draft,
  providers,
  providersLoading,
  providersError,
  busy,
  error,
  onChange,
}: {
  draft: RuleDraft;
  providers: readonly Provider[];
  providersLoading: boolean;
  providersError: Error | null;
  busy: boolean;
  error?: string;
  onChange: (draft: RuleDraft) => void;
}) {
  const errorID = useId();
  const selectedProviderID =
    draft.target.kind === "provider" ? draft.target.provider_id : "";
  const selectedProviderExists = providers.some(
    (provider) => provider.id === selectedProviderID,
  );

  return (
    <fieldset className="space-y-3">
      <legend className="text-sm font-semibold text-text-primary">Scope</legend>
      <div className="grid gap-3 sm:grid-cols-2">
        <label className="space-y-1 text-sm text-text-secondary">
          <span>Rule scope</span>
          <select
            className="input"
            value={draft.target.kind}
            disabled={providersLoading || busy}
            onChange={(event) =>
              onChange({
                ...draft,
                target:
                  event.target.value === "global"
                    ? { kind: "global" }
                    : {
                        kind: "provider",
                        provider_id: providers[0]?.id ?? "",
                      },
              })
            }
          >
            <option value="global">Global</option>
            <option value="provider">Provider</option>
          </select>
        </label>
        {draft.target.kind === "provider" && (
          <label className="space-y-1 text-sm text-text-secondary">
            <span>Provider</span>
            <select
              className="input"
              value={selectedProviderID}
              disabled={busy}
              aria-invalid={Boolean(error)}
              aria-describedby={error ? errorID : undefined}
              onChange={(event) =>
                onChange({
                  ...draft,
                  target: {
                    kind: "provider",
                    provider_id: event.target.value,
                  },
                })
              }
            >
              <option value="">Choose a provider</option>
              {!selectedProviderExists && selectedProviderID && (
                <option value={selectedProviderID}>
                  Deleted provider · {selectedProviderID}
                </option>
              )}
              {providers.map((provider) => (
                <option key={provider.id} value={provider.id}>
                  {provider.name}
                </option>
              ))}
            </select>
          </label>
        )}
      </div>
      {providersError && (
        <p role="status" className="text-xs text-warning-dark">
          Provider choices may be stale: {providersError.message}
        </p>
      )}
      <FieldError id={errorID} message={error} />
    </fieldset>
  );
}

function PresetPicker({
  draft,
  baseline,
  catalog,
  copy,
  disabled,
  onApply,
}: {
  draft: RuleDraft;
  baseline: RuleDraft;
  catalog: APICatalog;
  copy: Readonly<
    Record<InternalErrorRulePresetID, InternalErrorRulePresetCopy>
  >;
  disabled: boolean;
  onApply: (draft: RuleDraft) => void;
}) {
  const [pendingPreset, setPendingPreset] =
    useState<InternalErrorRulePreset | null>(null);
  const headingID = useId();

  function apply(preset: InternalErrorRulePreset) {
    if (!areRuleDraftsEqual(draft, baseline)) {
      setPendingPreset(preset);
      return;
    }
    onApply(applyRulePreset(draft, preset));
  }

  function confirmApply() {
    if (pendingPreset) onApply(applyRulePreset(draft, pendingPreset));
    setPendingPreset(null);
  }

  return (
    <section aria-labelledby={headingID} className="space-y-3">
      <div>
        <h3
          id={headingID}
          className="flex items-center gap-2 text-sm font-semibold text-text-primary"
        >
          <Sparkles className="h-4 w-4 text-primary" aria-hidden="true" />
          Draft presets
        </h3>
        <p className="mt-1 text-xs text-text-muted">
          Presets only change this local draft. Review and save explicitly.
        </p>
      </div>
      <div className="grid gap-2 sm:grid-cols-2">
        {INTERNAL_ERROR_RULE_PRESETS.map((preset) => {
          const entry = findBuiltInAPIType(catalog, preset.api_type);
          const unavailable = !entry?.semantic_error_supported;
          return (
            <button
              key={preset.id}
              type="button"
              disabled={disabled || unavailable}
              onClick={() => apply(preset)}
              className="rounded-xl border border-border bg-bg-secondary p-3 text-left transition-colors hover:border-primary disabled:cursor-not-allowed disabled:opacity-50"
            >
              <span className="block text-sm font-semibold text-text-primary">
                {copy[preset.id].name}
              </span>
              <span className="mt-1 block text-xs text-text-secondary">
                {unavailable
                  ? "Unavailable in the current server catalog."
                  : copy[preset.id].description}
              </span>
            </button>
          );
        })}
      </div>
      <ActionDialog
        open={pendingPreset !== null}
        title="Replace unsaved draft fields?"
        description="This preset will replace scope, API type, keywords, matching mode, action, and retry timing. Name and enabled state are preserved."
        confirmLabel="Replace draft"
        cancelLabel="Keep editing"
        onConfirm={confirmApply}
        onCancel={() => setPendingPreset(null)}
      />
    </section>
  );
}

function RuleIdentityFields({
  draft,
  catalog,
  errors,
  busy,
  onChange,
}: {
  draft: RuleDraft;
  catalog: APICatalog;
  errors: RuleDraftErrors;
  busy: boolean;
  onChange: (draft: RuleDraft) => void;
}) {
  const apiTypeListID = useId();
  const nameInputID = useId();
  const apiTypeInputID = useId();
  const nameErrorID = useId();
  const apiTypeErrorID = useId();
  const apiType = draft.api_type?.trim() ?? "";
  const builtIn = apiType ? findBuiltInAPIType(catalog, apiType) : undefined;
  const custom = apiType ? isValidCustomAPIType(catalog, apiType) : false;
  const unsupported = custom || (builtIn && !builtIn.semantic_error_supported);

  return (
    <>
      <div className="grid gap-4 sm:grid-cols-2">
        <div className="space-y-1 text-sm text-text-secondary">
          <label htmlFor={nameInputID}>Rule name</label>
          <input
            id={nameInputID}
            className="input"
            value={draft.name}
            disabled={busy}
            aria-invalid={Boolean(errors.name)}
            aria-describedby={errors.name ? nameErrorID : undefined}
            onChange={(event) =>
              onChange({ ...draft, name: event.target.value })
            }
          />
          <FieldError id={nameErrorID} message={errors.name} />
        </div>
        <div className="space-y-1 text-sm text-text-secondary">
          <label htmlFor={apiTypeInputID}>API type</label>
          <input
            id={apiTypeInputID}
            className="input"
            list={apiTypeListID}
            value={draft.api_type ?? ""}
            disabled={busy}
            placeholder="All supported built-in API types"
            aria-invalid={Boolean(errors.api_type)}
            aria-describedby={errors.api_type ? apiTypeErrorID : undefined}
            onChange={(event) =>
              onChange({ ...draft, api_type: event.target.value || null })
            }
          />
          <datalist id={apiTypeListID}>
            {catalog.api_types.map((entry) => (
              <option key={entry.api_type} value={entry.api_type}>
                {entry.label}
                {entry.semantic_error_supported ? "" : " · unsupported"}
              </option>
            ))}
          </datalist>
          <FieldError id={apiTypeErrorID} message={errors.api_type} />
        </div>
      </div>
      {unsupported && (
        <div
          role="status"
          className="flex gap-2 rounded-xl border border-warning/30 bg-warning-light/30 p-3 text-sm text-warning-dark"
        >
          <AlertTriangle
            className="mt-0.5 h-4 w-4 shrink-0"
            aria-hidden="true"
          />
          <span>
            {custom ? "Custom API types" : builtIn?.label} do not support
            structured error detection. This rule can only be saved while
            disabled.
          </span>
        </div>
      )}
    </>
  );
}

function KeywordMatchFields({
  draft,
  error,
  busy,
  onChange,
}: {
  draft: RuleDraft;
  error?: string;
  busy: boolean;
  onChange: (draft: RuleDraft) => void;
}) {
  const keywordErrorID = useId();
  return (
    <div className="grid gap-4 sm:grid-cols-2">
      <label className="space-y-1 text-sm text-text-secondary">
        <span>Keywords · one per line</span>
        <textarea
          className="input min-h-32 font-mono"
          value={draft.keywords_text}
          disabled={busy}
          aria-invalid={Boolean(error)}
          aria-describedby={error ? keywordErrorID : undefined}
          onChange={(event) =>
            onChange({ ...draft, keywords_text: event.target.value })
          }
        />
        <FieldError id={keywordErrorID} message={error} />
      </label>
      <fieldset className="space-y-2">
        <legend className="text-sm text-text-secondary">Match mode</legend>
        {(["any", "all"] as const).map((modeValue) => (
          <label
            key={modeValue}
            className="flex cursor-pointer items-start gap-3 rounded-xl border border-border p-3"
          >
            <input
              type="radio"
              name="match-mode"
              value={modeValue}
              checked={draft.match_mode === modeValue}
              disabled={busy}
              onChange={() => onChange({ ...draft, match_mode: modeValue })}
            />
            <span className="text-sm text-text-primary">
              <strong className="block capitalize">{modeValue}</strong>
              <span className="text-xs text-text-muted">
                {modeValue === "any"
                  ? "At least one keyword matches one extracted field."
                  : "Every keyword matches at least one extracted field."}
              </span>
            </span>
          </label>
        ))}
      </fieldset>
    </div>
  );
}

function RuleActionFields({
  draft,
  errors,
  busy,
  globalMaxAttempts,
  configUnavailable,
  onChange,
}: {
  draft: RuleDraft;
  errors: RuleDraftErrors;
  busy: boolean;
  globalMaxAttempts: number | null;
  configUnavailable: boolean;
  onChange: (draft: RuleDraft) => void;
}) {
  const [backoffExpanded, setBackoffExpanded] = useState(false);
  const retryErrorID = useId();
  const retryAction = draft.action.type === "passthrough" ? null : draft.action;

  function updateRetryBackoff(backoff: Partial<RuleBackoffPolicy>) {
    if (!retryAction) return;
    onChange({
      ...draft,
      action: {
        ...retryAction,
        backoff: {
          initial_delay:
            backoff.initial_delay ?? retryAction.backoff.initial_delay,
          max_delay: backoff.max_delay ?? retryAction.backoff.max_delay,
          multiplier: backoff.multiplier ?? retryAction.backoff.multiplier,
          jitter: backoff.jitter ?? retryAction.backoff.jitter,
        },
      },
    });
  }

  return (
    <div className="space-y-4">
      <div className="grid gap-4 sm:grid-cols-2">
        <label className="space-y-1 text-sm text-text-secondary">
          <span>Action</span>
          <select
            className="input"
            value={draft.action.type}
            disabled={busy}
            onChange={(event) => {
              const type = event.target.value as RuleDraft["action"]["type"];
              onChange({
                ...draft,
                action: changeRuleDraftAction(draft.action, type),
              });
            }}
          >
            <option value="passthrough">Pass through</option>
            <option value="retry_only">Retry same provider only</option>
            <option value="retry_then_switch">
              Retry, then switch provider
            </option>
          </select>
        </label>
        {retryAction && (
          <label className="space-y-1 text-sm text-text-secondary">
            <span>Same-provider retries</span>
            <input
              type="number"
              min={0}
              max={MAX_RULE_RETRIES}
              step={1}
              className="input"
              value={retryAction.max_retries}
              disabled={busy}
              aria-invalid={Boolean(errors.max_retries)}
              aria-describedby={errors.max_retries ? retryErrorID : undefined}
              onChange={(event) =>
                onChange({
                  ...draft,
                  action: { ...retryAction, max_retries: event.target.value },
                })
              }
            />
            <FieldError id={retryErrorID} message={errors.max_retries} />
          </label>
        )}
      </div>
      {retryAction && (
        <div className="space-y-3">
          <BackoffPolicyEditor
            backoff={retryAction.backoff}
            maxRetries={Number(retryAction.max_retries)}
            expanded={backoffExpanded}
            disabled={busy}
            onToggle={() => setBackoffExpanded((expanded) => !expanded)}
            onChange={updateRetryBackoff}
          />
          <FieldError id={`${retryErrorID}-backoff`} message={errors.backoff} />
        </div>
      )}
      <RetryWaitEstimate
        action={draftActionForEstimate(draft)}
        globalMaxAttempts={globalMaxAttempts}
        configUnavailable={configUnavailable}
      />
    </div>
  );
}

function submitLabel(mode: "create" | "edit", busy: boolean): string {
  if (busy) return "Saving…";
  return mode === "create" ? "Create rule" : "Save rule";
}

export interface RuleEditorProps {
  readonly mode: "create" | "edit";
  readonly draft: RuleDraft;
  readonly baseline: RuleDraft;
  readonly catalog: APICatalog;
  readonly providers: readonly Provider[];
  readonly providersLoading: boolean;
  readonly providersError: Error | null;
  readonly errors: RuleDraftErrors;
  readonly submitError: string | null;
  readonly busy: boolean;
  readonly globalMaxAttempts: number | null;
  readonly configUnavailable: boolean;
  readonly presetCopy?: Readonly<
    Record<InternalErrorRulePresetID, InternalErrorRulePresetCopy>
  >;
  readonly onChange: (draft: RuleDraft) => void;
  readonly onSubmit: () => void;
  readonly onCancel: () => void;
}

export function RuleEditor({
  mode,
  draft,
  baseline,
  catalog,
  providers,
  providersLoading,
  providersError,
  errors,
  submitError,
  busy,
  globalMaxAttempts,
  configUnavailable,
  presetCopy = ENGLISH_INTERNAL_ERROR_PRESET_COPY,
  onChange,
  onSubmit,
  onCancel,
}: RuleEditorProps) {
  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    onSubmit();
  }

  return (
    <section className="rounded-2xl border border-border bg-white p-5 shadow-sm">
      <div className="mb-5 flex items-start justify-between gap-4">
        <div>
          <h2 className="text-lg font-semibold text-text-primary">
            {mode === "create"
              ? "Create detection rule"
              : "Edit detection rule"}
          </h2>
          <p className="mt-1 text-sm text-text-secondary">
            Only structured protocol error fields are matched. Ordinary output
            is never scanned.
          </p>
        </div>
        <span className="rounded-full bg-bg-secondary px-2.5 py-1 text-xs text-text-muted">
          Unsaved draft
        </span>
      </div>

      <form onSubmit={handleSubmit} aria-busy={busy} className="space-y-6">
        <PresetPicker
          draft={draft}
          baseline={baseline}
          catalog={catalog}
          copy={presetCopy}
          disabled={busy}
          onApply={onChange}
        />

        <RuleIdentityFields
          draft={draft}
          catalog={catalog}
          errors={errors}
          busy={busy}
          onChange={onChange}
        />

        <ScopeFields
          draft={draft}
          providers={providers}
          providersLoading={providersLoading}
          providersError={providersError}
          busy={busy}
          error={errors.target}
          onChange={onChange}
        />

        <KeywordMatchFields
          draft={draft}
          error={errors.keywords}
          busy={busy}
          onChange={onChange}
        />

        <RuleActionFields
          draft={draft}
          errors={errors}
          busy={busy}
          globalMaxAttempts={globalMaxAttempts}
          configUnavailable={configUnavailable}
          onChange={onChange}
        />

        <label className="flex cursor-pointer items-start gap-3 rounded-xl border border-border bg-bg-secondary p-3">
          <input
            type="checkbox"
            checked={draft.enabled}
            disabled={busy}
            onChange={(event) =>
              onChange({ ...draft, enabled: event.target.checked })
            }
          />
          <span className="text-sm text-text-primary">
            <strong className="block">Enabled</strong>
            <span className="text-xs text-text-muted">
              Disabled rules remain ordered and editable but never affect
              traffic.
            </span>
          </span>
        </label>

        {submitError && (
          <div
            role="alert"
            className="rounded-xl bg-danger/5 p-3 text-sm text-danger"
          >
            {submitError}
          </div>
        )}

        <div className="flex justify-end gap-3 border-t border-border pt-4">
          <button
            type="button"
            disabled={busy}
            onClick={onCancel}
            className="btn btn-secondary"
          >
            Cancel
          </button>
          <button type="submit" disabled={busy} className="btn btn-primary">
            {submitLabel(mode, busy)}
          </button>
        </div>
      </form>
    </section>
  );
}
