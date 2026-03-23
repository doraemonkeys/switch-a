import { useState, useEffect, useRef, useId, useContext } from "react";
import type { FormEvent } from "react";
import type { Provider, ProviderAuthProfile, ProviderInput } from "../../api";
import { ApiContext } from "../../api/context";
import { ProviderFormBody } from "./ProviderFormBody";
import { isValidId } from "../../lib/utils";
import { normalizeProviderApiKey } from "../../lib/providerApiKey";
import { CloseIcon } from "../../components/icons/CloseIcon";
import {
  PROVIDER_DEFAULTS,
  FAILOVER_SCOPES,
  AUTH_MODES,
  API_TYPES,
  CHATGPT_CODEX_BASE_URL,
  PROVIDER_CREDENTIAL_TYPES,
} from "../../config/constants";

const CHATGPT_LOGIN_POLL_INTERVAL_MS = 1000;
const CHATGPT_LOGIN_WINDOW_TARGET = "_blank";
const CHATGPT_LOGIN_WINDOW_FEATURES = "noopener,noreferrer";
const CHATGPT_LOGIN_READY_MESSAGE =
  "Sign-in link ready. Open it in any browser on this machine. Switch-A will detect completion automatically.";
const CHATGPT_LOGIN_EXPIRED_MESSAGE =
  "GPT sign-in expired before it was saved. Start a new sign-in link.";
const CHATGPT_LOGIN_STATUS_ERROR_MESSAGE = "Failed to check GPT login status";
const CHATGPT_LOGIN_START_ERROR_MESSAGE = "Failed to start GPT login";
const CHATGPT_LOGIN_COMPLETED_MESSAGE =
  "GPT login completed. Save the provider to persist it.";

interface ChatGPTLoginSession {
  loginId: string;
  authURL: string;
}

function createChatGPTAPIType(): ProviderInput["api_types"] {
  return [
    {
      api_type: API_TYPES.CODEX,
      base_url: CHATGPT_CODEX_BASE_URL,
      api_key: "",
    },
  ];
}

function describeConnectedChatGPTAccount(
  authProfile?: ProviderAuthProfile | null,
): string {
  if (!authProfile?.ready) {
    return CHATGPT_LOGIN_COMPLETED_MESSAGE;
  }
  return authProfile.email
    ? `Connected as ${authProfile.email}. Save the provider to persist it.`
    : CHATGPT_LOGIN_COMPLETED_MESSAGE;
}

