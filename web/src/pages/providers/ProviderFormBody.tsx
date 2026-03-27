import { useState } from "react";
import type { ProviderAuthView, ProviderInput } from "../../api";
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
import { generateClientKey, type TrackedAPITypeEntry } from "./types";
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
} from "../../config/constants";
import { AUTH_STATUS_BADGE_CLASS } from "../../lib/providerAuth";

export interface FormState {
  data: ProviderInput;
  setData: React.Dispatch<React.SetStateAction<ProviderInput>>;
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
  onCancel: () => void;
  groups: Array<{ id: string; name: string }>;
  authView?: ProviderAuthView | null;
  onStartChatGPTLogin: () => Promise<void>;
  onOpenChatGPTLoginPage: () => void;
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
): string {
  if (chatGPTLoginState.loading) {
    return "Preparing...";
  }
  if (chatGPTLoginState.authURL) {
    return "Restart Sign-In";
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

function ChatGPTLoginSection({
  authView,
  chatGPTLoginState,
  onStartChatGPTLogin,
  onOpenChatGPTLoginPage,
}: {
  authView?: ProviderAuthView | null;
  chatGPTLoginState: ChatGPTLoginState;
  onStartChatGPTLogin: () => Promise<void>;
  onOpenChatGPTLoginPage: () => void;
}) {
  const authPlanType = resolveProviderPlanType(authView);
  const authUsage = resolveProviderUsage(authView);

  return (
    <section className="rounded-xl border border-border/70 bg-bg-secondary/40 p-4 space-y-3">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h4 className="text-sm font-semibold text-text-primary">GPT Login</h4>
          <p className="text-xs text-text-muted mt-1">
            Switch-A will create a Codex-only provider backed by a local ChatGPT
            OAuth session. The sign-in link can be completed in any browser on
            this machine.
          </p>
        </div>
        <button
          type="button"
          onClick={() => void onStartChatGPTLogin()}
          disabled={chatGPTLoginState.loading}
          className={`btn btn-secondary ${chatGPTLoginState.loading ? "opacity-60 cursor-wait" : ""}`}
        >
          {getChatGPTLoginButtonLabel(chatGPTLoginState, authView)}
        </button>
      </div>
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

export function ProviderFormBody({
  formState,
  idState,
  error,
  isEditMode,
  submitting,
  onCancel,
  groups,
  authView,
  onStartChatGPTLogin,
  onOpenChatGPTLoginPage,
  chatGPTLoginState,
}: ProviderFormBodyProps) {
  const { data: formData, setData: setFormData } = formState;
  const {
    manuallyEdited: idManuallyEdited,
    setManuallyEdited: setIdManuallyEdited,
    error: idError,
    setError: setIdError,
  } = idState;
  const [showApiKey, setShowApiKey] = useState(false);
  const [failoverExpanded, setFailoverExpanded] = useState(
    hasFailoverConfig(formData),
  );
  // Auto-expand backoff section when max_retries > 0 and backoff is configured
  const [backoffExpanded, setBackoffExpanded] = useState(
    Boolean(
      formData.max_retries && formData.max_retries > 0 && formData.backoff,
    ),
  );

  // Tracked entries carry stable client-side keys so React can reconcile
  // rows correctly when entries are added/removed from the middle.
  const [trackedEntries, setTrackedEntries] = useState<TrackedAPITypeEntry[]>(
    () =>
      formData.api_types.map((t) => ({
        clientKey: generateClientKey(),
        data: t,
      })),
  );

  const handleApiTypesChange = (entries: TrackedAPITypeEntry[]) => {
    setTrackedEntries(entries);
    setFormData((prev) => ({ ...prev, api_types: entries.map((e) => e.data) }));
  };
  const isChatGPTProvider =
    formData.credential_type === PROVIDER_CREDENTIAL_TYPES.CHATGPT;

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
      <FormField label="Credential Type">
        {(id) => (
          <>
            <select
              id={id}
              className="input"
              value={
                formData.credential_type || PROVIDER_CREDENTIAL_TYPES.API_KEY
              }
              onChange={(e) =>
                setFormData((prev) => ({
                  ...prev,
                  credential_type: e.target
                    .value as ProviderInput["credential_type"],
                }))
              }
            >
              {PROVIDER_CREDENTIAL_TYPE_OPTIONS.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </select>
            <p className="text-xs text-text-muted mt-1">
              {
                PROVIDER_CREDENTIAL_TYPE_OPTIONS.find(
                  (option) => option.value === formData.credential_type,
                )?.description
              }
            </p>
          </>
        )}
      </FormField>
      {isChatGPTProvider ? (
        <ChatGPTLoginSection
          authView={authView}
          chatGPTLoginState={chatGPTLoginState}
          onStartChatGPTLogin={onStartChatGPTLogin}
          onOpenChatGPTLoginPage={onOpenChatGPTLoginPage}
        />
      ) : (
        <>
          <ApiKeyField
            value={formData.api_key}
            onChange={(value) =>
              setFormData((prev) => ({ ...prev, api_key: value }))
            }
            showApiKey={showApiKey}
            onToggleVisibility={() => setShowApiKey(!showApiKey)}
          />
          <ApiTypesField
            entries={trackedEntries}
            onChange={handleApiTypesChange}
          />
          <AuthModeField
            value={formData.auth_mode || "auto"}
            onChange={(value) =>
              setFormData((prev) => ({ ...prev, auth_mode: value }))
            }
          />
        </>
      )}
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
        onCancel={onCancel}
      />
    </>
  );
}
