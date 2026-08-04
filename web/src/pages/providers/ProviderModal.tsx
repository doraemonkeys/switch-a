import { useState, useEffect, useRef, useId } from "react";
import type { FormEvent } from "react";
import type { APICatalog, Provider, ProviderInput } from "../../api";
import {
  ApiError,
  findBuiltInAPIType,
  isValidAPIType,
  useAPICatalog,
} from "../../api";
import { ConfirmModal } from "../../components";
import { ProviderFormBody } from "./ProviderFormBody";
import { useChatGPTLogin } from "./useChatGPTLogin";
import { isValidId } from "../../lib/utils";
import { normalizeProviderApiKey } from "../../lib/providerApiKey";
import { CloseIcon } from "../../components/icons/CloseIcon";
import {
  ADD_PROVIDER_DEFAULTS,
  FAILOVER_SCOPES,
  AUTH_MODES,
  CHATGPT_CODEX_BASE_URL,
  PROVIDER_CREDENTIAL_TYPES,
} from "../../config/constants";
import {
  hasProviderCredentialSnapshot,
  resolveProviderAuthView,
} from "../../lib/providerAuth";

// GPT login is intrinsically a Codex credential flow; catalog membership is
// still checked at submission time so this requirement cannot become a list.
const CHATGPT_API_TYPE = "codex";

function createChatGPTAPIType(apiType: string): ProviderInput["api_types"] {
  return [
    {
      api_type: apiType,
      base_url: CHATGPT_CODEX_BASE_URL,
      api_key: "",
    },
  ];
}

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

function createDefaultFormData(): ProviderInput {
  return {
    id: "",
    name: "",
    api_key: "",
    api_types: [],
    auth_mode: "auto",
    credential_type: PROVIDER_CREDENTIAL_TYPES.API_KEY,
    credential_login_id: "",
    group_id: null,
    weight: ADD_PROVIDER_DEFAULTS.WEIGHT,
    priority: ADD_PROVIDER_DEFAULTS.PRIORITY,
    concurrency: ADD_PROVIDER_DEFAULTS.CONCURRENCY,
    max_retries: ADD_PROVIDER_DEFAULTS.MAX_RETRIES,
    backoff: {
      initial_delay: ADD_PROVIDER_DEFAULTS.BACKOFF.INITIAL_DELAY,
      max_delay: ADD_PROVIDER_DEFAULTS.BACKOFF.MAX_DELAY,
      multiplier: ADD_PROVIDER_DEFAULTS.BACKOFF.MULTIPLIER,
      jitter: ADD_PROVIDER_DEFAULTS.BACKOFF.JITTER,
    },
    // Default: opt out of vendor isolation so new providers work without failover setup
    vendor: "",
    failover_scope: FAILOVER_SCOPES.ANY,
    accept_failover: FAILOVER_SCOPES.ANY,
    enabled: true,
  };
}

