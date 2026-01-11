import { useState, useEffect, useCallback } from "react";
import { useApi } from "../api";
import type { Provider, ProviderInput } from "../api/client";

interface UseProvidersResult {
  providers: Provider[];
  loading: boolean;
  error: Error | null;
  refetch: () => Promise<void>;
  createProvider: (data: ProviderInput) => Promise<Provider>;
  updateProvider: (id: string, data: ProviderInput) => Promise<Provider>;
  deleteProvider: (id: string) => Promise<void>;
  enableProvider: (id: string) => Promise<void>;
  disableProvider: (id: string) => Promise<void>;
  resetProvider: (id: string) => Promise<void>;
}

export function useProviders(): UseProvidersResult {
  const api = useApi();
  const [providers, setProviders] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<Error | null>(null);

  const refetch = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await api.providers.list();
      setProviders(data);
    } catch (err) {
      setError(
        err instanceof Error ? err : new Error("Failed to fetch providers"),
      );
    } finally {
      setLoading(false);
    }
  }, [api]);

  useEffect(() => {
    refetch();
  }, [refetch]);

  const createProvider = useCallback(
    async (data: ProviderInput): Promise<Provider> => {
      const provider = await api.providers.create(data);
      await refetch();
      return provider;
    },
    [api, refetch],
  );

  const updateProvider = useCallback(
    async (id: string, data: ProviderInput): Promise<Provider> => {
      const provider = await api.providers.update(id, data);
      await refetch();
      return provider;
    },
    [api, refetch],
  );

  const deleteProvider = useCallback(
    async (id: string): Promise<void> => {
      await api.providers.delete(id);
      await refetch();
    },
    [api, refetch],
  );

  const enableProvider = useCallback(
    async (id: string): Promise<void> => {
      await api.providers.enable(id);
      await refetch();
    },
    [api, refetch],
  );

  const disableProvider = useCallback(
    async (id: string): Promise<void> => {
      await api.providers.disable(id);
      await refetch();
    },
    [api, refetch],
  );

  const resetProvider = useCallback(
    async (id: string): Promise<void> => {
      await api.providers.reset(id);
      await refetch();
    },
    [api, refetch],
  );

  return {
    providers,
    loading,
    error,
    refetch,
    createProvider,
    updateProvider,
    deleteProvider,
    enableProvider,
    disableProvider,
    resetProvider,
  };
}

interface UseProviderResult {
  provider: Provider | null;
  loading: boolean;
  error: Error | null;
  refetch: () => Promise<void>;
}

export function useProvider(id: string): UseProviderResult {
  const api = useApi();
  const [provider, setProvider] = useState<Provider | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<Error | null>(null);

  const refetch = useCallback(async () => {
    if (!id) return;
    setLoading(true);
    setError(null);
    try {
      const data = await api.providers.get(id);
      setProvider(data);
    } catch (err) {
      setError(
        err instanceof Error ? err : new Error("Failed to fetch provider"),
      );
    } finally {
      setLoading(false);
    }
  }, [api, id]);

  useEffect(() => {
    refetch();
  }, [refetch]);

  return { provider, loading, error, refetch };
}
