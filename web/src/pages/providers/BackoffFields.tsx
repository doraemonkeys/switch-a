import type { BackoffPolicy, ProviderInput } from "../../api";
import { BackoffPolicyEditor } from "../../components/provider-settings/BackoffPolicyEditor";
import { PROVIDER_DEFAULTS } from "../../config/constants";

interface BackoffSectionProps {
  formData: ProviderInput;
  setFormData: React.Dispatch<React.SetStateAction<ProviderInput>>;
  expanded: boolean;
  onToggle: () => void;
}

function effectiveBackoff(backoff?: BackoffPolicy): BackoffPolicy {
  return {
    initial_delay:
      backoff?.initial_delay ?? PROVIDER_DEFAULTS.BACKOFF.INITIAL_DELAY,
    max_delay: backoff?.max_delay ?? PROVIDER_DEFAULTS.BACKOFF.MAX_DELAY,
    multiplier: backoff?.multiplier ?? PROVIDER_DEFAULTS.BACKOFF.MULTIPLIER,
    jitter: backoff?.jitter ?? PROVIDER_DEFAULTS.BACKOFF.JITTER,
  };
}

export function BackoffSection({
  formData,
  setFormData,
  expanded,
  onToggle,
}: BackoffSectionProps) {
  return (
    <BackoffPolicyEditor
      backoff={effectiveBackoff(formData.backoff)}
      maxRetries={formData.max_retries ?? 0}
      expanded={expanded}
      onToggle={onToggle}
      onChange={(backoff) =>
        setFormData((current) => ({ ...current, backoff }))
      }
    />
  );
}
