import { useState } from "react";
import type { ProviderInput, AuthMode } from "../../api";
import { useAPICatalog } from "../../api/useApi";
import { AUTH_MODE_OPTIONS } from "../../config/constants";
import { hasProviderApiKey } from "../../lib/providerApiKey";
import { FormField } from "./FormField";
import { generateClientKey, type TrackedAPITypeEntry } from "./types";
import type { APITypeInput } from "../../api";

interface ApiTypesFieldProps {
  entries: TrackedAPITypeEntry[];
  onChange: (entries: TrackedAPITypeEntry[]) => void;
}

export function ApiTypesField({ entries, onChange }: ApiTypesFieldProps) {
  const { catalog, loading, error, refetch } = useAPICatalog();
  const [visibleOverrideKeys, setVisibleOverrideKeys] = useState<
    Record<string, boolean>
  >({});

  const updateEntry = (
    clientKey: string,
    field: keyof APITypeInput,
    v: string,
  ) => {
    const next = entries.map((entry) =>
      entry.clientKey === clientKey
        ? { ...entry, data: { ...entry.data, [field]: v } }
        : entry,
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
    onChange(entries.filter((e) => e.clientKey !== clientKey));
  };

  const addEntry = (apiType = "") => {
    // Auto-fill base_url from the last existing row for convenience
    const lastUrl =
      entries.length > 0 ? entries[entries.length - 1].data.base_url : "";
    onChange([
      ...entries,
      {
        clientKey: generateClientKey(),
        data: { api_type: apiType, base_url: lastUrl, api_key: "" },
      },
    ]);
  };

  const toggleQuickType = (type: string) => {
    const existing = entries.find((e) => e.data.api_type === type);
    if (existing) {
      removeEntry(existing.clientKey);
    } else {
      addEntry(type);
    }
  };

  const selectedTypes = new Set(entries.map((e) => e.data.api_type));

  return (
    <fieldset>
      <legend className="block text-sm font-medium text-text-secondary mb-1">
        API Types
      </legend>
      <p className="text-xs text-text-muted mb-3">
        Base URL is required. API key override is optional and falls back to the
        default API key above.
      </p>
      <div className="space-y-3">
        {entries.map((entry, index) => (
          <div
            key={entry.clientKey}
            className="rounded-xl border border-border/70 bg-bg-secondary/30 p-3"
          >
            <div className="space-y-2.5">
              <div className="grid grid-cols-[minmax(6.5rem,10rem)_minmax(0,1fr)_auto] gap-2 items-start">
                <input
                  type="text"
                  className="input"
                  value={entry.data.api_type}
                  onChange={(e) =>
                    updateEntry(entry.clientKey, "api_type", e.target.value)
                  }
                  placeholder="API type"
                  aria-label={`API type ${index + 1}`}
                />
                <input
                  type="url"
                  className="input"
                  value={entry.data.base_url}
                  onChange={(e) =>
                    updateEntry(entry.clientKey, "base_url", e.target.value)
                  }
                  placeholder="https://api.example.com"
                  aria-label={`Base URL for ${entry.data.api_type || "entry " + String(index + 1)}`}
                />
                <button
                  type="button"
                  onClick={() => removeEntry(entry.clientKey)}
                  className="h-10 px-3 rounded-lg border border-border text-text-muted hover:text-danger hover:border-danger/30 transition-colors shrink-0 cursor-pointer"
                  aria-label={`Remove ${entry.data.api_type || "entry"}`}
                >
                  Remove
                </button>
              </div>
              <div className="space-y-1">
                <p className="text-[11px] font-medium uppercase tracking-wide text-text-muted">
                  API Key Override
                </p>
                <div className="relative">
                  <input
                    type={
                      visibleOverrideKeys[entry.clientKey] ? "text" : "password"
                    }
                    className="input pr-10"
                    value={entry.data.api_key ?? ""}
                    onChange={(e) =>
                      updateEntry(entry.clientKey, "api_key", e.target.value)
                    }
                    autoComplete="new-password"
                    placeholder="Use default key"
                    aria-label={`API key override for ${entry.data.api_type || "entry " + String(index + 1)}`}
                  />
                  <button
                    type="button"
                    onClick={() =>
                      setVisibleOverrideKeys((prev) => ({
                        ...prev,
                        [entry.clientKey]: !prev[entry.clientKey],
                      }))
                    }
                    className="absolute right-2 top-1/2 -translate-y-1/2 text-text-muted hover:text-text-primary transition-colors p-1"
                    aria-label={`${visibleOverrideKeys[entry.clientKey] ? "Hide" : "Show"} API key override for ${entry.data.api_type || "entry " + String(index + 1)}`}
                    title={`${visibleOverrideKeys[entry.clientKey] ? "Hide" : "Show"} API key override for ${entry.data.api_type || "entry " + String(index + 1)}`}
                  >
                    {visibleOverrideKeys[entry.clientKey] ? (
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
              </div>
            </div>
            <p className="mt-2 text-xs text-text-muted">
              {hasProviderApiKey(entry.data.api_key)
                ? "Custom API key override is active for this API type."
                : "Using the provider default API key."}
            </p>
          </div>
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

// Keys of ProviderInput that are number types
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
  setFormData: React.Dispatch<React.SetStateAction<ProviderInput>>;
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
  formData: ProviderInput;
  setFormData: React.Dispatch<React.SetStateAction<ProviderInput>>;
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
    <FormField label="Default API Key">
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
            Used by all API types unless a row below provides its own override.
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
