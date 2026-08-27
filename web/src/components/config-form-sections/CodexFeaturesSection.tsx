import { CODEX_FEATURES, type CodexFeatureKey } from "../../config";
import { ConfigSection } from "../ConfigSection";
import { ModifiedBadge } from "./ModifiedBadge";
import type { SectionProps } from "./types";

export function CodexFeaturesSection({
  getValue,
  handleChange,
  getDefault,
}: SectionProps) {
  const isEnabled = (key: CodexFeatureKey) => {
    const feature = CODEX_FEATURES.find((candidate) => candidate.key === key);
    const value = getValue(key, feature?.defaultValue ?? false).toLowerCase();
    return value === "true" || value === "1";
  };

  return (
    <ConfigSection
      title="Codex Protocol Features"
      description="Roll out Codex protocol boundaries independently. Continuity includes session identity; the provider Cookie jar is separate."
      icon={"\u{1F6E1}\uFE0F"}
    >
      <div className="space-y-3">
        {CODEX_FEATURES.map((feature) => {
          const enabled = isEnabled(feature.key);
          const missingDependency = feature.requires.find(
            (dependency) => !isEnabled(dependency),
          );
          const requiredByEnabledFeature =
            enabled &&
            CODEX_FEATURES.some(
              (candidate) =>
                (candidate.requires as readonly CodexFeatureKey[]).includes(
                  feature.key,
                ) && isEnabled(candidate.key),
            );
          const disabled =
            (!enabled && missingDependency !== undefined) ||
            requiredByEnabledFeature;
          const currentValue = getValue(feature.key, feature.defaultValue);

          return (
            <label
              key={feature.key}
              className={`flex items-start gap-3 p-4 rounded-xl bg-bg-secondary border border-border-light transition-colors ${
                disabled
                  ? "cursor-not-allowed opacity-70"
                  : "cursor-pointer hover:border-primary/30"
              }`}
            >
              <input
                type="checkbox"
                id={feature.key}
                checked={enabled}
                disabled={disabled}
                onChange={(event) =>
                  handleChange(feature.key, String(event.target.checked))
                }
              />
              <span className="min-w-0">
                <span className="flex items-center gap-2 font-medium text-text-primary">
                  {feature.label}
                  <ModifiedBadge
                    configKey={feature.key}
                    currentValue={currentValue}
                    getDefault={getDefault}
                  />
                </span>
                <span className="mt-1 block text-xs text-text-muted">
                  {feature.description}
                </span>
                {missingDependency !== undefined && (
                  <span className="mt-1 block text-xs text-warning">
                    Enable Upstream Header Hygiene first.
                  </span>
                )}
                {requiredByEnabledFeature && (
                  <span className="mt-1 block text-xs text-warning">
                    Disable dependent Codex features first.
                  </span>
                )}
              </span>
            </label>
          );
        })}
      </div>
    </ConfigSection>
  );
}
