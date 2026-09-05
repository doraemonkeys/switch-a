import { describe, it, expect } from "vitest";
import { buildImportRequest, hasVisibleChanges } from "./helpers";
import type { ExportedConfig, ImportPreviewResponse } from "@/api/types";
describe("client identity backup UI", () => {
  it("preserves the portable snapshot without rewriting identity records", () => {
    const snapshot = {
      version: 1,
      disguise: { logins: [{ device_id: "device" }] },
      client_identity: { clients: [{ id: "client" }] },
      continuity: [],
      keyring_hmac: [],
    };
    const config = {
      version: "5.0",
      providers: [],
      credential_sessions: [],
      groups: [],
      routing_policies: [],
      settings: {},
      internal_error_rules: [],
      codex_state: snapshot,
    } as unknown as ExportedConfig;
    expect(buildImportRequest(config, { mode: "full" }).codex_state).toBe(
      snapshot,
    );
  });
  it("enables import for identity-only changes and excludes them in settings-only mode", () => {
    const zero = { add: 0, update: 0, delete: 0, unchanged: 0 };
    const preview = {
      changes: {
        providers: zero,
        credential_sessions: zero,
        groups: zero,
        routing_policies: zero,
        settings: zero,
        internal_error_rules: zero,
        codex_state: { ...zero, update: 1 },
      },
    } as ImportPreviewResponse;
    expect(hasVisibleChanges(preview, "full")).toBe(true);
    expect(hasVisibleChanges(preview, "selection")).toBe(true);
    expect(hasVisibleChanges(preview, "settings_only")).toBe(false);
  });
});
