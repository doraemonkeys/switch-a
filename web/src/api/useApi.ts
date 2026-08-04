import { useContext } from "react";
import { type ApiClient } from "./client";
import { APICatalogContext, ApiContext } from "./context";
import type { APICatalogContextValue } from "./context";

// Hook to access API client
export function useApi(): ApiClient {
  const context = useContext(ApiContext);
  if (!context) {
    throw new Error("useApi must be used within ApiProvider");
  }
  return context;
}

export function useAPICatalog(): APICatalogContextValue {
  const context = useContext(APICatalogContext);
  if (!context) {
    throw new Error("useAPICatalog must be used within APICatalogProvider");
  }
  return context;
}
