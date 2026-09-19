import { useState } from "react";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { describe, it, expect, vi } from "vitest";
import { createLoginDraft } from "./loginDraft";
import { LoginSettings } from "./LoginSettings";
import { DisguiseEvidencePanel } from "./DisguiseEvidencePanel";
import { chooseSnapshot, expectSnapshot } from "./profiles/editorTestActions";
import type {
  DisguiseState,
  LoginView,
  ProfileBinding,
} from "@/api/client-disguise/types";

const tuple = { client_type: "desktop", platform: "windows", arch: "amd64" };
const login: LoginView = {
  credential_session_id: "login",
  name: "Login",
  providers: [],
  binding: {
    credential_session_id: "login",
    tuple,
    mode: "auto",
    revision_id: "new",
    reference_source_id: "reference",
    transport_sample_id: "",
    telemetry_path_mappings: null,
  },
};
const state: DisguiseState = {
  logins: [login],
  clients: [],
  transport_samples: [],
  references: [
    {
      id: "reference",
      name: "Windows reference",
      client_identity_id: "client",
    },
  ],
  tracks: [
    {
      ...tuple,
      source_id: "reference",
      revision_id: "new",
      client_version: "1",
      captured_at: "2026-09-05T12:00:00Z",
    },
  ],
  profiles: ["new", "old"].map((id, index) => ({
    id,
    tuple,
    client_version: "1",
    source_id: "reference",
    captured_at: index === 0 ? "2026-09-05T12:00:00Z" : "2026-09-05T10:00:00Z",
    created_at: "2026-09-05",
    features: {
      user_agent: id,
      originator: "desktop",
      client_version: "1",
      desktop_build: "1",
      os_version: index === 0 ? "11" : "10",
    },
  })),
};
function Editor({
  save,
  data = state,
  current = login,
  busy = false,
}: {
  save: (binding: ProfileBinding) => Promise<void>;
  data?: DisguiseState;
  current?: LoginView;
  busy?: boolean;
}) {
  const [draft, change] = useState(() => createLoginDraft(current));
  return (
    <LoginSettings
      login={current}
      state={data}
      busy={busy}
      save={save}
      draft={draft}
      change={change}
    />
  );
}
function mount(data = state, current = login) {
  const save = vi.fn().mockResolvedValue(undefined);
  render(
    <MemoryRouter>
      <Editor save={save} data={data} current={current} />
    </MemoryRouter>,
  );
  return { save, user: userEvent.setup() };
}

