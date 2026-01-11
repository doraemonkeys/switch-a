import { useState } from "react";
import type { ProviderInput } from "../../api/client";

export interface AddProviderModalProps {
  onClose: () => void;
  onSubmit: (data: ProviderInput) => Promise<void>;
  groups: Array<{ id: string; name: string }>;
}

export function AddProviderModal({
  onClose,
  onSubmit,
  groups,
}: AddProviderModalProps) {
  const [formData, setFormData] = useState<ProviderInput>({
    name: "",
    base_url: "",
    api_key: "",
    api_types: [],
    group_id: null,
    weight: 1,
    priority: 0,
    enabled: true,
  });
  const [submitting, setSubmitting] = useState(false);
  const [apiTypesInput, setApiTypesInput] = useState("");

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
              Add Provider
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
          <div>
            <label className="block text-sm font-medium text-text-secondary mb-1">
              Name
            </label>
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
          </div>
          <div>
            <label className="block text-sm font-medium text-text-secondary mb-1">
              Base URL
            </label>
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
          </div>
          <div>
            <label className="block text-sm font-medium text-text-secondary mb-1">
              API Key
            </label>
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
          </div>
          <div>
            <label className="block text-sm font-medium text-text-secondary mb-1">
              API Types (comma-separated)
            </label>
            <input
              type="text"
              className="input"
              value={apiTypesInput}
              onChange={(e) => setApiTypesInput(e.target.value)}
              placeholder="e.g., chat, embeddings, images"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-text-secondary mb-1">
              Group
            </label>
            <select
              className="input"
              value={formData.group_id ?? ""}
              onChange={(e) =>
                setFormData((prev) => ({
                  ...prev,
                  group_id: e.target.value || null,
                }))
              }
            >
              <option value="">No Group</option>
              {groups.map((group) => (
                <option key={group.id} value={group.id}>
                  {group.name}
                </option>
              ))}
            </select>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-text-secondary mb-1">
                Priority
              </label>
              <input
                type="number"
                className="input"
                value={formData.priority}
                onChange={(e) =>
                  setFormData((prev) => ({
                    ...prev,
                    priority: parseInt(e.target.value) || 0,
                  }))
                }
                min={0}
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-text-secondary mb-1">
                Weight
              </label>
              <input
                type="number"
                className="input"
                value={formData.weight}
                onChange={(e) =>
                  setFormData((prev) => ({
                    ...prev,
                    weight: parseInt(e.target.value) || 1,
                  }))
                }
                min={1}
              />
            </div>
          </div>
          <div className="flex items-center gap-2">
            <input
              type="checkbox"
              id="enabled"
              checked={formData.enabled}
              onChange={(e) =>
                setFormData((prev) => ({ ...prev, enabled: e.target.checked }))
              }
              className="w-4 h-4 rounded border-border text-primary focus:ring-primary"
            />
            <label
              htmlFor="enabled"
              className="text-sm font-medium text-text-secondary"
            >
              Enable provider immediately
            </label>
          </div>
          <div className="flex justify-end gap-3 pt-4">
            <button
              type="button"
              onClick={onClose}
              className="btn btn-secondary"
              disabled={submitting}
            >
              Cancel
            </button>
            <button
              type="submit"
              className="btn btn-primary"
              disabled={submitting}
            >
              {submitting ? (
                <>
                  <span className="animate-spin">⏳</span>
                  Creating...
                </>
              ) : (
                <>
                  <span>➕</span>
                  Add Provider
                </>
              )}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
