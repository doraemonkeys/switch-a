import type { FormEvent } from "react";
import type { Provider, RoutingPolicy } from "../../api";
import { useAPICatalog } from "../../api/useApi";
import { ProviderBadge } from "./badges";
import { getProviderOptionLabel } from "./providerLabels";
import {
  EMPTY_MODEL_MATCH,
  EMPTY_PROVIDER,
  MODEL_MATCH_OPTIONS,
  STALE_VENDOR_HELP_MESSAGE,
  TARGET_MODE_EXACT_PROVIDER,
  TARGET_MODE_OPTIONS,
  buildDraftWithAPIType,
  buildDraftWithTargetMode,
  buildPayload,
  createPolicyKey,
  getCompatibleProviders,
  getExactProviderPlaceholder,
  getProviderDerivedVendors,
  getVendorSelectionMessage,
  toggleStringSelection,
  type RoutingPolicyDraft,
} from "./shared";

interface RoutingPolicyEditorProps {
  draft: RoutingPolicyDraft;
  groups: Array<{ id: string; name: string }>;
  providers: Provider[];
  groupNameById: Map<string, string>;
  providersLoading: boolean;
  providersError: Error | null;
  error: string | null;
  saving: boolean;
  editingPolicy: RoutingPolicy | null;
  onChange: (draft: RoutingPolicyDraft) => void;
  onCancel: () => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
}

function RoutingPolicyCoreFields({
  draft,
  providers,
  selectedModelHint,
  onChange,
}: {
  draft: RoutingPolicyDraft;
  providers: Provider[];
  selectedModelHint?: string;
  onChange: (draft: RoutingPolicyDraft) => void;
}) {
  const { catalog, loading, error, refetch } = useAPICatalog();
  let apiCatalogGuidance = (
    <p className="text-xs text-text-muted">
      Requests must match this exact upstream API contract.
    </p>
  );
  if (loading) {
    apiCatalogGuidance = (
      <p className="text-xs text-text-muted" role="status">
        Loading built-in API types...
      </p>
    );
  } else if (error) {
    apiCatalogGuidance = (
      <button
        type="button"
        onClick={() => void refetch()}
        className="text-xs text-danger hover:underline cursor-pointer"
        title={error.message}
      >
        Retry API type catalog
      </button>
    );
  }

  return (
    <div className="grid grid-cols-1 xl:grid-cols-4 gap-4">
      <label className="space-y-2">
        <span className="text-sm font-medium text-text-primary">API Type</span>
        <input
          list="routing-policy-api-types"
          className="input"
          aria-label="API Type"
          value={draft.api_type}
          onChange={(event) =>
            onChange(
              buildDraftWithAPIType(draft, providers, event.target.value),
            )
          }
          placeholder="codex"
        />
        <datalist id="routing-policy-api-types">
          {catalog?.api_types.map((entry) => (
            <option key={entry.api_type} value={entry.api_type}>
              {entry.label}
            </option>
          ))}
        </datalist>
        {apiCatalogGuidance}
      </label>

      <label className="space-y-2">
        <span className="text-sm font-medium text-text-primary">
          Model Match
        </span>
        <select
          className="input"
          aria-label="Model Match"
          value={draft.model_match_type}
          onChange={(event) =>
            onChange({
              ...draft,
              model_match_type: event.target
                .value as RoutingPolicyDraft["model_match_type"],
              model_match_value:
                event.target.value === EMPTY_MODEL_MATCH
                  ? ""
                  : draft.model_match_value,
            })
          }
        >
          {MODEL_MATCH_OPTIONS.map((option) => (
            <option key={option.value || "any"} value={option.value}>
              {option.label}
            </option>
          ))}
        </select>
        <p className="text-xs text-text-muted">{selectedModelHint}</p>
      </label>

      <label className="space-y-2">
        <span className="text-sm font-medium text-text-primary">
          Model Value
        </span>
        <input
          className="input"
          aria-label="Model Value"
          value={draft.model_match_value}
          onChange={(event) =>
            onChange({
              ...draft,
              model_match_value: event.target.value,
            })
          }
          disabled={draft.model_match_type === EMPTY_MODEL_MATCH}
          placeholder={
            draft.model_match_type === "prefix" ? "gpt-5" : "gpt-5.1-codex"
          }
        />
        <p className="text-xs text-text-muted">
          Leave model matching off when the request model is unknown at
          selection time.
        </p>
      </label>

      <label className="rounded-xl border border-border/70 bg-bg-secondary/30 p-4 flex items-start gap-3">
        <input
          type="checkbox"
          aria-label="Enabled"
          checked={draft.enabled}
          onChange={(event) =>
            onChange({
              ...draft,
              enabled: event.target.checked,
            })
          }
          className="mt-1"
        />
        <span className="space-y-1">
          <span className="block text-sm font-semibold text-text-primary">
            Enabled
          </span>
          <span className="block text-xs text-text-muted">
            Disabled rules stay editable and keep their natural-key slot, but
            runtime selection ignores them until re-enabled.
          </span>
        </span>
      </label>
    </div>
  );
}

