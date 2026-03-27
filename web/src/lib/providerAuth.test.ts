import { describe, expect, it } from "vitest";
import { PROVIDER_CREDENTIAL_TYPES } from "../config/constants";
import { resolveLoginAuthView, resolveProviderAuthView } from "./providerAuth";

describe("providerAuth", () => {
  it("prefers the explicit auth view from provider snapshots", () => {
    const authView = resolveProviderAuthView({
      auth: {
        type: PROVIDER_CREDENTIAL_TYPES.CHATGPT,
        status: "reauth_required",
        reason: "invalid_grant",
      },
    });

    expect(authView).toEqual({
      type: PROVIDER_CREDENTIAL_TYPES.CHATGPT,
      status: "reauth_required",
      reason: "invalid_grant",
    });
  });

  it("returns null when a provider snapshot has no explicit auth view", () => {
    const authView = resolveProviderAuthView({});

    expect(authView).toBeNull();
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
