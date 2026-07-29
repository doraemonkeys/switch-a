import { useState } from "react";
import { useApi } from "../api";
import { useQuery } from "./useQuery";

interface UseConfigResult {
  /** Default configuration values from server */
  defaults: Record<string, string>;
  /** User-modified configuration values */
  values: Record<string, string>;
  /** Effective configuration; explicit values take precedence over defaults. */
  config: Record<string, string>;
  loading: boolean;
  error: Error | null;
  saving: boolean;
  refetch: () => Promise<void>;
  /** Update config - only sends changed values */
  updateConfig: (data: Record<string, string>) => Promise<void>;
  /** Check if a specific key has been modified from default */
  isModified: (key: string) => boolean;
}

export function useConfig(): UseConfigResult {
  const api = useApi();
  const query = useQuery(() => api.config.get(), {
    errorMessage: "Failed to fetch config",
  });
  const [saving, setSaving] = useState(false);
  const [mutationError, setMutationError] = useState<Error | null>(null);

  const defaults = query.data?.defaults ?? {};
  const values = query.data?.values ?? {};
  const config = { ...defaults, ...values };

  const updateConfig = async (data: Record<string, string>): Promise<void> => {
    setSaving(true);
    setMutationError(null);
    try {
      await api.config.update(data);
      await query.refetch();
    } catch (reason) {
      const error =
        reason instanceof Error ? reason : new Error("Failed to update config");
      setMutationError(error);
      throw reason;
    } finally {
      setSaving(false);
    }
  };

  return {
    defaults,
    values,
    config,
    loading: query.loading,
    error: mutationError ?? query.error,
    saving,
    refetch: query.refetch,
    updateConfig,
    isModified: (key: string) => key in values,
  };
}
