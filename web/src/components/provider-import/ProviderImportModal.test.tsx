import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type {
  Group,
  ProviderImportCommitResult,
  ProviderImportPreview,
} from "../../api";
import type { ProviderImportGateway } from "../../hooks/useProviderImportFlow";
import { ProviderImportModal } from "./ProviderImportModal";

const preview: ProviderImportPreview = {
  import_id: "import-1",
  expires_at: "2026-07-30T22:00:00Z",
  create_defaults: {
    weight: 1,
    max_retries: 0,
    backoff: {
      initial_delay: "100ms",
      max_delay: "5s",
      multiplier: 2,
      jitter: false,
    },
  },
  summary: {
    total: 3,
    ready: 1,
    existing: 1,
    duplicate: 0,
    invalid: 1,
    unsupported: 0,
  },
  warnings: [
    {
      code: "SOURCE_FIELD_IGNORED",
      message: "rate_multiplier has no equivalent and will be ignored.",
    },
  ],
  items: [
    {
      candidate_id: "ready-1",
      source_index: 0,
      status: "ready",
      name: "Ready Account",
      provider_id: "ready-account",
      email: "ready@example.com",
      account_id: "acct-ready-12345678",
      plan_type: "plus",
      priority: 1,
      concurrency: 10,
      default_selected: true,
      warnings: [],
    },
    {
      candidate_id: "existing-1",
      source_index: 1,
      status: "existing",
      name: "Existing Account",
      provider_id: "existing-provider",
      email: "existing@example.com",
      priority: 2,
      concurrency: 4,
      existing_provider_id: "existing-provider",
      existing_provider_name: "Existing Provider",
      default_selected: false,
      message: "This GPT account is already connected.",
      warnings: [],
    },
    {
      candidate_id: "invalid-1",
      source_index: 2,
      status: "invalid",
      name: "Invalid Account",
      provider_id: "invalid-account",
      email: "invalid@example.com",
      priority: 0,
      concurrency: 0,
      default_selected: false,
      message: "Refresh token is missing.",
      warnings: [],
    },
  ],
};

const commitResult: ProviderImportCommitResult = {
  import_id: "import-1",
  summary: { created: 1, updated: 1, skipped: 1 },
  items: [
    {
      candidate_id: "ready-1",
      outcome: "created",
      provider_id: "renamed-provider",
      name: "Renamed Provider",
    },
    {
      candidate_id: "existing-1",
      outcome: "updated",
      provider_id: "existing-provider",
      name: "Existing Provider",
    },
  ],
};

const groups = [
  {
    id: "gpt-accounts",
    name: "GPT Accounts",
    enabled: true,
  },
] as Group[];

function createGateway(
  commit: ProviderImportGateway["commit"] = vi
    .fn()
    .mockResolvedValue(commitResult),
): ProviderImportGateway {
  return {
    preview: vi.fn().mockResolvedValue(preview),
    commit,
    discard: vi.fn().mockResolvedValue(undefined),
  };
}

function createExportFile() {
  const source = JSON.stringify({
    accounts: [
      {
        credentials: {
          access_token: "secret-access-token",
          refresh_token: "secret-refresh-token",
        },
      },
    ],
  });
  const file = new File([source], "sub2api-account.txt", {
    type: "text/plain",
  });
  Object.defineProperty(file, "text", {
    value: vi.fn().mockResolvedValue(source),
  });
  return { file, source };
}

function renderModal(gateway = createGateway(), onClose = vi.fn()) {
  const onCommitted = vi.fn();
  const onCheckProviders = vi.fn();
  render(
    <ProviderImportModal
      gateway={gateway}
      existingProviderIds={["existing-provider"]}
      groups={groups}
      onClose={onClose}
      onCheckProviders={onCheckProviders}
      onCommitted={onCommitted}
    />,
  );
  return { gateway, onClose, onCheckProviders, onCommitted };
}

async function uploadExport(user: ReturnType<typeof userEvent.setup>) {
  const { file } = createExportFile();
  await user.upload(screen.getByLabelText("sub2api export file"), file);
  await screen.findByRole("heading", { name: "ready@example.com" });
}