function deriveFormData(initialData?: Provider): ProviderInput {
  if (!initialData) return createDefaultFormData();
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
    credential_type:
      initialData.credential_type || PROVIDER_CREDENTIAL_TYPES.API_KEY,
    usage_limit_policy: initialData.usage_limit_policy_explicit
      ? initialData.usage_limit_policy
      : undefined,
    credential_login_id: "",
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

type ProviderSubmissionPreparation =
  | { kind: "ok"; payload: ProviderInput }
  | { kind: "id-error"; message: string }
  | { kind: "form-error"; message: string };

interface CredentialBindingConflict {
  payload: ProviderInput;
  accountId?: string;
  providerId?: string;
}

type APITypeSubmissionPreparation =
  | {
      kind: "ok";
      apiTypes: ProviderInput["api_types"];
      isChatGPTProvider: boolean;
    }
  | { kind: "error"; message: string };

function prepareAPITypeSubmission(
  formData: ProviderInput,
  apiCatalog: APICatalog | null,
): APITypeSubmissionPreparation {
  if (!apiCatalog) {
    return {
      kind: "error",
      message: "The API type catalog is unavailable. Retry it before saving.",
    };
  }

  const isChatGPTProvider =
    formData.credential_type === PROVIDER_CREDENTIAL_TYPES.CHATGPT;
  if (isChatGPTProvider) {
    const chatGPTAPIType = findBuiltInAPIType(apiCatalog, CHATGPT_API_TYPE);
    if (!chatGPTAPIType) {
      return {
        kind: "error",
        message: "The server does not advertise the Codex API type.",
      };
    }
    return {
      kind: "ok",
      apiTypes: createChatGPTAPIType(chatGPTAPIType.api_type),
      isChatGPTProvider: true,
    };
  }

  const apiTypes = formData.api_types.filter((entry) => entry.api_type.trim());
  if (apiTypes.length === 0) {
    return {
      kind: "error",
      message: "At least one API type is required",
    };
  }

  const invalidAPIType = apiTypes.find(
    (entry) => !isValidAPIType(apiCatalog, entry.api_type),
  );
  if (invalidAPIType) {
    return {
      kind: "error",
      message: `Unsupported API type "${invalidAPIType.api_type}"`,
    };
  }

  return { kind: "ok", apiTypes, isChatGPTProvider: false };
}

function getCredentialBindingConflict(
  error: unknown,
  payload: ProviderInput,
): CredentialBindingConflict | null {
  if (
    !(error instanceof ApiError) ||
    error.details?.kind !== "credential_binding"
  ) {
    return null;
  }
  return {
    payload,
    accountId: error.details.account_id,
    providerId: error.details.provider_id,
  };
}

function describeCredentialBindingConflict(
  conflict: CredentialBindingConflict | null,
): string {
  if (!conflict) {
    return "This GPT account is already connected. Replace the previous credential data with this login? The previous provider will become disconnected.";
  }
  const account = conflict.accountId ? ` (${conflict.accountId})` : "";
  const provider = conflict.providerId
    ? `provider "${conflict.providerId}"`
    : "another provider";
  return `This GPT account${account} is already connected to ${provider}. Replace the previous credential data with this login? The previous provider will become disconnected.`;
}

function prepareProviderSubmission({
  formData,
  isEditMode,
  hasPersistedChatGPTProvider,
  apiCatalog,
}: {
  formData: ProviderInput;
  isEditMode: boolean;
  hasPersistedChatGPTProvider: boolean;
  apiCatalog: APICatalog | null;
}): ProviderSubmissionPreparation {
  if (!isEditMode && formData.id && !isValidId(formData.id)) {
    return {
      kind: "id-error",
      message: "ID can only contain lowercase letters, numbers, and hyphens",
    };
  }

  const apiTypePreparation = prepareAPITypeSubmission(formData, apiCatalog);
  if (apiTypePreparation.kind === "error") {
    return {
      kind: "form-error",
      message: apiTypePreparation.message,
    };
  }
  const { apiTypes: validApiTypes, isChatGPTProvider } = apiTypePreparation;

  if (!isChatGPTProvider) {
    const missingURL = validApiTypes.find(
      (apiType) => !apiType.base_url.trim(),
    );
    if (missingURL) {
      return {
        kind: "form-error",
        message: `Base URL is required for API type "${missingURL.api_type}"`,
      };
    }
  }

  const defaultAPIKey = normalizeProviderApiKey(formData.api_key);
  if (!isChatGPTProvider) {
    const missingKey = validApiTypes.find(
      (apiType) => !defaultAPIKey && !normalizeProviderApiKey(apiType.api_key),
    );
    if (missingKey) {
      return {
        kind: "form-error",
        message: `API key is required for API type "${missingKey.api_type}". Set a default API key or add an override for that API type.`,
      };
    }
  }

  if (
    isChatGPTProvider &&
    !formData.credential_login_id &&
    !(isEditMode && hasPersistedChatGPTProvider)
  ) {
    return {
      kind: "form-error",
      message: "Complete GPT login before saving this provider.",
    };
  }

  const normalizedApiTypes = validApiTypes.map((apiType) => ({
    ...apiType,
    api_key: normalizeProviderApiKey(apiType.api_key),
  }));
  return {
    kind: "ok",
    payload: {
      ...formData,
      api_key: isChatGPTProvider ? "" : defaultAPIKey,
      api_types: normalizedApiTypes,
      auth_mode: isChatGPTProvider ? AUTH_MODES.BEARER : formData.auth_mode,
    },
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
  const { catalog: apiCatalog } = useAPICatalog();
  const isEditMode = !!initialData;
  const titleId = useId();
  const modalRef = useRef<HTMLDivElement>(null);
  const initialAuthView = resolveProviderAuthView(initialData);

  const [formData, setFormData] = useState<ProviderInput>(() =>
    deriveFormData(initialData),
  );
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [credentialBindingConflict, setCredentialBindingConflict] =
    useState<CredentialBindingConflict | null>(null);
  const [idManuallyEdited, setIdManuallyEdited] = useState(false);
  const [idError, setIdError] = useState<string | null>(null);

  const {
    chatGPTStatus,
    chatGPTLoginError,
    startingChatGPTLogin,
    chatGPTLoginAuthURL,
    pendingChatGPTAuth,
    handleStartChatGPTLogin,
    handleOpenChatGPTLoginPage,
    handleImportChatGPTLogin,
  } = useChatGPTLogin({
    credentialType: formData.credential_type,
    setFormData,
    initialAuthView,
  });

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
    const preparedSubmission = prepareProviderSubmission({
      formData,
      isEditMode,
      hasPersistedChatGPTProvider: hasProviderCredentialSnapshot(initialData),
      apiCatalog,
    });
    if (preparedSubmission.kind === "id-error") {
      setIdError(preparedSubmission.message);
      return;
    }
    if (preparedSubmission.kind === "form-error") {
      setError(preparedSubmission.message);
      return;
    }

    setIdError(null);
    setSubmitting(true);
    setError(null);
    try {
      await onSubmit(preparedSubmission.payload);
      onClose();
    } catch (err) {
      const conflict = getCredentialBindingConflict(
        err,
        preparedSubmission.payload,
      );
      if (conflict) {
        setCredentialBindingConflict(conflict);
        return;
      }
      setError(err instanceof Error ? err.message : "Failed to save provider");
    } finally {
      setSubmitting(false);
    }
  };

  const handleCredentialBindingReplacement = async () => {
    if (!credentialBindingConflict) {
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      await onSubmit({
        ...credentialBindingConflict.payload,
        credential_binding_resolution: "replace",
      });
      setCredentialBindingConflict(null);
      onClose();
    } catch (err) {
      setError(
        err instanceof Error ? err.message : "Failed to replace GPT account",
      );
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
            authView={pendingChatGPTAuth ?? initialAuthView}
            onStartChatGPTLogin={handleStartChatGPTLogin}
            onOpenChatGPTLoginPage={handleOpenChatGPTLoginPage}
            onImportChatGPTLogin={handleImportChatGPTLogin}
            chatGPTLoginState={{
              status: chatGPTStatus,
              error: chatGPTLoginError,
              loading: startingChatGPTLogin,
              authURL: chatGPTLoginAuthURL,
            }}
          />
        </form>
      </div>
      <ConfirmModal
        isOpen={credentialBindingConflict !== null}
        onClose={() => setCredentialBindingConflict(null)}
        onConfirm={() => void handleCredentialBindingReplacement()}
        title="GPT account already connected"
        message={describeCredentialBindingConflict(credentialBindingConflict)}
        confirmText="Replace account"
        cancelText="Keep previous"
        variant="warning"
        loading={submitting}
      />
    </div>
  );
}
