import { useState } from "react";
import type { CredentialSession, ProviderAuthView } from "../../api";
import { CopyButton } from "../../components";
import { FormField } from "./FormField";
import {
  ApiTypesField,
  GroupSelectField,
  NumberFieldRow,
  EnabledCheckbox,
  FormActions,
  AuthModeField,
  ApiKeyField,
} from "./BasicFormFields";
import { type ProviderCredentialMode, type ProviderFormData } from "./types";
import { FailoverSection } from "./FailoverFields";
import { hasFailoverConfig } from "./failoverConfig";
import { BackoffSection } from "./BackoffFields";
import { slugify, isValidId } from "../../lib/utils";
import {
  formatProviderPlanType,
  formatProviderUsageWindowSummary,
  resolveProviderPlanType,
  resolveProviderUsage,
} from "../../lib/providerUsage";
import {
  PROVIDER_DEFAULTS,
  PROVIDER_CREDENTIAL_TYPES,
  PROVIDER_CREDENTIAL_TYPE_OPTIONS,
  PROVIDER_USAGE_LIMIT_POLICY_OPTIONS,
  defaultProviderUsageLimitPolicy,
} from "../../config/constants";
import { AUTH_STATUS_BADGE_CLASS } from "../../lib/providerAuth";

export interface FormState {
  data: ProviderFormData;
  setData: React.Dispatch<React.SetStateAction<ProviderFormData>>;
}

export interface IdState {
  manuallyEdited: boolean;
  setManuallyEdited: (value: boolean) => void;
  error: string | null;
  setError: (value: string | null) => void;
}

interface ProviderFormBodyProps {
  formState: FormState;
  idState: IdState;
  error: string | null;
  isEditMode: boolean;
  submitting: boolean;
  submissionBlocked: boolean;
  onCancel: () => void;
  groups: Array<{ id: string; name: string }>;
  credentialSessions: CredentialSession[];
  credentialSessionsLoading: boolean;
  credentialSessionsError: string | null;
  chatGPTCredentialSessionID: string;
  onChatGPTCredentialSessionChange: (sessionID: string) => void;
  authView?: ProviderAuthView | null;
  onStartChatGPTLogin: () => Promise<void>;
  onOpenChatGPTLoginPage: () => void;
  onImportChatGPTLogin: (authData: string) => Promise<boolean>;
  chatGPTLoginState: ChatGPTLoginState;
}

interface ChatGPTLoginState {
  status: string | null;
  error: string | null;
  loading: boolean;
  authURL: string | null;
}

function getChatGPTLoginButtonLabel(
  chatGPTLoginState: ChatGPTLoginState,
  authView?: ProviderAuthView | null,
  reauthenticatesExistingSession = false,
): string {
  if (chatGPTLoginState.loading) {
    return "Preparing...";
  }
  if (chatGPTLoginState.authURL) {
    return "Restart Sign-In";
  }
  if (reauthenticatesExistingSession) {
    return "Reconnect GPT";
  }
  if (authView?.status === "active" || authView?.status === "reauth_required") {
    return "Reconnect GPT";
  }
  return "Start Sign-In";
}

function AuthStatusBadge({ status }: { status: ProviderAuthView["status"] }) {
  return (
    <span
      className={`inline-flex items-center rounded-full px-2 py-0.5 text-[11px] font-semibold tracking-wide ${AUTH_STATUS_BADGE_CLASS[status]}`}
    >
      {status}
    </span>
  );
}

function CredentialTypeField({
  value,
  onChange,
  locked = false,
}: {
  value: ProviderCredentialMode;
  onChange: (value: ProviderCredentialMode) => void;
  locked?: boolean;
}) {
  return (
    <FormField label="Credential Type">
      {(id) => (
        <>
          {locked ? (
            <input
              id={id}
              className="input"
              readOnly
              value="Mixed route credentials"
            />
          ) : (
            <select
              id={id}
              className="input"
              value={value}
              onChange={(e) =>
                onChange(e.target.value as ProviderCredentialMode)
              }
            >
              {value === "mixed" && (
                <option value="mixed">Mixed route credentials</option>
              )}
              {PROVIDER_CREDENTIAL_TYPE_OPTIONS.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </select>
          )}
          <p className="text-xs text-text-muted mt-1">
            {locked
              ? "Mixed credentials are edited per route so changing one session cannot remove unrelated routes."
              : PROVIDER_CREDENTIAL_TYPE_OPTIONS.find(
                  (option) => option.value === value,
                )?.description}
          </p>
        </>
      )}
    </FormField>
  );
}

function UsageLimitPolicyField({
  value,
  onChange,
}: {
  value: ProviderFormData["usage_limit_policy"];
  onChange: (value: ProviderFormData["usage_limit_policy"]) => void;
}) {
  return (
    <FormField label="Usage Limit Policy">
      {(id) => (
        <>
          <select
            id={id}
            className="input"
            value={value}
            onChange={(e) =>
              onChange(e.target.value as ProviderFormData["usage_limit_policy"])
            }
          >
            {PROVIDER_USAGE_LIMIT_POLICY_OPTIONS.map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
              </option>
            ))}
          </select>
          <p className="text-xs text-text-muted mt-1">
            {
              PROVIDER_USAGE_LIMIT_POLICY_OPTIONS.find(
                (option) => option.value === value,
              )?.description
            }
          </p>
        </>
      )}
    </FormField>
  );
}

