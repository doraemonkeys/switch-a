import { useState } from "react";
import type { ProviderInput } from "../../api";
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
import { FailoverSection } from "./FailoverFields";
import { hasFailoverConfig } from "./failoverConfig";
import { BackoffSection } from "./BackoffFields";
import { slugify, isValidId } from "../../lib/utils";
import { PROVIDER_DEFAULTS } from "../../config/constants";

export interface FormState {
  data: ProviderInput;
  setData: React.Dispatch<React.SetStateAction<ProviderInput>>;
  apiTypesInput: string;
  setApiTypesInput: (value: string) => void;
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
}

export function ProviderFormBody({
  formState,
  idState,
  error,
  isEditMode,
  submitting,
  onCancel,
  groups,
}: ProviderFormBodyProps) {
  const {
    data: formData,
    setData: setFormData,
    apiTypesInput,
    setApiTypesInput,
  } = formState;
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
      <FormField label="Base URL">
        {(id) => (
          <input
            id={id}
            type="url"
            className="input"
            value={formData.base_url}
            onChange={(e) =>
              setFormData((prev) => ({ ...prev, base_url: e.target.value }))
            }
            required
            autoComplete="off"
            placeholder="e.g., https://api.openai.com"
          />
        )}
      </FormField>
      <ApiKeyField
        value={formData.api_key}
        onChange={(value) =>
          setFormData((prev) => ({ ...prev, api_key: value }))
        }
        showApiKey={showApiKey}
        onToggleVisibility={() => setShowApiKey(!showApiKey)}
      />
      <ApiTypesField value={apiTypesInput} onChange={setApiTypesInput} />
      <AuthModeField
        value={formData.auth_mode || "auto"}
        onChange={(value) =>
          setFormData((prev) => ({ ...prev, auth_mode: value }))
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
        onCancel={onCancel}
      />
    </>
  );
}
