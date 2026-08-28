import type { ProviderFormData } from "./types";
import { FAILOVER_SCOPES } from "../../config/constants";

/** Check if failover section has non-default configuration */
export function hasFailoverConfig(formData: ProviderFormData): boolean {
  return Boolean(
    formData.vendor ||
    (formData.failover_scope &&
      formData.failover_scope !== FAILOVER_SCOPES.ANY) ||
    (formData.accept_failover &&
      formData.accept_failover !== FAILOVER_SCOPES.ANY),
  );
}
