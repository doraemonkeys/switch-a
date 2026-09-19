import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useNavigate } from "react-router";
import { describe, it, expect, vi } from "vitest";
import { ApiContext } from "@/api/context";
import type { ApiClient } from "@/api/client";
import { ClientDisguisePage } from "./ClientDisguisePage";
import { chooseSnapshot, expectSnapshot } from "./profiles/editorTestActions";
import type { DisguiseState, LoginView } from "@/api/client-disguise/types";

const tuple = { client_type: "desktop", platform: "windows", arch: "amd64" };
const state: DisguiseState = {
  logins: [],
  profiles: [],
  tracks: [],
  references: [],
  transport_samples: [],
  clients: [{ client_id: "existing-client" }],
};
const login: LoginView = {
  credential_session_id: "login-one",
  name: "Office login",
  providers: [],
  binding: {
    credential_session_id: "login-one",
    tuple,
    mode: "auto",
    revision_id: "new",
    reference_source_id: "",
    transport_sample_id: "",
    telemetry_path_mappings: null,
  },
};
const populated: DisguiseState = {
  ...state,
  logins: [
    login,
    { ...login, credential_session_id: "login-two", name: "Personal login" },
  ],
  profiles: ["new", "old"].map((id) => ({
    id,
    tuple,
    client_version: "1",
    source_id: "reference",
    captured_at: "2026-09-05",
    created_at: "2026-09-05",
    features: {
      user_agent: id,
      originator: "desktop",
      client_version: "1",
      desktop_build: "1",
      os_version: "11",
    },
  })),
};

function HistoryControls() {
  const navigate = useNavigate();
  return <button onClick={() => navigate(-1)}>Go back</button>;
}
function setup(data = state, path = "/client-disguise", overrides = {}) {
  const clientDisguise = {
    get: vi.fn().mockResolvedValue(data),
    saveReference: vi.fn().mockResolvedValue({}),
    saveBinding: vi.fn().mockResolvedValue({}),
    bindKey: vi.fn().mockResolvedValue({ client_id: "existing-client" }),
    importSample: vi.fn().mockResolvedValue({}),
    importTransport: vi.fn().mockResolvedValue({}),
    ...overrides,
  };
  render(
    <MemoryRouter initialEntries={[path]}>
      <ApiContext.Provider value={{ clientDisguise } as unknown as ApiClient}>
        <ClientDisguisePage />
        <HistoryControls />
      </ApiContext.Provider>
    </MemoryRouter>,
  );
  return { api: clientDisguise, user: userEvent.setup() };
}
async function openLibrary(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: /Reference library/ }));
}

describe("client disguise reference following", () => {
  it.each([
    {
      outcome: "an available reference sample",
      revision: "latest",
      version: "0.151.0",
    },
    {
      outcome: "the built-in fallback",
      revision: "new",
      version: "0.150.0-alpha.8",
    },
  ])(
    "shows the concrete saved version after following $outcome",
    async ({ revision, version }) => {
      const builtin = {
        ...populated.profiles[0],
        source_id: "builtin",
        client_version: "0.150.0-alpha.8",
        features: {
          ...populated.profiles[0].features,
          client_version: "0.150.0-alpha.8",
        },
      };
      const latest = {
        ...builtin,
        id: "latest",
        source_id: "reference",
        client_version: "0.151.0",
        features: { ...builtin.features, client_version: "0.151.0" },
      };
      const initial: DisguiseState = {
        ...populated,
        logins: [login],
        references: [
          {
            id: "reference",
            name: "My desktop",
            client_identity_id: "existing-client",
          },
        ],
        profiles: revision === "latest" ? [builtin, latest] : [builtin],
      };
      const savedLogin: LoginView = {
        ...login,
        binding: {
          ...login.binding!,
          reference_source_id: "reference",
          revision_id: revision,
        },
      };
      const get = vi
        .fn()
        .mockResolvedValueOnce(initial)
        .mockResolvedValue({ ...initial, logins: [savedLogin] });
      const { api, user } = setup(initial, undefined, { get });
      await screen.findByLabelText("客户端环境");
      expectSnapshot("new");
      await user.selectOptions(screen.getByLabelText("参考来源"), "reference");
      expect(screen.getByRole("radio", { name: /自动跟随/ })).toBeChecked();
      expect(
        screen.getByRole("button", { name: "Save login settings" }),
      ).toBeEnabled();
      await user.click(
        screen.getByRole("button", { name: "Save login settings" }),
      );
      expect(api.saveBinding).toHaveBeenCalledWith(
        "login-one",
        expect.objectContaining({
          mode: "auto",
          reference_source_id: "reference",
          revision_id: "new",
        }),
      );
      await screen.findByText("All changes saved");
      expectSnapshot(revision);
      expect(screen.getByLabelText("生效预览")).toHaveTextContent(version);
      expect(
        screen.queryByRole("option", { name: "All versions" }),
      ).not.toBeInTheDocument();
      expect(screen.getByRole("radio", { name: /自动跟随/ })).toBeChecked();
    },
  );
});