function RoutingPolicyTargetModeSection({
  draft,
  onChange,
}: {
  draft: RoutingPolicyDraft;
  onChange: (draft: RoutingPolicyDraft) => void;
}) {
  return (
    <section className="rounded-xl border border-border/70 bg-bg-secondary/30 p-4 space-y-3">
      <div>
        <h4 className="text-sm font-semibold text-text-primary">Target Mode</h4>
        <p className="text-xs text-text-muted mt-1">
          Exact-provider and filter mode are mutually exclusive because they
          describe different scopes, not optional flags on the same scope.
        </p>
      </div>
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-3">
        {TARGET_MODE_OPTIONS.map((option) => (
          <label
            key={option.value}
            className={`rounded-xl border px-4 py-3 cursor-pointer transition-colors ${
              draft.target_mode === option.value
                ? "border-primary/25 bg-primary-light/40"
                : "border-border/60 bg-white"
            }`}
          >
            <div className="flex items-start gap-3">
              <input
                type="radio"
                name="routing-policy-target-mode"
                value={option.value}
                checked={draft.target_mode === option.value}
                onChange={() =>
                  onChange(buildDraftWithTargetMode(draft, option.value))
                }
                className="mt-1"
              />
              <span className="space-y-1">
                <span className="block text-sm font-semibold text-text-primary">
                  {option.label}
                </span>
                <span className="block text-xs text-text-muted">
                  {option.hint}
                </span>
              </span>
            </div>
          </label>
        ))}
      </div>
    </section>
  );
}

function ExactProviderTargetSection({
  draft,
  compatibleProviders,
  groupNameById,
  providersLoading,
  selectedProvider,
  showSelectedProviderFallback,
  onChange,
}: {
  draft: RoutingPolicyDraft;
  compatibleProviders: Provider[];
  groupNameById: Map<string, string>;
  providersLoading: boolean;
  selectedProvider: Provider | null;
  showSelectedProviderFallback: boolean;
  onChange: (draft: RoutingPolicyDraft) => void;
}) {
  const placeholder = getExactProviderPlaceholder(
    draft.api_type,
    providersLoading,
  );

  return (
    <section className="rounded-xl border border-border/70 bg-bg-secondary/30 p-4 space-y-3">
      <div>
        <h4 className="text-sm font-semibold text-text-primary">
          Exact Provider
        </h4>
        <p className="text-xs text-text-muted mt-1">
          The selected provider must support the current API type. Group and
          vendor filters are cleared for this mode.
        </p>
      </div>

      <label className="space-y-2 block">
        <span className="text-sm font-medium text-text-primary">
          Exact Provider
        </span>
        <select
          className="input"
          aria-label="Exact Provider"
          value={draft.target_provider_id}
          onChange={(event) =>
            onChange({
              ...draft,
              target_provider_id: event.target.value,
            })
          }
          disabled={providersLoading}
        >
          <option value={EMPTY_PROVIDER}>{placeholder}</option>
          {compatibleProviders.map((provider) => (
            <option key={provider.id} value={provider.id}>
              {getProviderOptionLabel(provider, groupNameById)}
            </option>
          ))}
          {showSelectedProviderFallback && draft.target_provider_id && (
            <option value={draft.target_provider_id}>
              {selectedProvider
                ? `${selectedProvider.name} (currently incompatible)`
                : `${draft.target_provider_id} (unavailable)`}
            </option>
          )}
        </select>
      </label>

      {draft.api_type.trim() &&
        compatibleProviders.length === 0 &&
        !providersLoading && (
          <p className="text-sm text-text-muted">
            No providers currently advertise this API type.
          </p>
        )}

      {selectedProvider && (
        <div className="rounded-lg border border-border/60 bg-white px-4 py-3">
          <p className="text-xs uppercase tracking-wide text-text-muted">
            Selected provider
          </p>
          <div className="mt-2">
            <ProviderBadge
              providerId={selectedProvider.id}
              providerById={new Map([[selectedProvider.id, selectedProvider]])}
              groupNameById={groupNameById}
            />
          </div>
        </div>
      )}
    </section>
  );
}

