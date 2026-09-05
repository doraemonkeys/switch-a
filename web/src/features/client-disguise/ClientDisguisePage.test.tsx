import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { describe, it, expect, vi } from "vitest";
import { ApiContext } from "@/api/context";
import type { ApiClient } from "@/api/client";
import { ClientDisguisePage } from "./ClientDisguisePage";
import type { DisguiseState } from "@/api/client-disguise/types";
const state: DisguiseState = {
  logins: [],
  profiles: [],
  references: [],
  transport_samples: [],
  clients: [{ client_id: "existing-client" }],
};
describe("client disguise page", () => {
  it("creates reference bindings and preserves existing identity when replacing a key", async () => {
    const user = userEvent.setup();
    const get = vi.fn().mockResolvedValue(state);
    const saveReference = vi.fn().mockResolvedValue({});
    const bindKey = vi.fn().mockResolvedValue({ client_id: "existing-client" });
    const api = {
      clientDisguise: { get, saveReference, bindKey },
    } as unknown as ApiClient;
    render(
      <MemoryRouter>
        <ApiContext.Provider value={api}>
          <ClientDisguisePage />
        </ApiContext.Provider>
      </MemoryRouter>,
    );
    await screen.findByText(
      "Create a credential login before configuring its profile.",
    );
    await user.type(screen.getByLabelText("Source ID"), "reference");
    await user.type(screen.getByLabelText("Source name"), "My desktop");
    await user.selectOptions(
      screen.getByLabelText("Reference client"),
      "existing-client",
    );
    await user.click(
      screen.getByRole("button", { name: "Save reference source" }),
    );
    expect(saveReference).toHaveBeenCalledWith({
      id: "reference",
      name: "My desktop",
      client_identity_id: "existing-client",
    });
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
    expect(bindKey).toHaveBeenCalledWith("replacement", "existing-client");
    expect(screen.getByLabelText("Replacement API key")).toHaveValue("");
  });
  it("reports API and malformed import errors without losing settings", async () => {
    const user = userEvent.setup();
    const api = {
      clientDisguise: {
        get: vi.fn().mockResolvedValue(state),
        importSample: vi.fn(),
      },
    } as unknown as ApiClient;
    render(
      <MemoryRouter>
        <ApiContext.Provider value={api}>
          <ClientDisguisePage />
        </ApiContext.Provider>
      </MemoryRouter>,
    );
    await screen.findByText(
      "Create a credential login before configuring its profile.",
    );
    await user.type(
      screen.getByLabelText("Application sample JSON"),
      "invalid",
    );
    await user.click(
      screen.getByRole("button", { name: "Import application sample" }),
    );
    expect(screen.getByRole("alert")).toBeInTheDocument();
    expect(api.clientDisguise.importSample).not.toHaveBeenCalled();
  });
});
