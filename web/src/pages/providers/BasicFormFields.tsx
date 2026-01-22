import type { ProviderInput, AuthMode } from "../../api";
import { AUTH_MODE_OPTIONS, COMMON_API_TYPES } from "../../config/constants";
import { FormField } from "./FormField";

interface ApiTypesFieldProps {
  value: string;
  onChange: (value: string) => void;
}

export function ApiTypesField({ value, onChange }: ApiTypesFieldProps) {
  const toggleApiType = (type: string) => {
    const currentTypes = value
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);

    let newTypes: string[];
    if (currentTypes.includes(type)) {
      newTypes = currentTypes.filter((t) => t !== type);
    } else {
      newTypes = [...currentTypes, type];
    }
    onChange(newTypes.join(", "));
  };

  return (
    <FormField label="API Types">
      {(id) => (
        <>
          <input
            id={id}
            type="text"
            className="input"
            value={value}
            onChange={(e) => onChange(e.target.value)}
            placeholder="e.g., claude, codex, gemini"
          />
          <div className="flex flex-wrap gap-2 mt-2">
            {COMMON_API_TYPES.map((type) => {
              const isSelected = value
                .split(",")
                .map((s) => s.trim())
                .includes(type);
              return (
                <button
                  key={type}
                  type="button"
                  aria-pressed={isSelected}
                  onClick={() => toggleApiType(type)}
                  className={`px-2 py-1 text-xs rounded-full border transition-colors cursor-pointer ${
                    isSelected
                      ? "bg-primary text-white border-primary"
                      : "bg-bg-secondary text-text-secondary border-border hover:border-primary"
                  }`}
                >
                  {type}
                </button>
              );
            })}
          </div>
        </>
      )}
    </FormField>
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
  | "weight"
  | "priority"
  | "concurrency"
  | "max_retries";

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
    <FormField label="API Key">
      {(id) => (
        <div className="relative">
          <input
            id={id}
            type={showApiKey ? "text" : "password"}
            className="input pr-10"
            value={value}
            onChange={(e) => onChange(e.target.value)}
            required
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
