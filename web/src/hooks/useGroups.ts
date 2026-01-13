import { useState, useEffect, useCallback } from "react";
import { useApi } from "../api";
import type { Group, GroupInput } from "../api/client";

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
  const [groups, setGroups] = useState<Group[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<Error | null>(null);

  const refetch = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await api.groups.list();
      setGroups(data);
    } catch (err) {
      setError(
        err instanceof Error ? err : new Error("Failed to fetch groups"),
      );
    } finally {
      setLoading(false);
    }
  }, [api]);

  useEffect(() => {
    refetch();
  }, [refetch]);

  const createGroup = useCallback(
    async (data: GroupInput): Promise<Group> => {
      const group = await api.groups.create(data);
      await refetch();
      return group;
    },
    [api, refetch],
  );

  const updateGroup = useCallback(
    async (id: string, data: GroupInput): Promise<Group> => {
      const group = await api.groups.update(id, data);
      await refetch();
      return group;
    },
    [api, refetch],
  );

  const deleteGroup = useCallback(
    async (id: string): Promise<void> => {
      await api.groups.delete(id);
      await refetch();
    },
    [api, refetch],
  );

  const enableGroup = useCallback(
    async (id: string): Promise<void> => {
      await api.groups.enable(id);
      await refetch();
    },
    [api, refetch],
  );

  const disableGroup = useCallback(
    async (id: string): Promise<void> => {
      await api.groups.disable(id);
      await refetch();
    },
    [api, refetch],
  );

  return {
    groups,
    loading,
    error,
    refetch,
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
  const [group, setGroup] = useState<Group | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<Error | null>(null);

  const refetch = useCallback(async () => {
    if (!id) {
      setLoading(false);
      return;
    }
    setLoading(true);
    setError(null);
    try {
      const data = await api.groups.get(id);
      setGroup(data);
    } catch (err) {
      setError(err instanceof Error ? err : new Error("Failed to fetch group"));
    } finally {
      setLoading(false);
    }
  }, [api, id]);

  useEffect(() => {
    refetch();
  }, [refetch]);

  return { group, loading, error, refetch };
}
