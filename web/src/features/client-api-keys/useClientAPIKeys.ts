import { useState } from "react";
import { useApi } from "@/api/useApi";
import type {
  ClientAPIKey,
  ClientAccessMode,
  ClientAPIKeysState,
} from "@/api/client-api-keys/types";
import { useQuery } from "@/hooks/useQuery";

function withoutKey(state: ClientAPIKeysState | null, id: string) {
  return (
    state && { ...state, keys: state.keys.filter((item) => item.id !== id) }
  );
}

export function useClientAPIKeys() {
  const api = useApi();
  const query = useQuery(() => api.clientApiKeys.get(), { queryKey: api });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const mutate = async <T>(action: () => Promise<T>): Promise<T | null> => {
    setBusy(true);
    setError(null);
    try {
      return await action();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Operation failed");
      return null;
    } finally {
      setBusy(false);
    }
  };

  const upsert = (item: ClientAPIKey) => {
    query.updateData(
      (state) =>
        state && {
          ...state,
          keys: state.keys.some((current) => current.id === item.id)
            ? state.keys.map((current) =>
                current.id === item.id ? item : current,
              )
            : [...state.keys, item],
        },
    );
  };

  const setPolicy = (mode: ClientAccessMode) =>
    mutate(async () => {
      const policy = await api.clientApiKeys.setPolicy(mode);
      query.updateData((state) => state && { ...state, ...policy });
    });
  const create = (name: string, key?: string) =>
    mutate(async () => {
      const item =
        key === undefined
          ? await api.clientApiKeys.generate(name)
          : await api.clientApiKeys.add(name, key);
      upsert(item);
      return item;
    });
  const rename = async (id: string, name: string) =>
    (await mutate(async () => {
      upsert(await api.clientApiKeys.rename(id, name));
      return true;
    })) === true;
  const remove = async (id: string) =>
    (await mutate(async () => {
      await api.clientApiKeys.delete(id);
      query.updateData((state) => withoutKey(state, id));
      return true;
    })) === true;

  return {
    query,
    busy,
    error,
    clearError: () => setError(null),
    setPolicy,
    create,
    rename,
    remove,
  };
}
