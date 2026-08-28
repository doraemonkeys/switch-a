import { describe, expect, it } from "vitest";
import { PROVIDER_CREDENTIAL_TYPES } from "../config/constants";
import type { Provider } from "../api";
import {
  formatProviderCredentialType,
  resolveLoginAuthView,
  resolveProviderAuthView,
} from "./providerAuth";

function providerWithAuthState(
  authState: Provider["credential_sessions"][number]["auth_state"],
): Provider {
  return {
    id: "provider-1",
    name: "Provider",
    api_types: [
      {
        api_type: "codex",
        base_url: "https://chatgpt.com/backend-api/codex",
        credential_session_id: "credential-1",
      },
    ],
    auth_mode: "bearer",
    credential_sessions: [
      {
        id: "credential-1",
        kind: PROVIDER_CREDENTIAL_TYPES.CHATGPT,
        version: 1,
        subject: { kind: "account", value: "account-1" },
        auth_state: authState,
      },
    ],
    group_id: null,
    weight: 1,
    priority: 0,
    concurrency: 0,
    max_retries: 0,
    vendor: "",
    failover_scope: "any",
    accept_failover: "any",
    enabled: true,
    created_at: "2026-08-28T00:00:00Z",
    updated_at: "2026-08-28T00:00:00Z",
  };
}

describe("providerAuth", () => {
  it("derives the auth view from the routed credential session", () => {
    const authView = resolveProviderAuthView(
      providerWithAuthState({
        status: "reauth_required",
        status_reason: "invalid_grant",
      }),
    );

    expect(authView).toEqual({
      type: PROVIDER_CREDENTIAL_TYPES.CHATGPT,
      status: "reauth_required",
      reason: "invalid_grant",
    });
  });

  it("returns null when no provider snapshot is available", () => {
    const authView = resolveProviderAuthView();

    expect(authView).toBeNull();
  });

  it("labels routed GPT sessions without relying on removed provider fields", () => {
    const provider = providerWithAuthState({ status: "active" });

    expect(formatProviderCredentialType(provider)).toBe("GPT Login");
    expect(formatProviderCredentialType()).toBe("Not configured");
  });

  it("uses the explicit login response auth snapshot when polling completes", () => {
    const authView = resolveLoginAuthView({
      login_id: "login-1",
      status: "completed",
      auth: {
        type: PROVIDER_CREDENTIAL_TYPES.CHATGPT,
        status: "active",
        email: "user@example.com",
      },
    });

    expect(authView).toEqual({
      type: PROVIDER_CREDENTIAL_TYPES.CHATGPT,
      status: "active",
      email: "user@example.com",
    });
  });
});
