import { render, screen } from "@testing-library/react";
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
    remap_cache_keys: false,
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
describe("login lifecycle controls", () => {
  it("pins a manually selected historical revision then allows explicit automatic follow", async () => {
    const user = userEvent.setup();
    const save = vi.fn().mockResolvedValue(undefined);
    render(
      <MemoryRouter>
        <LoginSettings login={login} state={state} busy={false} save={save} />
      </MemoryRouter>,
    );
    expect(screen.getByText(/created atomically/)).toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText("Profile revision"), "old");
    expect(screen.getByLabelText("Update mode")).toHaveValue("pinned");
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
    await user.selectOptions(screen.getByLabelText("Update mode"), "auto");
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
        <LoginSettings login={login} state={state} busy={false} save={save} />
      </MemoryRouter>,
    );
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
                location: "Thread-Id",
                original: "original-thread",
                derived: "mapped-thread",
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
    expect(screen.getByText("original-thread")).toBeInTheDocument();
    expect(screen.getByText("mapped-thread")).toBeInTheDocument();
    expect(screen.getByText(/platform mismatch/)).toBeInTheDocument();
    expect(screen.getByRole("link")).toHaveAttribute(
      "href",
      "/client-disguise?login=login",
    );
  });
});