describe("client disguise reference selection", () => {
  it("finds recent clients and preserves the reference draft and selection across refreshes", async () => {
    const latest = {
      client_id: "latest-client",
      last_request: {
        observed_at: "2026-09-18T10:30:00Z",
        tuple,
        client_version: "0.153.4",
        user_agent: "Codex Desktop/0.153.4 (Windows; x86_64)",
        originator: "Codex Desktop",
      },
    };
    const earlier = {
      client_id: "earlier-client",
      last_request: {
        ...latest.last_request,
        observed_at: "2026-09-18T09:00:00Z",
        tuple: { client_type: "cli", platform: "macos", arch: "arm64" },
        originator: "codex_cli_rs",
      },
    };
    const initial = { ...state, clients: [state.clients[0], earlier, latest] };
    const { api, user } = setup(initial, "/client-disguise?view=references");
    await user.click(
      await screen.findByRole("button", { name: "Add reference" }),
    );
    await user.type(screen.getByLabelText("Source name"), "My desktop");
    await user.type(screen.getByLabelText(/Source ID/), "my-desktop");
    const group = screen.getByRole("radiogroup", { name: "Reference client" });
    const choices = within(group).getAllByRole("radio");
    expect(choices.map((choice) => (choice as HTMLInputElement).value)).toEqual(
      ["latest-client", "earlier-client", "existing-client"],
    );
    expect(choices[0]).toHaveAccessibleName(/Codex Desktop.*最近请求.*Windows/);
    expect(
      choices.every((choice) => !(choice as HTMLInputElement).checked),
    ).toBe(true);
    expect(within(group).getByText("暂无请求记录")).toBeInTheDocument();
    await user.click(choices[0]);
    await user.click(screen.getByText("已选：Codex Desktop 0.153.4"));
    expect(screen.getByText(latest.last_request.user_agent)).toBeVisible();
    expect(screen.getByText("latest-client", { selector: "dd" })).toBeVisible();
    await user.type(
      screen.getByRole("searchbox", { name: "搜索参考客户端" }),
      "Windows{Enter}",
    );
    expect(api.saveReference).not.toHaveBeenCalled();
    expect(screen.getByLabelText("Source name")).toHaveValue("My desktop");
    await user.clear(screen.getByRole("searchbox", { name: "搜索参考客户端" }));
    await user.type(
      screen.getByRole("searchbox", { name: "搜索参考客户端" }),
      "macOS",
    );
    expect(within(group).getAllByRole("radio")).toHaveLength(1);
    expect(within(group).getByRole("radio")).toHaveAccessibleName(
      /Codex 默认入口标识（未指定入口）/,
    );
    await user.clear(screen.getByRole("searchbox", { name: "搜索参考客户端" }));
    api.get.mockResolvedValue({
      ...initial,
      clients: [
        latest,
        {
          ...earlier,
          last_request: {
            ...earlier.last_request,
            observed_at: "2026-09-18T11:00:00Z",
          },
        },
      ],
    });
    await user.click(screen.getByRole("button", { name: "刷新客户端" }));
    await waitFor(() =>
      expect(within(group).getAllByRole("radio")[0]).toHaveAttribute(
        "value",
        "earlier-client",
      ),
    );
    expect(
      within(group).getByRole("radio", { name: /Codex Desktop/ }),
    ).toBeChecked();
    expect(screen.getByLabelText("Source name")).toHaveValue("My desktop");
    expect(screen.getByLabelText(/Source ID/)).toHaveValue("my-desktop");
    await user.click(
      screen.getByRole("button", { name: "Save reference source" }),
    );
    expect(api.saveReference).toHaveBeenCalledWith({
      id: "my-desktop",
      name: "My desktop",
      client_identity_id: "latest-client",
    });
  });

  it("waits for a client refresh before allowing saves in another tab", async () => {
    const { api, user } = setup(populated);
    await screen.findByLabelText("客户端环境");
    await chooseSnapshot(user, "old");
    await openLibrary(user);
    await user.click(screen.getByRole("button", { name: "Add reference" }));
    let finishRefresh!: (value: DisguiseState) => void;
    api.get.mockReturnValueOnce(
      new Promise<DisguiseState>((resolve) => {
        finishRefresh = resolve;
      }),
    );
    await user.click(screen.getByRole("button", { name: "刷新客户端" }));
    expect(screen.getByRole("button", { name: "刷新中…" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Login profiles" }));
    expect(screen.getByRole("button", { name: "Please wait…" })).toBeDisabled();
    await act(async () => finishRefresh(populated));
    expectSnapshot("old");
    expect(
      screen.getByRole("button", { name: "Save login settings" }),
    ).toBeEnabled();
    expect(api.saveBinding).not.toHaveBeenCalled();
  });

  it("explains empty client lists and keeps selections when refresh fails", async () => {
    const { api, user } = setup(
      { ...state, clients: [] },
      "/client-disguise?view=references",
    );
    await user.click(
      await screen.findByRole("button", { name: "Add reference" }),
    );
    expect(
      screen.getByText("暂无客户端。先从要参考的客户端发送一次请求，再刷新。"),
    ).toBeVisible();
    expect(
      screen.getByRole("button", { name: "Save reference source" }),
    ).toBeDisabled();
    api.get.mockResolvedValue(state);
    await user.click(screen.getByRole("button", { name: "刷新客户端" }));
    const radio = await screen.findByRole("radio");
    await user.click(radio);
    expect(
      screen.queryByText("最近请求", { exact: true }),
    ).not.toBeInTheDocument();
    api.get.mockRejectedValueOnce(new Error("Refresh unavailable"));
    await user.click(screen.getByRole("button", { name: "刷新客户端" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Refresh unavailable",
    );
    expect(radio).toBeChecked();
    expect(screen.getByRole("button", { name: "刷新客户端" })).toBeEnabled();
    await user.type(
      screen.getByRole("searchbox", { name: "搜索参考客户端" }),
      "no-match",
    );
    expect(
      screen.getByText("没有匹配的客户端，试试其他关键词。"),
    ).toBeVisible();
  });
});

describe("client disguise workspace", () => {
  it("checks and displays the official stable release without changing login drafts", async () => {
    const official = {
      release: {
        version: "0.151.0",
        tag: "rust-v0.151.0",
        url: "https://github.com/openai/codex/releases/tag/rust-v0.151.0",
        published_at: "2026-09-10T00:00:00Z",
      },
      checked_at: "2026-09-10T01:00:00Z",
      synced_at: "2026-09-10T01:00:00Z",
      last_error: "",
    };
    const syncOfficialVersion = vi.fn().mockResolvedValue(official);
    const { api, user } = setup(populated, "/client-disguise", {
      syncOfficialVersion,
    });
    await screen.findByLabelText("发送版本来源");
    await user.selectOptions(
      screen.getByLabelText("发送版本来源"),
      "official_stable",
    );
    api.get.mockResolvedValue({ ...populated, official_version: official });
    await user.click(screen.getByRole("button", { name: "Check now" }));
    await screen.findByText("Codex CLI 官方稳定版: 0.151.0");
    expect(syncOfficialVersion).toHaveBeenCalledTimes(1);
    expect(screen.getByLabelText("发送版本来源")).toHaveValue(
      "official_stable",
    );
    expect(
      screen.getByRole("link", { name: "View Codex CLI release" }),
    ).toHaveAttribute("href", official.release.url);
  });
  it("keeps the last release visible when an official version check fails", async () => {
    const official = {
      release: {
        version: "0.151.0",
        tag: "rust-v0.151.0",
        url: "https://github.com/openai/codex/releases/tag/rust-v0.151.0",
        published_at: "2026-09-10T00:00:00Z",
      },
      checked_at: "2026-09-10T01:00:00Z",
      synced_at: "2026-09-10T01:00:00Z",
      last_error: "Previous check failed",
    };
    const { user } = setup(
      { ...populated, official_version: official },
      "/client-disguise",
      {
        syncOfficialVersion: vi
          .fn()
          .mockRejectedValue(new Error("GitHub unavailable")),
      },
    );
    await screen.findByText("Codex CLI 官方稳定版: 0.151.0");
    expect(screen.getByText(/Continuing with 0.151.0/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Check now" }));
    await screen.findByText("GitHub unavailable");
    expect(
      screen.getByText("Codex CLI 官方稳定版: 0.151.0"),
    ).toBeInTheDocument();
  });
  it("creates reference sources and preserves identity when replacing a key", async () => {
    const { api, user } = setup();
    await screen.findByText(
      "Create a credential login before configuring its profile.",
    );
    await openLibrary(user);
    await user.click(screen.getByRole("button", { name: "Add reference" }));
    await user.type(screen.getByLabelText(/Source ID/), "reference");
    await user.type(screen.getByLabelText("Source name"), "My desktop");
    await user.click(
      within(
        screen.getByRole("radiogroup", { name: "Reference client" }),
      ).getByRole("radio"),
    );
    await user.click(
      screen.getByRole("button", { name: "Save reference source" }),
    );
    expect(api.saveReference).toHaveBeenCalledWith({
      id: "reference",
      name: "My desktop",
      client_identity_id: "existing-client",
    });
    await user.click(screen.getByRole("button", { name: "Client identities" }));
    await user.selectOptions(
      screen.getByLabelText("Existing client identity"),
      "existing-client",
    );
    await user.type(
      screen.getByLabelText("Replacement API key"),
      "replacement",
    );
    await user.click(
      screen.getByRole("button", { name: "Bind key to client" }),
    );
    expect(api.bindKey).toHaveBeenCalledWith("replacement", "existing-client");
    await waitFor(() =>
      expect(screen.getByLabelText("Replacement API key")).toHaveValue(""),
    );
  });

  it("reports malformed import errors and retains the sample for correction", async () => {
    const { api, user } = setup();
    await screen.findByText(
      "Create a credential login before configuring its profile.",
    );
    await openLibrary(user);
    await user.type(
      screen.getByLabelText("Application sample JSON"),
      "invalid",
    );
    await user.click(
      screen.getByRole("button", { name: "Import application sample" }),
    );
    expect(screen.getByRole("alert")).toBeInTheDocument();
    expect(api.importSample).not.toHaveBeenCalled();
    expect(screen.getByLabelText("Application sample JSON")).toHaveValue(
      "invalid",
    );
  });

  it("preserves separate login drafts across account and section navigation", async () => {
    const { api, user } = setup(populated);
    await screen.findByRole("heading", { name: "Office login" });
    await chooseSnapshot(user, "old");
    await user.click(screen.getByRole("button", { name: /Personal login/ }));
    expectSnapshot("new");
    await user.click(screen.getByRole("button", { name: /Office login/ }));
    expectSnapshot("old");
    await openLibrary(user);
    await user.click(screen.getByRole("button", { name: "Login profiles" }));
    expectSnapshot("old");
    await user.click(
      screen.getByRole("button", { name: "Save login settings" }),
    );
    expect(api.saveBinding).toHaveBeenCalledWith(
      "login-one",
      expect.objectContaining({ revision_id: "old", mode: "pinned" }),
    );
  });

  it("supports deep links and browser history, and filters by login name", async () => {
    const { user } = setup(populated, "/client-disguise?login=login-two");
    await screen.findByRole("heading", { name: "Personal login" });
    await user.click(screen.getByRole("button", { name: /Office login/ }));
    expect(
      screen.getByRole("heading", { name: "Office login" }),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Go back" }));
    expect(
      screen.getByRole("heading", { name: "Personal login" }),
    ).toBeInTheDocument();
    await user.type(
      screen.getByLabelText("Search credential logins"),
      "Office",
    );
    expect(
      screen.queryByRole("button", { name: /Personal login/ }),
    ).not.toBeInTheDocument();
    await user.clear(screen.getByLabelText("Search credential logins"));
    await user.type(
      screen.getByLabelText("Search credential logins"),
      "missing",
    );
    expect(screen.getByText(/No matching logins/)).toBeInTheDocument();
  });

  it("resets a draft to the saved configuration without sending a request", async () => {
    const { api, user } = setup(populated);
    await screen.findByRole("heading", { name: "Office login" });
    expect(
      screen.getByRole("button", { name: "Save login settings" }),
    ).toBeDisabled();
    await chooseSnapshot(user, "old");
    await user.click(screen.getByRole("button", { name: "Reset" }));
    expectSnapshot("new");
    expect(screen.getByRole("radio", { name: /自动跟随/ })).toBeChecked();
    expect(api.saveBinding).not.toHaveBeenCalled();
  });

  it("keeps the draft after a save fails and clears it only after a successful refresh", async () => {
    const saveBinding = vi
      .fn()
      .mockRejectedValueOnce(new Error("Save unavailable"))
      .mockResolvedValue({});
    const get = vi
      .fn()
      .mockResolvedValueOnce(populated)
      .mockResolvedValue({
        ...populated,
        logins: [
          {
            ...login,
            binding: { ...login.binding, revision_id: "old", mode: "pinned" },
          },
          populated.logins[1],
        ],
      });
    const { user } = setup(populated, undefined, { saveBinding, get });
    await screen.findByRole("heading", { name: "Office login" });
    await chooseSnapshot(user, "old");
    await user.click(
      screen.getByRole("button", { name: "Save login settings" }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Save unavailable",
    );
    expectSnapshot("old");
    expect(screen.getByText("Unsaved changes")).toBeInTheDocument();
    await user.click(
      screen.getByRole("button", { name: "Save login settings" }),
    );
    await screen.findByText("All changes saved");
    expect(screen.getByRole("status")).toHaveTextContent("Login profile saved");
    expect(
      screen.getByRole("button", { name: "Save login settings" }),
    ).toBeDisabled();
  });

  it("offers retry after an initial fetch error instead of remaining in a loading state", async () => {
    const get = vi
      .fn()
      .mockRejectedValueOnce(new Error("Network offline"))
      .mockResolvedValue(state);
    const { user } = setup(state, undefined, { get });
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Network offline",
    );
    expect(
      screen.queryByText("Loading client disguise settings?"),
    ).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(
      await screen.findByText(
        "Create a credential login before configuring its profile.",
      ),
    ).toBeInTheDocument();
  });

  it("edits an existing reference source without changing its identifier", async () => {
    const reference = {
      id: "reference",
      name: "Office desktop",
      client_identity_id: "existing-client",
    };
    const { api, user } = setup(
      { ...state, references: [reference] },
      "/client-disguise?view=references",
    );
    await user.click(
      await screen.findByRole("button", { name: /Office desktop/ }),
    );
    expect(screen.getByRole("textbox", { name: /^Source ID/ })).toHaveAttribute(
      "readonly",
    );
    await user.clear(screen.getByLabelText("Source name"));
    await user.type(screen.getByLabelText("Source name"), "Renamed desktop");
    await user.click(
      screen.getByRole("button", { name: "Save reference source" }),
    );
    expect(api.saveReference).toHaveBeenCalledWith({
      ...reference,
      name: "Renamed desktop",
    });
  });

  it("keeps independent import drafts and sends transport samples through their own API", async () => {
    const { api, user } = setup(state, "/client-disguise?view=references");
    const input = await screen.findByLabelText("Application sample JSON");
    await user.click(input);
    await user.paste('{"source_id":"reference"}');
    await user.click(screen.getByRole("button", { name: /Transport/ }));
    await user.click(
      screen.getByLabelText("Independent transport sample JSON"),
    );
    await user.paste('{"id":"transport-one"}');
    await user.click(
      screen.getByRole("button", { name: "Import transport sample" }),
    );
    expect(api.importTransport).toHaveBeenCalledWith({ id: "transport-one" });
    await waitFor(() =>
      expect(
        screen.getByLabelText("Independent transport sample JSON"),
      ).toHaveValue(""),
    );
    await user.click(screen.getByRole("button", { name: /Application/ }));
    expect(screen.getByLabelText("Application sample JSON")).toHaveValue(
      '{"source_id":"reference"}',
    );
    expect(api.importSample).not.toHaveBeenCalled();
  });

  it("disables form inputs while a profile save is pending", async () => {
    let finish!: () => void;
    const saveBinding = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          finish = resolve;
        }),
    );
    const { user } = setup(populated, undefined, { saveBinding });
    await screen.findByRole("heading", { name: "Office login" });
    await chooseSnapshot(user, "old");
    await user.click(
      screen.getByRole("button", { name: "Save login settings" }),
    );
    expect(screen.getByLabelText("客户端环境")).toBeDisabled();
    await act(async () => finish());
    await waitFor(() =>
      expect(screen.getByLabelText("客户端环境")).toBeEnabled(),
    );
  });
});