function ChatGPTTokenImport({
  onImport,
  disabled,
}: {
  onImport: (authData: string) => Promise<boolean>;
  disabled: boolean;
}) {
  const [authData, setAuthData] = useState("");
  const [importing, setImporting] = useState(false);
  const trimmed = authData.trim();
  const importDisabled = disabled || importing || !trimmed;

  const handleImport = async () => {
    if (importDisabled) {
      return;
    }
    setImporting(true);
    try {
      // Clear only on success so a failed paste stays editable for correction.
      if (await onImport(trimmed)) {
        setAuthData("");
      }
    } finally {
      setImporting(false);
    }
  };

  return (
    <div className="rounded-lg border border-border/70 bg-bg-primary/40 p-3 space-y-2">
      <FormField label="Import via token">
        {(id) => (
          <textarea
            id={id}
            className="input font-mono text-xs"
            rows={4}
            value={authData}
            onChange={(e) => setAuthData(e.target.value)}
            placeholder="Paste Codex auth.json, a token JSON, or chatgpt.com /api/auth/session output"
            spellCheck={false}
            autoComplete="off"
          />
        )}
      </FormField>
      <p className="text-xs text-text-muted">
        Connect an existing GPT account without opening the sign-in page. The
        pasted credential must include a refresh token.
      </p>
      <div className="flex justify-end">
        <button
          type="button"
          onClick={() => void handleImport()}
          disabled={importDisabled}
          className={`btn btn-secondary ${importDisabled ? "opacity-60 cursor-not-allowed" : ""}`}
        >
          {importing ? "Importing..." : "Import Token"}
        </button>
      </div>
    </div>
  );
}

