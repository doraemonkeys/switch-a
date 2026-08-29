import { useState } from "react";
import type { AuthMode, CredentialSession } from "../../api";
import { useAPICatalog } from "../../api/useApi";
import { AUTH_MODE_OPTIONS } from "../../config/constants";
import { hasProviderApiKey } from "../../lib/providerApiKey";
import { CopyButton } from "../../components";
import { FormField } from "./FormField";
import {
  generateClientKey,
  type ProviderAPITypeDraft,
  type ProviderCredentialMode,
  type ProviderFormData,
} from "./types";

interface ApiTypesFieldProps {
  entries: ProviderAPITypeDraft[];
  onChange: (entries: ProviderAPITypeDraft[]) => void;
  credentialSessions?: CredentialSession[];
  credentialMode?: ProviderCredentialMode;
}

interface ApiTypeRouteRowProps {
  entry: ProviderAPITypeDraft;
  index: number;
  credentialSessions: CredentialSession[];
  credentialMode: ProviderCredentialMode;
  allowApiKeyDrafts: boolean;
  overrideVisible: boolean;
  onUpdate: (
    clientKey: string,
    field: keyof ProviderAPITypeDraft,
    value: string,
  ) => void;
  onRemove: (clientKey: string) => void;
  onToggleOverrideVisibility: (clientKey: string) => void;
}

function describeCredentialBinding(entry: ProviderAPITypeDraft): string {
  if (hasProviderApiKey(entry.api_key)) {
    return "A new route-specific credential session will be created on save.";
  }
  if (entry.credential_session_id) {
    return `Bound to credential session ${entry.credential_session_id}.`;
  }
  return "Choose a session or provide a new API key before saving.";
}

function CredentialVisibilityIcon({ visible }: { visible: boolean }) {
  if (visible) {
    return (
      <svg
        className="w-5 h-5"
        fill="none"
        stroke="currentColor"
        viewBox="0 0 24 24"
      >
        <path
          strokeLinecap="round"
          strokeLinejoin="round"
          strokeWidth={2}
          d="M13.875 18.825A10.05 10.05 0 0112 19c-4.478 0-8.268-2.943-9.543-7a9.97 9.97 0 011.563-3.029m5.858.908a3 3 0 114.243 4.243M9.878 9.878l4.242 4.242M9.88 9.88l-3.29-3.29m7.532 7.532l3.29 3.29M3 3l3.59 3.59m0 0A9.953 9.953 0 0112 5c4.478 0 8.268 2.943 9.543 7a10.025 10.025 0 01-4.132 5.411m0 0L21 21"
        />
      </svg>
    );
  }
  return (
    <svg
      className="w-5 h-5"
      fill="none"
      stroke="currentColor"
      viewBox="0 0 24 24"
    >
      <path
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth={2}
        d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"
      />
      <path
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth={2}
        d="M2.458 12C3.732 7.943 7.523 5 12 5c4.478 0 8.268 2.943 9.542 7-1.274 4.057-5.064 7-9.542 7-4.477 0-8.268-2.943-9.542-7z"
      />
    </svg>
  );
}

function CurrentApiKeyField({
  apiKey,
  entryLabel,
}: {
  apiKey: string;
  entryLabel: string;
}) {
  const [visible, setVisible] = useState(false);
  const visibilityAction = visible ? "Hide" : "Show";

  return (
    <div className="space-y-1">
      <p className="text-[11px] font-medium uppercase tracking-wide text-text-muted">
        Current API Key
      </p>
      <div className="flex items-center gap-2">
        <div className="relative min-w-0 flex-1">
          <input
            type={visible ? "text" : "password"}
            className="input pr-10 font-mono"
            value={apiKey}
            readOnly
            aria-label={`Current API key for ${entryLabel}`}
          />
          <button
            type="button"
            onClick={() => setVisible((current) => !current)}
            className="absolute right-2 top-1/2 -translate-y-1/2 text-text-muted hover:text-text-primary transition-colors p-1"
            aria-label={`${visibilityAction} current API key for ${entryLabel}`}
            title={`${visibilityAction} current API key`}
          >
            <CredentialVisibilityIcon visible={visible} />
          </button>
        </div>
        <CopyButton
          text={apiKey}
          className="h-10 shrink-0 rounded-lg border border-border px-3 hover:border-primary"
        />
      </div>
    </div>
  );
}

