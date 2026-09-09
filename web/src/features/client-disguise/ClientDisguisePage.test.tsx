import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useNavigate } from "react-router";
import { describe, it, expect, vi } from "vitest";
import { ApiContext } from "@/api/context";
import type { ApiClient } from "@/api/client";
import { ClientDisguisePage } from "./ClientDisguisePage";
import type { DisguiseState, LoginView } from "@/api/client-disguise/types";

const tuple = { client_type: "desktop", platform: "windows", arch: "amd64" };
const state: DisguiseState = {
  logins: [],
  profiles: [],
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

describe("client disguise workspace", () => {
  it("creates reference sources and preserves identity when replacing a key", async () => {
    const { api, user } = setup();
    await screen.findByText(
      "Create a credential login before configuring its profile.",
    );
    await openLibrary(user);
    await user.click(screen.getByRole("button", { name: "Add reference" }));
    await user.type(screen.getByLabelText(/Source ID/), "reference");
    await user.type(screen.getByLabelText("Source name"), "My desktop");
    await user.selectOptions(
      screen.getByLabelText("Reference client"),
      "existing-client",
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
    await user.selectOptions(screen.getByLabelText("Profile revision"), "old");
    await user.click(screen.getByRole("button", { name: /Personal login/ }));
    expect(screen.getByLabelText("Profile revision")).toHaveValue("new");
    await user.click(screen.getByRole("button", { name: /Office login/ }));
    expect(screen.getByLabelText("Profile revision")).toHaveValue("old");
    await openLibrary(user);
    await user.click(screen.getByRole("button", { name: "Login profiles" }));
    expect(screen.getByLabelText("Profile revision")).toHaveValue("old");
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
    await user.selectOptions(screen.getByLabelText("Profile revision"), "old");
    await user.click(screen.getByRole("button", { name: "Reset" }));
    expect(screen.getByLabelText("Profile revision")).toHaveValue("new");
    expect(
      screen.getByRole("radio", { name: /Automatic follow/ }),
    ).toBeChecked();
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
    await user.selectOptions(screen.getByLabelText("Profile revision"), "old");
    await user.click(
      screen.getByRole("button", { name: "Save login settings" }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Save unavailable",
    );
    expect(screen.getByLabelText("Profile revision")).toHaveValue("old");
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
    expect(screen.getByLabelText(/Source ID/)).toHaveAttribute("readonly");
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
    await user.selectOptions(screen.getByLabelText("Profile revision"), "old");
    await user.click(
      screen.getByRole("button", { name: "Save login settings" }),
    );
    expect(screen.getByLabelText("Profile revision")).toBeDisabled();
    await act(async () => finish());
    await waitFor(() =>
      expect(screen.getByLabelText("Profile revision")).toBeEnabled(),
    );
  });
});