function describePersistedChatGPTAccount(
  authProfile?: ProviderAuthProfile | null,
): string | null {
  if (!authProfile?.ready) {
    return null;
  }
  return authProfile.email
    ? `Connected as ${authProfile.email}.`
    : "A GPT account is already connected for this provider.";
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

const DEFAULT_FORM_DATA: ProviderInput = {
  id: "",
  name: "",
  api_key: "",
  api_types: [],
  auth_mode: "auto",
  credential_type: PROVIDER_CREDENTIAL_TYPES.API_KEY,
  credential_login_id: "",
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
    credential_type:
      initialData.credential_type || PROVIDER_CREDENTIAL_TYPES.API_KEY,
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

function prepareProviderSubmission({
  formData,
  isEditMode,
  hasPersistedChatGPTLogin,
}: {
  formData: ProviderInput;
  isEditMode: boolean;
  hasPersistedChatGPTLogin: boolean;
}): ProviderSubmissionPreparation {
  if (!isEditMode && formData.id && !isValidId(formData.id)) {
    return {
      kind: "id-error",
      message: "ID can only contain lowercase letters, numbers, and hyphens",
    };
  }

  const isChatGPTProvider =
    formData.credential_type === PROVIDER_CREDENTIAL_TYPES.CHATGPT;
  const validApiTypes = isChatGPTProvider
    ? createChatGPTAPIType()
    : formData.api_types.filter((apiType) => apiType.api_type.trim());
  if (!isChatGPTProvider && validApiTypes.length === 0) {
    return {
      kind: "form-error",
      message: "At least one API type is required",
    };
  }

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
    !hasPersistedChatGPTLogin
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
  const api = useContext(ApiContext);
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
  const [chatGPTStatus, setChatGPTStatus] = useState<string | null>(() =>
    describePersistedChatGPTAccount(initialData?.auth_profile),
  );
  const [chatGPTLoginError, setChatGPTLoginError] = useState<string | null>(
    null,
  );
  const [startingChatGPTLogin, setStartingChatGPTLogin] = useState(false);
  const [chatGPTLoginSession, setChatGPTLoginSession] =
    useState<ChatGPTLoginSession | null>(null);

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

  useEffect(() => {
    if (
      !api ||
      !chatGPTLoginSession ||
      formData.credential_type !== PROVIDER_CREDENTIAL_TYPES.CHATGPT
    ) {
      return;
    }

    const activeLoginID = chatGPTLoginSession.loginId;
    let cancelled = false;
    let timeoutID: number | undefined;

    const scheduleNextPoll = () => {
      timeoutID = window.setTimeout(() => {
        void pollLoginStatus();
      }, CHATGPT_LOGIN_POLL_INTERVAL_MS);
    };

    const pollLoginStatus = async () => {
      try {
        const loginStatus =
          await api.providers.getChatGPTLoginStatus(activeLoginID);
        if (cancelled) {
          return;
        }

        if (loginStatus.status === "completed") {
          setChatGPTLoginSession(null);
          setChatGPTLoginError(null);
          setChatGPTStatus(
            describeConnectedChatGPTAccount(loginStatus.auth_profile),
          );
          setFormData((prev) => ({
            ...prev,
            credential_type: PROVIDER_CREDENTIAL_TYPES.CHATGPT,
            credential_login_id: activeLoginID,
          }));
          return;
        }

        if (loginStatus.status === "expired") {
          setChatGPTLoginSession(null);
          setChatGPTStatus(null);
          setChatGPTLoginError(CHATGPT_LOGIN_EXPIRED_MESSAGE);
          return;
        }

        scheduleNextPoll();
      } catch (err) {
        if (cancelled) {
          return;
        }
        setChatGPTLoginError(
          err instanceof Error
            ? err.message
            : CHATGPT_LOGIN_STATUS_ERROR_MESSAGE,
        );
        scheduleNextPoll();
      }
    };

    void pollLoginStatus();

    return () => {
      cancelled = true;
      if (timeoutID !== undefined) {
        window.clearTimeout(timeoutID);
      }
    };
  }, [api, chatGPTLoginSession, formData.credential_type]);

  const handleStartChatGPTLogin = async () => {
    setStartingChatGPTLogin(true);
    setChatGPTLoginError(null);
    try {
      if (!api) {
        throw new Error("API client is unavailable for GPT login");
      }
      setFormData((prev) => ({ ...prev, credential_login_id: "" }));
      const start = await api.providers.startChatGPTLogin();
      setChatGPTLoginSession({
        loginId: start.login_id,
        authURL: start.auth_url,
      });
      const loginWindow = window.open(
        start.auth_url,
        CHATGPT_LOGIN_WINDOW_TARGET,
        CHATGPT_LOGIN_WINDOW_FEATURES,
      );
      if (loginWindow) {
        loginWindow.focus();
      }
      setChatGPTStatus(CHATGPT_LOGIN_READY_MESSAGE);
    } catch (err) {
      setChatGPTLoginSession(null);
      setChatGPTStatus(null);
      setChatGPTLoginError(
        err instanceof Error ? err.message : CHATGPT_LOGIN_START_ERROR_MESSAGE,
      );
    } finally {
      setStartingChatGPTLogin(false);
    }
  };

  const handleOpenChatGPTLoginPage = () => {
    if (!chatGPTLoginSession?.authURL) {
      return;
    }
    const loginWindow = window.open(
      chatGPTLoginSession.authURL,
      CHATGPT_LOGIN_WINDOW_TARGET,
      CHATGPT_LOGIN_WINDOW_FEATURES,
    );
    loginWindow?.focus();
  };

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    const preparedSubmission = prepareProviderSubmission({
      formData,
      isEditMode,
      hasPersistedChatGPTLogin: Boolean(initialData?.auth_profile?.ready),
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
            authProfile={initialData?.auth_profile}
            onStartChatGPTLogin={handleStartChatGPTLogin}
            onOpenChatGPTLoginPage={handleOpenChatGPTLoginPage}
            chatGPTLoginState={{
              status: chatGPTStatus,
              error: chatGPTLoginError,
              loading: startingChatGPTLogin,
              authURL: chatGPTLoginSession?.authURL ?? null,
            }}
          />
        </form>
      </div>
    </div>
  );
}