function ChatGPTLoginSection({
  authView,
  chatGPTLoginState,
  onStartChatGPTLogin,
  onOpenChatGPTLoginPage,
  onImportChatGPTLogin,
  reauthenticatesExistingSession = false,
}: {
  authView?: ProviderAuthView | null;
  chatGPTLoginState: ChatGPTLoginState;
  onStartChatGPTLogin: () => Promise<void>;
  onOpenChatGPTLoginPage: () => void;
  onImportChatGPTLogin: (authData: string) => Promise<boolean>;
  reauthenticatesExistingSession?: boolean;
}) {
  const authPlanType = resolveProviderPlanType(authView);
  const authUsage = resolveProviderUsage(authView);

  return (
    <section className="rounded-xl border border-border/70 bg-bg-secondary/40 p-4 space-y-3">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h4 className="text-sm font-semibold text-text-primary">GPT Login</h4>
          <p className="text-xs text-text-muted mt-1">
            {reauthenticatesExistingSession
              ? "Reconnect this credential session in place. Every route sharing it recovers together, while provider route bindings stay unchanged. A different GPT account is rejected."
              : "Switch-A will create a Codex-only provider backed by a local ChatGPT OAuth session. The sign-in link can be completed in any browser on this machine."}
          </p>
        </div>
        <button
          type="button"
          onClick={() => void onStartChatGPTLogin()}
          disabled={chatGPTLoginState.loading}
          className={`btn btn-secondary ${chatGPTLoginState.loading ? "opacity-60 cursor-wait" : ""}`}
        >
          {getChatGPTLoginButtonLabel(
            chatGPTLoginState,
            authView,
            reauthenticatesExistingSession,
          )}
        </button>
      </div>
      <ChatGPTTokenImport
        onImport={onImportChatGPTLogin}
        disabled={chatGPTLoginState.loading}
      />
      {chatGPTLoginState.authURL && (
        <div className="rounded-lg border border-border/70 bg-bg-primary/40 p-3 space-y-3">
          <p className="text-xs text-text-muted">
            This sign-in link can be opened in any browser on this machine.
            Switch-A tracks the login session by ID and updates this form
            automatically after OAuth completes.
          </p>
          <div className="flex flex-col gap-2">
            <input
              aria-label="GPT sign-in link"
              readOnly
              value={chatGPTLoginState.authURL}
              className="input font-mono text-xs"
            />
            <div className="flex items-center justify-between gap-3 flex-wrap">
              <button
                type="button"
                onClick={onOpenChatGPTLoginPage}
                className="btn btn-secondary"
              >
                Open Sign-In Page
              </button>
              <CopyButton text={chatGPTLoginState.authURL} />
            </div>
          </div>
        </div>
      )}
      {authView && (
        <div className="rounded-lg border border-border/70 bg-bg-primary/50 p-3 text-xs text-text-secondary space-y-2">
          <div className="flex items-center justify-between gap-3">
            <span className="font-medium text-text-primary">Auth Snapshot</span>
            <AuthStatusBadge status={authView.status} />
          </div>
          {authView.reason && <p>Reason: {authView.reason}</p>}
          {authView.email && <p>Account: {authView.email}</p>}
          {authView.account_id && <p>Workspace: {authView.account_id}</p>}
          {authPlanType && <p>Plan: {formatProviderPlanType(authPlanType)}</p>}
          {authUsage && (
            <p>
              Usage:{" "}
              {formatProviderUsageWindowSummary("5h", authUsage.five_hour)}
              {" / "}
              {formatProviderUsageWindowSummary("1w", authUsage.one_week)}
            </p>
          )}
          {authView.expires_at && (
            <p>
              Token expiry: {new Date(authView.expires_at).toLocaleString()}
            </p>
          )}
          {authView.last_refresh_at && (
            <p>
              Last refresh:{" "}
              {new Date(authView.last_refresh_at).toLocaleString()}
            </p>
          )}
          {authView.last_error && <p>Last error: {authView.last_error}</p>}
        </div>
      )}
      {chatGPTLoginState.status && (
        <p className="text-xs text-success">{chatGPTLoginState.status}</p>
      )}
      {chatGPTLoginState.error && (
        <p className="text-xs text-danger">{chatGPTLoginState.error}</p>
      )}
    </section>
  );
}

function ChatGPTCredentialSessionField({
  sessions,
  value,
  onChange,
  loading,
  error,
}: {
  sessions: CredentialSession[];
  value: string;
  onChange: (sessionID: string) => void;
  loading: boolean;
  error: string | null;
}) {
  const chatGPTSessions = sessions.filter(
    (session) => session.kind === PROVIDER_CREDENTIAL_TYPES.CHATGPT,
  );
  return (
    <FormField label="Credential Session">
      {(id) => (
        <>
          <select
            id={id}
            className="input"
            value={value}
            onChange={(event) => onChange(event.target.value)}
          >
            <option value="">Create from GPT login below</option>
            {chatGPTSessions.map((session) => (
              <option key={session.id} value={session.id}>
                {session.name} · {session.auth_state.status} ·{" "}
                {session.route_references.length} route
                {session.route_references.length === 1 ? "" : "s"}
              </option>
            ))}
          </select>
          {loading && (
            <p className="text-xs text-text-muted mt-1">
              Loading credential sessions...
            </p>
          )}
          {error && <p className="text-xs text-danger mt-1">{error}</p>}
          {!loading && !error && (
            <p className="text-xs text-text-muted mt-1">
              Select a reusable GPT session, or complete a new login below.
            </p>
          )}
        </>
      )}
    </FormField>
  );
}

function hasUnboundCredentialRoute(formData: ProviderFormData): boolean {
  return formData.api_types.some((entry) => !entry.credential_session_id);
}

type ProviderCredentialFieldsProps = Pick<
  ProviderFormBodyProps,
  | "isEditMode"
  | "credentialSessions"
  | "credentialSessionsLoading"
  | "credentialSessionsError"
  | "chatGPTCredentialSessionID"
  | "onChatGPTCredentialSessionChange"
  | "authView"
  | "onStartChatGPTLogin"
  | "onOpenChatGPTLoginPage"
  | "onImportChatGPTLogin"
  | "chatGPTLoginState"
> & { formState: FormState };

