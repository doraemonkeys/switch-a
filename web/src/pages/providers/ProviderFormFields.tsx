import type { ProviderInput } from "../../api/client";

const COMMON_API_TYPES = [
  "chat",
  "embeddings",
  "images",
  "audio",
  "moderation",
];

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
        placeholder="e.g., chat, embeddings, images"
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
              className={`px-2 py-1 text-xs rounded-full border transition-colors ${
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

interface NumberFieldRowProps {
  formData: ProviderInput;
  setFormData: React.Dispatch<React.SetStateAction<ProviderInput>>;
  fields: Array<{
    key: keyof ProviderInput;
    label: string;
    min: number;
    defaultValue: number;
  }>;
}

export function NumberFieldRow({
  formData,
  setFormData,
  fields,
}: NumberFieldRowProps) {
  return (
    <div className="grid grid-cols-2 gap-4">
      {fields.map(({ key, label, min, defaultValue }) => (
        <FormField key={key} label={label}>
          <input
            type="number"
            className="input"
            value={formData[key] as number}
            onChange={(e) =>
              setFormData((prev) => ({
                ...prev,
                [key]: parseInt(e.target.value) || defaultValue,
              }))
            }
            min={min}
          />
        </FormField>
      ))}
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
        className="text-sm font-medium text-text-secondary"
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
