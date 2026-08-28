import { useApi } from "../api";
import type { Provider, ProviderInput } from "../api/client";
import { useQuery } from "./useQuery";

interface UseProvidersResult {
  providers: Provider[];
  hasSnapshot: boolean;
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
  const query = useQuery(() => api.providers.list(), {
    errorMessage: "Failed to fetch providers",
  });

  const createProvider = async (data: ProviderInput): Promise<Provider> => {
    const provider = await api.providers.create(data);
    await query.refetch();
    return provider;
  };

  const updateProvider = async (
    id: string,
    data: ProviderInput,
  ): Promise<Provider> => {
    const provider = await api.providers.update(id, data);
    await query.refetch();
    return provider;
  };

  const deleteProvider = async (id: string): Promise<void> => {
    await api.providers.delete(id);
    await query.refetch();
  };

  const enableProvider = async (id: string): Promise<void> => {
    await api.providers.enable(id);
    await query.refetch();
  };

  const disableProvider = async (id: string): Promise<void> => {
    await api.providers.disable(id);
    await query.refetch();
  };

  const resetProvider = async (id: string): Promise<void> => {
    await api.providers.reset(id);
    await query.refetch();
  };

  return {
    providers: query.data ?? [],
    hasSnapshot: query.data !== null,
    loading: query.loading,
    error: query.error,
    refetch: query.refetch,
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
  const query = useQuery(() => api.providers.get(id), {
    queryKey: id,
    skip: !id,
    errorMessage: "Failed to fetch provider",
  });

  return {
    provider: query.data,
    loading: query.loading,
    error: query.error,
    refetch: query.refetch,
  };
}
