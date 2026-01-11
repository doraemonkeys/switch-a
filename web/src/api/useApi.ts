import { useContext } from "react";
import { type ApiClient } from "./client";
import { ApiContext } from "./context";

// Hook to access API client
export function useApi(): ApiClient {
  const context = useContext(ApiContext);
  if (!context) {
    throw new Error("useApi must be used within ApiProvider");
  }
  return context;
}
