import { useState, useEffect, useRef, useId } from "react";
import type { FormEvent } from "react";
import type { Provider, ProviderInput } from "../../api";
import { ProviderFormBody } from "./ProviderFormBody";
import { isValidId } from "../../lib/utils";
import { CloseIcon } from "../../components/icons/CloseIcon";
import { PROVIDER_DEFAULTS, FAILOVER_SCOPES } from "../../config/constants";

function ModalHeader({
  title,
  titleId,
  onClose,
}: {
  title: string;
  titleId: string;
  onClose: () => void;
}) {
  return (
    <div className="p-6 border-b border-border">
      <div className="flex items-center justify-between">
        <h3 id={titleId} className="text-lg font-semibold text-text-primary">
          {title}
        </h3>
        <button
          type="button"
          onClick={onClose}
          className="text-text-muted hover:text-text-primary transition-colors cursor-pointer"
          aria-label="Close"
        >
          <CloseIcon />
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
  weight: PROVIDER_DEFAULTS.WEIGHT,
  priority: PROVIDER_DEFAULTS.PRIORITY,
  concurrency: PROVIDER_DEFAULTS.CONCURRENCY,
  max_retries: PROVIDER_DEFAULTS.MAX_RETRIES,
  // Default: opt out of vendor isolation so new providers work without failover setup
  vendor: "",
  failover_scope: FAILOVER_SCOPES.ANY,
  accept_failover: FAILOVER_SCOPES.ANY,
  enabled: true,
};

function deriveFormData(initialData?: Provider): ProviderInput {
  if (!initialData) return DEFAULT_FORM_DATA;
  return {
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
    backoff: initialData.backoff,
    vendor: initialData.vendor || "",
    failover_scope: initialData.failover_scope || FAILOVER_SCOPES.ANY,
    accept_failover: initialData.accept_failover || FAILOVER_SCOPES.ANY,
    enabled: initialData.enabled,
  };
}

function deriveApiTypesInput(initialData?: Provider): string {
  if (!initialData) return "";
  return initialData.api_types.map((t) => t.api_type).join(", ");
}

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
  const titleId = useId();
  const modalRef = useRef<HTMLDivElement>(null);

  const [formData, setFormData] = useState<ProviderInput>(() =>
    deriveFormData(initialData),
  );
  const [apiTypesInput, setApiTypesInput] = useState(() =>
    deriveApiTypesInput(initialData),
  );
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [idManuallyEdited, setIdManuallyEdited] = useState(false);
  const [idError, setIdError] = useState<string | null>(null);

  // Auto-focus first focusable element when modal opens
  useEffect(() => {
    const firstFocusable = modalRef.current?.querySelector<HTMLElement>(
      'input, select, textarea, button:not([aria-label="Close"])',
    );
    firstFocusable?.focus();
  }, []);

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !submitting) {
        onClose();
        return;
      }

      if (e.key === "Tab" && modalRef.current) {
        const focusableElements =
          modalRef.current.querySelectorAll<HTMLElement>(
            'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])',
          );
        const firstElement = focusableElements[0];
        const lastElement = focusableElements[focusableElements.length - 1];

        if (e.shiftKey && document.activeElement === firstElement) {
          e.preventDefault();
          lastElement?.focus();
        } else if (!e.shiftKey && document.activeElement === lastElement) {
          e.preventDefault();
          firstElement?.focus();
        }
      }
    };
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [onClose, submitting]);

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
      <div
        ref={modalRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        className="bg-bg-primary rounded-xl shadow-xl w-full max-w-2xl mx-4 max-h-[90vh] overflow-y-auto"
      >
        <ModalHeader
          title={isEditMode ? "Edit Provider" : "Add Provider"}
          titleId={titleId}
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
