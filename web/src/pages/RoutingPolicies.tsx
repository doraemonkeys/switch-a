import { useState } from "react";
import type { FormEvent } from "react";
import { Plus, RefreshCw, Shield, Waypoints } from "lucide-react";
import type { Provider, RoutingPolicy } from "../api";
import { ConfirmModal } from "../components";
import { useGroups } from "../hooks/useGroups";
import { useProviders } from "../hooks/useProviders";
import { useRoutingPolicies } from "../hooks/useRoutingPolicies";
import { useToast } from "../hooks/useToast";
import { RoutingPolicyEditor } from "./routing-policies/RoutingPolicyEditor";
import { RoutingPoliciesListSection } from "./routing-policies/RoutingPoliciesListSection";
import {
  buildPayload,
  createEmptyDraft,
  getDeleteRoutingPolicyMessage,
  toDraft,
  validateRoutingPolicyDraft,
  type RoutingPolicyDraft,
} from "./routing-policies/shared";

function RoutingPoliciesHero({
  loading,
  available,
  onRefresh,
  onAddPolicy,
}: {
  loading: boolean;
  available: boolean;
  onRefresh: () => void;
  onAddPolicy: () => void;
}) {
  return (
    <>
      <section className="flex flex-col lg:flex-row lg:items-center lg:justify-between gap-4 bg-white rounded-2xl border border-border shadow-sm p-6">
        <div>
          <h2 className="text-2xl font-bold text-text-primary tracking-tight">
            Routing Policies
          </h2>
          <p className="text-sm text-text-secondary mt-1.5">
            Configure runtime-significant routing resources by api_type and
            optional model match before failover begins.
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <button
            type="button"
            onClick={onRefresh}
            disabled={loading}
            className="btn btn-secondary h-10 px-4"
          >
            <RefreshCw
              className={`w-4 h-4 ${loading ? "animate-spin text-primary" : "text-text-secondary"}`}
            />
            <span>Refresh</span>
          </button>
          <button
            type="button"
            onClick={onAddPolicy}
            disabled={!available}
            className="btn btn-primary h-10 px-5"
          >
            <Plus className="w-4 h-4" />
            <span>Add Policy</span>
          </button>
        </div>
      </section>

      <section className="grid grid-cols-1 xl:grid-cols-3 gap-4">
        <div className="xl:col-span-2 rounded-2xl border border-border bg-gradient-to-br from-primary-light/50 to-primary-light/15 p-5 shadow-sm">
          <div className="flex items-start gap-3">
            <div className="w-10 h-10 rounded-xl bg-white/90 border border-primary/10 flex items-center justify-center shrink-0">
              <Waypoints className="w-5 h-5 text-primary" />
            </div>
            <div className="space-y-2">
              <h3 className="text-sm font-semibold text-text-primary">
                Explicit Scope
              </h3>
              <p className="text-sm text-text-secondary leading-relaxed">
                Each rule now declares either one exact provider or a filter
                scope. That keeps the resource orthogonal: exact-provider
                routing is not modeled as a special case of free-form filters.
              </p>
            </div>
          </div>
        </div>
        <div className="rounded-2xl border border-border bg-white p-5 shadow-sm">
          <div className="flex items-start gap-3">
            <div className="w-10 h-10 rounded-xl bg-bg-secondary border border-border flex items-center justify-center shrink-0">
              <Shield className="w-5 h-5 text-text-secondary" />
            </div>
            <div className="space-y-2">
              <h3 className="text-sm font-semibold text-text-primary">
                Lifecycle State
              </h3>
              <p className="text-sm text-text-secondary leading-relaxed">
                Disable a rule without releasing its natural key. The page
                treats enabled state as normal resource data, so re-enabling is
                an edit, not a rebuild.
              </p>
            </div>
          </div>
        </div>
      </section>
    </>
  );
}

