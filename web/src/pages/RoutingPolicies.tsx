import { useState } from "react";
import type { FormEvent } from "react";
import {
  Pencil,
  Plus,
  RefreshCw,
  Shield,
  Trash2,
  Waypoints,
} from "lucide-react";
import type {
  RoutingPolicy,
  RoutingPolicyInput,
  RoutingPolicyModelMatchType,
} from "../api";
import { ConfirmModal } from "../components";
import { API_TYPE_OPTIONS } from "../config/constants";
import { useGroups } from "../hooks/useGroups";
import { useRoutingPolicies } from "../hooks/useRoutingPolicies";
import { useToast } from "../hooks/useToast";
import { stringToColor } from "../lib/utils";

const EMPTY_MODEL_MATCH = "";
const MODEL_MATCH_OPTIONS: Array<{
  value: RoutingPolicyModelMatchType | typeof EMPTY_MODEL_MATCH;
  label: string;
  hint: string;
}> = [
  {
    value: EMPTY_MODEL_MATCH,
    label: "Any model",
    hint: "Match only on api_type when the model is unknown or unrestricted.",
  },
  {
    value: "exact",
    label: "Exact",
    hint: "Apply the rule only when the request model matches exactly.",
  },
  {
    value: "prefix",
    label: "Prefix",
    hint: "Apply the rule when the request model starts with the configured value.",
  },
];

interface RoutingPolicyDraft {
  api_type: string;
  model_match_type: RoutingPolicyModelMatchType | typeof EMPTY_MODEL_MATCH;
  model_match_value: string;
  allowed_group_ids: string[];
  allowed_vendors_text: string;
}

function createEmptyDraft(): RoutingPolicyDraft {
  return {
    api_type: "",
    model_match_type: EMPTY_MODEL_MATCH,
    model_match_value: "",
    allowed_group_ids: [],
    allowed_vendors_text: "",
  };
}

function parseVendorList(rawValue: string): string[] {
  return Array.from(
    new Set(
      rawValue
        .split(/[\n,]/)
        .map((value) => value.trim())
        .filter(Boolean),
    ),
  );
}

function createPolicyKey(input: {
  api_type: string;
  model_match_type?: RoutingPolicyModelMatchType | null;
  model_match_value?: string | null;
}): string {
  return [
    input.api_type.trim(),
    input.model_match_type ?? EMPTY_MODEL_MATCH,
    input.model_match_value?.trim() ?? EMPTY_MODEL_MATCH,
  ].join("|");
}

function toDraft(policy?: RoutingPolicy | null): RoutingPolicyDraft {
  if (!policy) {
    return createEmptyDraft();
  }

  return {
    api_type: policy.api_type,
    model_match_type: policy.model_match_type ?? EMPTY_MODEL_MATCH,
    model_match_value: policy.model_match_value ?? "",
    allowed_group_ids: policy.allowed_group_ids ?? [],
    allowed_vendors_text: (policy.allowed_vendors ?? []).join(", "),
  };
}

function buildPayload(draft: RoutingPolicyDraft): RoutingPolicyInput {
  const normalizedMatchType =
    draft.model_match_type === EMPTY_MODEL_MATCH
      ? null
      : draft.model_match_type;
  const normalizedMatchValue = normalizedMatchType
    ? draft.model_match_value.trim()
    : null;

  return {
    api_type: draft.api_type.trim(),
    model_match_type: normalizedMatchType,
    model_match_value: normalizedMatchValue,
    allowed_group_ids: draft.allowed_group_ids,
    allowed_vendors: parseVendorList(draft.allowed_vendors_text),
  };
}

function formatModelMatch(policy: RoutingPolicy): string {
  if (!policy.model_match_type || !policy.model_match_value) {
    return "api_type only";
  }

  return `${policy.model_match_type}: ${policy.model_match_value}`;
}

function formatPolicyCount(count: number): string {
  return `${count} policy${count === 1 ? "" : "ies"} currently constrain selector eligibility.`;
}