function FilterTargetSection({
  draft,
  groups,
  visibleVendors,
  availableVendorSet,
  staleVendors,
  providersLoading,
  onChange,
}: {
  draft: RoutingPolicyDraft;
  groups: Array<{ id: string; name: string }>;
  visibleVendors: string[];
  availableVendorSet: Set<string>;
  staleVendors: string[];
  providersLoading: boolean;
  onChange: (draft: RoutingPolicyDraft) => void;
}) {
  const vendorSelectionMessage = getVendorSelectionMessage(
    draft.api_type,
    providersLoading,
    visibleVendors,
  );

  return (
    <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
      <section className="rounded-xl border border-border/70 bg-bg-secondary/30 p-4 space-y-3">
        <div>
          <h4 className="text-sm font-semibold text-text-primary">
            Allowed Groups
          </h4>
          <p className="text-xs text-text-muted mt-1">
            Group filters intersect with vendor filters. Leave groups empty when
            vendor filtering alone is sufficient.
          </p>
        </div>
        {groups.length === 0 ? (
          <p className="text-sm text-text-muted">No groups configured yet.</p>
        ) : (
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
            {groups.map((group) => {
              const checked = draft.allowed_group_ids.includes(group.id);
              return (
                <label
                  key={group.id}
                  className="flex items-center gap-2 rounded-lg border border-border/60 bg-white px-3 py-2 text-sm text-text-secondary"
                >
                  <input
                    type="checkbox"
                    aria-label={group.name}
                    checked={checked}
                    onChange={(event) =>
                      onChange({
                        ...draft,
                        allowed_group_ids: toggleStringSelection(
                          draft.allowed_group_ids,
                          group.id,
                          event.target.checked,
                        ),
                      })
                    }
                  />
                  <span>{group.name}</span>
                </label>
              );
            })}
          </div>
        )}
      </section>

      <section className="rounded-xl border border-border/70 bg-bg-secondary/30 p-4 space-y-3">
        <div>
          <h4 className="text-sm font-semibold text-text-primary">
            Allowed Vendors
          </h4>
          <p className="text-xs text-text-muted mt-1">
            Vendor choices come from current providers for the selected API
            type. Persisted stale vendors stay visible until removed so the UI
            never mutates scope silently.
          </p>
        </div>

        {vendorSelectionMessage ? (
          <p className="text-sm text-text-muted">{vendorSelectionMessage}</p>
        ) : (
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
            {visibleVendors.map((vendor) => {
              const checked = draft.allowed_vendors.includes(vendor);
              const stale = !availableVendorSet.has(vendor);

              return (
                <label
                  key={vendor}
                  className={`rounded-lg border px-3 py-2 text-sm ${
                    stale
                      ? "border-warning/20 bg-warning-light/40 text-warning-dark"
                      : "border-border/60 bg-white text-text-secondary"
                  }`}
                >
                  <span className="flex items-start gap-2">
                    <input
                      type="checkbox"
                      aria-label={stale ? `${vendor} (stale)` : vendor}
                      checked={checked}
                      onChange={(event) =>
                        onChange({
                          ...draft,
                          allowed_vendors: toggleStringSelection(
                            draft.allowed_vendors,
                            vendor,
                            event.target.checked,
                          ),
                        })
                      }
                      className="mt-1"
                    />
                    <span className="space-y-1">
                      <span className="block">
                        {stale ? `${vendor} (stale)` : vendor}
                      </span>
                      {stale && (
                        <span className="block text-xs">
                          Preserved from the stored rule until you remove it.
                        </span>
                      )}
                    </span>
                  </span>
                </label>
              );
            })}
          </div>
        )}

        {staleVendors.length > 0 && (
          <p className="text-xs text-warning-dark">
            {STALE_VENDOR_HELP_MESSAGE}
          </p>
        )}
      </section>
    </div>
  );
}

