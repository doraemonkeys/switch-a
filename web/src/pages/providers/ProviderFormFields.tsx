import type { ProviderInput, AuthMode, FailoverScope } from "../../api";
import {
  AUTH_MODE_OPTIONS,
  COMMON_API_TYPES,
  COMMON_VENDORS,
  FAILOVER_SCOPE_OPTIONS,
  FAILOVER_SCOPES,
  VENDOR_WILDCARD,
} from "../../config/constants";

interface FormFieldProps {
  label: string;
  children: React.ReactNode;
}

export function FormField({ label, children }: FormFieldProps) {
  return (
    <div>
      <label className="block text-sm font-medium text-text-secondary mb-1">
        {label}
      </label>
      {children}
    </div>
  );
}

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
      <input
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
      <select
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
      <select
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
    </FormField>
  );
}

// Keys of ProviderInput that are number types
type ProviderInputNumberKey =
  | "weight"
  | "priority"
  | "concurrency"
  | "max_retries";

interface NumberFieldRowProps {
  formData: ProviderInput;
  setFormData: React.Dispatch<React.SetStateAction<ProviderInput>>;
  fields: Array<{
    key: ProviderInputNumberKey;
    label: string;
    min: number;
    defaultValue: number;
    hint?: string;
  }>;
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
            <input
              type="number"
              className="input"
              value={value}
              onChange={(e) =>
                setFormData((prev) => ({
                  ...prev,
                  [key]: parseInt(e.target.value) || defaultValue,
                }))
              }
              min={min}
            />
            {hint && <p className="text-xs text-text-muted mt-1">{hint}</p>}
          </FormField>
        );
      })}
    </div>
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

// ============================================================================
// Vendor Failover Isolation Fields
// ============================================================================

interface VendorFieldProps {
  value: string;
  onChange: (value: string) => void;
  failoverScope?: FailoverScope;
  acceptFailover?: FailoverScope;
}

export function VendorField({
  value,
  onChange,
  failoverScope,
  acceptFailover,
}: VendorFieldProps) {
  const isWildcard = value === VENDOR_WILDCARD;
  const hasVendorScope =
    failoverScope === FAILOVER_SCOPES.VENDOR ||
    acceptFailover === FAILOVER_SCOPES.VENDOR;
  const showWarning = hasVendorScope && !value;

  return (
    <FormField label="Vendor">
      <div className="space-y-2">
        <div className="relative">
          <input
            type="text"
            className={`input ${showWarning ? "border-warning focus:border-warning" : ""}`}
            value={value}
            onChange={(e) => onChange(e.target.value)}
            placeholder="e.g., yescode, openrouter, or * for wildcard"
          />
          {isWildcard && (
            <span className="absolute right-2 top-1/2 -translate-y-1/2 text-xs bg-info-light text-info-dark px-1.5 py-0.5 rounded">
              Wildcard
            </span>
          )}
        </div>

        {/* Quick select buttons */}
        <div className="flex flex-wrap gap-1.5">
          {COMMON_VENDORS.map((vendor) => (
            <button
              key={vendor}
              type="button"
              onClick={() => onChange(vendor)}
              className={`px-2 py-0.5 text-xs rounded-full border transition-colors cursor-pointer ${
                value === vendor
                  ? "bg-primary text-white border-primary"
                  : "bg-bg-secondary text-text-secondary border-border hover:border-primary"
              }`}
            >
              {vendor}
            </button>
          ))}
          <button
            type="button"
            onClick={() => onChange(VENDOR_WILDCARD)}
            className={`px-2 py-0.5 text-xs rounded-full border transition-colors cursor-pointer ${
              isWildcard
                ? "bg-info text-white border-info"
                : "bg-bg-secondary text-text-secondary border-border hover:border-info"
            }`}
            title="Wildcard matches any vendor"
          >
            * (any)
          </button>
          {value && (
            <button
              type="button"
              onClick={() => onChange("")}
              className="px-2 py-0.5 text-xs rounded-full border border-border text-text-muted hover:border-danger hover:text-danger transition-colors cursor-pointer"
            >
              ✕ Clear
            </button>
          )}
        </div>

        {showWarning && (
          <p className="text-xs text-warning flex items-center gap-1">
            <span>⚠️</span>
            Vendor scope is set but vendor is empty - failover will be blocked
          </p>
        )}

        <p className="text-xs text-text-muted">
          Used for failover isolation. Providers with same vendor can failover
          to each other. Empty = no isolation, * = matches any vendor.
        </p>
      </div>
    </FormField>
  );
}

