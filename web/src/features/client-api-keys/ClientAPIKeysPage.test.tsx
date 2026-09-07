import { act, fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterAll, beforeAll, describe, expect, it, vi } from "vitest";
import { ApiContext } from "@/api/context";
import type { ApiClient } from "@/api/client";
import type {
  ClientAPIKey,
  ClientAPIKeysState,
} from "@/api/client-api-keys/types";
import { ClientAPIKeysPage } from "./ClientAPIKeysPage";

// jsdom lacks the dialog lifecycle; focus trapping is checked in the browser.
beforeAll(() => {
  Object.defineProperties(HTMLDialogElement.prototype, {
    showModal: {
      configurable: true,
      value: function (this: HTMLDialogElement) {
        this.open = true;
      },
    },
    close: {
      configurable: true,
      value: function (this: HTMLDialogElement) {
        this.open = false;
      },
    },
  });
});
afterAll(() => {
  Reflect.deleteProperty(HTMLDialogElement.prototype, "showModal");
  Reflect.deleteProperty(HTMLDialogElement.prototype, "close");
});

const item = {
  id: "key-1",
  name: "Laptop",
  key: "sk-client-secret",
  created_at: "2026-09-06T12:00:00Z",
  updated_at: "2026-09-06T12:00:00Z",
};
function setup(
  state: ClientAPIKeysState = { mode: "permissive", keys: [] },
  loadError?: Error,
) {
  const clientApiKeys = {
    get: vi.fn().mockResolvedValue(state),
    setPolicy: vi.fn().mockImplementation(async (mode) => ({ mode })),
    add: vi.fn().mockResolvedValue(item),
    generate: vi.fn().mockResolvedValue(item),
    rename: vi.fn().mockResolvedValue({ ...item, name: "Desktop" }),
    delete: vi.fn().mockResolvedValue(undefined),
  };
  if (loadError) clientApiKeys.get.mockRejectedValueOnce(loadError);
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
    expect(screen.queryByLabelText("Key name")).not.toBeInTheDocument();
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

  it("adds an existing key exactly and keeps failed input and error in the dialog", async () => {
    const { user, clientApiKeys } = setup();
    await user.click(
      await screen.findByRole("button", { name: "Create your first key" }),
    );
    await user.click(
      screen.getByRole("radio", { name: "Add an existing key" }),
    );
    await user.type(screen.getByLabelText("Key name"), "Laptop");
    await user.type(screen.getByLabelText("Existing API key"), " exact-key ");
    clientApiKeys.add.mockRejectedValueOnce(new Error("Key already exists"));
    await user.click(screen.getByRole("button", { name: "Add key" }));
    const dialog = screen.getByRole("dialog", { name: "Create a client key" });
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Key already exists",
    );
    expect(screen.getByLabelText("Existing API key")).toHaveValue(
      " exact-key ",
    );
    await user.click(screen.getByRole("button", { name: "Add key" }));
    expect(
      await screen.findByRole("dialog", { name: "Your key is ready" }),
    ).toBeInTheDocument();
    expect(clientApiKeys.add).toHaveBeenLastCalledWith("Laptop", " exact-key ");
    await user.click(screen.getByRole("button", { name: "Done" }));
    expect(screen.queryByText(item.key)).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Create key" }));
    expect(screen.getByLabelText("Key name")).toHaveValue("");
    expect(
      screen.getByRole("radio", { name: "Generate a new key" }),
    ).toBeChecked();
  });

  it("generates a recoverable key, copies, reveals, renames and deletes it", async () => {
    const { user, clientApiKeys } = setup();
    await screen.findByRole("radio", { name: /Allow any key/ });
    await user.click(screen.getByRole("button", { name: "Create key" }));
    await user.type(screen.getByLabelText("Key name"), "Laptop");
    await user.click(screen.getByRole("button", { name: "Generate key" }));
    const generated = await screen.findByRole("region", {
      name: "Created key",
    });
    expect(within(generated).getByText(item.key)).toBeInTheDocument();
    await user.click(within(generated).getByRole("button", { name: "Copy" }));
    expect(await navigator.clipboard.readText()).toBe(item.key);
    expect(clientApiKeys.generate).toHaveBeenCalledWith("Laptop");
    await user.click(screen.getByRole("button", { name: "Done" }));
    expect(screen.queryByText(item.key)).not.toBeInTheDocument();
    await user.click(
      screen.getByRole("button", { name: "Reveal key for Laptop" }),
    );
    expect(screen.getByText(item.key)).toBeInTheDocument();
    await user.click(
      screen.getByRole("button", { name: "Hide key for Laptop" }),
    );
    const row = screen.getByRole("listitem");
    await user.click(within(row).getByRole("button", { name: "Copy" }));
    expect(await navigator.clipboard.readText()).toBe(item.key);
    await user.click(screen.getByRole("button", { name: "Rename Laptop" }));
    await user.clear(screen.getByLabelText("New name for Laptop"));
    await user.type(screen.getByLabelText("New name for Laptop"), "Desktop");
    await user.click(screen.getByRole("button", { name: "Save name" }));
    expect(
      await screen.findByRole("heading", { name: "Desktop" }),
    ).toBeInTheDocument();
    expect(clientApiKeys.rename).toHaveBeenCalledWith(item.id, "Desktop");
    await user.click(screen.getByRole("button", { name: "Delete Desktop" }));
    await user.click(screen.getByRole("button", { name: "Delete key" }));
    expect(await screen.findByText(/No client keys yet/)).toBeInTheDocument();
    expect(clientApiKeys.delete).toHaveBeenCalledWith(item.id);
  });

  it("keeps deletion failures visible and retryable in the confirmation", async () => {
    const { user, clientApiKeys } = setup({ mode: "restricted", keys: [item] });
    await user.click(
      await screen.findByRole("button", { name: "Delete Laptop" }),
    );
    clientApiKeys.delete.mockRejectedValueOnce(new Error("Delete unavailable"));
    await user.click(screen.getByRole("button", { name: "Delete key" }));
    const dialog = screen.getByRole("dialog", {
      name: "Delete client API key",
    });
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Delete unavailable",
    );
    expect(dialog).toHaveTextContent('Delete "Laptop"?');
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

  it("searches names case-insensitively, clears no results and targets the correct key", async () => {
    const desktop = {
      ...item,
      id: "key-2",
      name: "Desktop",
      key: "desktop-secret",
    };
    const { user, clientApiKeys } = setup({
      mode: "permissive",
      keys: [item, desktop],
    });
    const search = await screen.findByRole("searchbox", {
      name: "Search keys by name",
    });
    await user.type(search, " LAP ");
    expect(screen.getByRole("heading", { name: "Laptop" })).toBeInTheDocument();
    expect(
      screen.queryByRole("heading", { name: "Desktop" }),
    ).not.toBeInTheDocument();
    expect(screen.getByText("Showing 1 of 2 keys")).toBeInTheDocument();
    await user.clear(search);
    await user.type(search, "sk-client-secret");
    expect(screen.getByText("No matching keys")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Show all keys" }));
    expect(screen.getAllByRole("listitem")).toHaveLength(2);
    await user.click(screen.getByRole("button", { name: "Delete Desktop" }));
    await user.click(screen.getByRole("button", { name: "Delete key" }));
    expect(clientApiKeys.delete).toHaveBeenCalledWith(desktop.id);
    expect(await screen.findByText("Showing 1 of 1 keys")).toBeInTheDocument();
  });

  it("cancels creation and deletion without mutating data", async () => {
    const { user, clientApiKeys } = setup({ mode: "restricted", keys: [item] });
    await user.click(
      await screen.findByRole("button", { name: "Delete Laptop" }),
    );
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(clientApiKeys.delete).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Create key" }));
    await user.type(screen.getByLabelText("Key name"), "Discarded");
    fireEvent(
      screen.getByRole("dialog"),
      new Event("cancel", { cancelable: true }),
    );
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(clientApiKeys.generate).not.toHaveBeenCalled();
  });

  it("keeps the creation dialog open and prevents duplicate submissions while saving", async () => {
    const { user, clientApiKeys } = setup();
    let resolveCreate!: (value: ClientAPIKey) => void;
    clientApiKeys.generate.mockImplementationOnce(
      () =>
        new Promise<ClientAPIKey>((resolve) => {
          resolveCreate = resolve;
        }),
    );
    await user.click(
      await screen.findByRole("button", { name: "Create your first key" }),
    );
    await user.type(screen.getByLabelText("Key name"), "Laptop");
    await user.click(screen.getByRole("button", { name: "Generate key" }));
    const dialog = screen.getByRole("dialog");
    expect(dialog).toHaveAttribute("aria-busy", "true");
    expect(screen.getByRole("button", { name: "Generate key" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Close" })).toBeDisabled();
    fireEvent(dialog, new Event("cancel", { cancelable: true }));
    expect(dialog).toBeInTheDocument();
    await act(async () => resolveCreate(item));
    expect(
      await screen.findByRole("dialog", { name: "Your key is ready" }),
    ).toBeInTheDocument();
    expect(clientApiKeys.generate).toHaveBeenCalledTimes(1);
  });

  it("retains a failed rename and lets the user cancel it", async () => {
    const { user, clientApiKeys } = setup({ mode: "restricted", keys: [item] });
    await user.click(
      await screen.findByRole("button", { name: "Rename Laptop" }),
    );
    await user.clear(screen.getByLabelText("New name for Laptop"));
    await user.type(screen.getByLabelText("New name for Laptop"), "Desktop");
    clientApiKeys.rename.mockRejectedValueOnce(new Error("Rename failed"));
    await user.click(screen.getByRole("button", { name: "Save name" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Rename failed");
    expect(screen.getByLabelText("New name for Laptop")).toHaveValue("Desktop");
    expect(screen.getByRole("heading", { name: "Laptop" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Cancel rename" }));
    expect(
      screen.queryByLabelText("New name for Laptop"),
    ).not.toBeInTheDocument();
  });

  it("retries an initial load failure before enabling creation", async () => {
    const { user } = setup(undefined, new Error("Network unavailable"));
    expect(screen.getByRole("button", { name: "Create key" })).toBeDisabled();
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Network unavailable",
    );
    await user.click(screen.getByRole("button", { name: "Retry" }));
    await screen.findByRole("radio", { name: /Allow any key/ });
    expect(screen.getByRole("button", { name: "Create key" })).toBeEnabled();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
