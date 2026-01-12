import { useState } from "react";
import { useGroups } from "../hooks/useGroups";
import { GroupModal, ConfirmModal } from "../components";
import type { Group, GroupInput } from "../api/types";

// SVG Icons
const PlusIcon = () => (
  <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
  </svg>
);

const EditIcon = () => (
  <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z" />
  </svg>
);

const TrashIcon = () => (
  <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
  </svg>
);

export function Groups() {
  const { groups, loading, error, createGroup, updateGroup, deleteGroup } = useGroups();
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [editingGroup, setEditingGroup] = useState<Group | null>(null);
  const [deleteConfirm, setDeleteConfirm] = useState<{ isOpen: boolean; groupId: string | null }>({
    isOpen: false,
    groupId: null,
  });
  const [deleting, setDeleting] = useState(false);

  const handleCreate = () => {
    setEditingGroup(null);
    setIsModalOpen(true);
  };

  const handleEdit = (group: Group) => {
    setEditingGroup(group);
    setIsModalOpen(true);
  };

  const handleDeleteClick = (id: string) => {
    setDeleteConfirm({ isOpen: true, groupId: id });
  };

  const handleDeleteConfirm = async () => {
    if (deleteConfirm.groupId) {
      setDeleting(true);
      try {
        await deleteGroup(deleteConfirm.groupId);
        setDeleteConfirm({ isOpen: false, groupId: null });
      } catch (err) {
        console.error("Failed to delete group:", err);
      } finally {
        setDeleting(false);
      }
    }
  };

  const handleDeleteCancel = () => {
    setDeleteConfirm({ isOpen: false, groupId: null });
  };

  const handleSubmit = async (data: GroupInput) => {
    if (editingGroup) {
      await updateGroup(editingGroup.id, data);
    } else {
      await createGroup(data);
    }
  };

  if (loading && groups.length === 0) {
    return (
      <div className="flex items-center justify-center min-h-[400px]">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-primary"></div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="p-4 rounded-lg bg-red-500/10 border border-red-500/20 text-red-400">
        Error loading groups: {error.message}
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Page Header */}
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-2xl font-bold text-text-primary">Groups</h2>
          <p className="text-text-secondary mt-1">Manage provider groups and routing strategies</p>
        </div>
        <button
          onClick={handleCreate}
          className="btn btn-primary flex items-center gap-2"
        >
          <PlusIcon />
          Add Group
        </button>
      </div>

      {/* Groups Grid */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
        {/* Create New Card */}
        <div
          onClick={handleCreate}
          className="card border-dashed border-2 hover:border-primary/50 transition-colors cursor-pointer group flex flex-col items-center justify-center min-h-[200px]"
        >
          <div className="w-14 h-14 mb-3 bg-bg-tertiary rounded-xl flex items-center justify-center group-hover:bg-primary-light transition-colors">
            <svg className="w-8 h-8 text-text-secondary group-hover:scale-110 transition-transform" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
            </svg>
          </div>
          <p className="font-medium text-text-primary">Create New Group</p>
          <p className="text-sm text-text-muted mt-1">Organize providers into groups</p>
        </div>

        {/* Group Cards */}
        {groups.map((group) => (
          <GroupCard
            key={group.id}
            group={group}
            onEdit={() => handleEdit(group)}
            onDelete={() => handleDeleteClick(group.id)}
          />
        ))}
      </div>

      <GroupModal
        isOpen={isModalOpen}
        onClose={() => setIsModalOpen(false)}
        onSubmit={handleSubmit}
        initialData={editingGroup}
        title={editingGroup ? "Edit Group" : "Create Group"}
      />

      <ConfirmModal
        isOpen={deleteConfirm.isOpen}
        onClose={handleDeleteCancel}
        onConfirm={handleDeleteConfirm}
        title="Delete Group"
        message="Are you sure you want to delete this group? This action cannot be undone."
        confirmText="Delete"
        cancelText="Cancel"
        variant="danger"
        loading={deleting}
      />

      {/* Strategy Guide */}
      <div className="card mt-8">
        <h3 className="text-lg font-semibold text-text-primary mb-4">
          Strategy Guide
        </h3>
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <StrategyCard
            name="Priority"
            description="Select providers in order of priority. Best for primary/backup setups."
            icon="🎯"
          />
          <StrategyCard
            name="Random"
            description="Randomly select from available providers. Good for basic load balancing."
            icon="🎲"
          />
          <StrategyCard
            name="Weight"
            description="Select based on configured weights. Fine-tune traffic distribution."
            icon="⚖️"
          />
        </div>
      </div>
    </div>
  );
}

interface GroupCardProps {
  group: Group;
  onEdit: () => void;
  onDelete: () => void;
}

function GroupCard({ group, onEdit, onDelete }: GroupCardProps) {
  return (
    <div className="card relative group hover:border-primary/30 transition-colors">
      <div className="absolute top-4 right-4 flex gap-2 opacity-0 group-hover:opacity-100 transition-opacity">
        <button
          onClick={(e) => { e.stopPropagation(); onEdit(); }}
          className="p-1.5 rounded-lg bg-bg-tertiary text-text-secondary hover:text-primary hover:bg-primary/10 transition-colors"
          title="Edit"
        >
          <EditIcon />
        </button>
        <button
          onClick={(e) => { e.stopPropagation(); onDelete(); }}
          className="p-1.5 rounded-lg bg-bg-tertiary text-text-secondary hover:text-red-400 hover:bg-red-400/10 transition-colors"
          title="Delete"
        >
          <TrashIcon />
        </button>
      </div>

      <div className="flex items-start justify-between mb-4">
        <div>
          <div className="flex items-center gap-2">
            <h3 className="text-lg font-semibold text-text-primary">{group.name}</h3>
            <span className={`px-2 py-0.5 rounded text-xs font-medium ${group.enabled
              ? "bg-green-500/10 text-green-400"
              : "bg-red-500/10 text-red-400"
              }`}>
              {group.enabled ? "Active" : "Disabled"}
            </span>
          </div>
          <div className="text-sm text-text-secondary mt-1 capitalize">
            Strategy: <span className="text-primary">{group.strategy}</span>
          </div>
        </div>
      </div>

      <div className="grid grid-cols-2 gap-4 py-4 border-t border-b border-border-light my-4">
        <div>
          <div className="text-xs text-text-muted mb-1">Priority</div>
          <div className="text-lg font-mono text-text-primary">{group.priority}</div>
        </div>
        <div>
          <div className="text-xs text-text-muted mb-1">Weight</div>
          <div className="text-lg font-mono text-text-primary">{group.weight}</div>
        </div>
      </div>

      <div className="flex justify-between items-center text-sm text-text-secondary">
        <div>
          {group.providers?.length || 0} Providers
        </div>
        <div className="text-xs text-text-muted">
          Updated: {new Date(group.updated_at).toLocaleDateString()}
        </div>
      </div>
    </div>
  );
}

interface StrategyCardProps {
  name: string;
  description: string;
  icon: string;
}

function StrategyCard({ name, description, icon }: StrategyCardProps) {
  return (
    <div className="p-4 rounded-xl bg-bg-secondary border border-border-light hover:border-primary/30 transition-colors">
      <div className="flex items-center gap-3 mb-2">
        <span className="text-xl">{icon}</span>
        <h4 className="font-semibold text-text-primary">{name}</h4>
      </div>
      <p className="text-sm text-text-secondary">{description}</p>
    </div>
  );
}