interface FailoverScopeFieldProps {
  label: string;
  value: FailoverScope;
  onChange: (value: FailoverScope) => void;
  direction: "outbound" | "inbound";
}

export function FailoverScopeField({
  label,
  value,
  onChange,
  direction,
}: FailoverScopeFieldProps) {
  const description =
    direction === "outbound"
      ? "Controls where requests can failover TO after this provider fails"
      : "Controls which failover requests this provider accepts FROM";

  return (
    <FormField label={label}>
      <div className="space-y-2">
        <div className="grid grid-cols-3 gap-2">
          {FAILOVER_SCOPE_OPTIONS.map((option) => (
            <button
              key={option.value}
              type="button"
              onClick={() => onChange(option.value as FailoverScope)}
              className={`relative flex flex-col items-center gap-1 p-3 rounded-lg border-2 transition-all cursor-pointer ${
                value === option.value
                  ? "border-primary bg-primary-light/50"
                  : "border-border hover:border-primary/50 bg-bg-secondary"
              }`}
            >
              <span className="text-lg">{option.icon}</span>
              <span
                className={`text-xs font-medium ${value === option.value ? "text-primary" : "text-text-secondary"}`}
              >
                {option.label}
              </span>
              {value === option.value && (
                <span className="absolute top-1 right-1 w-2 h-2 bg-primary rounded-full" />
              )}
            </button>
          ))}
        </div>
        <p className="text-xs text-text-muted">{description}</p>
      </div>
    </FormField>
  );
}

interface FailoverSectionProps {
  formData: ProviderInput;
  setFormData: React.Dispatch<React.SetStateAction<ProviderInput>>;
  expanded: boolean;
  onToggle: () => void;
}

export function FailoverSection({
  formData,
  setFormData,
  expanded,
  onToggle,
}: FailoverSectionProps) {
  const hasConfig =
    formData.vendor ||
    (formData.failover_scope &&
      formData.failover_scope !== FAILOVER_SCOPES.ANY) ||
    (formData.accept_failover &&
      formData.accept_failover !== FAILOVER_SCOPES.ANY);

  return (
    <div className="border border-border rounded-lg overflow-hidden">
      <button
        type="button"
        onClick={onToggle}
        className="w-full flex items-center justify-between p-3 bg-bg-secondary hover:bg-bg-tertiary transition-colors cursor-pointer"
      >
        <div className="flex items-center gap-2">
          <span className="text-lg">🔀</span>
          <span className="font-medium text-text-primary">
            Vendor Failover Isolation
          </span>
          {hasConfig && (
            <span className="px-1.5 py-0.5 text-xs bg-info-light text-info-dark rounded">
              Configured
            </span>
          )}
        </div>
        <span
          className={`text-text-muted transition-transform ${expanded ? "rotate-180" : ""}`}
        >
          ▼
        </span>
      </button>

      {expanded && (
        <div className="p-4 space-y-4 border-t border-border bg-bg-primary">
          <div className="p-3 rounded-lg bg-info-light/30 border border-info-light/50">
            <p className="text-xs text-text-secondary">
              <strong>Vendor Isolation</strong> prevents cross-vendor failover
              to protect against signature detection. Configure vendors with
              same ID to allow failover between them.
            </p>
          </div>

          <VendorField
            value={formData.vendor ?? ""}
            onChange={(value) =>
              setFormData((prev) => ({ ...prev, vendor: value }))
            }
            failoverScope={formData.failover_scope}
            acceptFailover={formData.accept_failover}
          />

          <div className="grid grid-cols-2 gap-4">
            <FailoverScopeField
              label="Failover Scope (Outbound)"
              value={formData.failover_scope ?? FAILOVER_SCOPES.ANY}
              onChange={(value) =>
                setFormData((prev) => ({ ...prev, failover_scope: value }))
              }
              direction="outbound"
            />
            <FailoverScopeField
              label="Accept Failover (Inbound)"
              value={formData.accept_failover ?? FAILOVER_SCOPES.ANY}
              onChange={(value) =>
                setFormData((prev) => ({ ...prev, accept_failover: value }))
              }
              direction="inbound"
            />
          </div>
        </div>
      )}
    </div>
  );
}
