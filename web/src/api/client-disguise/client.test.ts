import { describe, it, expect, vi } from "vitest";
import { createClientDisguiseApi } from "./client";
import { parseDisguisePolicy } from "./types";
import { parseDisguiseEvidence } from "./evidence";
import {
  parseDisguiseState,
  parseBinding,
  parseOfficialVersion,
} from "./decoder";

const tuple = { client_type: "desktop", platform: "windows", arch: "amd64" };
const features = {
  user_agent: "desktop/1",
  originator: "desktop",
  client_version: "1",
  desktop_build: "2",
  os_version: "11",
};
const profile = {
  id: "revision",
  tuple,
  features,
  client_version: "1",
  source_id: "ref",
  captured_at: "2026-09-05",
  created_at: "2026-09-05",
};
const binding = {
  credential_session_id: "login",
  tuple,
  mode: "pinned",
  revision_id: "revision",
  reference_source_id: "ref",
  transport_sample_id: "",
  telemetry_path_mappings: null,
};
const reference = {
  id: "ref",
  name: "Reference",
  client_identity_id: "client",
};
const transport = {
  id: "transport",
  name: "TLS sample",
  source_id: "ref",
  captured_at: "2026-09-05",
  tls_profile: "observed",
  http_profile: "observed",
  config: {},
};
describe("client disguise administration contract", () => {
  it("preserves version selection and parses official synchronization results", async () => {
    expect(
      parseBinding({ ...binding, version_source: "official_stable" })
        .version_source,
    ).toBe("official_stable");
    expect(() =>
      parseBinding({ ...binding, version_source: "alpha" }),
    ).toThrow();
    const official = {
      release: {
        version: "0.151.0",
        tag: "rust-v0.151.0",
        url: "https://github.com/openai/codex/releases/tag/rust-v0.151.0",
        published_at: "2026-09-10",
      },
      checked_at: "2026-09-10",
      synced_at: "2026-09-10",
      last_error: "",
    };
    const request = vi.fn().mockResolvedValue(official);
    expect(
      await createClientDisguiseApi(request).syncOfficialVersion(),
    ).toEqual(official);
    expect(request).toHaveBeenCalledWith(
      "/client-disguise/official-version/sync",
      expect.objectContaining({ method: "POST" }),
    );
    expect(
      parseDisguiseState({
        logins: [],
        profiles: [],
        references: [],
        transport_samples: [],
        clients: [],
        official_version: official,
      }).official_version,
    ).toEqual(official);
    expect(() =>
      parseOfficialVersion({ ...official, release: null }),
    ).toThrow();
  });
  it("normalizes policy defaults and rejects invalid enums", () => {
    expect(parseDisguisePolicy(undefined)).toEqual({
      enabled: false,
      match_platform: true,
      unknown_platform: "exclude",
    });
    expect(
      parseDisguisePolicy({
        enabled: true,
        match_platform: false,
        unknown_platform: "allow_current",
      }).match_platform,
    ).toBe(false);
    for (const value of [
      false,
      [],
      { enabled: true, unknown_platform: "guess" },
    ])
      expect(() => parseDisguisePolicy(value)).toThrow();
  });
  it("decodes unbound and shared logins without inventing identities", () => {
    const result = parseDisguiseState({
      logins: [
        {
          credential_session_id: "login",
          name: "Login",
          providers: [
            {
              provider_id: "provider",
              provider_name: "Provider",
              client_disguise: { enabled: true },
            },
          ],
        },
      ],
      profiles: [profile],
      references: [reference],
      transport_samples: [transport],
      clients: [{ client_id: "client" }],
    });
    expect(result.logins[0].identity).toBeUndefined();
    expect(result.profiles[0].id).toBe("revision");
    expect(() => parseDisguiseState({})).toThrow();
  });
  it("uses encoded resource IDs and committed server results", async () => {
    const request = vi.fn();
    const api = createClientDisguiseApi(request);
    request.mockResolvedValueOnce(binding);
    await api.saveBinding(
      "login/a",
      binding as Parameters<typeof api.saveBinding>[1],
    );
    expect(request).toHaveBeenLastCalledWith(
      "/client-disguise/logins/login%2Fa",
      expect.objectContaining({ method: "PUT" }),
    );
    request.mockResolvedValueOnce({
      revision: profile,
      created: true,
      advanced_sessions: ["login"],
    });
    expect(
      (
        await api.importSample({
          source_id: "ref",
          captured_at: "2026",
          tuple,
          client_version: "1",
          features,
        })
      ).created,
    ).toBe(true);
    request.mockResolvedValueOnce(reference);
    expect(await api.saveReference(reference)).toEqual(reference);
    request.mockResolvedValueOnce(transport);
    expect(await api.importTransport(transport)).toEqual(transport);
    request.mockResolvedValueOnce({ client_id: "client" });
    expect(await api.bindKey("replacement", "client")).toEqual({
      client_id: "client",
    });
    expect(JSON.parse(request.mock.calls.at(-1)![1].body)).toEqual({
      api_key: "replacement",
      client_id: "client",
    });
  });
  it("decodes failure evidence and field differences with original values", () => {
    const result = parseDisguiseEvidence({
      diagnostic_id: "diag",
      decision: "failed",
      provider_id: "p",
      platform_facts: { user_agent: "original" },
      candidates: [
        { provider_id: "p", outcome: "excluded", reason: "platform_mismatch" },
      ],
      differences: [
        {
          carrier: "header",
          location: "Thread-Id",
          original: "thread",
          derived: "mapped",
        },
      ],
      failure: {
        phase: "encode",
        location: "metadata",
        error_chain: ["bad JSON"],
        original_snippet: "original",
      },
    });
    expect(result.failure?.location).toBe("metadata");
    expect(result.context.provider_id).toBe("p");
    expect(result.differences[0].original).toBe("thread");
    expect(() => parseDisguiseEvidence({ decision: "failed" })).toThrow();
  });
});