function ApiTypeRouteRow({
  entry,
  index,
  credentialSessions,
  credentialMode,
  allowApiKeyDrafts,
  overrideVisible,
  onUpdate,
  onRemove,
  onToggleOverrideVisibility,
}: ApiTypeRouteRowProps) {
  const entryLabel = entry.api_type || `entry ${index + 1}`;
  const availableSessions = credentialSessions.filter(
    (session) =>
      session.kind === "api_key" ||
      (credentialMode === "mixed" && entry.api_type === "codex"),
  );
  const selectedSession = credentialSessions.find(
    (session) => session.id === entry.credential_session_id,
  );
  const currentApiKey =
    selectedSession?.kind === "api_key"
      ? selectedSession.secret_data
      : undefined;
  const visibilityAction = overrideVisible ? "Hide" : "Show";

  return (
    <div className="rounded-xl border border-border/70 bg-bg-secondary/30 p-3">
      <div className="space-y-2.5">
        <div className="grid grid-cols-[minmax(6.5rem,10rem)_minmax(0,1fr)_auto] gap-2 items-start">
          <input
            type="text"
            className="input"
            value={entry.api_type}
            onChange={(event) =>
              onUpdate(entry.client_key, "api_type", event.target.value)
            }
            placeholder="API type"
            aria-label={`API type ${index + 1}`}
          />
          <input
            type="url"
            className="input"
            value={entry.base_url}
            onChange={(event) =>
              onUpdate(entry.client_key, "base_url", event.target.value)
            }
            placeholder="https://api.example.com"
            aria-label={`Base URL for ${entryLabel}`}
          />
          <button
            type="button"
            onClick={() => onRemove(entry.client_key)}
            className="h-10 px-3 rounded-lg border border-border text-text-muted hover:text-danger hover:border-danger/30 transition-colors shrink-0 cursor-pointer"
            aria-label={`Remove ${entry.api_type || "entry"}`}
          >
            Remove
          </button>
        </div>
        <div className="space-y-1">
          <p className="text-[11px] font-medium uppercase tracking-wide text-text-muted">
            Credential Session
          </p>
          <select
            className="input"
            value={entry.credential_session_id}
            onChange={(event) =>
              onUpdate(
                entry.client_key,
                "credential_session_id",
                event.target.value,
              )
            }
            aria-label={`Credential session for ${entryLabel}`}
          >
            <option value="">Create or select a credential</option>
            {availableSessions.map((session) => (
              <option key={session.id} value={session.id}>
                {session.kind === "chatgpt" ? "GPT Login" : "API Key"} -{" "}
                {session.auth_state.email || session.id}
              </option>
            ))}
          </select>
        </div>
        {currentApiKey && (
          <CurrentApiKeyField apiKey={currentApiKey} entryLabel={entryLabel} />
        )}
        {allowApiKeyDrafts && (
          <div className="space-y-1">
            <p className="text-[11px] font-medium uppercase tracking-wide text-text-muted">
              New API Key for This Route
            </p>
            <div className="relative">
              <input
                type={overrideVisible ? "text" : "password"}
                className="input pr-10"
                value={entry.api_key ?? ""}
                onChange={(event) =>
                  onUpdate(entry.client_key, "api_key", event.target.value)
                }
                autoComplete="new-password"
                placeholder="Keep selected session or use default key"
                aria-label={`API key override for ${entryLabel}`}
              />
              <button
                type="button"
                onClick={() => onToggleOverrideVisibility(entry.client_key)}
                className="absolute right-2 top-1/2 -translate-y-1/2 text-text-muted hover:text-text-primary transition-colors p-1"
                aria-label={`${visibilityAction} API key override for ${entryLabel}`}
                title={`${visibilityAction} API key override for ${entryLabel}`}
              >
                <CredentialVisibilityIcon visible={overrideVisible} />
              </button>
            </div>
          </div>
        )}
      </div>
      <p className="mt-2 text-xs text-text-muted">
        {describeCredentialBinding(entry)}
      </p>
    </div>
  );
}