describe("login environment and snapshot selection", () => {
  it("changes environments without changing update mode and restores each environment selection", async () => {
    const macTuple = { ...tuple, platform: "macos", arch: "arm64" };
    const data: DisguiseState = {
      ...state,
      references: [
        ...state.references,
        { id: "mac", name: "Mac reference", client_identity_id: "mac-client" },
      ],
      profiles: [
        ...state.profiles,
        {
          ...state.profiles[0],
          id: "mac-profile",
          tuple: macTuple,
          source_id: "mac",
        },
      ],
      tracks: [
        ...state.tracks,
        {
          ...state.tracks[0],
          ...macTuple,
          source_id: "mac",
          revision_id: "mac-profile",
        },
      ],
    };
    const { user, save } = mount(data);
    expect(screen.getByLabelText("客户端环境")).toHaveValue(
      "desktop/windows/amd64",
    );
    await user.selectOptions(
      screen.getByLabelText("发送版本来源"),
      "official_stable",
    );
    await user.selectOptions(
      screen.getByLabelText("客户端环境"),
      "desktop/macos/arm64",
    );
    expect(screen.getByRole("radio", { name: /自动跟随/ })).toBeChecked();
    expect(screen.getByLabelText("参考来源")).toHaveValue("mac");
    expectSnapshot("mac-profile");
    expect(
      screen.queryByRole("option", { name: "Windows reference" }),
    ).not.toBeInTheDocument();
    await user.click(
      screen.getByRole("button", { name: "Save login settings" }),
    );
    expect(save).toHaveBeenLastCalledWith(
      expect.objectContaining({
        tuple: macTuple,
        revision_id: "mac-profile",
        reference_source_id: "mac",
        mode: "auto",
        version_source: "official_stable",
      }),
    );
    await user.selectOptions(
      screen.getByLabelText("客户端环境"),
      "desktop/windows/amd64",
    );
    await chooseSnapshot(user, "old");
    await user.selectOptions(
      screen.getByLabelText("客户端环境"),
      "desktop/macos/arm64",
    );
    expect(screen.getByRole("radio", { name: /固定快照/ })).toBeChecked();
    expectSnapshot("mac-profile");
    await user.selectOptions(
      screen.getByLabelText("客户端环境"),
      "desktop/windows/amd64",
    );
    expectSnapshot("old");
    expect(screen.getByLabelText("发送版本来源")).toHaveValue(
      "official_stable",
    );
    expect(
      within(screen.getByRole("group", { name: "环境快照" }))
        .getAllByRole("radio")
        .find((radio) => radio.getAttribute("value") === "old"),
    ).toBeChecked();
    await user.click(screen.getByRole("button", { name: "Reset" }));
    expectSnapshot("new");
    expect(screen.getByRole("radio", { name: /自动跟随/ })).toBeChecked();
    expect(
      screen.getByRole("button", { name: "Save login settings" }),
    ).toBeDisabled();
  });

  it("distinguishes same-version snapshots and saves the exact historical choice", async () => {
    const { user, save } = mount();
    await user.click(screen.getByRole("radio", { name: /固定快照/ }));
    const group = screen.getByRole("group", { name: "环境快照" });
    expect(within(group).getAllByRole("radio")).toHaveLength(1);
    expect(within(group).getByText("来源当前")).toBeVisible();
    await user.click(within(group).getByText("对比变化（2）"));
    expect(
      within(group).getByRole("table", { name: "快照字段差异" }),
    ).toHaveTextContent("User-Agent");
    expect(within(group).getByRole("table")).toHaveTextContent("系统版本");
    await chooseSnapshot(user, "old");
    expectSnapshot("old");
    await user.click(
      screen.getByRole("button", { name: "Save login settings" }),
    );
    expect(save).toHaveBeenLastCalledWith(
      expect.objectContaining({ revision_id: "old", tuple, mode: "pinned" }),
    );
    await user.click(screen.getByRole("radio", { name: /自动跟随/ }));
    expectSnapshot("new");
    await user.click(screen.getByRole("radio", { name: /固定快照/ }));
    expectSnapshot("old");
  });

  it("keeps different sources visible even when their version and features match", async () => {
    const data = {
      ...state,
      references: [
        ...state.references,
        {
          id: "office",
          name: "Office reference",
          client_identity_id: "office-client",
        },
      ],
      profiles: [
        ...state.profiles,
        { ...state.profiles[0], id: "office-profile", source_id: "office" },
      ],
    };
    const { user } = mount(data);
    await user.click(screen.getByRole("radio", { name: /固定快照/ }));
    const group = screen.getByRole("group", { name: "环境快照" });
    expect(within(group).getByText("Windows reference")).toBeVisible();
    expect(within(group).getByText("Office reference")).toBeVisible();
    expect(within(group).getAllByRole("radio")).toHaveLength(2);
  });

  it("uses the server track even when a historical revision has a later capture", async () => {
    const data = {
      ...state,
      profiles: state.profiles.map((profile) =>
        profile.id === "old"
          ? { ...profile, captured_at: "2026-09-05T13:00:00Z" }
          : profile,
      ),
    };
    const { user } = mount(data);
    expectSnapshot("new");
    await user.click(screen.getByRole("radio", { name: /固定快照/ }));
    const radios = within(
      screen.getByRole("group", { name: "环境快照" }),
    ).getAllByRole("radio");
    expect(radios).toHaveLength(1);
    expect(radios[0]).toHaveAttribute("value", "new");
  });

  it("does not downgrade to an older reference head", () => {
    const newer = { ...state.profiles[0], id: "newer", client_version: "2" };
    const current = {
      ...login,
      binding: { ...login.binding!, revision_id: "newer" },
    };
    mount({ ...state, profiles: [...state.profiles, newer] }, current);
    expectSnapshot("newer");
    expect(screen.getByText(/来源当前版本较旧/)).toBeVisible();
  });

  it("shows incoming version preservation for partial profiles and independent official version projection", async () => {
    const partial = {
      ...state.profiles[0],
      evidence_kind: "source",
      features: { ...state.profiles[0].features, user_agent: "" },
    };
    const official = {
      release: { version: "0.154.0", tag: "", url: "", published_at: "" },
      checked_at: "",
      synced_at: "",
      last_error: "",
    };
    const { user, save } = mount({
      ...state,
      profiles: [partial],
      official_version: official,
    });
    const preview = screen.getByLabelText("生效预览");
    expect(within(preview).getByText("沿用原请求版本")).toBeVisible();
    expect(within(preview).getByText(/部分特征/)).toBeVisible();
    await user.selectOptions(
      screen.getByLabelText("发送版本来源"),
      "official_stable",
    );
    expect(within(preview).getByText("0.154.0 · 官方稳定版")).toBeVisible();
    await user.click(
      screen.getByRole("button", { name: "Save login settings" }),
    );
    expect(save).toHaveBeenLastCalledWith(
      expect.objectContaining({
        revision_id: "new",
        version_source: "official_stable",
        mode: "auto",
      }),
    );
  });

  it("keeps a selected reference with no samples available and explains the fallback", () => {
    mount({
      ...state,
      tracks: [],
      profiles: [{ ...state.profiles[0], source_id: "builtin" }],
    });
    expect(screen.getByLabelText("参考来源")).toHaveValue("reference");
    expect(screen.getByText(/来源暂无该环境的有效跟随快照/)).toBeVisible();
    expectSnapshot("new");
  });

  it("starts an unbound login by choosing an available environment", async () => {
    const current: LoginView = { ...login, binding: undefined };
    const { user, save } = mount(state, current);
    expect(
      screen.getByRole("button", { name: "Save login settings" }),
    ).toBeDisabled();
    await user.selectOptions(
      screen.getByLabelText("客户端环境"),
      "desktop/windows/amd64",
    );
    expectSnapshot("new");
    await user.click(
      screen.getByRole("button", { name: "Save login settings" }),
    );
    expect(save).toHaveBeenCalledWith(
      expect.objectContaining({ revision_id: "new", tuple, mode: "auto" }),
    );
  });

  it("explains missing profiles and prevents saving a stale revision", () => {
    mount({ ...state, profiles: [] });
    expect(screen.getByText(/暂无环境快照/)).toBeVisible();
    expect(
      screen.getByRole("button", { name: "Save login settings" }),
    ).toBeDisabled();
  });

  it("blocks malformed telemetry maps without saving", async () => {
    const { user, save } = mount();
    await user.click(screen.getByText("Advanced login settings"));
    await user.clear(screen.getByLabelText("Telemetry path mappings"));
    await user.type(screen.getByLabelText("Telemetry path mappings"), "null");
    await user.click(
      screen.getByRole("button", { name: "Save login settings" }),
    );
    expect(screen.getByRole("alert")).toHaveTextContent(
      "object of path strings",
    );
    expect(save).not.toHaveBeenCalled();
  });
});

describe("disguise diagnostics", () => {
  it("shows exclusion reasons and field-level original/derived diagnostics", () => {
    render(
      <MemoryRouter>
        <DisguiseEvidencePanel
          evidence={{
            diagnostic_id: "diag",
            decision: "failed",
            context: { credential_session_id: "login" },
            platform_facts: { ua: "Linux" },
            candidates: [
              {
                provider_id: "provider",
                outcome: "excluded",
                reason: "platform mismatch",
              },
            ],
            differences: [
              {
                carrier: "header",
                location: "Installation-Id",
                original: "original-device",
                derived: "virtual-device",
              },
            ],
            failure: {
              phase: "encode",
              location: "metadata",
              error_chain: ["invalid JSON"],
            },
          }}
        />
      </MemoryRouter>,
    );
    expect(screen.getByText("original-device")).toBeInTheDocument();
    expect(screen.getByText("virtual-device")).toBeInTheDocument();
    expect(screen.getByText(/platform mismatch/)).toBeInTheDocument();
    expect(screen.getByRole("link")).toHaveAttribute(
      "href",
      "/client-disguise?login=login",
    );
  });
});
