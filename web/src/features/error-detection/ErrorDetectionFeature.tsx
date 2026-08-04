import { useState } from "react";
import { RefreshCw, ShieldAlert } from "lucide-react";
import { useAPICatalog } from "@/api";
import type { InternalErrorRule, InternalErrorRuleSpec } from "./contracts";
import { CONFIG_KEYS } from "@/config/constants";
import { useConfig } from "@/hooks/useConfig";
import { useProviders } from "@/hooks/useProviders";
import {
  useErrorDetectionResources,
  type RuleRevisionConflict,
} from "./application";
import { ActionDialog } from "./components/ActionDialog";
import { RuleEditor } from "./components/RuleEditor";
import { RuleList } from "./components/RuleList";
import { TestMessagePanel } from "./components/TestMessagePanel";
import {
  createEmptyRuleDraft,
  parseGlobalMaxAttempts,
  ruleToDraft,
  ruleToSpec,
  validateRuleDraft,
  type ErrorDetectionPrefill,
  type RuleDraft,
  type RuleDraftErrors,
} from "./model";

type EditorSession =
  | {
      readonly mode: "create";
      readonly baseline: RuleDraft;
      readonly draft: RuleDraft;
    }
  | {
      readonly mode: "edit";
      readonly ruleID: string;
      readonly baseline: RuleDraft;
      readonly draft: RuleDraft;
    };

function createSession(prefill?: ErrorDetectionPrefill): EditorSession {
  const draft = createEmptyRuleDraft(prefill);
  return { mode: "create", baseline: draft, draft };
}

function editSession(rule: InternalErrorRule): EditorSession {
  const draft = ruleToDraft(rule);
  return { mode: "edit", ruleID: rule.id, baseline: draft, draft };
}

function FeatureHero({
  busy,
  refreshing,
  onRefresh,
}: {
  readonly busy: boolean;
  readonly refreshing: boolean;
  readonly onRefresh: () => void;
}) {
  return (
    <section className="flex flex-col gap-4 rounded-2xl border border-border bg-white p-6 shadow-sm lg:flex-row lg:items-center lg:justify-between">
      <div className="flex items-start gap-3">
        <div className="rounded-xl border border-primary/10 bg-primary-light/40 p-3">
          <ShieldAlert className="h-6 w-6 text-primary" aria-hidden="true" />
        </div>
        <div>
          <h1 className="text-2xl font-bold tracking-tight text-text-primary">
            Internal Error Detection
          </h1>
          <p className="mt-1.5 max-w-3xl text-sm text-text-secondary">
            Match keywords only in protocol-recognized error envelopes, then
            pass through, retry the same provider, or retry before switching.
          </p>
        </div>
      </div>
      <button
        type="button"
        disabled={busy}
        onClick={onRefresh}
        className="btn btn-secondary"
      >
        <RefreshCw
          className={`h-4 w-4 ${refreshing ? "animate-spin" : ""}`}
          aria-hidden="true"
        />
        Refresh
      </button>
    </section>
  );
}

function FeatureFeedback({
  notice,
  conflict,
  mutationError,
  busy,
  onRefreshConflict,
  onClearMutationError,
}: {
  readonly notice: string | null;
  readonly conflict: RuleRevisionConflict | null;
  readonly mutationError: Error | null;
  readonly busy: boolean;
  readonly onRefreshConflict: () => void;
  readonly onClearMutationError: () => void;
}) {
  return (
    <>
      {notice && (
        <div
          role="status"
          className="rounded-xl border border-success/20 bg-success-light/50 px-4 py-3 text-sm text-success-dark"
        >
          {notice}
        </div>
      )}
      {conflict && (
        <div
          role="alert"
          aria-label="Rule revision changed on the server"
          className="rounded-xl border border-warning/30 bg-warning-light/40 p-4"
        >
          <h2 className="text-sm font-semibold text-warning-dark">
            Rule revision changed on the server
          </h2>
          <p className="mt-1 text-sm text-text-secondary">
            Your draft was not overwritten. The server is now at revision{" "}
            {conflict.current_revision ?? "unknown"}. Refresh, review, and
            reapply the draft explicitly.
          </p>
          <button
            type="button"
            disabled={busy}
            onClick={onRefreshConflict}
            className="btn btn-secondary mt-3"
          >
            Refresh server rules
          </button>
        </div>
      )}
      {mutationError && (
        <div
          role="alert"
          className="rounded-xl border border-danger/20 bg-danger/5 p-4 text-sm text-danger"
        >
          {mutationError.message}
          <button
            type="button"
            onClick={onClearMutationError}
            className="ml-3 underline"
          >
            Dismiss
          </button>
        </div>
      )}
    </>
  );
}

export interface ErrorDetectionFeatureProps {
  readonly prefill?: ErrorDetectionPrefill;
}

