import { useState, useEffect, useCallback } from "react";
import type { FormEvent } from "react";
import type { Provider, ProviderInput } from "../../api/client";
import { ProviderFormBody } from "./ProviderFormBody";
import { isValidId } from "../../lib/utils";

function ModalHeader({
  title,
  onClose,
}: {
  title: string;
  onClose: () => void;
}) {
  return (
    <div className="p-6 border-b border-border">
      <div className="flex items-center justify-between">
        <h3 className="text-lg font-semibold text-text-primary">{title}</h3>
        <button
          type="button"
          onClick={onClose}
          className="text-text-muted hover:text-text-primary transition-colors cursor-pointer"
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
      </div>
    </div>
  );
}

const DEFAULT_FORM_DATA: ProviderInput = {
  id: "",
  name: "",
  base_url: "",
  api_key: "",
  api_types: [],
  auth_mode: "auto",
  group_id: null,
  weight: 1,
  priority: 0,
  concurrency: 10,
  max_retries: 3,
  enabled: true,
};

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

  const [formData, setFormData] = useState<ProviderInput>(DEFAULT_FORM_DATA);
  const [apiTypesInput, setApiTypesInput] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [idManuallyEdited, setIdManuallyEdited] = useState(false);
  const [idError, setIdError] = useState<string | null>(null);

  const handleEscape = useCallback(
    (e: KeyboardEvent) => {
      if (e.key === "Escape" && !submitting) {
        onClose();
      }
    },
    [onClose, submitting],
  );

  useEffect(() => {
    document.addEventListener("keydown", handleEscape);
    return () => document.removeEventListener("keydown", handleEscape);
  }, [handleEscape]);

  useEffect(() => {
    if (initialData) {
      setFormData({
        id: initialData.id,
        name: initialData.name,
        base_url: initialData.base_url,
        api_key: initialData.api_key,
        api_types: initialData.api_types.map((t) => t.api_type),
        auth_mode: initialData.auth_mode || "auto",
        group_id: initialData.group_id,
        weight: initialData.weight,
        priority: initialData.priority,
        concurrency: initialData.concurrency,
        max_retries: initialData.max_retries,
        enabled: initialData.enabled,
      });
      setApiTypesInput(initialData.api_types.map((t) => t.api_type).join(", "));
      setIdManuallyEdited(false);
      setIdError(null);
      setError(null);
    } else {
      setFormData(DEFAULT_FORM_DATA);
      setApiTypesInput("");
      setIdManuallyEdited(false);
      setIdError(null);
      setError(null);
    }
  }, [initialData]);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (!isEditMode && formData.id && !isValidId(formData.id)) {
      setIdError("ID can only contain lowercase letters, numbers, and hyphens");
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      await onSubmit({
        ...formData,
        api_types: apiTypesInput
          .split(",")
          .map((s) => s.trim())
          .filter(Boolean),
      });
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to save provider");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/50 backdrop-blur-sm">
      <div className="bg-bg-primary rounded-xl shadow-xl w-full max-w-2xl mx-4 max-h-[90vh] overflow-y-auto">
        <ModalHeader
          title={isEditMode ? "Edit Provider" : "Add Provider"}
          onClose={onClose}
        />
        <form
          onSubmit={handleSubmit}
          className="p-6 space-y-4"
          autoComplete="off"
        >
          <ProviderFormBody
            formState={{
              data: formData,
              setData: setFormData,
              apiTypesInput,
              setApiTypesInput,
            }}
            idState={{
              manuallyEdited: idManuallyEdited,
              setManuallyEdited: setIdManuallyEdited,
              error: idError,
              setError: setIdError,
            }}
            error={error}
            isEditMode={isEditMode}
            submitting={submitting}
            onCancel={onClose}
            groups={groups}
          />
        </form>
      </div>
    </div>
  );
}
