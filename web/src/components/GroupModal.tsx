import { useEffect, useEffectEvent, useState } from "react";
import type { FormEvent } from "react";
import type { Group, GroupInput, Strategy } from "../api/types";
import { STRATEGIES, STRATEGY_OPTIONS } from "../config/constants";
import { slugify, isValidId } from "../lib/utils";

// Close button component
const CloseButton = ({
  onClick,
  disabled,
}: {
  onClick: () => void;
  disabled?: boolean;
}) => (
  <button
    onClick={onClick}
    disabled={disabled}
    className="text-text-secondary hover:text-text-primary transition-colors disabled:opacity-50 cursor-pointer disabled:cursor-not-allowed"
    aria-label="Close"
  >
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
        d="M6 18L18 6M6 6l12 12"
      />
    </svg>
  </button>
);

// Strategy selector component
interface StrategySelectorProps {
  value: Strategy;
  onChange: (value: Strategy) => void;
}

const StrategySelector = ({ value, onChange }: StrategySelectorProps) => (
  <div>
    <label className="block text-sm font-medium text-text-secondary mb-1">
      Strategy
    </label>
    <div className="grid grid-cols-1 gap-2">
      {STRATEGY_OPTIONS.map((strategy) => (
        <label
          key={strategy.value}
          className={`flex items-start gap-3 p-3 rounded-lg border cursor-pointer transition-all
                        ${
                          value === strategy.value
                            ? "bg-primary/10 border-primary"
                            : "bg-bg-tertiary border-border-light hover:border-primary/50"
                        }`}
        >
          <input
            type="radio"
            name="strategy"
            value={strategy.value}
            checked={value === strategy.value}
            onChange={(e) => onChange(e.target.value as Strategy)}
            className="mt-1"
          />
          <div>
            <div className="font-medium text-text-primary">
              {strategy.label}
            </div>
            <div className="text-xs text-text-secondary">
              {strategy.description}
            </div>
          </div>
        </label>
      ))}
    </div>
  </div>
);

interface GroupModalProps {
  isOpen: boolean;
  onClose: () => void;
  onSubmit: (data: GroupInput) => Promise<void>;
  initialData?: Group | null;
  title: string;
}

type GroupModalFormProps = Omit<GroupModalProps, "isOpen">;

const NEW_GROUP_FORM_KEY = "new-group";

function createInitialFormData(initialData?: Group | null): GroupInput {
  if (initialData) {
    return {
      id: initialData.id,
      name: initialData.name,
      strategy: initialData.strategy,
      priority: initialData.priority,
      weight: initialData.weight,
      enabled: initialData.enabled,
    };
  }

  return {
    id: "",
    name: "",
    strategy: STRATEGIES.PRIORITY,
    priority: 0,
    weight: 0,
    enabled: true,
  };
}

