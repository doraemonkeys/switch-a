import { describe, expect, it } from "vitest";
import { parseProvider, ProviderContractError } from "./provider-contract";

function providerPayload() {
  return {
    id: "provider-gpt",
    name: "GPT Provider",
    api_types: [
      {
        api_type: "codex",
        base_url: "https://chatgpt.com/backend-api/codex",
        credential_session_id: "credential-gpt",
      },
    ],
    auth_mode: "bearer",
    credential_sessions: [
      {
        id: "credential-gpt",
        kind: "chatgpt",
        version: 2,
        subject: { kind: "account", value: "account-1" },
        auth_state: {
          status: "active",
          email: "user@example.com",
        },
      },
    ],
    usage_limit_policy: "switch_provider",
    group_id: null,
    weight: 1,
    priority: 0,
    concurrency: 0,
    max_retries: 0,
    vendor: "openai",
    failover_scope: "any",
    accept_failover: "any",
    enabled: true,
    created_at: "2026-08-28T00:00:00Z",
    updated_at: "2026-08-28T00:00:00Z",
  };
}

describe("provider response contract", () => {
  it("preserves the route-to-credential-session relationship", () => {
    const provider = parseProvider(providerPayload());

    expect(provider.api_types[0]?.credential_session_id).toBe("credential-gpt");
    expect(provider.credential_sessions[0]).toMatchObject({
      id: "credential-gpt",
      kind: "chatgpt",
      auth_state: { status: "active", email: "user@example.com" },
    });
  });

  it("rejects legacy provider payloads instead of silently guessing API Key", () => {
    const legacyPayload = {
      ...providerPayload(),
      credential_type: "chatgpt",
      credential_sessions: undefined,
    };

    expect(() => parseProvider(legacyPayload)).toThrow(ProviderContractError);
  });

  it("rejects routes that reference a missing credential session", () => {
    const payload = providerPayload();
    payload.api_types[0]!.credential_session_id = "missing-session";

    expect(() => parseProvider(payload)).toThrow(
      /references absent credential session/,
    );
  });
});
