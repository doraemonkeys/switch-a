import type {
  ChatGPTLoginStatusResponse,
  Provider,
  ProviderCredentialType,
  ProviderAuthStatus,
  ProviderAuthView,
} from "../api/types";
import { PROVIDER_CREDENTIAL_TYPES } from "../config/constants";

type AuthCarrier = {
  auth?: ProviderAuthView | null;
};

export const AUTH_STATUS_BADGE_CLASS: Record<ProviderAuthStatus, string> = {
  not_connected: "bg-bg-tertiary text-text-secondary border border-border/60",
  active: "bg-success-light text-success border border-success/20",
  reauth_required: "bg-danger-light text-danger border border-danger/20",
};

export function resolveProviderAuthView(
  source?: AuthCarrier | null,
): ProviderAuthView | null {
  if (!source) {
    return null;
  }
  return source.auth ?? null;
}

export function resolveLoginAuthView(
  response?: ChatGPTLoginStatusResponse | null,
): ProviderAuthView | null {
  if (!response) {
    return null;
  }
  return response.auth ?? null;
}

export function formatProviderCredentialType(
  credentialType?: ProviderCredentialType,
): string {
  if (credentialType === PROVIDER_CREDENTIAL_TYPES.CHATGPT) {
    return "GPT Login";
  }
  return "API Key";
}

export function hasProviderCredentialSnapshot(
  provider?: Provider | null,
): boolean {
  return Boolean(
    provider && provider.credential_type === PROVIDER_CREDENTIAL_TYPES.CHATGPT,
  );
}
