import { describe, expect, it } from "vitest";
import {
  compareVersions,
  sortedProfiles,
  snapshotGroups,
} from "./profileCatalog";
import {
  featureDifferences,
  requestVersion,
  sampledUserAgent,
} from "./profileFeatures";
import {
  changeEnvironment,
  createLoginDraft,
  hasLoginChanges,
} from "../loginDraft";
import type {
  DisguiseState,
  ProfileRevision,
} from "@/api/client-disguise/types";

const tuple = { client_type: "desktop", platform: "windows", arch: "amd64" };
const profile: ProfileRevision = {
  id: "original",
  tuple,
  source_id: "reference",
  client_version: "0.155.0-alpha.9",
  captured_at: "2026-09-18T09:00:00Z",
  created_at: "2026-09-18T09:00:00Z",
  features: {
    user_agent: "Codex Desktop/0.155.0-alpha.9 (Windows 11; x86_64)",
    originator: "Codex Desktop",
    client_version: "0.155.0-alpha.9",
    os_version: "11",
    desktop_build: "26.900",
  },
};
describe("profile catalog semantics", () => {
  it.each([
    ["0.155.0-alpha.10", "0.155.0-alpha.9"],
    ["0.155.0", "0.155.0-alpha.10"],
    ["0.155.0-alpha.1", "0.155.0-alpha"],
    ["0.155.0-beta", "0.155.0-alpha"],
    ["0.155.0-alpha", "0.155.0-10"],
    ["0.156.0", "0.155.99"],
    ["0.155.0-alpha-b", "0.155.0-alpha-a"],
  ])("orders %s after %s", (newer, older) => {
    expect(compareVersions(newer, older)).toBeGreaterThan(0);
    expect(compareVersions(older, newer)).toBeLessThan(0);
  });
  it("ignores the v prefix and build metadata when comparing releases", () => {
    expect(compareVersions("v0.155.0+desktop.123", "0.155.0+desktop.124")).toBe(
      0,
    );
  });
  it("sorts releases numerically and groups revisions without merging provenance", () => {
    const alpha10 = {
      ...profile,
      id: "alpha10",
      client_version: "0.155.0-alpha.10",
    };
    const otherSource = { ...profile, id: "other-source", source_id: "other" };
    const profiles = [profile, alpha10, otherSource];
    const state: DisguiseState = {
      profiles,
      tracks: [],
      clients: [],
      logins: [],
      references: [],
      transport_samples: [],
    };
    expect(sortedProfiles(profiles)[0].id).toBe("alpha10");
    const groups = snapshotGroups(state, "desktop/windows/amd64");
    expect(groups).toHaveLength(3);
    expect(
      groups.flatMap((group) => group.profiles.map((item) => item.id)),
    ).toEqual(["alpha10", "original", "other-source"]);
  });
  it("compares every observed field, including custom headers and missing observations", () => {
    const other = {
      ...profile,
      features: {
        ...profile.features,
        os_version: "",
        headers: { "X-Stainless-Arch": "arm64" },
      },
    };
    expect(featureDifferences(profile, other)).toEqual([
      { key: "os_version", label: "系统版本", from: "11", to: "" },
      {
        key: "header:X-Stainless-Arch",
        label: "Header · X-Stainless-Arch",
        from: "",
        to: "arm64",
      },
    ]);
    expect(
      featureDifferences(profile, {
        ...profile,
        id: "duplicate",
        source_id: "other",
      }),
    ).toEqual([]);
  });
  it("projects the observed UA version and respects imported aliases", () => {
    expect(
      requestVersion({
        ...profile,
        features: {
          ...profile.features,
          user_agent: "codex_exec/0.151.0 (Linux; x86_64)",
        },
      }),
    ).toBe("0.151.0");
    expect(requestVersion({ ...profile, client_version: "0.156.0" })).toBe(
      "0.155.0-alpha.9",
    );
    const imported = {
      ...profile,
      features: {
        ...profile.features,
        user_agent: "",
        headers: { "user-agent": "codex_cli_rs/0.153.4" },
      },
    };
    expect(requestVersion(imported)).toBe("0.153.4");
    expect(sampledUserAgent(imported)).toBe("codex_cli_rs/0.153.4");
    expect(
      requestVersion({
        ...profile,
        features: { ...profile.features, user_agent: "" },
      }),
    ).toBe("");
  });
  it.each([
    {
      user_agent:
        "codex-browser-use/0.155.0-alpha.2.6 (Windows 10.0.26200; x86_64) unknown (codex-browser-use; 0.1.0)",
    },
    {
      headers: {
        "user-agent":
          "CODEX-BROWSER-USE/0.155.0-alpha.2.6 (Linux; x86_64) unknown (codex-browser-use; 0.1.0)",
      },
    },
  ])(
    "uses the Browser Use product release instead of its caller or stored version",
    (features) => {
      expect(
        requestVersion({
          ...profile,
          tuple: { ...tuple, client_type: "browser-use" },
          client_version: "0.156.0",
          features: { ...profile.features, user_agent: "", ...features },
        }),
      ).toBe("0.155.0-alpha.2.6");
    },
  );
  it("uses the source head for new environments and excludes remembered navigation from dirty state", () => {
    const login = {
      credential_session_id: "login",
      name: "Login",
      providers: [],
      binding: {
        credential_session_id: "login",
        tuple,
        revision_id: "original",
        reference_source_id: "reference",
        mode: "pinned" as const,
        transport_sample_id: "",
        telemetry_path_mappings: null,
      },
    };
    const mac = {
      ...profile,
      id: "mac",
      tuple: { ...tuple, platform: "macos", arch: "arm64" },
    };
    const lateHistory = {
      ...mac,
      id: "late-history",
      captured_at: "2026-09-18T10:00:00Z",
    };
    const state: DisguiseState = {
      profiles: [profile, mac, lateHistory],
      tracks: [
        {
          ...mac.tuple,
          source_id: "reference",
          revision_id: "mac",
          client_version: mac.client_version,
          captured_at: "2026-09-18T11:00:00Z",
        },
      ],
      clients: [],
      logins: [login],
      references: [
        { id: "reference", name: "Reference", client_identity_id: "client" },
      ],
      transport_samples: [],
    };
    const switched = changeEnvironment(
      createLoginDraft(login),
      "desktop/macos/arm64",
      state,
    );
    expect(switched.selections[switched.environment].revisionID).toBe("mac");
    expect(switched.mode).toBe("pinned");
    expect(hasLoginChanges(switched, login)).toBe(true);
    expect(
      hasLoginChanges(
        changeEnvironment(switched, "desktop/windows/amd64", state),
        login,
      ),
    ).toBe(false);
  });
});