export function ErrorDetectionFeature({ prefill }: ErrorDetectionFeatureProps) {
  const catalogState = useAPICatalog();
  const providersState = useProviders();
  const configState = useConfig();
  const resources = useErrorDetectionResources();
  const [editor, setEditor] = useState<EditorSession | null>(() =>
    prefill ? createSession(prefill) : null,
  );
  const [draftErrors, setDraftErrors] = useState<RuleDraftErrors>({});
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<InternalErrorRule | null>(
    null,
  );
  const [notice, setNotice] = useState<string | null>(null);
  const catalog = catalogState.catalog;
  const rules = resources.rulesResource?.value.rules ?? [];
  const ruleRevision = resources.rulesResource?.value.rule_set_revision ?? null;
  const busy = resources.pendingOperation !== null;
  const globalMaxAttempts = parseGlobalMaxAttempts(
    configState.config[CONFIG_KEYS.GLOBAL_MAX_ATTEMPTS],
  );

  function closeEditor() {
    setEditor(null);
    setDraftErrors({});
    setSubmitError(null);
  }

  function changeDraft(draft: RuleDraft) {
    if (!editor) return;
    setEditor({ ...editor, draft });
    setDraftErrors({});
    setSubmitError(null);
  }

  async function saveRule() {
    if (!editor || !catalog) return;
    const validation = validateRuleDraft(
      editor.draft,
      catalog,
      providersState.providers,
    );
    if (!validation.valid) {
      setDraftErrors(validation.errors);
      setSubmitError("Review the highlighted fields before saving.");
      return;
    }

    setSubmitError(null);
    try {
      if (editor.mode === "create") {
        await resources.createRule(validation.value);
        setNotice("Detection rule created.");
      } else {
        await resources.updateRule(editor.ruleID, validation.value);
        setNotice("Detection rule updated.");
      }
      closeEditor();
    } catch (reason) {
      setSubmitError(
        reason instanceof Error
          ? reason.message
          : "Failed to save detection rule",
      );
    }
  }

  async function toggleRule(rule: InternalErrorRule) {
    const spec: InternalErrorRuleSpec = {
      ...ruleToSpec(rule),
      enabled: !rule.enabled,
    };
    try {
      await resources.updateRule(rule.id, spec);
      setNotice(
        spec.enabled ? "Detection rule enabled." : "Detection rule disabled.",
      );
    } catch {
      // The resource controller exposes a typed conflict or mutation error banner.
    }
  }

  async function reorderRules(orderedRuleIDs: readonly string[]) {
    try {
      await resources.reorderRules(orderedRuleIDs);
      setNotice("Rule order saved.");
    } catch {
      // Focus restoration and the controller's explicit error state remain intact.
    }
  }

  async function confirmDelete() {
    if (!deleteTarget) return;
    try {
      await resources.deleteRule(deleteTarget.id);
      setNotice("Detection rule deleted.");
      if (editor?.mode === "edit" && editor.ruleID === deleteTarget.id) {
        closeEditor();
      }
      setDeleteTarget(null);
    } catch {
      // Keep the dialog and target so the operator can retry or cancel.
    }
  }

  async function refreshAll() {
    setNotice(null);
    try {
      await Promise.all([
        resources.refresh(),
        catalogState.refetch(),
        providersState.refetch(),
        configState.refetch(),
      ]);
      setNotice("Detection data refreshed.");
    } catch {
      // Each external source publishes its own actionable error state.
    }
  }

  async function refreshConflict() {
    try {
      await resources.refreshAfterConflict();
      setNotice(
        "Latest rule revision loaded. Review the draft, then save again.",
      );
    } catch {
      // The stale draft remains open and the refresh error is shown separately.
    }
  }

  return (
    <div className="space-y-5">
      <FeatureHero
        busy={busy}
        refreshing={resources.pendingOperation === "refresh"}
        onRefresh={() => void refreshAll()}
      />

      <FeatureFeedback
        notice={notice}
        conflict={resources.conflict}
        mutationError={resources.mutationError}
        busy={busy}
        onRefreshConflict={() => void refreshConflict()}
        onClearMutationError={resources.clearMutationError}
      />

      {!catalog && (
        <section
          role={catalogState.error ? "alert" : "status"}
          className="rounded-2xl border border-border bg-white p-5 shadow-sm"
        >
          <h2 className="text-sm font-semibold text-text-primary">
            {catalogState.error
              ? "API catalog unavailable"
              : "Loading the API catalog…"}
          </h2>
          <p className="mt-1 text-sm text-text-secondary">
            {catalogState.error?.message ??
              "Rule API choices are always derived from the authenticated server catalog."}
          </p>
        </section>
      )}

      {catalog && editor && (
        <RuleEditor
          mode={editor.mode}
          draft={editor.draft}
          baseline={editor.baseline}
          catalog={catalog}
          providers={providersState.providers}
          providersLoading={providersState.loading}
          providersError={providersState.error}
          errors={draftErrors}
          submitError={submitError}
          busy={busy}
          globalMaxAttempts={globalMaxAttempts}
          configUnavailable={Boolean(configState.error)}
          onChange={changeDraft}
          onSubmit={() => void saveRule()}
          onCancel={closeEditor}
        />
      )}

      {catalog && (
        <RuleList
          rules={rules}
          ruleRevision={ruleRevision}
          catalog={catalog}
          providers={providersState.providers}
          stats={resources.stats}
          statsLoading={resources.statsLoading}
          statsError={resources.statsError}
          loading={resources.rulesLoading}
          error={resources.rulesError}
          busy={busy}
          canCreate={resources.rulesResource !== null}
          onCreate={() => {
            setEditor(createSession(prefill));
            setDraftErrors({});
            setSubmitError(null);
          }}
          onEdit={(rule) => {
            setEditor(editSession(rule));
            setDraftErrors({});
            setSubmitError(null);
          }}
          onDelete={setDeleteTarget}
          onToggle={toggleRule}
          onReorder={reorderRules}
        />
      )}

      {catalog && resources.rulesResource && (
        <TestMessagePanel
          catalog={catalog}
          providers={providersState.providers}
          prefill={prefill}
          disabled={busy}
          onTest={resources.testMessage}
        />
      )}

      <ActionDialog
        open={deleteTarget !== null}
        title="Delete detection rule?"
        description={
          deleteTarget
            ? `Delete “${deleteTarget.name}”? This removes its statistics and cannot be undone.`
            : ""
        }
        confirmLabel="Delete rule"
        danger
        busy={resources.pendingOperation === "delete"}
        onConfirm={() => void confirmDelete()}
        onCancel={() => setDeleteTarget(null)}
      />
    </div>
  );
}
