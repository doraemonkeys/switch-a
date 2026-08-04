import { createContext } from "react";
import { type ApiClient } from "./client";
import type { APICatalog } from "./api-catalog";

export const ApiContext = createContext<ApiClient | null>(null);

export interface APICatalogContextValue {
  catalog: APICatalog | null;
  loading: boolean;
  error: Error | null;
  refetch: () => Promise<void>;
}

export const APICatalogContext = createContext<APICatalogContextValue | null>(
  null,
);