export function ApiTypesField({
  entries,
  onChange,
  credentialSessions = [],
  credentialMode = "api_key",
}: ApiTypesFieldProps) {
  const allowApiKeyDrafts = credentialMode !== "chatgpt";
  const { catalog, loading, error, refetch } = useAPICatalog();
  const [visibleOverrideKeys, setVisibleOverrideKeys] = useState<
    Record<string, boolean>
  >({});

  const updateEntry = (
    clientKey: string,
    field: keyof ProviderAPITypeDraft,
    v: string,
  ) => {
    const next = entries.map((entry) =>
      entry.client_key === clientKey ? { ...entry, [field]: v } : entry,
    );
    onChange(next);
  };

  const removeEntry = (clientKey: string) => {
    setVisibleOverrideKeys((prev) => {
      if (!(clientKey in prev)) {
        return prev;
      }

      const next = { ...prev };
      delete next[clientKey];
      return next;
    });
    onChange(entries.filter((entry) => entry.client_key !== clientKey));
  };

  const addEntry = (apiType = "") => {
    // Auto-fill base_url from the last existing row for convenience
    const lastUrl =
      entries.length > 0 ? entries[entries.length - 1].base_url : "";
    onChange([
      ...entries,
      {
        client_key: generateClientKey(),
        api_type: apiType,
        base_url: lastUrl,
        credential_session_id: "",
        api_key: "",
      },
    ]);
  };

  const toggleQuickType = (type: string) => {
    const existing = entries.find((entry) => entry.api_type === type);
    if (existing) {
      removeEntry(existing.client_key);
    } else {
      addEntry(type);
    }
  };

  const selectedTypes = new Set(entries.map((entry) => entry.api_type));

  return (
    <fieldset>
      <legend className="block text-sm font-medium text-text-secondary mb-1">
        API Types
      </legend>
      <p className="text-xs text-text-muted mb-3">
        Every route binds an explicit credential session. Select an existing
        session, or enter a replacement API key to create one when you save.
      </p>
      <div className="space-y-3">
        {entries.map((entry, index) => (
          <ApiTypeRouteRow
            key={entry.client_key}
            entry={entry}
            index={index}
            credentialSessions={credentialSessions}
            credentialMode={credentialMode}
            allowApiKeyDrafts={allowApiKeyDrafts}
            overrideVisible={Boolean(visibleOverrideKeys[entry.client_key])}
            onUpdate={updateEntry}
            onRemove={removeEntry}
            onToggleOverrideVisibility={(clientKey) =>
              setVisibleOverrideKeys((previous) => ({
                ...previous,
                [clientKey]: !previous[clientKey],
              }))
            }
          />
        ))}
      </div>
      <div className="flex flex-wrap items-center gap-2 mt-2">
        {catalog?.api_types.map((entry) => {
          const selected = selectedTypes.has(entry.api_type);
          return (
            <button
              key={entry.api_type}
              type="button"
              aria-pressed={selected}
              onClick={() => toggleQuickType(entry.api_type)}
              title={entry.description}
              className={`px-2 py-1 text-xs rounded-full border transition-colors cursor-pointer ${
                selected
                  ? "bg-primary text-white border-primary"
                  : "bg-bg-secondary text-text-secondary border-border hover:border-primary"
              }`}
            >
              {entry.api_type}
            </button>
          );
        })}
        <button
          type="button"
          onClick={() => addEntry()}
          className="px-2 py-1 text-xs rounded-full border border-dashed border-border text-text-secondary hover:border-primary hover:text-primary transition-colors cursor-pointer"
        >
          + Custom
        </button>
        {loading && (
          <span className="text-xs text-text-muted" role="status">
            Loading built-in API types...
          </span>
        )}
        {error && (
          <button
            type="button"
            onClick={() => void refetch()}
            className="text-xs text-danger hover:underline cursor-pointer"
            title={error.message}
          >
            Retry API type catalog
          </button>
        )}
      </div>
    </fieldset>
  );
}

