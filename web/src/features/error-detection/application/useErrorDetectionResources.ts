import { useRef, useState } from "react";
import { ApiError, useApi } from "@/api";
import { useQuery } from "@/hooks/useQuery";
import type {
  InternalErrorRule,
  InternalErrorRuleListResponse,
  InternalErrorRuleSpec,
  RevisionedInternalErrorResource,
  TestMessageInput,
  TestMessageResponse,
} from "../contracts";

export type ErrorDetectionOperation =
  "refresh" | "create" | "update" | "delete" | "reorder" | "test_message";

export interface RuleRevisionConflict {
  readonly operation: ErrorDetectionOperation;
  readonly current_revision: string | null;
  readonly message: string;
}

function asError(reason: unknown, fallback: string): Error {
  return reason instanceof Error ? reason : new Error(fallback);
}

function readRevisionConflict(
  operation: ErrorDetectionOperation,
  error: Error,
): RuleRevisionConflict | null {
  if (!(error instanceof ApiError) || error.code !== "REVISION_MISMATCH") {
    return null;
  }
  return {
    operation,
    current_revision: error.details?.current_revision ?? null,
    message: error.message,
  };
}

function freezeRuleList(
  current: RevisionedInternalErrorResource<InternalErrorRuleListResponse>,
  revision: string,
  rules: readonly InternalErrorRule[],
  etag: RevisionedInternalErrorResource<InternalErrorRuleListResponse>["etag"],
): RevisionedInternalErrorResource<InternalErrorRuleListResponse> {
  return Object.freeze({
    value: Object.freeze({
      ...current.value,
      rule_set_revision: revision,
      rules: Object.freeze(rules),
    }),
    etag,
  });
}

export function useErrorDetectionResources() {
  const api = useApi();
  const rulesQuery = useQuery(() => api.errorDetection.rules.list(), {
    queryKey: api.errorDetection.rules,
    errorMessage: "Failed to load internal error rules",
  });
  const statsQuery = useQuery(() => api.errorDetection.stats.get(), {
    queryKey: api.errorDetection.stats,
    errorMessage: "Failed to load internal error rule statistics",
  });
  const operationLock = useRef<ErrorDetectionOperation | null>(null);
  const [pendingOperation, setPendingOperation] =
    useState<ErrorDetectionOperation | null>(null);
  const [mutationError, setMutationError] = useState<Error | null>(null);
  const [conflict, setConflict] = useState<RuleRevisionConflict | null>(null);

  async function runExclusive<T>(
    operation: ErrorDetectionOperation,
    action: () => Promise<T>,
  ): Promise<T> {
    if (operationLock.current !== null) {
      throw new Error(
        `Cannot start ${operation}; ${operationLock.current} is pending.`,
      );
    }
    operationLock.current = operation;
    setPendingOperation(operation);
    setMutationError(null);
    try {
      const result = await action();
      setConflict(null);
      return result;
    } catch (reason) {
      const error = asError(reason, `Failed to ${operation.replace("_", " ")}`);
      const revisionConflict = readRevisionConflict(operation, error);
      if (revisionConflict) setConflict(revisionConflict);
      else setMutationError(error);
      throw error;
    } finally {
      operationLock.current = null;
      setPendingOperation(null);
    }
  }

  function requireRules() {
    if (!rulesQuery.data) {
      throw new Error("Internal error rules are not loaded yet.");
    }
    return rulesQuery.data;
  }

  function refreshStatsAfterMutation() {
    // Statistics are independently revisioned, so reconcile them without
    // delaying the mutation result or replacing the authoritative rule ETag.
    void statsQuery.refetch();
  }

  const refresh = () =>
    runExclusive("refresh", async () => {
      const [rules, stats] = await Promise.all([
        api.errorDetection.rules.list(),
        api.errorDetection.stats.get(),
      ]);
      rulesQuery.replaceData(rules);
      statsQuery.replaceData(stats);
    });

  const createRule = (spec: InternalErrorRuleSpec) =>
    runExclusive("create", async () => {
      const current = requireRules();
      const result = await api.errorDetection.rules.create(spec, current.etag);
      rulesQuery.replaceData(
        freezeRuleList(
          current,
          result.value.rule_set_revision,
          [...current.value.rules, result.value.rule].sort(
            (left, right) => left.position - right.position,
          ),
          result.etag,
        ),
      );
      refreshStatsAfterMutation();
      return result.value.rule;
    });

  const updateRule = (ruleID: string, spec: InternalErrorRuleSpec) =>
    runExclusive("update", async () => {
      const current = requireRules();
      const result = await api.errorDetection.rules.update(
        ruleID,
        spec,
        current.etag,
      );
      const rules = current.value.rules.map((rule) =>
        rule.id === ruleID ? result.value.rule : rule,
      );
      rulesQuery.replaceData(
        freezeRuleList(
          current,
          result.value.rule_set_revision,
          rules,
          result.etag,
        ),
      );
      refreshStatsAfterMutation();
      return result.value.rule;
    });

  const deleteRule = (ruleID: string) =>
    runExclusive("delete", async () => {
      const current = requireRules();
      const result = await api.errorDetection.rules.delete(
        ruleID,
        current.etag,
      );
      const rules = current.value.rules
        .filter((rule) => rule.id !== ruleID)
        .map((rule, position) => Object.freeze({ ...rule, position }));
      rulesQuery.replaceData(
        freezeRuleList(current, result.rule_set_revision, rules, result.etag),
      );
      refreshStatsAfterMutation();
    });

  const reorderRules = (orderedRuleIDs: readonly string[]) =>
    runExclusive("reorder", async () => {
      const current = requireRules();
      const result = await api.errorDetection.rules.reorder(
        orderedRuleIDs,
        current.etag,
      );
      rulesQuery.replaceData(result);
      refreshStatsAfterMutation();
    });

  const testMessage = (input: TestMessageInput): Promise<TestMessageResponse> =>
    runExclusive("test_message", async () => {
      const current = requireRules();
      return api.errorDetection.testMessage(input, current.etag);
    });

  return {
    rulesResource: rulesQuery.data,
    rulesLoading: rulesQuery.loading,
    rulesError: rulesQuery.error,
    stats: statsQuery.data,
    statsLoading: statsQuery.loading,
    statsError: statsQuery.error,
    pendingOperation,
    mutationError,
    conflict,
    clearMutationError: () => setMutationError(null),
    refresh,
    refreshAfterConflict: refresh,
    createRule,
    updateRule,
    deleteRule,
    reorderRules,
    testMessage,
  };
}
