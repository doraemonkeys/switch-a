import { type ReactNode } from "react";
import { createApiClient, api as defaultApi } from "./client";
import {
  type ApiClientDeps,
  browserStorage,
  browserHttpClient,
} from "./interfaces";
import { API_BASE } from "../config";
import { APICatalogContext, ApiContext } from "./context";
import { useApi } from "./useApi";
import { useQuery } from "../hooks/useQuery";

interface ApiProviderProps {
  children: ReactNode;
  deps?: Partial<ApiClientDeps>;
}

// Provider for dependency injection (useful for testing)
export function ApiProvider({ children, deps }: ApiProviderProps) {
  const apiClient = deps
    ? createApiClient({
        storage: deps.storage ?? browserStorage,
        httpClient: deps.httpClient ?? browserHttpClient,
        baseUrl: deps.baseUrl ?? API_BASE,
        onUnauthorized: deps.onUnauthorized,
      })
    : defaultApi;

  return (
    <ApiContext.Provider value={apiClient}>{children}</ApiContext.Provider>
  );
}

/** Fetches the catalog once inside the authenticated tree and shares it. */
export function APICatalogProvider({ children }: { children: ReactNode }) {
  const apiClient = useApi();
  const query = useQuery(() => apiClient.apiCatalog.get(), {
    queryKey: apiClient,
    errorMessage: "Failed to fetch the API catalog",
  });

  return (
    <APICatalogContext.Provider
      value={{
        catalog: query.data,
        loading: query.loading,
        error: query.error,
        refetch: query.refetch,
      }}
    >
      {children}
    </APICatalogContext.Provider>
  );
}