interface GroupSelectFieldProps {
  value: string | null;
  onChange: (value: string | null) => void;
  groups: Array<{ id: string; name: string }>;
}

export function GroupSelectField({
  value,
  onChange,
  groups,
}: GroupSelectFieldProps) {
  return (
    <FormField label="Group">
      {(id) => (
        <select
          id={id}
          className="input"
          value={value ?? ""}
          onChange={(e) => onChange(e.target.value || null)}
        >
          <option value="">No Group</option>
          {groups.map((group) => (
            <option key={group.id} value={group.id}>
              {group.name}
            </option>
          ))}
        </select>
      )}
    </FormField>
  );
}

interface AuthModeFieldProps {
  value: AuthMode;
  onChange: (value: AuthMode) => void;
}

export function AuthModeField({ value, onChange }: AuthModeFieldProps) {
  return (
    <FormField label="Auth Mode">
      {(id) => (
        <select
          id={id}
          className="input"
          value={value}
          onChange={(e) => onChange(e.target.value as AuthMode)}
        >
          {AUTH_MODE_OPTIONS.map((mode) => (
            <option key={mode.value} value={mode.value}>
              {mode.label}
            </option>
          ))}
        </select>
      )}
    </FormField>
  );
}

// Keys of ProviderFormData that are number types
type ProviderInputNumberKey =
  "weight" | "priority" | "concurrency" | "max_retries";

interface NumberFieldConfig {
  key: ProviderInputNumberKey;
  label: string;
  min: number;
  defaultValue: number;
  hint?: string;
}

interface SingleNumberFieldProps {
  id: string;
  value: number;
  min: number;
  defaultValue: number;
  hint?: string;
  fieldKey: ProviderInputNumberKey;
  setFormData: React.Dispatch<React.SetStateAction<ProviderFormData>>;
}

function SingleNumberInput({
  id,
  value,
  min,
  defaultValue,
  hint,
  fieldKey,
  setFormData,
}: SingleNumberFieldProps) {
  const handleChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const newValue = parseInt(e.target.value) || defaultValue;
    setFormData((prev) => ({ ...prev, [fieldKey]: newValue }));
  };

  return (
    <>
      <input
        id={id}
        type="number"
        className="input"
        value={value}
        onChange={handleChange}
        min={min}
      />
      {hint && <p className="text-xs text-text-muted mt-1">{hint}</p>}
    </>
  );
}

interface NumberFieldRowProps {
  formData: ProviderFormData;
  setFormData: React.Dispatch<React.SetStateAction<ProviderFormData>>;
  fields: NumberFieldConfig[];
}

export function NumberFieldRow({
  formData,
  setFormData,
  fields,
}: NumberFieldRowProps) {
  return (
    <div className="grid grid-cols-2 gap-4">
      {fields.map(({ key, label, min, defaultValue, hint }) => {
        // key is constrained to ProviderInputNumberKey, so formData[key] is number | undefined
        const value = (formData[key] as number | undefined) ?? defaultValue;
        return (
          <FormField key={key} label={label}>
            {(id) => (
              <SingleNumberInput
                id={id}
                value={value}
                min={min}
                defaultValue={defaultValue}
                hint={hint}
                fieldKey={key}
                setFormData={setFormData}
              />
            )}
          </FormField>
        );
      })}
    </div>
  );
}