function ProviderCredentialFields({
  formState,
  isEditMode,
  credentialSessions,
  credentialSessionsLoading,
  credentialSessionsError,
  chatGPTCredentialSessionID,
  onChatGPTCredentialSessionChange,
  authView,
  onStartChatGPTLogin,
  onOpenChatGPTLoginPage,
  onImportChatGPTLogin,
  chatGPTLoginState,
}: ProviderCredentialFieldsProps) {
  const { data: formData, setData: setFormData } = formState;
  const [showApiKey, setShowApiKey] = useState(false);
  const isChatGPTProvider =
    formData.credential_mode === PROVIDER_CREDENTIAL_TYPES.CHATGPT;
  const isMixedCredentialProvider = formData.credential_mode === "mixed";
  const reauthenticatesExistingSession = Boolean(chatGPTCredentialSessionID);

  const handleApiTypesChange = (entries: ProviderFormData["api_types"]) => {
    if (isMixedCredentialProvider) {
      const previousSessionID =
        formData.api_types.find((entry) => entry.api_type === "codex")
          ?.credential_session_id ?? "";
      const nextSessionID =
        entries.find((entry) => entry.api_type === "codex")
          ?.credential_session_id ?? "";
      if (nextSessionID !== previousSessionID) {
        onChatGPTCredentialSessionChange(nextSessionID);
      }
    }
    setFormData((previous) => ({ ...previous, api_types: entries }));
  };

  return (
    <>
      <CredentialTypeField
        value={formData.credential_mode}
        onChange={(credentialMode) =>
          setFormData((previous) => ({
            ...previous,
            credential_mode: credentialMode,
          }))
        }
        locked={isEditMode && isMixedCredentialProvider}
      />
      {isChatGPTProvider ? (
        <>
          <ChatGPTCredentialSessionField
            sessions={credentialSessions}
            value={chatGPTCredentialSessionID}
            onChange={onChatGPTCredentialSessionChange}
            loading={credentialSessionsLoading}
            error={credentialSessionsError}
          />
          <ChatGPTLoginSection
            authView={authView}
            chatGPTLoginState={chatGPTLoginState}
            onStartChatGPTLogin={onStartChatGPTLogin}
            onOpenChatGPTLoginPage={onOpenChatGPTLoginPage}
            onImportChatGPTLogin={onImportChatGPTLogin}
            reauthenticatesExistingSession={reauthenticatesExistingSession}
          />
        </>
      ) : (
        <>
          {isMixedCredentialProvider && chatGPTCredentialSessionID && (
            <ChatGPTLoginSection
              authView={authView}
              chatGPTLoginState={chatGPTLoginState}
              onStartChatGPTLogin={onStartChatGPTLogin}
              onOpenChatGPTLoginPage={onOpenChatGPTLoginPage}
              onImportChatGPTLogin={onImportChatGPTLogin}
              reauthenticatesExistingSession
            />
          )}
          {formData.credential_mode === PROVIDER_CREDENTIAL_TYPES.API_KEY &&
            (!isEditMode || hasUnboundCredentialRoute(formData)) && (
              <ApiKeyField
                value={formData.new_shared_api_key}
                onChange={(value) =>
                  setFormData((previous) => ({
                    ...previous,
                    new_shared_api_key: value,
                  }))
                }
                showApiKey={showApiKey}
                onToggleVisibility={() => setShowApiKey(!showApiKey)}
              />
            )}
          <ApiTypesField
            entries={formData.api_types}
            onChange={handleApiTypesChange}
            credentialSessions={credentialSessions}
            credentialMode={formData.credential_mode}
          />
          <AuthModeField
            value={formData.auth_mode || "auto"}
            onChange={(value) =>
              setFormData((previous) => ({ ...previous, auth_mode: value }))
            }
          />
        </>
      )}
    </>
  );
}