export function RoutingPolicies() {
  const toast = useToast();
  const { groups, refetch: refetchGroups } = useGroups();
  const {
    providers,
    loading: providersLoading,
    error: providersError,
    refetch: refetchProviders,
  } = useProviders();
  const {
    policies,
    loading,
    error,
    available,
    refetch,
    createPolicy,
    updatePolicy,
    deletePolicy,
  } = useRoutingPolicies();
  const [editorOpen, setEditorOpen] = useState(false);
  const [draft, setDraft] = useState<RoutingPolicyDraft>(createEmptyDraft());
  const [editingPolicy, setEditingPolicy] = useState<RoutingPolicy | null>(
    null,
  );
  const [formError, setFormError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<RoutingPolicy | null>(null);
  const [togglingPolicyID, setTogglingPolicyID] = useState<string | null>(null);
  const groupNameById = new Map(groups.map((group) => [group.id, group.name]));
  const providerById = new Map(
    providers.map((provider) => [provider.id, provider as Provider]),
  );

  const openCreateEditor = () => {
    setEditorOpen(true);
    setDraft(createEmptyDraft());
    setEditingPolicy(null);
    setFormError(null);
  };

  const openEditEditor = (policy: RoutingPolicy) => {
    setEditorOpen(true);
    setEditingPolicy(policy);
    setDraft(toDraft(policy));
    setFormError(null);
  };

  const resetEditor = () => {
    setEditorOpen(false);
    setDraft(createEmptyDraft());
    setEditingPolicy(null);
    setFormError(null);
  };

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const validationError = validateRoutingPolicyDraft({
      draft,
      providers,
      policies,
      editingPolicy,
    });
    if (validationError) {
      setFormError(validationError);
      return;
    }

    setSaving(true);
    setFormError(null);
    try {
      const payload = buildPayload(draft);
      if (editingPolicy) {
        await updatePolicy(editingPolicy.id, payload);
        toast.success("Routing policy updated");
      } else {
        await createPolicy(payload);
        toast.success("Routing policy created");
      }
      resetEditor();
    } catch (submitError) {
      const message =
        submitError instanceof Error
          ? submitError.message
          : "Failed to save routing policy";
      setFormError(message);
      toast.error(message);
    } finally {
      setSaving(false);
    }
  };

  const handleTogglePolicy = async (policy: RoutingPolicy) => {
    setTogglingPolicyID(policy.id);
    try {
      const updatedPolicy = await updatePolicy(
        policy.id,
        buildPayload(
          toDraft({
            ...policy,
            enabled: !policy.enabled,
          }),
        ),
      );

      if (editingPolicy?.id === policy.id) {
        setEditingPolicy(updatedPolicy);
        setDraft(toDraft(updatedPolicy));
      }

      toast.success(
        updatedPolicy.enabled
          ? "Routing policy enabled"
          : "Routing policy disabled",
      );
    } catch (toggleError) {
      toast.error(
        toggleError instanceof Error
          ? toggleError.message
          : "Failed to update routing policy",
      );
    } finally {
      setTogglingPolicyID(null);
    }
  };

  const handleRefreshAll = async () => {
    await Promise.all([refetch(), refetchGroups(), refetchProviders()]);
  };

  const handleRetryLoad = async () => {
    await refetch();
  };

  const handleDeleteConfirm = async () => {
    if (!deleteTarget) {
      return;
    }

    try {
      await deletePolicy(deleteTarget.id);
      toast.success("Routing policy deleted");
      setDeleteTarget(null);
      if (editingPolicy?.id === deleteTarget.id) {
        resetEditor();
      }
    } catch (deleteError) {
      toast.error(
        deleteError instanceof Error
          ? deleteError.message
          : "Failed to delete routing policy",
      );
    }
  };

  return (
    <div className="space-y-5">
      <RoutingPoliciesHero
        loading={loading || providersLoading}
        available={available}
        onRefresh={handleRefreshAll}
        onAddPolicy={openCreateEditor}
      />

      {editorOpen && (
        <RoutingPolicyEditor
          draft={draft}
          groups={groups}
          providers={providers}
          groupNameById={groupNameById}
          providersLoading={providersLoading}
          providersError={providersError}
          error={formError}
          saving={saving}
          editingPolicy={editingPolicy}
          onChange={setDraft}
          onCancel={resetEditor}
          onSubmit={handleSubmit}
        />
      )}

      {error && (
        <section className="rounded-2xl border border-danger/20 bg-danger/5 px-5 py-4 shadow-sm">
          <div className="flex items-center justify-between gap-4">
            <div>
              <h3 className="text-sm font-semibold text-danger">
                Failed to load routing policies
              </h3>
              <p className="text-sm text-danger/90 mt-1">{error.message}</p>
            </div>
            <button
              type="button"
              onClick={handleRetryLoad}
              className="btn btn-secondary"
            >
              Retry
            </button>
          </div>
        </section>
      )}

      {providersError && (
        <section className="rounded-2xl border border-warning/20 bg-warning-light/30 px-5 py-4 shadow-sm">
          <h3 className="text-sm font-semibold text-warning-dark">
            Failed to load provider catalog
          </h3>
          <p className="text-sm text-text-secondary mt-1">
            Exact-provider options and vendor choices are derived from
            providers.
            {` ${providersError.message}`}
          </p>
        </section>
      )}

      <RoutingPoliciesListSection
        available={available}
        error={error}
        loading={loading}
        policies={policies}
        groupNameById={groupNameById}
        providerById={providerById}
        togglingPolicyID={togglingPolicyID}
        onEditPolicy={openEditEditor}
        onDeletePolicy={setDeleteTarget}
        onTogglePolicy={handleTogglePolicy}
      />

      <ConfirmModal
        isOpen={Boolean(deleteTarget)}
        onClose={() => setDeleteTarget(null)}
        onConfirm={handleDeleteConfirm}
        title="Delete Routing Policy"
        message={getDeleteRoutingPolicyMessage(deleteTarget)}
        confirmText="Delete"
        cancelText="Cancel"
        variant="danger"
        loading={false}
      />
    </div>
  );
}