export function RoutingPolicyEditor({
  draft,
  groups,
  providers,
  groupNameById,
  providersLoading,
  providersError,
  error,
  saving,
  editingPolicy,
  onChange,
  onCancel,
  onSubmit,
}: RoutingPolicyEditorProps) {
  const selectedModelOption = MODEL_MATCH_OPTIONS.find(
    (option) => option.value === draft.model_match_type,
  );
  const compatibleProviders = getCompatibleProviders(providers, draft.api_type);
  const compatibleProviderIDs = new Set(
    compatibleProviders.map((provider) => provider.id),
  );
  const availableVendors = getProviderDerivedVendors(providers, draft.api_type);
  const availableVendorSet = new Set(availableVendors);
  const staleVendors = draft.allowed_vendors
    .filter((vendor) => !availableVendorSet.has(vendor))
    .slice()
    .sort((left, right) => left.localeCompare(right));
  const visibleVendors = [...availableVendors, ...staleVendors];
  const selectedProvider = draft.target_provider_id
    ? (providers.find((provider) => provider.id === draft.target_provider_id) ??
      null)
    : null;
  const showSelectedProviderFallback =
    draft.target_provider_id !== EMPTY_PROVIDER &&
    !compatibleProviderIDs.has(draft.target_provider_id);
  const submitActionLabel = editingPolicy ? "Update Policy" : "Create Policy";
  const submitButtonLabel = saving ? "Saving..." : submitActionLabel;

  return (
    <section className="bg-white rounded-2xl border border-border shadow-sm p-6 space-y-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h3 className="text-lg font-semibold text-text-primary">
            {editingPolicy ? "Edit Routing Policy" : "Add Routing Policy"}
          </h3>
          <p className="text-sm text-text-secondary mt-1">
            Routing policy is a first-class resource now: choose either one
            provider or a filter scope, and keep lifecycle state on the rule
            itself instead of hiding it in delete/recreate workflows.
          </p>
        </div>
        <button type="button" onClick={onCancel} className="btn btn-secondary">
          Cancel
        </button>
      </div>

      <form onSubmit={onSubmit} className="space-y-6">
        <RoutingPolicyCoreFields
          draft={draft}
          providers={providers}
          selectedModelHint={selectedModelOption?.hint}
          onChange={onChange}
        />

        <RoutingPolicyTargetModeSection draft={draft} onChange={onChange} />

        {providersError && (
          <div className="rounded-xl border border-warning/20 bg-warning-light/30 px-4 py-3 text-sm text-warning-dark">
            Provider catalog unavailable. Exact-provider targeting and live
            vendor validation may be incomplete until providers reload.{" "}
            {providersError.message}
          </div>
        )}

        {draft.target_mode === TARGET_MODE_EXACT_PROVIDER ? (
          <ExactProviderTargetSection
            draft={draft}
            compatibleProviders={compatibleProviders}
            groupNameById={groupNameById}
            providersLoading={providersLoading}
            selectedProvider={selectedProvider}
            showSelectedProviderFallback={showSelectedProviderFallback}
            onChange={onChange}
          />
        ) : (
          <FilterTargetSection
            draft={draft}
            groups={groups}
            visibleVendors={visibleVendors}
            availableVendorSet={availableVendorSet}
            staleVendors={staleVendors}
            providersLoading={providersLoading}
            onChange={onChange}
          />
        )}

        {error && (
          <div className="rounded-xl border border-danger/20 bg-danger/5 px-4 py-3 text-sm text-danger">
            {error}
          </div>
        )}

        <div className="rounded-xl border border-border/70 bg-bg-secondary/30 px-4 py-3 text-sm text-text-secondary">
          <span className="font-medium text-text-primary">Rule key:</span>{" "}
          {createPolicyKey(buildPayload(draft))}
        </div>

        <div className="flex items-center justify-end gap-3">
          <button
            type="button"
            onClick={onCancel}
            className="btn btn-secondary"
          >
            Cancel
          </button>
          <button type="submit" disabled={saving} className="btn btn-primary">
            {submitButtonLabel}
          </button>
        </div>
      </form>
    </section>
  );
}
