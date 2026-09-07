import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ApiContext } from "@/api/context";
import type { ApiClient } from "@/api/client";
import type { ClientAPIKeysState } from "@/api/client-api-keys/types";
import { ClientAPIKeysPage } from "./ClientAPIKeysPage";

const item = {
  id: "key-1",
  name: "Laptop",
  key: "sk-client-secret",
  created_at: "2026-09-06T12:00:00Z",
  updated_at: "2026-09-06T12:00:00Z",
};
function setup(state: ClientAPIKeysState = { mode: "permissive", keys: [] }) {
  const clientApiKeys = {
    get: vi.fn().mockResolvedValue(state),
    setPolicy: vi.fn().mockImplementation(async (mode) => ({ mode })),
    add: vi.fn().mockResolvedValue(item),
    generate: vi.fn().mockResolvedValue(item),
    rename: vi.fn().mockResolvedValue({ ...item, name: "Desktop" }),
    delete: vi.fn().mockResolvedValue(undefined),
  };
  const api = { clientApiKeys } as unknown as ApiClient;
  render(
    <ApiContext.Provider value={api}>
      <ClientAPIKeysPage />
    </ApiContext.Provider>,
  );
  return { user: userEvent.setup(), clientApiKeys };
}

describe("client API key management", () => {
  it("preserves permissive defaults and exposes restricted empty state and reversal", async () => {
    const { user, clientApiKeys } = setup();
    expect(
      await screen.findByRole("radio", { name: /Allow any key/ }),
    ).toBeChecked();
    await user.click(
      screen.getByRole("radio", { name: /Configured keys only/ }),
    );
    expect(await screen.findByRole("status")).toHaveTextContent(
      "All new client requests are blocked",
    );
    await user.click(screen.getByRole("radio", { name: /Allow any key/ }));
    expect(clientApiKeys.setPolicy.mock.calls).toEqual([
      ["restricted"],
      ["permissive"],
    ]);
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("adds an existing key exactly, retains failed input, and exposes actionable errors", async () => {
    const { user, clientApiKeys } = setup();
    await screen.findByRole("radio", { name: /Allow any key/ });
    await user.click(
      screen.getByRole("radio", { name: "Add an existing key" }),
    );
    await user.type(screen.getByLabelText("Key name"), "Laptop");
    await user.type(screen.getByLabelText("Existing API key"), " exact-key ");
    clientApiKeys.add.mockRejectedValueOnce(new Error("Key already exists"));
    await user.click(screen.getByRole("button", { name: "Add key" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Key already exists",
    );
    expect(screen.getByLabelText("Existing API key")).toHaveValue(
      " exact-key ",
    );
    await user.click(screen.getByRole("button", { name: "Add key" }));
    expect(
      await screen.findByRole("heading", { name: "Laptop" }),
    ).toBeInTheDocument();
    expect(clientApiKeys.add).toHaveBeenLastCalledWith("Laptop", " exact-key ");
    expect(screen.getByLabelText("Existing API key")).toHaveValue("");
    expect(screen.queryByText(item.key)).not.toBeInTheDocument();
  });

  it("generates a recoverable key, copies, reveals, renames and deletes it", async () => {
    const { user, clientApiKeys } = setup();
    await screen.findByRole("radio", { name: /Allow any key/ });
    await user.type(screen.getByLabelText("Key name"), "Laptop");
    await user.click(screen.getByRole("button", { name: "Generate key" }));
    const generated = await screen.findByRole("region", {
      name: "Generated key",
    });
    expect(within(generated).getByText(item.key)).toBeInTheDocument();
    await user.click(within(generated).getByRole("button", { name: "Copy" }));
    expect(await navigator.clipboard.readText()).toBe(item.key);
    expect(clientApiKeys.generate).toHaveBeenCalledWith("Laptop");
    await user.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(screen.queryByText(item.key)).not.toBeInTheDocument();
    await user.click(
      screen.getByRole("button", { name: "Reveal key for Laptop" }),
    );
    expect(screen.getByText(item.key)).toBeInTheDocument();
    await user.click(
      screen.getByRole("button", { name: "Hide key for Laptop" }),
    );
    await user.click(screen.getByRole("button", { name: "Rename" }));
    await user.clear(screen.getByLabelText("New name for Laptop"));
    await user.type(screen.getByLabelText("New name for Laptop"), "Desktop");
    await user.click(screen.getByRole("button", { name: "Save name" }));
    expect(
      await screen.findByRole("heading", { name: "Desktop" }),
    ).toBeInTheDocument();
    expect(clientApiKeys.rename).toHaveBeenCalledWith(item.id, "Desktop");
    await user.click(screen.getByRole("button", { name: "Delete" }));
    await user.click(screen.getByRole("button", { name: "Delete key" }));
    expect(await screen.findByText(/No client keys yet/)).toBeInTheDocument();
    expect(clientApiKeys.delete).toHaveBeenCalledWith(item.id);
  });

  it("keeps deletion failures visible and retryable in the confirmation", async () => {
    const { user, clientApiKeys } = setup({ mode: "restricted", keys: [item] });
    await screen.findByRole("heading", { name: "Laptop" });
    await user.click(screen.getByRole("button", { name: "Delete" }));
    clientApiKeys.delete.mockRejectedValueOnce(new Error("Delete unavailable"));
    await user.click(screen.getByRole("button", { name: "Delete key" }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Delete unavailable");
    expect(alert.parentElement).toHaveTextContent('Delete "Laptop"?');
    expect(screen.getByRole("heading", { name: "Laptop" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Delete key" }));
    expect(await screen.findByText(/No client keys yet/)).toBeInTheDocument();
    expect(clientApiKeys.delete).toHaveBeenCalledTimes(2);
  });

  it("leaves active policy unchanged when saving fails", async () => {
    const { user, clientApiKeys } = setup();
    await screen.findByRole("radio", { name: /Allow any key/ });
    clientApiKeys.setPolicy.mockRejectedValueOnce(
      new Error("Database unavailable"),
    );
    await user.click(
      screen.getByRole("radio", { name: /Configured keys only/ }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Database unavailable",
    );
    expect(screen.getByRole("radio", { name: /Allow any key/ })).toBeChecked();
  });
});
