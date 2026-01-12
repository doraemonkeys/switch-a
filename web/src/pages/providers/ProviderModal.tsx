import { useState, useEffect } from "react";
import type { Provider, ProviderInput } from "../../api/client";
import {
  FormField,
  ApiTypesField,
  GroupSelectField,
  NumberFieldRow,
  EnabledCheckbox,
  FormActions,
} from "./ProviderFormFields";

export interface ProviderModalProps {
  initialData?: Provider;
  onClose: () => void;
  onSubmit: (data: ProviderInput) => Promise<void>;
  groups: Array<{ id: string; name: string }>;
}

export function ProviderModal({
  initialData,
  onClose,
  onSubmit,
  groups,
}: ProviderModalProps) {
  const isEditMode = !!initialData;

  const [formData, setFormData] = useState<ProviderInput>({
    name: "",
    base_url: "",
    api_key: "",
    api_types: [],
    group_id: null,
    weight: 1,
    priority: 0,
    concurrency: 10,
    max_retries: 3,
    enabled: true,
  });

  const [apiTypesInput, setApiTypesInput] = useState("");
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (initialData) {
      setFormData({
        id: initialData.id,
        name: initialData.name,
        base_url: initialData.base_url,
        api_key: initialData.api_key,
        api_types: initialData.api_types.map((t) => t.api_type),
        group_id: initialData.group_id,
        weight: initialData.weight,
        priority: initialData.priority,
        concurrency: initialData.concurrency,
        max_retries: initialData.max_retries,
        enabled: initialData.enabled,
      });
      setApiTypesInput(initialData.api_types.map((t) => t.api_type).join(", "));
    }
  }, [initialData]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSubmitting(true);
    try {
      await onSubmit({
        ...formData,
        api_types: apiTypesInput
          .split(",")
          .map((s) => s.trim())
          .filter(Boolean),
      });
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
      <div className="bg-bg-primary rounded-xl shadow-xl w-full max-w-lg mx-4 max-h-[90vh] overflow-y-auto">
        <div className="p-6 border-b border-border">
          <div className="flex items-center justify-between">
            <h3 className="text-lg font-semibold text-text-primary">
              {isEditMode ? "Edit Provider" : "Add Provider"}
            </h3>
            <button
              onClick={onClose}
              className="text-text-muted hover:text-text-primary"
            >
              ✕
            </button>
          </div>
        </div>
        <form onSubmit={handleSubmit} className="p-6 space-y-4">
          <FormField label="Name">
            <input
              type="text"
              className="input"
              value={formData.name}
              onChange={(e) =>
                setFormData((prev) => ({ ...prev, name: e.target.value }))
              }
              required
              placeholder="e.g., OpenAI Production"
            />
          </FormField>
          <FormField label="Base URL">
            <input
              type="url"
              className="input"
              value={formData.base_url}
              onChange={(e) =>
                setFormData((prev) => ({ ...prev, base_url: e.target.value }))
              }
              required
              placeholder="e.g., https://api.openai.com"
            />
          </FormField>
          <FormField label="API Key">
            <input
              type="password"
              className="input"
              value={formData.api_key}
              onChange={(e) =>
                setFormData((prev) => ({ ...prev, api_key: e.target.value }))
              }
              required
              placeholder="sk-..."
            />
          </FormField>
          <ApiTypesField value={apiTypesInput} onChange={setApiTypesInput} />
          <GroupSelectField
            value={formData.group_id ?? null}
            onChange={(value) =>
              setFormData((prev) => ({ ...prev, group_id: value }))
            }
            groups={groups}
          />
          <NumberFieldRow
            formData={formData}
            setFormData={setFormData}
            fields={[
              { key: "priority", label: "Priority", min: 0, defaultValue: 0 },
              { key: "weight", label: "Weight", min: 1, defaultValue: 1 },
            ]}
          />
          <NumberFieldRow
            formData={formData}
            setFormData={setFormData}
            fields={[
              {
                key: "concurrency",
                label: "Concurrency Limit",
                min: 1,
                defaultValue: 10,
              },
              {
                key: "max_retries",
                label: "Max Retries",
                min: 0,
                defaultValue: 3,
              },
            ]}
          />
          <EnabledCheckbox
            checked={formData.enabled ?? true}
            onChange={(checked) =>
              setFormData((prev) => ({ ...prev, enabled: checked }))
            }
          />
          <FormActions
            isEditMode={isEditMode}
            submitting={submitting}
            onCancel={onClose}
          />
        </form>
      </div>
    </div>
  );
}