interface ApiKeyFieldProps {
  value: string;
  onChange: (value: string) => void;
  showApiKey: boolean;
  onToggleVisibility: () => void;
}

export function ApiKeyField({
  value,
  onChange,
  showApiKey,
  onToggleVisibility,
}: ApiKeyFieldProps) {
  return (
    <FormField label="New Shared API Key">
      {(id) => (
        <div className="space-y-1.5">
          <div className="relative">
            <input
              id={id}
              type={showApiKey ? "text" : "password"}
              className="input pr-10"
              value={value}
              onChange={(e) => onChange(e.target.value)}
              autoComplete="new-password"
              placeholder="sk-..."
            />
            <button
              type="button"
              onClick={onToggleVisibility}
              className="absolute right-2 top-1/2 -translate-y-1/2 text-text-muted hover:text-text-primary transition-colors p-1"
              title={showApiKey ? "Hide API Key" : "Show API Key"}
            >
              {showApiKey ? (
                <svg
                  className="w-5 h-5"
                  fill="none"
                  stroke="currentColor"
                  viewBox="0 0 24 24"
                >
                  <path
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    strokeWidth={2}
                    d="M13.875 18.825A10.05 10.05 0 0112 19c-4.478 0-8.268-2.943-9.543-7a9.97 9.97 0 011.563-3.029m5.858.908a3 3 0 114.243 4.243M9.878 9.878l4.242 4.242M9.88 9.88l-3.29-3.29m7.532 7.532l3.29 3.29M3 3l3.59 3.59m0 0A9.953 9.953 0 0112 5c4.478 0 8.268 2.943 9.543 7a10.025 10.025 0 01-4.132 5.411m0 0L21 21"
                  />
                </svg>
              ) : (
                <svg
                  className="w-5 h-5"
                  fill="none"
                  stroke="currentColor"
                  viewBox="0 0 24 24"
                >
                  <path
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    strokeWidth={2}
                    d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"
                  />
                  <path
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    strokeWidth={2}
                    d="M2.458 12C3.732 7.943 7.523 5 12 5c4.478 0 8.268 2.943 9.542 7-1.274 4.057-5.064 7-9.542 7-4.477 0-8.268-2.943-9.542-7z"
                  />
                </svg>
              )}
            </button>
          </div>
          <p className="text-xs text-text-muted">
            Saving creates one reusable credential session for routes without a
            selected session or new route-specific key. Existing route bindings
            are always preserved.
          </p>
        </div>
      )}
    </FormField>
  );
}

interface EnabledCheckboxProps {
  checked: boolean;
  onChange: (checked: boolean) => void;
}

export function EnabledCheckbox({ checked, onChange }: EnabledCheckboxProps) {
  return (
    <div className="flex items-center gap-2">
      <input
        type="checkbox"
        id="enabled"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
        className="w-4 h-4 rounded border-border text-primary focus:ring-primary"
      />
      <label
        htmlFor="enabled"
        className="text-sm font-medium text-text-secondary cursor-pointer"
      >
        Enable provider immediately
      </label>
    </div>
  );
}

interface FormActionsProps {
  isEditMode: boolean;
  submitting: boolean;
  onCancel: () => void;
}

export function FormActions({
  isEditMode,
  submitting,
  onCancel,
}: FormActionsProps) {
  return (
    <div className="flex justify-end gap-3 pt-4">
      <button
        type="button"
        onClick={onCancel}
        className="btn btn-secondary"
        disabled={submitting}
      >
        Cancel
      </button>
      <button type="submit" className="btn btn-primary" disabled={submitting}>
        {submitting ? (
          <>
            <span className="animate-spin">⏳</span>
            {isEditMode ? "Saving..." : "Creating..."}
          </>
        ) : (
          <>
            <span>{isEditMode ? "💾" : "➕"}</span>
            {isEditMode ? "Save Changes" : "Add Provider"}
          </>
        )}
      </button>
    </div>
  );
}
