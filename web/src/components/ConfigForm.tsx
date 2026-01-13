import React, { useState, useMemo } from "react";
import { useToast } from "../hooks/useToast";
import {
  RoutingStrategySection,
  AuthSettingsSection,
  StickySessionSection,
  CircuitBreakerSection,
  ConfigFormActions,
} from "./ConfigFormSections";

interface ConfigFormProps {
  initialConfig: Record<string, string>;
  /** Default values from server (for reset to default) */
  defaults?: Record<string, string>;
  /** Keys that have been modified from default */
  modifiedKeys?: Set<string>;
  onSave: (config: Record<string, string>) => Promise<void>;
  saving: boolean;
}

function normalizeConfig(
  config: Record<string, string>,
): Record<string, string> {
  const normalized: Record<string, string> = {};
  Object.entries(config).forEach(([key, value]) => {
    normalized[key] = String(value);
  });
  return normalized;
}

export function ConfigForm({
  initialConfig,
  defaults = {},
  modifiedKeys = new Set(),
  onSave,
  saving,
}: ConfigFormProps) {
  const toast = useToast();

  // Memoize the normalized initial config to maintain stable reference
  const normalizedInitialConfig = useMemo(
    () => normalizeConfig(initialConfig),
    [initialConfig],
  );

  // Track the config we're synced to, and local edits
  const [syncedConfig, setSyncedConfig] = useState(normalizedInitialConfig);
  const [localConfig, setLocalConfig] = useState(normalizedInitialConfig);
  const [isDirty, setIsDirty] = useState(false);

  // Sync when initialConfig changes (React-recommended pattern for prop-to-state sync)
  // See: https://react.dev/learn/you-might-not-need-an-effect#adjusting-some-state-when-a-prop-changes
  if (normalizedInitialConfig !== syncedConfig) {
    setSyncedConfig(normalizedInitialConfig);
    setLocalConfig(normalizedInitialConfig);
    setIsDirty(false);
  }

  const handleChange = (key: string, value: string) => {
    setLocalConfig((prev) => ({
      ...prev,
      [key]: value,
    }));
    setIsDirty(true);
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!isDirty) return;

    try {
      await onSave(localConfig);
      setIsDirty(false);
      toast.success("Configuration saved successfully");
    } catch (err) {
      console.error("Failed to save config:", err);
      toast.error(
        err instanceof Error ? err.message : "Failed to save configuration",
      );
    }
  };

  const handleReset = () => {
    setLocalConfig(normalizedInitialConfig);
    setIsDirty(false);
    toast.info("Configuration reset to last saved state");
  };

  // Helper to get value with default fallback
  const getValue = (key: string, defaultValue: string | number | boolean) => {
    if (localConfig[key] !== undefined) {
      return localConfig[key];
    }
    return String(defaultValue);
  };

  // Helper to check if a key is modified from server default
  const isModified = (key: string): boolean => {
    return modifiedKeys.has(key);
  };

  // Helper to get server default value for a key
  const getDefault = (key: string): string | undefined => {
    return defaults[key];
  };

  return (
    <div className="card">
      <div className="mb-6 flex justify-end">
        <span
          className={`badge ${!isDirty ? "badge-success" : "badge-warning"}`}
        >
          <span
            className={`w-2 h-2 ${!isDirty ? "bg-success" : "bg-warning"} rounded-full mr-1.5`}
          ></span>
          {isDirty ? "Unsaved Changes" : "Synced"}
        </span>
      </div>

      <form onSubmit={handleSubmit} className="space-y-8">
        <RoutingStrategySection
          getValue={getValue}
          handleChange={handleChange}
          isModified={isModified}
          getDefault={getDefault}
        />
        <AuthSettingsSection
          getValue={getValue}
          handleChange={handleChange}
          isModified={isModified}
          getDefault={getDefault}
        />
        <StickySessionSection
          getValue={getValue}
          handleChange={handleChange}
          isModified={isModified}
          getDefault={getDefault}
        />
        <CircuitBreakerSection
          getValue={getValue}
          handleChange={handleChange}
          isModified={isModified}
          getDefault={getDefault}
        />
        <ConfigFormActions
          isDirty={isDirty}
          saving={saving}
          onReset={handleReset}
        />
      </form>
    </div>
  );
}
