import type {
  ChatGPTLoginStatusResponse,
  CredentialSessionKind,
  Provider,
  ProviderCredentialSession,
  ProviderAuthStatus,
  ProviderAuthView,
} from "../api/types";
import { PROVIDER_CREDENTIAL_TYPES } from "../config/constants";

export const AUTH_STATUS_BADGE_CLASS: Record<ProviderAuthStatus, string> = {
  not_connected: "bg-bg-tertiary text-text-secondary border border-border/60",
  active: "bg-success-light text-success border border-success/20",
  reauth_required: "bg-danger-light text-danger border border-danger/20",
};

export function resolveProviderAuthView(
  provider?: Provider | null,
): ProviderAuthView | null {
  return resolveCredentialSessionAuthView(
    resolveProviderPrimaryCredentialSession(provider),
  );
}

export function resolveCredentialSessionAuthView(
  session?: ProviderCredentialSession | null,
): ProviderAuthView | null {
  if (!session) {
    return null;
  }
  return {
    type: session.kind,
    status: session.auth_state.status,
    reason: session.auth_state.status_reason,
    email: session.auth_state.email,
    account_id: session.auth_state.account_id,
    plan_type: session.auth_state.plan_type,
    usage: session.auth_state.usage_snapshot,
    expires_at: session.auth_state.expires_at,
    last_refresh_at: session.auth_state.last_refresh_at,
    last_error: session.auth_state.last_error,
  };
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
  source?: Provider | CredentialSessionKind | null,
): string {
  const kind =
    typeof source === "string" ? source : resolveProviderCredentialKind(source);
  if (kind === "mixed") {
    return "Mixed";
  }
  if (kind === PROVIDER_CREDENTIAL_TYPES.CHATGPT) {
    return "GPT Login";
  }
  if (kind === PROVIDER_CREDENTIAL_TYPES.API_KEY) {
    return "API Key";
  }
  return "Not configured";
}

export function resolveProviderCredentialSession(
  provider: Provider | null | undefined,
  apiType: string,
): ProviderCredentialSession | null {
  if (!provider) {
    return null;
  }
  const sessionID = provider.api_types.find(
    (entry) => entry.api_type === apiType,
  )?.credential_session_id;
  if (!sessionID) {
    return null;
  }
  return (
    provider.credential_sessions.find((session) => session.id === sessionID) ??
    null
  );
}

export function resolveProviderCredentialKind(
  provider?: Provider | null,
): CredentialSessionKind | "mixed" | null {
  if (!provider) {
    return null;
  }
  const kinds = new Set(
    provider.api_types
      .map((entry) =>
        provider.credential_sessions.find(
          (session) => session.id === entry.credential_session_id,
        ),
      )
      .filter((session): session is ProviderCredentialSession =>
        Boolean(session),
      )
      .map((session) => session.kind),
  );
  if (kinds.size === 0) {
    return null;
  }
  if (kinds.size > 1) {
    return "mixed";
  }
  return [...kinds][0] ?? null;
}

export function resolveProviderPrimaryCredentialSession(
  provider?: Provider | null,
): ProviderCredentialSession | null {
  if (!provider) {
    return null;
  }
  const codexSession = resolveProviderCredentialSession(provider, "codex");
  if (codexSession?.kind === PROVIDER_CREDENTIAL_TYPES.CHATGPT) {
    return codexSession;
  }
  const firstRoute = provider.api_types[0];
  if (!firstRoute) {
    return null;
  }
  return resolveProviderCredentialSession(provider, firstRoute.api_type);
}

export function resolveProviderChatGPTCredentialSession(
  provider?: Provider | null,
): ProviderCredentialSession | null {
  if (!provider) {
    return null;
  }
  const codexSession = resolveProviderCredentialSession(provider, "codex");
  if (codexSession?.kind === PROVIDER_CREDENTIAL_TYPES.CHATGPT) {
    return codexSession;
  }
  return (
    provider.credential_sessions.find(
      (session) => session.kind === PROVIDER_CREDENTIAL_TYPES.CHATGPT,
    ) ?? null
  );
}

interface CodexAuthExportAvailabilityBase {
  session: ProviderCredentialSession;
  referencingRouteTargets: Provider[];
}

export type CodexAuthExportAvailability =
  | {
      kind: "unavailable";
      reason: "missing_chatgpt_session" | "credential_inactive";
      session: ProviderCredentialSession | null;
      referencingRouteTargets: Provider[];
    }
  | (CodexAuthExportAvailabilityBase & {
      kind: "blocked";
      blockingRouteTargets: Provider[];
    })
  | (CodexAuthExportAvailabilityBase & {
      kind: "available";
      blockingRouteTargets: [];
    });

export function resolveCodexAuthExportAvailability(
  provider: Provider,
  providers: Provider[],
): CodexAuthExportAvailability {
  const session = resolveProviderChatGPTCredentialSession(provider);
  if (!session) {
    return {
      kind: "unavailable",
      reason: "missing_chatgpt_session",
      session: null,
      referencingRouteTargets: [],
    };
  }
  if (session.auth_state.status !== "active") {
    return {
      kind: "unavailable",
      reason: "credential_inactive",
      session,
      referencingRouteTargets: [],
    };
  }

  const routeTargets = providers.some(
    (candidate) => candidate.id === provider.id,
  )
    ? providers
    : [...providers, provider];
  const referencingRouteTargets = routeTargets.filter((candidate) =>
    candidate.api_types.some(
      (entry) => entry.credential_session_id === session.id,
    ),
  );
  const blockingRouteTargets = referencingRouteTargets.filter(
    (candidate) => candidate.enabled,
  );
  if (blockingRouteTargets.length > 0) {
    return {
      kind: "blocked",
      session,
      referencingRouteTargets,
      blockingRouteTargets,
    };
  }
  return {
    kind: "available",
    session,
    referencingRouteTargets,
    blockingRouteTargets: [],
  };
}

export function hasProviderCredentialSnapshot(
  provider?: Provider | null,
): boolean {
  return resolveProviderChatGPTCredentialSession(provider) !== null;
}
