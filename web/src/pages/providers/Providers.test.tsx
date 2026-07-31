import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type {
  ApiClient,
  ProviderImportCommitResult,
  ProviderImportPreview,
} from "../../api";
import { ApiContext } from "../../api/context";
import { Providers } from "./Providers";
import { useProviderActions } from "./useProviderActions";
import { useGroups } from "../../hooks/useGroups";
import { useLocalStorage } from "../../hooks/useLocalStorage";

vi.mock("./useProviderActions", () => ({ useProviderActions: vi.fn() }));
vi.mock("../../hooks/useGroups", () => ({ useGroups: vi.fn() }));
vi.mock("../../hooks/useLocalStorage", () => ({ useLocalStorage: vi.fn() }));

function createProviderActions() {
  return {
    providers: [],
    hasSnapshot: true,
    loading: false,
    error: null,
    refetch: vi.fn().mockResolvedValue(undefined),
    deleteConfirm: { isOpen: false, provider: null },
    deleting: false,
    handleDeleteClick: vi.fn(),
    handleDeleteConfirm: vi.fn(),
    handleDeleteCancel: vi.fn(),
    resetConfirm: { isOpen: false, provider: null },
    resetting: false,
    handleResetClick: vi.fn(),
    handleResetConfirm: vi.fn(),
    handleResetCancel: vi.fn(),
    handleToggleProvider: vi.fn(),
    handleSaveProvider: vi.fn(),
    handleRefreshCredential: vi.fn(),
    handleRefreshUsage: vi.fn(),
  };
}

function createApiClient(
  providerImports = {
    preview: vi.fn(),
    commit: vi.fn(),
    discard: vi.fn().mockResolvedValue(undefined),
  },
) {
  return {
    providerImports,
  } as unknown as ApiClient;
}

const successfulPreview: ProviderImportPreview = {
  import_id: "import-1",
  expires_at: "2026-07-30T22:00:00Z",
  summary: {
    total: 1,
    ready: 1,
    existing: 0,
    duplicate: 0,
    invalid: 0,
    unsupported: 0,
  },
  warnings: [],
  items: [
    {
      candidate_id: "candidate-1",
      source_index: 0,
      status: "ready",
      name: "Imported account",
      provider_id: "imported-account",
      priority: 0,
      concurrency: 0,
      default_selected: true,
      warnings: [],
    },
  ],
};

const successfulResult: ProviderImportCommitResult = {
  import_id: "import-1",
  summary: { created: 1, updated: 0, skipped: 0 },
  items: [
    {
      candidate_id: "candidate-1",
      outcome: "created",
      provider_id: "imported-account",
      name: "Imported account",
    },
  ],
};

describe("Providers import entry points", () => {
  beforeEach(() => {
    vi.mocked(useProviderActions).mockReturnValue(createProviderActions());
    vi.mocked(useGroups).mockReturnValue({
      groups: [],
      loading: false,
      error: null,
      refetch: vi.fn(),
      createGroup: vi.fn(),
      updateGroup: vi.fn(),
      deleteGroup: vi.fn(),
      enableGroup: vi.fn(),
      disableGroup: vi.fn(),
    });
    vi.mocked(useLocalStorage).mockReturnValue([0, vi.fn()]);
  });

  it("opens the account importer from both the header and empty state", async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <ApiContext.Provider value={createApiClient()}>
          <Providers />
        </ApiContext.Provider>
      </MemoryRouter>,
    );

    const entryButtons = screen.getAllByRole("button", {
      name: "Import Accounts",
    });
    expect(entryButtons).toHaveLength(2);

    await user.click(entryButtons[0]);
    expect(
      screen.getByRole("dialog", { name: "Import GPT accounts" }),
    ).toBeInTheDocument();
    await user.click(
      screen.getByRole("button", { name: "Close account import" }),
    );
    expect(
      screen.queryByRole("dialog", { name: "Import GPT accounts" }),
    ).not.toBeInTheDocument();

    await user.click(entryButtons[1]);
    expect(
      screen.getByRole("dialog", { name: "Import GPT accounts" }),
    ).toBeInTheDocument();
  });

  it("refreshes providers exactly once across successful commit and close", async () => {
    const user = userEvent.setup();
    const actions = createProviderActions();
    vi.mocked(useProviderActions).mockReturnValue(actions);
    const providerImports = {
      preview: vi.fn().mockResolvedValue(successfulPreview),
      commit: vi.fn().mockResolvedValue(successfulResult),
      discard: vi.fn().mockResolvedValue(undefined),
    };
    render(
      <MemoryRouter>
        <ApiContext.Provider value={createApiClient(providerImports)}>
          <Providers />
        </ApiContext.Provider>
      </MemoryRouter>,
    );
    await user.click(
      screen.getAllByRole("button", { name: "Import Accounts" })[0],
    );
    const file = new File(['{"accounts":[]}'], "sub2api-account.txt", {
      type: "text/plain",
    });
    Object.defineProperty(file, "text", {
      value: vi.fn().mockResolvedValue('{"accounts":[]}'),
    });
    await user.upload(screen.getByLabelText("sub2api export file"), file);
    await screen.findByRole("heading", { name: "Imported account" });
    await user.click(
      screen.getByRole("checkbox", {
        name: /I understand the OAuth token ownership risk/i,
      }),
    );
    await user.click(screen.getByRole("button", { name: "Import 1 account" }));

    await screen.findByRole("heading", { name: "Import complete" });
    expect(actions.refetch).toHaveBeenCalledTimes(1);
    await user.click(screen.getByRole("button", { name: "View providers" }));

    expect(
      screen.queryByRole("dialog", { name: "Import GPT accounts" }),
    ).not.toBeInTheDocument();
    expect(actions.refetch).toHaveBeenCalledTimes(1);
  });

  it("keeps an active import review mounted when a background refresh fails", async () => {
    const user = userEvent.setup();
    const actions = createProviderActions();
    vi.mocked(useProviderActions).mockReturnValue(actions);
    const view = render(
      <MemoryRouter>
        <ApiContext.Provider value={createApiClient()}>
          <Providers />
        </ApiContext.Provider>
      </MemoryRouter>,
    );
    await user.click(
      screen.getAllByRole("button", { name: "Import Accounts" })[0],
    );

    vi.mocked(useProviderActions).mockReturnValue({
      ...actions,
      error: new Error("temporary provider list failure"),
    });
    view.rerender(
      <MemoryRouter>
        <ApiContext.Provider value={createApiClient()}>
          <Providers />
        </ApiContext.Provider>
      </MemoryRouter>,
    );

    expect(
      screen.getByRole("dialog", { name: "Import GPT accounts" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent(
      "The last successful snapshot is still shown",
    );
  });

  it("pauses provider polling while the import dialog is open", () => {
    vi.useFakeTimers();
    try {
      const actions = createProviderActions();
      vi.mocked(useProviderActions).mockReturnValue(actions);
      vi.mocked(useLocalStorage).mockReturnValue([1_000, vi.fn()]);
      render(
        <MemoryRouter>
          <ApiContext.Provider value={createApiClient()}>
            <Providers />
          </ApiContext.Provider>
        </MemoryRouter>,
      );

      fireEvent.click(
        screen.getAllByRole("button", { name: "Import Accounts" })[0],
      );
      act(() => vi.advanceTimersByTime(3_000));
      expect(actions.refetch).not.toHaveBeenCalled();

      fireEvent.click(
        screen.getByRole("button", { name: "Close account import" }),
      );
      act(() => vi.advanceTimersByTime(1_000));
      expect(actions.refetch).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });
});
