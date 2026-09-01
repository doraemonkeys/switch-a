import { useState } from "react";
import { AlertCircle } from "lucide-react";
import type {
  CreateCredentialSessionInput,
  CredentialSession,
} from "../../api";
import { ConfirmModal } from "../../components";
import { useCredentialSessions } from "../../hooks/useCredentialSessions";
import { useToast } from "../../hooks/useToast";
import { CredentialCreateModal } from "./CredentialCreateModal";
import {
  CredentialEditorModal,
  type CredentialEditorState,
} from "./CredentialEditorModal";
import { CredentialEmptyState } from "./CredentialEmptyState";
import {
  CredentialFilterToolbar,
  type CredentialKindFilter,
  type CredentialSortOption,
  type CredentialStatusFilter,
  type CredentialUsageFilter,
  type CredentialViewMode,
} from "./CredentialFilterToolbar";
import { CredentialGrid } from "./CredentialGrid";
import { CredentialHeroHeader } from "./CredentialHeroHeader";
import { filterAndSortCredentialSessions } from "./credentialFilterUtils";
import { CredentialSessionReauthenticationModal } from "./CredentialSessionReauthenticationModal";
import { CredentialStatsBar } from "./CredentialStatsBar";
import { CredentialTable } from "./CredentialTable";

export function CredentialSessions() {
  const {
    credentialSessions,
    loading,
    error,
    refetch,
    createCredentialSession,
    renameCredentialSession,
    updateCredentialSession,
    deleteCredentialSession,
  } = useCredentialSessions();
  const toast = useToast();

  // Modal and mutation states
  const [editor, setEditor] = useState<CredentialEditorState>(null);
  const [deleteTarget, setDeleteTarget] = useState<CredentialSession | null>(
    null,
  );
  const [reauthenticationTarget, setReauthenticationTarget] =
    useState<CredentialSession | null>(null);
  const [isCreating, setIsCreating] = useState(false);
  const [busy, setBusy] = useState(false);

  // Search, Filter, Sort & View states
  const [search, setSearch] = useState("");
  const [kindFilter, setKindFilter] = useState<CredentialKindFilter>("all");
  const [statusFilter, setStatusFilter] =
    useState<CredentialStatusFilter>("all");
  const [usageFilter, setUsageFilter] = useState<CredentialUsageFilter>("all");
  const [sortOption, setSortOption] = useState<CredentialSortOption>("updated");
  const [viewMode, setViewMode] = useState<CredentialViewMode>("grid");

  const resetFilters = () => {
    setSearch("");
    setKindFilter("all");
    setStatusFilter("all");
    setUsageFilter("all");
  };

  const filteredSessions = filterAndSortCredentialSessions(credentialSessions, {
    search,
    kindFilter,
    statusFilter,
    usageFilter,
    sortOption,
  });

  const saveEditor = async () => {
    if (!editor || !editor.value.trim()) return;
    setBusy(true);
    try {
      if (editor.kind === "rename") {
        await renameCredentialSession(editor.session.id, {
          expected_version: editor.session.version,
          name: editor.value.trim(),
        });
        toast.success("Credential renamed");
      } else {
        await updateCredentialSession(editor.session.id, {
          expected_version: editor.session.version,
          secret_data: editor.value.trim(),
        });
        toast.success("API key rotated for every referenced route");
      }
      setEditor(null);
    } catch (cause) {
      toast.error(
        cause instanceof Error ? cause.message : "Credential update failed",
      );
    } finally {
      setBusy(false);
    }
  };

  const confirmDelete = async () => {
    if (!deleteTarget) return;
    setBusy(true);
    try {
      await deleteCredentialSession(deleteTarget.id);
      toast.success(`Credential "${deleteTarget.name}" deleted`);
      setDeleteTarget(null);
    } catch (cause) {
      toast.error(
        cause instanceof Error ? cause.message : "Credential deletion failed",
      );
    } finally {
      setBusy(false);
    }
  };

  const completeReauthentication = async (session: CredentialSession) => {
    await refetch();
    toast.success(`Credential "${session.name}" reconnected`);
    setReauthenticationTarget(null);
  };

  const handleCreateCredential = async (
    input: CreateCredentialSessionInput,
  ) => {
    setBusy(true);
    try {
      await createCredentialSession(input);
      toast.success(`Credential "${input.name}" created successfully`);
      setIsCreating(false);
    } catch (cause) {
      toast.error(
        cause instanceof Error ? cause.message : "Failed to create credential",
      );
    } finally {
      setBusy(false);
    }
  };

  let mainContent = (
    <CredentialTable
      sessions={filteredSessions}
      disabled={busy}
      onReconnect={(s) => setReauthenticationTarget(s)}
      onRename={(s) => setEditor({ kind: "rename", session: s, value: s.name })}
      onRotate={(s) => setEditor({ kind: "rotate", session: s, value: "" })}
      onDelete={(s) => setDeleteTarget(s)}
    />
  );

  if (credentialSessions.length === 0 && !loading) {
    mainContent = (
      <CredentialEmptyState
        isFiltered={false}
        onCreateCredential={() => setIsCreating(true)}
      />
    );
  } else if (filteredSessions.length === 0 && !loading) {
    mainContent = (
      <CredentialEmptyState isFiltered={true} onResetFilters={resetFilters} />
    );
  } else if (viewMode === "grid") {
    mainContent = (
      <CredentialGrid
        sessions={filteredSessions}
        disabled={busy}
        onReconnect={(s) => setReauthenticationTarget(s)}
        onRename={(s) =>
          setEditor({ kind: "rename", session: s, value: s.name })
        }
        onRotate={(s) => setEditor({ kind: "rotate", session: s, value: "" })}
        onDelete={(s) => setDeleteTarget(s)}
      />
    );
  }

  return (
    <div className="space-y-6">
      {/* Hero Header */}
      <CredentialHeroHeader
        loading={loading}
        onRefresh={() => void refetch()}
        onAddCredential={() => setIsCreating(true)}
      />

      {/* Error Alert */}
      {error && (
        <div
          role="alert"
          className="flex items-center gap-3 rounded-2xl border border-danger/20 bg-danger/5 p-4 text-sm text-danger shadow-xs"
        >
          <AlertCircle className="h-5 w-5 shrink-0" />
          <span>{error.message}</span>
        </div>
      )}

      {/* KPI Stats Overview */}
      <CredentialStatsBar sessions={credentialSessions} />

      {/* Filter & Command Toolbar */}
      <CredentialFilterToolbar
        search={search}
        onSearchChange={setSearch}
        kindFilter={kindFilter}
        onKindFilterChange={setKindFilter}
        statusFilter={statusFilter}
        onStatusFilterChange={setStatusFilter}
        usageFilter={usageFilter}
        onUsageFilterChange={setUsageFilter}
        sortOption={sortOption}
        onSortChange={setSortOption}
        viewMode={viewMode}
        onViewModeChange={setViewMode}
        totalCount={credentialSessions.length}
        filteredCount={filteredSessions.length}
        onResetFilters={resetFilters}
      />

      {/* Content Area */}
      {mainContent}

      {/* Modals & Dialogs */}
      {editor && (
        <CredentialEditorModal
          editor={editor}
          busy={busy}
          onChange={(value) => setEditor({ ...editor, value })}
          onCancel={() => setEditor(null)}
          onSave={() => void saveEditor()}
        />
      )}

      {isCreating && (
        <CredentialCreateModal
          isOpen={isCreating}
          busy={busy}
          onClose={() => setIsCreating(false)}
          onSubmit={handleCreateCredential}
        />
      )}

      {reauthenticationTarget && (
        <CredentialSessionReauthenticationModal
          session={reauthenticationTarget}
          onClose={() => setReauthenticationTarget(null)}
          onReauthenticated={(session) =>
            void completeReauthentication(session)
          }
        />
      )}

      <ConfirmModal
        isOpen={deleteTarget !== null}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => void confirmDelete()}
        title="Delete Credential"
        message={`Delete "${deleteTarget?.name ?? ""}"? Only credentials with no route references can be deleted.`}
        confirmText="Delete"
        variant="danger"
        loading={busy}
      />
    </div>
  );
}
