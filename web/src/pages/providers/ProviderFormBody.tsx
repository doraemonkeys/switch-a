import { useState } from "react";
import type { ProviderInput } from "../../api/client";
import {
  FormField,
  ApiTypesField,
  GroupSelectField,
  NumberFieldRow,
  EnabledCheckbox,
  FormActions,
  AuthModeField,
} from "./ProviderFormFields";
import { slugify, isValidId } from "../../lib/utils";

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

  return (
    <>
      {error && (
        <div className="bg-red-500/10 border border-red-500/20 text-red-400 p-3 rounded-lg text-sm">
          {error}
        </div>
      )}
      <FormField label="Name">
        <input
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
      </FormField>
      {!isEditMode && (
        <FormField label="ID">
          <input
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
        </FormField>
      )}
      <FormField label="Base URL">
        <input
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
      </FormField>
      <FormField label="API Key">
        <div className="relative">
          <input
            type={showApiKey ? "text" : "password"}
            className="input pr-10"
            value={formData.api_key}
            onChange={(e) =>
              setFormData((prev) => ({ ...prev, api_key: e.target.value }))
            }
            required
            autoComplete="new-password"
            placeholder="sk-..."
          />
          <button
            type="button"
            onClick={() => setShowApiKey(!showApiKey)}
            className="absolute right-2 top-1/2 -translate-y-1/2 text-text-muted hover:text-text-primary transition-colors p-1"
            title={showApiKey ? "Hide API Key" : "Show API Key"}
          >
            {showApiKey ? (
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
                  d="M13.875 18.825A10.05 10.05 0 0112 19c-4.478 0-8.268-2.943-9.543-7a9.97 9.97 0 011.563-3.029m5.858.908a3 3 0 114.243 4.243M9.878 9.878l4.242 4.242M9.88 9.88l-3.29-3.29m7.532 7.532l3.29 3.29M3 3l3.59 3.59m0 0A9.953 9.953 0 0112 5c4.478 0 8.268 2.943 9.543 7a10.025 10.025 0 01-4.132 5.411m0 0L21 21"
                />
              </svg>
            ) : (
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
                  d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"
                />
                <path
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  strokeWidth={2}
                  d="M2.458 12C3.732 7.943 7.523 5 12 5c4.478 0 8.268 2.943 9.542 7-1.274 4.057-5.064 7-9.542 7-4.477 0-8.268-2.943-9.542-7z"
                />
              </svg>
            )}
          </button>
        </div>
      </FormField>
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
            defaultValue: 0,
            hint: "Lower = higher priority (0 is highest)",
          },
          {
            key: "weight",
            label: "Weight",
            min: 1,
            defaultValue: 1,
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
            defaultValue: 10,
          },
          {
            key: "max_retries",
            label: "Max Retries",
            min: -1,
            defaultValue: 3,
          },
        ]}
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