function GroupModalForm({
  onClose,
  onSubmit,
  initialData,
  title,
}: GroupModalFormProps) {
  const [formData, setFormData] = useState<GroupInput>(() =>
    createInitialFormData(initialData),
  );
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [idManuallyEdited, setIdManuallyEdited] = useState(false);
  const [idError, setIdError] = useState<string | null>(null);
  const isEditMode = !!initialData;

  const handleEscape = useEffectEvent((event: KeyboardEvent) => {
    if (event.key === "Escape" && !loading) onClose();
  });

  useEffect(() => {
    document.addEventListener("keydown", handleEscape);
    return () => document.removeEventListener("keydown", handleEscape);
  }, []);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (!isEditMode && formData.id && !isValidId(formData.id)) {
      setIdError("ID can only contain lowercase letters, numbers, and hyphens");
      return;
    }
    setLoading(true);
    setError(null);
    try {
      await onSubmit(formData);
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to save group");
    } finally {
      setLoading(false);
    }
  };

  const handleNameChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const newName = e.target.value;
    setFormData((prev) => {
      if (!isEditMode && !idManuallyEdited) {
        return { ...prev, name: newName, id: slugify(newName) };
      }
      return { ...prev, name: newName };
    });
  };

  const handleIdChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const newId = e.target.value;
    const autoId = slugify(formData.name || "");
    setIdManuallyEdited(newId !== autoId);
    setFormData((prev) => ({ ...prev, id: newId }));
    setIdError(
      newId && !isValidId(newId)
        ? "ID can only contain lowercase letters, numbers, and hyphens"
        : null,
    );
  };

  const inputClass =
    "w-full px-3 py-2 bg-bg-tertiary border border-border-light rounded-lg focus:outline-none focus:border-primary text-text-primary placeholder-text-muted";

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/50 backdrop-blur-sm">
      <div className="bg-bg-secondary w-full max-w-lg rounded-xl shadow-2xl border border-border-light max-h-[90vh] overflow-y-auto">
        <div className="p-6 border-b border-border-light flex justify-between items-center">
          <h2 className="text-xl font-bold text-text-primary">{title}</h2>
          <CloseButton onClick={onClose} disabled={loading} />
        </div>

        <form onSubmit={handleSubmit} className="p-6 space-y-4">
          {error && (
            <div className="bg-red-500/10 border border-red-500/20 text-red-400 p-3 rounded-lg text-sm">
              {error}
            </div>
          )}

          <div>
            <label className="block text-sm font-medium text-text-secondary mb-1">
              Group Name
            </label>
            <input
              type="text"
              required
              value={formData.name}
              onChange={handleNameChange}
              className={inputClass}
              placeholder="e.g. OpenAI Providers"
            />
          </div>

          {!isEditMode && (
            <div>
              <label className="block text-sm font-medium text-text-secondary mb-1">
                Group ID
              </label>
              <input
                type="text"
                required
                value={formData.id}
                onChange={handleIdChange}
                className={`${inputClass} ${idError ? "border-red-500 focus:border-red-500" : ""}`}
                placeholder="Auto-generated: name-random"
              />
              <p
                className={`text-xs mt-1 ${idError ? "text-red-400" : "text-text-muted"}`}
              >
                {idError ||
                  "Auto-generated from Name + random ID. Only lowercase letters, numbers, and hyphens allowed."}
              </p>
            </div>
          )}

          <StrategySelector
            value={formData.strategy || STRATEGIES.PRIORITY}
            onChange={(strategy) => setFormData({ ...formData, strategy })}
          />

          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-text-secondary mb-1">
                Priority
              </label>
              <input
                type="number"
                value={formData.priority}
                onChange={(e) =>
                  setFormData({
                    ...formData,
                    priority: parseInt(e.target.value) || 0,
                  })
                }
                className={inputClass}
              />
              <p className="text-xs text-text-muted mt-1">
                Lower number = higher priority (0 is highest)
              </p>
            </div>
            <div>
              <label className="block text-sm font-medium text-text-secondary mb-1">
                Weight
              </label>
              <input
                type="number"
                value={formData.weight}
                onChange={(e) =>
                  setFormData({
                    ...formData,
                    weight: parseInt(e.target.value) || 0,
                  })
                }
                className={inputClass}
              />
              <p className="text-xs text-text-muted mt-1">
                Higher weight = more traffic (for weight strategy)
              </p>
            </div>
          </div>

          <div className="flex items-center gap-2">
            <input
              type="checkbox"
              id="enabled"
              checked={formData.enabled}
              onChange={(e) =>
                setFormData({ ...formData, enabled: e.target.checked })
              }
              className="rounded border-border-light bg-bg-tertiary text-primary focus:ring-primary"
            />
            <label
              htmlFor="enabled"
              className="text-sm font-medium text-text-secondary cursor-pointer"
            >
              Enable this group
            </label>
          </div>

          <div className="flex justify-end gap-3 mt-6 pt-4 border-t border-border-light">
            <button
              type="button"
              onClick={onClose}
              disabled={loading}
              className="px-4 py-2 text-text-secondary hover:text-text-primary transition-colors cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={loading}
              className="btn btn-primary disabled:opacity-50 disabled:cursor-not-allowed"
            >
              {loading ? "Saving..." : "Save Group"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

export function GroupModal({ isOpen, initialData, ...props }: GroupModalProps) {
  if (!isOpen) return null;

  // Opening a modal creates a fresh editing session. The entity key also
  // resets the form when callers switch directly between two groups.
  return (
    <GroupModalForm
      key={initialData?.id ?? NEW_GROUP_FORM_KEY}
      initialData={initialData}
      {...props}
    />
  );
}