describe("ProviderImportModal", () => {
  it("reviews a .txt export without exposing credentials", async () => {
    const user = userEvent.setup();
    const { gateway } = renderModal();
    const { file, source } = createExportFile();

    expect(
      screen.getByRole("dialog", { name: "Import GPT accounts" }),
    ).toBeInTheDocument();
    const fileInput = screen.getByLabelText("sub2api export file");
    expect(fileInput).toHaveAttribute("tabindex", "-1");
    await user.upload(fileInput, file);

    expect(
      await screen.findByRole("heading", { name: "ready@example.com" }),
    ).toBeInTheDocument();
    expect(
      document.querySelector('time[datetime="2026-07-30T22:00:00Z"]'),
    ).toBeInTheDocument();
    expect(gateway.preview).toHaveBeenCalledWith(source);
    expect(
      screen.getByRole("checkbox", {
        name: "Create provider for ready@example.com",
      }),
    ).toBeChecked();
    expect(
      screen.getByRole("checkbox", {
        name: "Update credentials on Existing Provider",
      }),
    ).not.toBeChecked();
    expect(
      screen.getByRole("checkbox", {
        name: "Cannot import invalid@example.com",
      }),
    ).toBeDisabled();
    expect(
      screen.getByText(/rate_multiplier has no equivalent/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        /Codex 5-hour and 7-day quota snapshots plus codex_usage_updated_at are imported/,
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Import GPT accounts" }),
    ).toHaveFocus();
    await user.keyboard("{Shift>}{Tab}{/Shift}");
    expect(screen.getByRole("button", { name: "Cancel" })).toHaveFocus();
    expect(document.body).not.toHaveTextContent("secret-access-token");
    expect(document.body).not.toHaveTextContent("secret-refresh-token");
  });

  it("commits edited creates and an explicitly selected credential update", async () => {
    const user = userEvent.setup();
    const { gateway, onCommitted } = renderModal();
    await uploadExport(user);

    const importButton = screen.getByRole("button", {
      name: "Import 1 account",
    });
    expect(importButton).toBeDisabled();
    expect(
      screen.getByText("Confirm the OAuth token ownership risk above."),
    ).toBeInTheDocument();

    const defaultWeight = screen.getByRole("spinbutton", { name: "Weight" });
    const defaultRetries = screen.getByRole("spinbutton", {
      name: "Max retries",
    });
    await user.clear(defaultWeight);
    await user.type(defaultWeight, "5");
    await user.clear(defaultRetries);
    await user.type(defaultRetries, "2");
    await user.click(
      screen.getByRole("button", { name: "Apply to 1 new provider" }),
    );

    await user.click(
      screen.getByText("Provider settings for ready@example.com"),
    );

    const providerNameInput = screen.getByLabelText(
      "Provider name for ready@example.com",
    );
    await user.clear(providerNameInput);
    expect(providerNameInput).toHaveAttribute("aria-invalid", "true");
    expect(
      screen.getByText(/Fix settings for 1 selected account/),
    ).toBeInTheDocument();
    await user.type(providerNameInput, "Renamed Provider");
    await user.clear(
      screen.getByLabelText("Provider ID for ready@example.com"),
    );
    await user.type(
      screen.getByLabelText("Provider ID for ready@example.com"),
      "renamed-provider",
    );
    await user.selectOptions(screen.getByLabelText("Group"), "gpt-accounts");
    await user.click(
      screen.getByRole("checkbox", {
        name: "Update credentials on Existing Provider",
      }),
    );
    await user.click(
      screen.getByRole("checkbox", {
        name: /I understand the OAuth token ownership risk/i,
      }),
    );
    await user.click(screen.getByRole("button", { name: "Import 2 accounts" }));

    await waitFor(() =>
      expect(gateway.commit).toHaveBeenCalledWith("import-1", {
        group_id: "gpt-accounts",
        items: [
          {
            candidate_id: "ready-1",
            action: "create",
            provider_id: "renamed-provider",
            name: "Renamed Provider",
            priority: 1,
            weight: 5,
            concurrency: 10,
            max_retries: 2,
            backoff: {
              initial_delay: "100ms",
              max_delay: "5s",
              multiplier: 2,
              jitter: false,
            },
          },
          {
            candidate_id: "existing-1",
            action: "update",
            provider_id: "existing-provider",
          },
        ],
      }),
    );
    expect(
      await screen.findByRole("heading", { name: "Import complete" }),
    ).toBeInTheDocument();
    expect(onCommitted).toHaveBeenCalledWith(commitResult);
  });

  it("discards a reviewed draft when Escape closes the dialog", async () => {
    const user = userEvent.setup();
    const { gateway, onClose } = renderModal();
    await uploadExport(user);

    await user.keyboard("{Escape}");

    expect(onClose).toHaveBeenCalledTimes(1);
    await waitFor(() =>
      expect(gateway.discard).toHaveBeenCalledWith("import-1"),
    );
  });

  it("cannot close while an atomic commit is in flight", async () => {
    const user = userEvent.setup();
    let resolveCommit!: (result: ProviderImportCommitResult) => void;
    const pendingCommit = new Promise<ProviderImportCommitResult>((resolve) => {
      resolveCommit = resolve;
    });
    const gateway = createGateway(vi.fn().mockReturnValue(pendingCommit));
    const onClose = vi.fn();
    renderModal(gateway, onClose);
    await uploadExport(user);
    await user.click(
      screen.getByRole("checkbox", {
        name: /I understand the OAuth token ownership risk/i,
      }),
    );
    await user.click(screen.getByRole("button", { name: "Import 1 account" }));

    expect(
      screen.getByRole("button", { name: "Close account import" }),
    ).toBeDisabled();
    await user.keyboard("{Escape}");
    expect(onClose).not.toHaveBeenCalled();

    await act(async () => {
      resolveCommit(commitResult);
      await pendingCommit;
    });
    expect(
      await screen.findByRole("heading", { name: "Import complete" }),
    ).toBeInTheDocument();
  });

  it("requires a new preview when the reviewed draft has expired", async () => {
    const user = userEvent.setup();
    const gateway = createGateway(
      vi
        .fn()
        .mockRejectedValue(
          Object.assign(new Error("Preview expired"), { status: 410 }),
        ),
    );
    renderModal(gateway);
    await uploadExport(user);
    await user.click(
      screen.getByRole("checkbox", {
        name: /I understand the OAuth token ownership risk/i,
      }),
    );
    await user.click(screen.getByRole("button", { name: "Import 1 account" }));

    expect(
      await screen.findByRole("heading", { name: "Preview expired" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveFocus();
    expect(
      screen.getByText(/Check Providers for any applied changes/),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Close and check providers" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Retry" }),
    ).not.toBeInTheDocument();

    await user.click(
      screen.getByRole("button", { name: "Choose file and preview again" }),
    );
    expect(screen.getByLabelText("sub2api export file")).toBeInTheDocument();
    await waitFor(() =>
      expect(gateway.discard).toHaveBeenCalledWith("import-1"),
    );
  });

  it("refreshes the provider view when recovery is closed", async () => {
    const user = userEvent.setup();
    const gateway = createGateway(
      vi
        .fn()
        .mockRejectedValue(
          Object.assign(new Error("Preview unavailable"), { status: 404 }),
        ),
    );
    const { onCheckProviders, onClose } = renderModal(gateway);
    await uploadExport(user);
    await user.click(
      screen.getByRole("checkbox", {
        name: /I understand the OAuth token ownership risk/i,
      }),
    );
    await user.click(screen.getByRole("button", { name: "Import 1 account" }));
    await screen.findByRole("heading", {
      name: "Preview is no longer available",
    });

    await user.click(
      screen.getByRole("button", { name: "Close and check providers" }),
    );

    expect(onCheckProviders).toHaveBeenCalledTimes(1);
    expect(onClose).not.toHaveBeenCalled();
    await waitFor(() =>
      expect(gateway.discard).toHaveBeenCalledWith("import-1"),
    );
  });
});
