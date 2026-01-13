import { useState, useEffect, useCallback, useMemo } from "react";
import { useApi } from "../api";

interface UseConfigResult {
  /** Default configuration values from server */
  defaults: Record<string, string>;
  /** User-modified configuration values */
  values: Record<string, string>;
  /** Merged config: values override defaults (for backward compatibility) */
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
  const [defaults, setDefaults] = useState<Record<string, string>>({});
  const [values, setValues] = useState<Record<string, string>>({});
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  // Compute merged config: values override defaults
  const config = useMemo(() => {
    return { ...defaults, ...values };
  }, [defaults, values]);

  const refetch = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await api.config.get();
      setDefaults(data.defaults);
      setValues(data.values);
    } catch (err) {
      setError(
        err instanceof Error ? err : new Error("Failed to fetch config"),
      );
    } finally {
      setLoading(false);
    }
  }, [api]);

  useEffect(() => {
    refetch();
  }, [refetch]);

  const updateConfig = useCallback(
    async (data: Record<string, string>): Promise<void> => {
      setSaving(true);
      setError(null);
      try {
        await api.config.update(data);
        await refetch();
      } catch (err) {
        setError(
          err instanceof Error ? err : new Error("Failed to update config"),
        );
        throw err;
      } finally {
        setSaving(false);
      }
    },
    [api, refetch],
  );

  const isModified = useCallback(
    (key: string): boolean => {
      return key in values;
    },
    [values],
  );

  return {
    defaults,
    values,
    config,
    loading,
    error,
    saving,
    refetch,
    updateConfig,
    isModified,
  };
}
