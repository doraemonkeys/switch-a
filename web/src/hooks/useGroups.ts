import { useApi } from "../api";
import type { Group, GroupInput } from "../api/client";
import { useQuery } from "./useQuery";

interface UseGroupsResult {
  groups: Group[];
  loading: boolean;
  error: Error | null;
  refetch: () => Promise<void>;
  createGroup: (data: GroupInput) => Promise<Group>;
  updateGroup: (id: string, data: GroupInput) => Promise<Group>;
  deleteGroup: (id: string) => Promise<void>;
  enableGroup: (id: string) => Promise<void>;
  disableGroup: (id: string) => Promise<void>;
}

export function useGroups(): UseGroupsResult {
  const api = useApi();
  const query = useQuery(() => api.groups.list(), {
    errorMessage: "Failed to fetch groups",
  });

  const createGroup = async (data: GroupInput): Promise<Group> => {
    const group = await api.groups.create(data);
    await query.refetch();
    return group;
  };

  const updateGroup = async (id: string, data: GroupInput): Promise<Group> => {
    const group = await api.groups.update(id, data);
    await query.refetch();
    return group;
  };

  const deleteGroup = async (id: string): Promise<void> => {
    await api.groups.delete(id);
    await query.refetch();
  };

  const enableGroup = async (id: string): Promise<void> => {
    await api.groups.enable(id);
    await query.refetch();
  };

  const disableGroup = async (id: string): Promise<void> => {
    await api.groups.disable(id);
    await query.refetch();
  };

  return {
    groups: query.data ?? [],
    loading: query.loading,
    error: query.error,
    refetch: query.refetch,
    createGroup,
    updateGroup,
    deleteGroup,
    enableGroup,
    disableGroup,
  };
}

interface UseGroupResult {
  group: Group | null;
  loading: boolean;
  error: Error | null;
  refetch: () => Promise<void>;
}

export function useGroup(id: string): UseGroupResult {
  const api = useApi();
  const query = useQuery(() => api.groups.get(id), {
    queryKey: id,
    skip: !id,
    errorMessage: "Failed to fetch group",
  });

  return {
    group: query.data,
    loading: query.loading,
    error: query.error,
    refetch: query.refetch,
  };
}
