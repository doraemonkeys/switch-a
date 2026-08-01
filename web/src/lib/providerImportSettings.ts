import type { BackoffPolicy } from "../api";
import { FORM_CONSTRAINTS } from "../config/constants";
import { parseGoDurationMilliseconds } from "./utils";

export const PROVIDER_IMPORT_MAX_PROVIDER_ID_LENGTH = 200;
export const PROVIDER_IMPORT_MAX_PROVIDER_NAME_LENGTH = 200;
export const PROVIDER_IMPORT_MAX_SCHEDULING_VALUE = 1_000_000;
export const PROVIDER_IMPORT_MAX_RETRIES =
  FORM_CONSTRAINTS.MAX_PROVIDER_RETRIES;

export interface ProviderImportCreateDraft {
  providerId: string;
  name: string;
  priority: number;
  weight: number;
  concurrency: number;
  maxRetries: number;
  backoff: BackoffPolicy;
}

export interface ProviderImportNewProviderDefaults {
  weight: number;
  maxRetries: number;
  backoff: BackoffPolicy;
}

export interface ProviderImportValidationError {
  field: keyof ProviderImportCreateDraft;
  message: string;
}

export function cloneProviderImportBackoff(
  backoff: BackoffPolicy,
): BackoffPolicy {
  return { ...backoff };
}

export function getProviderImportNewProviderDefaultsError(
  defaults: ProviderImportNewProviderDefaults,
): ProviderImportValidationError | null {
  if (
    !Number.isSafeInteger(defaults.weight) ||
    defaults.weight < 1 ||
    defaults.weight > PROVIDER_IMPORT_MAX_SCHEDULING_VALUE
  ) {
    return {
      field: "weight",
      message: `Weight must be an integer from 1 to ${PROVIDER_IMPORT_MAX_SCHEDULING_VALUE.toLocaleString()}`,
    };
  }
  if (
    !Number.isSafeInteger(defaults.maxRetries) ||
    defaults.maxRetries < 0 ||
    defaults.maxRetries > PROVIDER_IMPORT_MAX_RETRIES
  ) {
    return {
      field: "maxRetries",
      message: `Max retries must be an integer from 0 to ${PROVIDER_IMPORT_MAX_RETRIES}`,
    };
  }

  const initialDelay = parseGoDurationMilliseconds(
    defaults.backoff.initial_delay,
  );
  const maxDelay = parseGoDurationMilliseconds(defaults.backoff.max_delay);
  if (initialDelay === null) {
    return {
      field: "backoff",
      message: "Initial retry delay must use a duration such as 500ms or 1s",
    };
  }
  if (maxDelay === null) {
    return {
      field: "backoff",
      message: "Maximum retry delay must use a duration such as 5s or 1m",
    };
  }
  if (maxDelay > 0 && initialDelay > maxDelay) {
    return {
      field: "backoff",
      message: "Initial retry delay cannot exceed the maximum delay",
    };
  }
  const multiplier = defaults.backoff.multiplier ?? 0;
  if (
    !Number.isFinite(multiplier) ||
    (multiplier !== 0 &&
      (multiplier < 1 || multiplier > FORM_CONSTRAINTS.BACKOFF_MAX_MULTIPLIER))
  ) {
    return {
      field: "backoff",
      message: `Retry multiplier must be from 1 to ${FORM_CONSTRAINTS.BACKOFF_MAX_MULTIPLIER}`,
    };
  }
  return null;
}
