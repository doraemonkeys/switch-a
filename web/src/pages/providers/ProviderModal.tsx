import { useState, useEffect, useRef, useId } from "react";
import type { FormEvent } from "react";
import type { Provider, ProviderInput } from "../../api";
import { ProviderFormBody } from "./ProviderFormBody";
import { isValidId } from "../../lib/utils";
import { normalizeProviderApiKey } from "../../lib/providerApiKey";
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
    api_key: normalizeProviderApiKey(initialData.api_key),
    api_types: initialData.api_types.map((t) => ({
      api_type: t.api_type,
      base_url: t.base_url,
      api_key: normalizeProviderApiKey(t.api_key),
    })),
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

    // Filter out entries with empty api_type, then validate remaining entries
    const validApiTypes = formData.api_types.filter((t) => t.api_type.trim());
    if (validApiTypes.length === 0) {
      setError("At least one API type is required");
      return;
    }
    const missingUrl = validApiTypes.find((t) => !t.base_url.trim());
    if (missingUrl) {
      setError(`Base URL is required for API type "${missingUrl.api_type}"`);
      return;
    }
    const defaultAPIKey = normalizeProviderApiKey(formData.api_key);
    const missingKey = validApiTypes.find(
      (t) => !defaultAPIKey && !normalizeProviderApiKey(t.api_key),
    );
    if (missingKey) {
      setError(
        `API key is required for API type "${missingKey.api_type}". Set a default API key or add an override for that API type.`,
      );
      return;
    }

    setSubmitting(true);
    setError(null);
    try {
      const normalizedApiTypes = validApiTypes.map((apiType) => ({
        ...apiType,
        api_key: normalizeProviderApiKey(apiType.api_key),
      }));
      await onSubmit({
        ...formData,
        api_key: defaultAPIKey,
        api_types: normalizedApiTypes,
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
