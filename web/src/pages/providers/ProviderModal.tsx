import { useState, useEffect, useRef, useId } from "react";
import type { FormEvent } from "react";
import type {
  APICatalog,
  CreateCredentialSessionInput,
  CredentialSession,
  Provider,
  ProviderInput,
} from "../../api";
import { findBuiltInAPIType, isValidAPIType, useAPICatalog } from "../../api";
import { ProviderFormBody } from "./ProviderFormBody";
import { useChatGPTLogin } from "./useChatGPTLogin";
import { useCredentialSessions } from "../../hooks/useCredentialSessions";
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
  resolveProviderAuthView,
  resolveProviderChatGPTCredentialSession,
  resolveProviderCredentialKind,
} from "../../lib/providerAuth";
import type { ProviderAPITypeDraft, ProviderFormData } from "./types";
import { generateClientKey } from "./types";

// GPT login is intrinsically a Codex credential flow; catalog membership is
// still checked at submission time so this requirement cannot become a list.
const CHATGPT_API_TYPE = "codex";

function createChatGPTAPIType(
  apiType: string,
  credentialSessionID = "",
): ProviderFormData["api_types"] {
  return [
    {
      client_key: generateClientKey(),
      api_type: apiType,
      base_url: CHATGPT_CODEX_BASE_URL,
      credential_session_id: credentialSessionID,
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

function createDefaultFormData(): ProviderFormData {
  return {
    id: "",
    name: "",
    default_api_key: "",
    chatgpt_credential_session_id: "",
    api_types: [],
    auth_mode: "auto",
    credential_mode: PROVIDER_CREDENTIAL_TYPES.API_KEY,
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

function deriveFormData(initialData?: Provider): ProviderFormData {
  if (!initialData) return createDefaultFormData();
  const credentialMode = resolveProviderCredentialKind(initialData) ?? "mixed";
  const chatGPTCredentialSession =
    resolveProviderChatGPTCredentialSession(initialData);
  return {
    id: initialData.id,
    name: initialData.name,
    default_api_key: "",
    chatgpt_credential_session_id: chatGPTCredentialSession?.id ?? "",
    api_types: initialData.api_types.map((t) => ({
      client_key: generateClientKey(),
      api_type: t.api_type,
      base_url: t.base_url,
      credential_session_id:
        credentialMode === PROVIDER_CREDENTIAL_TYPES.CHATGPT
          ? ""
          : t.credential_session_id,
      api_key: "",
    })),
    auth_mode: initialData.auth_mode || "auto",
    credential_mode: credentialMode,
    usage_limit_policy: initialData.usage_limit_policy_explicit
      ? initialData.usage_limit_policy
      : undefined,
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
  | {
      kind: "ok";
      apiTypes: ProviderAPITypeDraft[];
      isChatGPTProvider: boolean;
    }
  | { kind: "id-error"; message: string }
  | { kind: "form-error"; message: string };

type APITypeSubmissionPreparation =
  | {
      kind: "ok";
      apiTypes: ProviderAPITypeDraft[];
      isChatGPTProvider: boolean;
    }
  | { kind: "error"; message: string };

function prepareAPITypeSubmission(
  formData: ProviderFormData,
  apiCatalog: APICatalog | null,
): APITypeSubmissionPreparation {
  if (!apiCatalog) {
    return {
      kind: "error",
      message: "The API type catalog is unavailable. Retry it before saving.",
    };
  }

  const isChatGPTProvider =
    formData.credential_mode === PROVIDER_CREDENTIAL_TYPES.CHATGPT;
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
      apiTypes: createChatGPTAPIType(
        chatGPTAPIType.api_type,
        formData.chatgpt_credential_session_id,
      ),
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

function prepareProviderSubmission({
  formData,
  isEditMode,
  apiCatalog,
  credentialLoginID,
}: {
  formData: ProviderFormData;
  isEditMode: boolean;
  apiCatalog: APICatalog | null;
  credentialLoginID: string;
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

  const defaultAPIKey =
    formData.credential_mode === PROVIDER_CREDENTIAL_TYPES.API_KEY
      ? normalizeProviderApiKey(formData.default_api_key)
      : "";
  if (!isChatGPTProvider) {
    const missingKey = validApiTypes.find(
      (apiType) =>
        !apiType.credential_session_id &&
        !defaultAPIKey &&
        !normalizeProviderApiKey(apiType.api_key),
    );
    if (missingKey) {
      return {
        kind: "form-error",
        message: `Credential session is required for API type "${missingKey.api_type}". Select one or provide a new API key.`,
      };
    }
  }

  if (
    isChatGPTProvider &&
    !credentialLoginID &&
    !validApiTypes[0]?.credential_session_id
  ) {
    return {
      kind: "form-error",
      message: "Complete GPT login before saving this provider.",
    };
  }

  return {
    kind: "ok",
    apiTypes: validApiTypes.map((apiType) => ({
      ...apiType,
      api_key: normalizeProviderApiKey(apiType.api_key),
    })),
    isChatGPTProvider,
  };
}

type CreateCredentialSession = (
  input: CreateCredentialSessionInput,
) => Promise<CredentialSession>;

function providerInputFromForm(
  formData: ProviderFormData,
  apiTypes: ProviderInput["api_types"],
  isChatGPTProvider: boolean,
): ProviderInput {
  return {
    id: formData.id,
    name: formData.name,
    api_types: apiTypes,
    auth_mode: isChatGPTProvider ? AUTH_MODES.BEARER : formData.auth_mode,
    usage_limit_policy: formData.usage_limit_policy,
    group_id: formData.group_id,
    weight: formData.weight,
    priority: formData.priority,
    concurrency: formData.concurrency,
    max_retries: formData.max_retries,
    backoff: formData.backoff,
    vendor: formData.vendor,
    failover_scope: formData.failover_scope,
    accept_failover: formData.accept_failover,
    enabled: formData.enabled,
  };
}

async function materializeProviderCredentials({
  formData,
  apiTypes,
  isChatGPTProvider,
  credentialLoginID,
  createCredentialSession,
}: {
  formData: ProviderFormData;
  apiTypes: ProviderAPITypeDraft[];
  isChatGPTProvider: boolean;
  credentialLoginID: string;
  createCredentialSession: CreateCredentialSession;
}): Promise<{ payload: ProviderInput; formData: ProviderFormData }> {
  if (isChatGPTProvider) {
    let sessionID = apiTypes[0]?.credential_session_id ?? "";
    if (credentialLoginID) {
      const created = await createCredentialSession({
        kind: PROVIDER_CREDENTIAL_TYPES.CHATGPT,
        credential_login_id: credentialLoginID,
      });
      sessionID = created.id;
    }
    const resolved = apiTypes.map((entry) => ({
      api_type: entry.api_type,
      base_url: entry.base_url,
      credential_session_id: sessionID,
    }));
    return {
      payload: providerInputFromForm(formData, resolved, true),
      formData: {
        ...formData,
        chatgpt_credential_session_id: sessionID,
        api_types: resolved.map((entry) => ({
          ...entry,
          client_key: generateClientKey(),
          api_key: "",
        })),
      },
    };
  }

  const defaultSecret =
    formData.credential_mode === PROVIDER_CREDENTIAL_TYPES.API_KEY
      ? normalizeProviderApiKey(formData.default_api_key)
      : "";
  let defaultSessionID = "";
  const resolved: ProviderInput["api_types"] = [];
  for (const entry of apiTypes) {
    const routeSecret = normalizeProviderApiKey(entry.api_key);
    let sessionID = entry.credential_session_id;
    if (routeSecret) {
      sessionID = (
        await createCredentialSession({
          kind: PROVIDER_CREDENTIAL_TYPES.API_KEY,
          secret_data: routeSecret,
        })
      ).id;
    } else if (defaultSecret) {
      if (!defaultSessionID) {
        defaultSessionID = (
          await createCredentialSession({
            kind: PROVIDER_CREDENTIAL_TYPES.API_KEY,
            secret_data: defaultSecret,
          })
        ).id;
      }
      sessionID = defaultSessionID;
    }
    resolved.push({
      api_type: entry.api_type,
      base_url: entry.base_url,
      credential_session_id: sessionID,
    });
  }
  return {
    payload: providerInputFromForm(formData, resolved, false),
    formData: {
      ...formData,
      default_api_key: "",
      api_types: resolved.map((entry) => ({
        ...entry,
        client_key: generateClientKey(),
        api_key: "",
      })),
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
  const {
    credentialSessions,
    loading: credentialSessionsLoading,
    error: credentialSessionsQueryError,
    createCredentialSession,
  } = useCredentialSessions();

  const [formData, setFormData] = useState<ProviderFormData>(() =>
    deriveFormData(initialData),
  );
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [idManuallyEdited, setIdManuallyEdited] = useState(false);
  const [idError, setIdError] = useState<string | null>(null);

  const {
    chatGPTStatus,
    chatGPTLoginError,
    startingChatGPTLogin,
    chatGPTLoginAuthURL,
    pendingChatGPTAuth,
    credentialLoginID,
    clearCredentialLogin,
    handleStartChatGPTLogin,
    handleOpenChatGPTLoginPage,
    handleImportChatGPTLogin,
  } = useChatGPTLogin({
    enabled: formData.credential_mode === PROVIDER_CREDENTIAL_TYPES.CHATGPT,
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
      apiCatalog,
      credentialLoginID,
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
      const materialized = await materializeProviderCredentials({
        formData,
        apiTypes: preparedSubmission.apiTypes,
        isChatGPTProvider: preparedSubmission.isChatGPTProvider,
        credentialLoginID,
        createCredentialSession,
      });
      // A newly created credential session is durable and reusable even if the
      // provider write fails, so retain its binding in the open form for retry.
      setFormData(materialized.formData);
      if (preparedSubmission.isChatGPTProvider && credentialLoginID) {
        // A completed login is single-use. Once it has materialized a durable
        // session, retries must reuse that session instead of consuming it again.
        clearCredentialLogin();
      }
      await onSubmit(materialized.payload);
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
            credentialSessions={credentialSessions}
            credentialSessionsLoading={credentialSessionsLoading}
            credentialSessionsError={
              credentialSessionsQueryError?.message ?? null
            }
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
    </div>
  );
}