function GroupBadge({ groupId, label }: { groupId: string; label: string }) {
  const colors = stringToColor(groupId);
  return (
    <span
      className="inline-flex items-center rounded-md border px-2 py-0.5 text-xs font-medium"
      style={{
        backgroundColor: colors.bg,
        color: colors.text,
        borderColor: colors.border,
      }}
    >
      {label}
    </span>
  );
}

interface RoutingPolicyEditorProps {
  draft: RoutingPolicyDraft;
  groups: Array<{ id: string; name: string }>;
  error: string | null;
  saving: boolean;
  editingPolicy: RoutingPolicy | null;
  onChange: (draft: RoutingPolicyDraft) => void;
  onCancel: () => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
}

function RoutingPolicyEditor({
  draft,
  groups,
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
            Hard constraints are applied before failover. A rule must target at
            least one group or vendor so it never degenerates into a hidden
            no-op.
          </p>
        </div>
        <button type="button" onClick={onCancel} className="btn btn-secondary">
          Cancel
        </button>
      </div>

      <form onSubmit={onSubmit} className="space-y-6">
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
          <label className="space-y-2">
            <span className="text-sm font-medium text-text-primary">
              API Type
            </span>
            <input
              list="routing-policy-api-types"
              className="input"
              value={draft.api_type}
              onChange={(event) =>
                onChange({
                  ...draft,
                  api_type: event.target.value,
                })
              }
              placeholder="codex"
            />
            <datalist id="routing-policy-api-types">
              {API_TYPE_OPTIONS.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </datalist>
            <p className="text-xs text-text-muted">
              Requests must match this exact upstream API contract.
            </p>
          </label>

          <label className="space-y-2">
            <span className="text-sm font-medium text-text-primary">
              Model Match
            </span>
            <select
              className="input"
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
            <p className="text-xs text-text-muted">
              {selectedModelOption?.hint}
            </p>
          </label>

          <label className="space-y-2">
            <span className="text-sm font-medium text-text-primary">
              Model Value
            </span>
            <input
              className="input"
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
        </div>

        <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
          <section className="rounded-xl border border-border/70 bg-bg-secondary/30 p-4 space-y-3">
            <div>
              <h4 className="text-sm font-semibold text-text-primary">
                Allowed Groups
              </h4>
              <p className="text-xs text-text-muted mt-1">
                Group and vendor constraints intersect. Empty groups means the
                policy relies on vendor constraints only.
              </p>
            </div>
            {groups.length === 0 ? (
              <p className="text-sm text-text-muted">
                No groups configured yet.
              </p>
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
                        checked={checked}
                        onChange={(event) =>
                          onChange({
                            ...draft,
                            allowed_group_ids: event.target.checked
                              ? [...draft.allowed_group_ids, group.id]
                              : draft.allowed_group_ids.filter(
                                  (value) => value !== group.id,
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

          <label className="rounded-xl border border-border/70 bg-bg-secondary/30 p-4 space-y-3 block">
            <span className="text-sm font-semibold text-text-primary block">
              Allowed Vendors
            </span>
            <span className="text-xs text-text-muted block">
              Enter one vendor per line or separate values with commas.
            </span>
            <textarea
              className="input min-h-32"
              value={draft.allowed_vendors_text}
              onChange={(event) =>
                onChange({
                  ...draft,
                  allowed_vendors_text: event.target.value,
                })
              }
              placeholder={"openai\nanthropic"}
            />
          </label>
        </div>

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
            Configure hard routing constraints by api_type and optional model
            match before failover begins.
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
                Constraint Closure
              </h3>
              <p className="text-sm text-text-secondary leading-relaxed">
                When a request matches one of these rules, failover stays inside
                the constrained candidate set. The page is intentionally strict:
                rules are keyed by api_type plus optional model match so the UI
                does not smuggle routing semantics into provider health.
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
                Specificity Model
              </h3>
              <p className="text-sm text-text-secondary leading-relaxed">
                Use exact for a single model, prefix for a family, or leave
                model matching empty for api_type-only routing. Duplicate rule
                keys are blocked in the UI before the request reaches the
                server.
              </p>
            </div>
          </div>
        </div>
      </section>
    </>
  );
}

interface RoutingPoliciesListSectionProps {
  available: boolean;
  error: Error | null;
  loading: boolean;
  policies: RoutingPolicy[];
  groupNameById: Map<string, string>;
  onEditPolicy: (policy: RoutingPolicy) => void;
  onDeletePolicy: (policy: RoutingPolicy) => void;
}

function RoutingPoliciesListSection({
  available,
  error,
  loading,
  policies,
  groupNameById,
  onEditPolicy,
  onDeletePolicy,
}: RoutingPoliciesListSectionProps) {
  const policySummary = formatPolicyCount(policies.length);
  const showEmptyState = !loading && policies.length === 0;

  if (!available && !error && !loading) {
    return (
      <section className="rounded-2xl border border-warning/20 bg-warning-light/30 px-5 py-4 shadow-sm">
        <h3 className="text-sm font-semibold text-warning-dark">
          Routing policy API unavailable
        </h3>
        <p className="text-sm text-text-secondary mt-1">
          The frontend is ready for Phase 5 routing policy management, but the
          current backend build does not expose `/routing-policies` yet.
        </p>
      </section>
    );
  }

  if (loading && policies.length === 0) {
    return (
      <section className="bg-white rounded-2xl border border-border shadow-sm overflow-hidden">
        <div className="flex items-center justify-between gap-4 px-5 py-4 border-b border-border/60">
          <div>
            <h3 className="text-lg font-semibold text-text-primary">
              Active Rules
            </h3>
            <p className="text-sm text-text-secondary mt-1">{policySummary}</p>
          </div>
        </div>
        <div className="flex items-center justify-center py-20 text-text-muted">
          <RefreshCw className="w-6 h-6 animate-spin text-primary mr-3" />
          Loading routing policies...
        </div>
      </section>
    );
  }

  if (showEmptyState) {
    return (
      <section className="bg-white rounded-2xl border border-border shadow-sm overflow-hidden">
        <div className="flex items-center justify-between gap-4 px-5 py-4 border-b border-border/60">
          <div>
            <h3 className="text-lg font-semibold text-text-primary">
              Active Rules
            </h3>
            <p className="text-sm text-text-secondary mt-1">{policySummary}</p>
          </div>
        </div>
        <div className="px-5 py-16 text-center text-text-secondary">
          <p className="text-lg font-semibold text-text-primary">
            No routing policies configured
          </p>
          <p className="text-sm mt-2">
            Requests will continue using the default selector behavior until a
            rule is created for a specific api_type.
          </p>
        </div>
      </section>
    );
  }

  return (
    <section className="bg-white rounded-2xl border border-border shadow-sm overflow-hidden">
      <div className="flex items-center justify-between gap-4 px-5 py-4 border-b border-border/60">
        <div>
          <h3 className="text-lg font-semibold text-text-primary">
            Active Rules
          </h3>
          <p className="text-sm text-text-secondary mt-1">{policySummary}</p>
        </div>
      </div>

      <div className="overflow-x-auto">
        <table className="w-full min-w-[920px] table-auto">
          <thead className="bg-bg-secondary border-b border-border/60">
            <tr>
              {[
                "API Type",
                "Model Match",
                "Allowed Groups",
                "Allowed Vendors",
                "Updated",
                "",
              ].map((label) => (
                <th
                  key={label || "actions"}
                  className="px-4 py-3 text-left text-[11px] font-semibold uppercase tracking-wider text-text-secondary"
                >
                  {label}
                </th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y divide-border/60 bg-white">
            {policies.map((policy) => (
              <tr
                key={policy.id}
                className="hover:bg-bg-secondary/50 transition-colors"
              >
                <td className="px-4 py-3 align-top">
                  <span className="inline-flex rounded-md bg-bg-tertiary border border-border/60 px-2 py-0.5 text-xs font-semibold uppercase tracking-wide text-text-primary">
                    {policy.api_type}
                  </span>
                </td>
                <td className="px-4 py-3 align-top text-sm text-text-primary">
                  {formatModelMatch(policy)}
                </td>
                <td className="px-4 py-3 align-top">
                  <div className="flex flex-wrap gap-1.5">
                    {policy.allowed_group_ids.length === 0 ? (
                      <span className="text-sm text-text-muted">—</span>
                    ) : (
                      policy.allowed_group_ids.map((groupId) => (
                        <GroupBadge
                          key={groupId}
                          groupId={groupId}
                          label={groupNameById.get(groupId) ?? groupId}
                        />
                      ))
                    )}
                  </div>
                </td>
                <td className="px-4 py-3 align-top">
                  <div className="flex flex-wrap gap-1.5">
                    {policy.allowed_vendors.length === 0 ? (
                      <span className="text-sm text-text-muted">—</span>
                    ) : (
                      policy.allowed_vendors.map((vendor) => (
                        <span
                          key={vendor}
                          className="inline-flex rounded-md border border-border/60 bg-bg-secondary px-2 py-0.5 text-xs font-medium text-text-secondary"
                        >
                          {vendor}
                        </span>
                      ))
                    )}
                  </div>
                </td>
                <td className="px-4 py-3 align-top text-sm text-text-secondary">
                  {policy.updated_at
                    ? new Date(policy.updated_at).toLocaleString()
                    : "—"}
                </td>
                <td className="px-4 py-3 align-top text-right">
                  <div className="flex items-center justify-end gap-1">
                    <button
                      type="button"
                      onClick={() => onEditPolicy(policy)}
                      className="p-2 rounded-md text-text-muted hover:text-primary hover:bg-primary-light transition-colors"
                      aria-label={`Edit routing policy for ${policy.api_type}`}
                    >
                      <Pencil className="w-4 h-4" />
                    </button>
                    <button
                      type="button"
                      onClick={() => onDeletePolicy(policy)}
                      className="p-2 rounded-md text-text-muted hover:text-danger hover:bg-danger-light transition-colors"
                      aria-label={`Delete routing policy for ${policy.api_type}`}
                    >
                      <Trash2 className="w-4 h-4" />
                    </button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

export function RoutingPolicies() {
  const toast = useToast();
  const { groups } = useGroups();
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
  const groupNameById = new Map(groups.map((group) => [group.id, group.name]));
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

  const validateDraft = (currentDraft: RoutingPolicyDraft): string | null => {
    const payload = buildPayload(currentDraft);

    if (!payload.api_type) {
      return "API type is required.";
    }

    if (payload.model_match_type && !payload.model_match_value) {
      return "Model match value is required when a model match type is selected.";
    }

    if (
      payload.allowed_group_ids.length === 0 &&
      payload.allowed_vendors.length === 0
    ) {
      return "Select at least one allowed group or vendor.";
    }

    const duplicate = policies.find(
      (policy) =>
        policy.id !== editingPolicy?.id &&
        createPolicyKey(policy) === createPolicyKey(payload),
    );
    if (duplicate) {
      return "A rule with the same api_type and model match already exists.";
    }

    return null;
  };

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const validationError = validateDraft(draft);
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
    } catch (err) {
      const message =
        err instanceof Error ? err.message : "Failed to save routing policy";
      setFormError(message);
      toast.error(message);
    } finally {
      setSaving(false);
    }
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
    } catch (err) {
      toast.error(
        err instanceof Error ? err.message : "Failed to delete routing policy",
      );
    }
  };

  return (
    <div className="space-y-5">
      <RoutingPoliciesHero
        loading={loading}
        available={available}
        onRefresh={() => void refetch()}
        onAddPolicy={openCreateEditor}
      />

      {editorOpen && (
        <RoutingPolicyEditor
          draft={draft}
          groups={groups}
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
              onClick={() => void refetch()}
              className="btn btn-secondary"
            >
              Retry
            </button>
          </div>
        </section>
      )}

      <RoutingPoliciesListSection
        available={available}
        error={error}
        loading={loading}
        policies={policies}
        groupNameById={groupNameById}
        onEditPolicy={openEditEditor}
        onDeletePolicy={setDeleteTarget}
      />

      <ConfirmModal
        isOpen={Boolean(deleteTarget)}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => void handleDeleteConfirm()}
        title="Delete Routing Policy"
        message={`Delete routing policy "${deleteTarget?.api_type ?? ""}"? Requests that matched this rule will fall back to the default selector behavior.`}
        confirmText="Delete"
        cancelText="Cancel"
        variant="danger"
        loading={false}
      />
    </div>
  );
}