export function ProviderFormBody({
  formState,
  idState,
  error,
  isEditMode,
  submitting,
  submissionBlocked,
  onCancel,
  groups,
  credentialSessions,
  credentialSessionsLoading,
  credentialSessionsError,
  chatGPTCredentialSessionID,
  onChatGPTCredentialSessionChange,
  authView,
  onStartChatGPTLogin,
  onOpenChatGPTLoginPage,
  onImportChatGPTLogin,
  chatGPTLoginState,
}: ProviderFormBodyProps) {
  const { data: formData, setData: setFormData } = formState;
  const {
    manuallyEdited: idManuallyEdited,
    setManuallyEdited: setIdManuallyEdited,
    error: idError,
    setError: setIdError,
  } = idState;
  const [failoverExpanded, setFailoverExpanded] = useState(
    hasFailoverConfig(formData),
  );
  // Auto-expand backoff section when max_retries > 0 and backoff is configured
  const [backoffExpanded, setBackoffExpanded] = useState(
    Boolean(
      formData.max_retries && formData.max_retries > 0 && formData.backoff,
    ),
  );

  const effectiveUsageLimitPolicy =
    formData.usage_limit_policy || defaultProviderUsageLimitPolicy();

  return (
    <>
      {error && (
        <div className="bg-red-500/10 border border-red-500/20 text-red-400 p-3 rounded-lg text-sm">
          {error}
        </div>
      )}
      <FormField label="Name">
        {(id) => (
          <input
            id={id}
            type="text"
            className="input"
            value={formData.name}
            onChange={(e) => {
              const newName = e.target.value;
              setFormData((prev) => {
                // Auto-generate ID from name if not in edit mode and ID hasn't been manually edited
                if (!isEditMode && !idManuallyEdited) {
                  return { ...prev, name: newName, id: slugify(newName) };
                }
                return { ...prev, name: newName };
              });
            }}
            required
            autoComplete="off"
            placeholder="e.g., OpenAI Production"
          />
        )}
      </FormField>
      {!isEditMode && (
        <FormField label="ID">
          {(id) => (
            <>
              <input
                id={id}
                type="text"
                className={`input ${idError ? "border-red-500 focus:border-red-500" : ""}`}
                value={formData.id}
                onChange={(e) => {
                  const newId = e.target.value;
                  // Only mark as manually edited if user types something different from auto-generated
                  const autoId = slugify(formData.name);
                  if (newId !== autoId) {
                    setIdManuallyEdited(true);
                  } else {
                    setIdManuallyEdited(false);
                  }
                  setFormData((prev) => ({ ...prev, id: newId }));
                  // Validate ID and show error if invalid
                  if (newId && !isValidId(newId)) {
                    setIdError(
                      "ID can only contain lowercase letters, numbers, and hyphens",
                    );
                  } else {
                    setIdError(null);
                  }
                }}
                required
                autoComplete="off"
                placeholder="Auto-generated: name-random"
              />
              {idError ? (
                <p className="text-xs text-red-400 mt-1">{idError}</p>
              ) : (
                <p className="text-xs text-text-muted mt-1">
                  Auto-generated from Name + random ID. Only lowercase letters,
                  numbers, and hyphens allowed.
                </p>
              )}
            </>
          )}
        </FormField>
      )}
      <ProviderCredentialFields
        formState={formState}
        isEditMode={isEditMode}
        credentialSessions={credentialSessions}
        credentialSessionsLoading={credentialSessionsLoading}
        credentialSessionsError={credentialSessionsError}
        chatGPTCredentialSessionID={chatGPTCredentialSessionID}
        onChatGPTCredentialSessionChange={onChatGPTCredentialSessionChange}
        authView={authView}
        onStartChatGPTLogin={onStartChatGPTLogin}
        onOpenChatGPTLoginPage={onOpenChatGPTLoginPage}
        onImportChatGPTLogin={onImportChatGPTLogin}
        chatGPTLoginState={chatGPTLoginState}
      />
      <UsageLimitPolicyField
        value={effectiveUsageLimitPolicy}
        onChange={(value) =>
          setFormData((prev) => ({ ...prev, usage_limit_policy: value }))
        }
      />
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
          {
            key: "priority",
            label: "Priority",
            min: 0,
            defaultValue: PROVIDER_DEFAULTS.PRIORITY,
            hint: "Lower = higher priority (0 is highest)",
          },
          {
            key: "weight",
            label: "Weight",
            min: 1,
            defaultValue: PROVIDER_DEFAULTS.WEIGHT,
            hint: "Higher = more traffic (for weight strategy)",
          },
        ]}
      />
      <NumberFieldRow
        formData={formData}
        setFormData={setFormData}
        fields={[
          {
            key: "concurrency",
            label: "Concurrency Limit",
            min: 0,
            defaultValue: PROVIDER_DEFAULTS.CONCURRENCY,
            hint: "Max concurrent requests (0 = unlimited)",
          },
          {
            key: "max_retries",
            label: "Max Retries",
            min: 0,
            defaultValue: PROVIDER_DEFAULTS.MAX_RETRIES,
            hint: "Retries per provider (0 = no retry, switch immediately)",
          },
        ]}
      />
      <BackoffSection
        formData={formData}
        setFormData={setFormData}
        expanded={backoffExpanded}
        onToggle={() => setBackoffExpanded(!backoffExpanded)}
      />
      <FailoverSection
        formData={formData}
        setFormData={setFormData}
        expanded={failoverExpanded}
        onToggle={() => setFailoverExpanded(!failoverExpanded)}
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
        submissionBlocked={submissionBlocked}
        onCancel={onCancel}
      />
    </>
  );
}
