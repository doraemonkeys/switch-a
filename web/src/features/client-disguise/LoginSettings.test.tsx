import { useState } from "react";
import { render, screen } from "@testing-library/react";
import { createLoginDraft } from "./loginDraft";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { describe, it, expect, vi } from "vitest";
import { LoginSettings } from "./LoginSettings";
import { DisguiseEvidencePanel } from "./DisguiseEvidencePanel";
import type { DisguiseState, LoginView } from "@/api/client-disguise/types";
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
  references: [
    { id: "reference", name: "Reference", client_identity_id: "client" },
  ],
  transport_samples: [],
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
function Editor({
  save,
  data = state,
}: {
  save: (
    binding: import("@/api/client-disguise/types").ProfileBinding,
  ) => Promise<void>;
  data?: DisguiseState;
}) {
  const [draft, change] = useState(() => createLoginDraft(login));
  return (
    <LoginSettings
      login={login}
      state={data}
      busy={false}
      save={save}
      draft={draft}
      change={change}
    />
  );
}
describe("login lifecycle controls", () => {
  it("selects a newer version and environment in one step and saves that revision", async () => {
    const user = userEvent.setup();
    const save = vi.fn().mockResolvedValue(undefined);
    const latest = {
      ...state.profiles[0],
      id: "latest-windows",
      client_version: "0.151.0",
    };
    const data = {
      ...state,
      profiles: [
        ...state.profiles.map((profile) => ({
          ...profile,
          client_version: "0.150.0-alpha.8",
        })),
        {
          ...latest,
          id: "latest-linux",
          tuple: { ...tuple, platform: "linux" },
        },
        latest,
      ],
    };
    render(
      <MemoryRouter>
        <Editor save={save} data={data} />
      </MemoryRouter>,
    );

    await user.selectOptions(
      screen.getByLabelText("Profile revision"),
      "latest-windows",
    );
    expect(
      screen.queryByRole("option", { name: "All versions" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("option", {
        name: /0.151.0.*desktop \/ windows \/ amd64/,
        selected: true,
      }),
    ).toHaveValue("latest-windows");
    expect(
      screen.getByRole("radio", { name: /Pin this revision/ }),
    ).toBeChecked();
    expect(
      screen.getByRole("button", { name: "Save login settings" }),
    ).toBeEnabled();
    await user.click(
      screen.getByRole("button", { name: "Save login settings" }),
    );
    expect(save).toHaveBeenLastCalledWith(
      expect.objectContaining({
        revision_id: "latest-windows",
        tuple,
        mode: "pinned",
        reference_source_id: "reference",
      }),
    );

    await user.click(screen.getByRole("button", { name: "Reset" }));
    expect(screen.getByLabelText("Profile revision")).toHaveValue("new");
    expect(
      screen.getByRole("radio", { name: /Automatic follow/ }),
    ).toBeChecked();
    expect(
      screen.getByRole("button", { name: "Save login settings" }),
    ).toBeDisabled();
  });

  it("saves official version following independently of the profile revision", async () => {
    const user = userEvent.setup();
    const save = vi.fn().mockResolvedValue(undefined);
    render(
      <MemoryRouter>
        <Editor save={save} />
      </MemoryRouter>,
    );
    expect(screen.getByLabelText("Version source")).toHaveValue("");
    await user.selectOptions(
      screen.getByLabelText("Version source"),
      "official_stable",
    );
    expect(screen.getByLabelText("Profile revision")).toHaveValue("new");
    await user.click(
      screen.getByRole("button", { name: "Save login settings" }),
    );
    expect(save).toHaveBeenLastCalledWith(
      expect.objectContaining({
        version_source: "official_stable",
        revision_id: "new",
        tuple,
        reference_source_id: "reference",
      }),
    );
    await user.click(screen.getByRole("button", { name: "Reset" }));
    expect(screen.getByLabelText("Version source")).toHaveValue("");
  });
  it("pins a manually selected historical revision then allows explicit automatic follow", async () => {
    const user = userEvent.setup();
    const save = vi.fn().mockResolvedValue(undefined);
    render(
      <MemoryRouter>
        <Editor save={save} />
      </MemoryRouter>,
    );
    expect(screen.getByText(/created atomically/i)).toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText("Profile revision"), "old");
    expect(
      screen.getByRole("radio", { name: /Pin this revision/ }),
    ).toBeChecked();
    await user.click(
      screen.getByRole("button", { name: "Save login settings" }),
    );
    expect(save).toHaveBeenLastCalledWith(
      expect.objectContaining({
        revision_id: "old",
        mode: "pinned",
        credential_session_id: "login",
      }),
    );
    await user.click(screen.getByRole("radio", { name: /Automatic follow/ }));
    await user.selectOptions(screen.getByLabelText("Reference source"), "");
    await user.click(
      screen.getByRole("button", { name: "Save login settings" }),
    );
    expect(save).toHaveBeenLastCalledWith(
      expect.objectContaining({ mode: "auto" }),
    );
  });
  it("blocks malformed telemetry maps without saving", async () => {
    const user = userEvent.setup();
    const save = vi.fn();
    render(
      <MemoryRouter>
        <Editor save={save} />
      </MemoryRouter>,
    );
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
