import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type {
  Group,
  ProviderImportCandidate,
  ProviderImportCandidateStatus,
  ProviderImportPreview,
} from "../../api";
import type { ProviderImportDecision } from "../../lib/providerImport";
import {
  PROVIDER_IMPORT_MAX_PROVIDER_ID_LENGTH,
  PROVIDER_IMPORT_MAX_PROVIDER_NAME_LENGTH,
  PROVIDER_IMPORT_MAX_SCHEDULING_VALUE,
  type ProviderImportValidationError,
} from "../../lib/providerImportSettings";
import { ReviewStep } from "./ReviewStep";

function createCandidate(
  number: number,
  status: ProviderImportCandidateStatus,
): ProviderImportCandidate {
  return {
    candidate_id: `candidate-${number}`,
    source_index: number - 1,
    status,
    name: `Account ${number}`,
    provider_id: `account-${number}`,
    email: `account-${number}@example.com`,
    account_id: `account-id-${number.toString().padStart(8, "0")}`,
    plan_type: "plus",
    priority: number,
    concurrency: 4,
    existing_provider_id:
      status === "existing" ? `existing-${number}` : undefined,
    existing_provider_name:
      status === "existing" ? `Existing Provider ${number}` : undefined,
    default_selected: false,
    message: status === "invalid" ? "Refresh token is missing." : undefined,
    warnings: [],
  };
}

function createDecision(
  candidate: ProviderImportCandidate,
  action: ProviderImportDecision["action"] = "skip",
): ProviderImportDecision {
  return {
    candidateId: candidate.candidate_id,
    action,
    provider: {
      providerId: candidate.provider_id,
      name: candidate.name,
      priority: candidate.priority,
      weight: 1,
      concurrency: candidate.concurrency,
      maxRetries: 0,
      backoff: {
        initial_delay: "100ms",
        max_delay: "5s",
        multiplier: 2,
        jitter: false,
      },
    },
  };
}

function createPreview(
  items: ProviderImportCandidate[],
  expiresAt = "2026-07-31T03:30:00Z",
): ProviderImportPreview {
  const count = (status: ProviderImportCandidateStatus) =>
    items.filter((item) => item.status === status).length;
  return {
    import_id: "import-1",
    expires_at: expiresAt,
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
    items,
    summary: {
      total: items.length,
      ready: count("ready"),
      existing: count("existing"),
      duplicate: count("duplicate"),
      invalid: count("invalid"),
      unsupported: count("unsupported"),
    },
    warnings: [
      {
        code: "SOURCE_FIELD_IGNORED",
        message: "rate_multiplier has no equivalent and will be ignored.",
      },
    ],
  };
}

function renderReview({
  candidates,
  decisions = candidates.map((candidate) => createDecision(candidate)),
  expiresAt,
  validationErrors = new Map<string, ProviderImportValidationError>(),
}: {
  candidates: ProviderImportCandidate[];
  decisions?: ProviderImportDecision[];
  expiresAt?: string;
  validationErrors?: ReadonlyMap<string, ProviderImportValidationError>;
}) {
  const handlers = {
    onSetAction: vi.fn(),
    onEditProvider: vi.fn(),
    onApplyNewProviderDefaults: vi.fn(),
    onSetGroup: vi.fn(),
    onSetAcknowledgement: vi.fn(),
    onSelectAllReady: vi.fn(),
    onSelectAllExisting: vi.fn(),
    onClearSelection: vi.fn(),
  };
  const preview = createPreview(candidates, expiresAt);
  const draft = {
    groupId: null,
    newProviderDefaults: {
      weight: 1,
      maxRetries: 0,
      backoff: {
        initial_delay: "100ms",
        max_delay: "5s",
        multiplier: 2,
        jitter: false,
      },
    },
    acknowledgedRefreshTokenOwnership: false,
    decisions,
  };
  const groups: Group[] = [
    {
      id: "gpt",
      name: "GPT Accounts",
      strategy: "priority",
      priority: 0,
      weight: 1,
      enabled: true,
      created_at: "2026-07-30T00:00:00Z",
      updated_at: "2026-07-30T00:00:00Z",
    },
  ];
  const view = render(
    <ReviewStep
      preview={preview}
      draft={draft}
      groups={groups}
      validationErrors={validationErrors}
      error={null}
      {...handlers}
    />,
  );
  return {
    ...handlers,
    rerenderWithValidationErrors: (
      nextValidationErrors: ReadonlyMap<string, ProviderImportValidationError>,
    ) =>
      view.rerender(
        <ReviewStep
          preview={preview}
          draft={draft}
          groups={groups}
          validationErrors={nextValidationErrors}
          error={null}
          {...handlers}
        />,
      ),
  };
}

describe("ReviewStep", () => {
  it("keeps risk acknowledgement and expiry context ahead of compact account rows", async () => {
    const user = userEvent.setup();
    const ready = createCandidate(1, "ready");
    const existing = createCandidate(2, "existing");
    const handlers = renderReview({
      candidates: [ready, existing],
      decisions: [createDecision(ready, "create"), createDecision(existing)],
    });

    const accountList = screen.getByRole("list", {
      name: "Accounts to import",
    });
    const acknowledgement = screen.getByRole("checkbox", {
      name: /I understand the OAuth token ownership risk/i,
    });
    expect(
      screen.getByText(/verifies selected tokens with OpenAI signing keys/i),
    ).toBeInTheDocument();
    expect(
      acknowledgement.compareDocumentPosition(accountList) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).not.toBe(0);
    expect(screen.getByText(/Preview expires/)).toContainElement(
      document.querySelector("time"),
    );
    expect(document.querySelector("time")).toHaveAttribute(
      "datetime",
      "2026-07-31T03:30:00Z",
    );

    const settingsSummary = screen.getByText(
      "Provider settings for account-1@example.com",
    );
    const settings = settingsSummary.closest("details");
    expect(settings).not.toHaveAttribute("open");
    expect(
      screen.getByText("Priority 1 · Weight 1 · Concurrency 4 · Retries none"),
    ).toBeInTheDocument();
    await user.click(settingsSummary);
    expect(settings).toHaveAttribute("open");

    const providerName = screen.getByRole("textbox", {
      name: "Provider name for account-1@example.com",
    });
    expect(providerName).toHaveAttribute(
      "maxlength",
      String(PROVIDER_IMPORT_MAX_PROVIDER_NAME_LENGTH),
    );
    expect(
      screen.getByRole("textbox", {
        name: "Provider ID for account-1@example.com",
      }),
    ).toHaveAttribute(
      "maxlength",
      String(PROVIDER_IMPORT_MAX_PROVIDER_ID_LENGTH),
    );
    for (const label of [
      "Priority for account-1@example.com",
      "Weight for account-1@example.com",
      "Concurrency for account-1@example.com",
    ]) {
      expect(screen.getByRole("spinbutton", { name: label })).toHaveAttribute(
        "max",
        String(PROVIDER_IMPORT_MAX_SCHEDULING_VALUE),
      );
    }
    expect(
      screen.getByRole("spinbutton", {
        name: "Max retries for account-1@example.com",
      }),
    ).toHaveAttribute("max", "10");
    fireEvent.change(providerName, { target: { value: "Renamed Provider" } });
    expect(handlers.onEditProvider).toHaveBeenCalledWith(
      ready.candidate_id,
      "name",
      "Renamed Provider",
    );

    await user.click(acknowledgement);
    expect(handlers.onSetAcknowledgement).toHaveBeenCalledWith(true);
    await user.click(
      screen.getByRole("checkbox", {
        name: "Update credentials on Existing Provider 2",
      }),
    );
    expect(handlers.onSetAction).toHaveBeenCalledWith(
      existing.candidate_id,
      "update",
    );
  });

  it("filters selected, ready, existing, and blocked accounts with pressed state", async () => {
    const user = userEvent.setup();
    const candidates = [
      createCandidate(1, "ready"),
      createCandidate(2, "ready"),
      createCandidate(3, "existing"),
      createCandidate(4, "existing"),
      createCandidate(5, "duplicate"),
      createCandidate(6, "invalid"),
      createCandidate(7, "unsupported"),
    ];
    const selectedActions = new Map<string, ProviderImportDecision["action"]>([
      ["candidate-1", "create"],
      ["candidate-3", "update"],
    ]);
    const decisions = candidates.map((candidate) =>
      createDecision(candidate, selectedActions.get(candidate.candidate_id)),
    );
    renderReview({ candidates, decisions });

    const selectedFilter = screen.getByRole("button", {
      name: "Selected: 2 accounts",
    });
    await user.click(selectedFilter);
    expect(selectedFilter).toHaveAttribute("aria-pressed", "true");
    expect(
      within(
        screen.getByRole("list", { name: "Accounts to import" }),
      ).getAllByRole("listitem"),
    ).toHaveLength(2);
    expect(
      screen.getByRole("heading", { name: "account-1@example.com" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "account-3@example.com" }),
    ).toBeInTheDocument();

    const blockedFilter = screen.getByRole("button", {
      name: "Blocked: 3 accounts",
    });
    await user.click(blockedFilter);
    expect(blockedFilter).toHaveAttribute("aria-pressed", "true");
    expect(
      within(
        screen.getByRole("list", { name: "Accounts to import" }),
      ).getAllByRole("listitem"),
    ).toHaveLength(3);
    expect(
      screen.getByRole("button", { name: "Ready: 2 accounts" }),
    ).toBeEnabled();
    expect(
      screen.getByRole("button", { name: "Existing: 2 accounts" }),
    ).toBeEnabled();
    expect(
      screen.getByRole("button", { name: "All: 7 accounts" }),
    ).toBeEnabled();
  });

  it("paginates large previews at 25 accounts per page", async () => {
    const user = userEvent.setup();
    const candidates = Array.from({ length: 52 }, (_, index) =>
      createCandidate(index + 1, "ready"),
    );
    renderReview({ candidates });

    const visibleAccounts = () =>
      within(
        screen.getByRole("list", { name: "Accounts to import" }),
      ).getAllByRole("listitem");
    expect(visibleAccounts()).toHaveLength(25);
    expect(
      screen.getByRole("heading", { name: "account-1@example.com" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("heading", { name: "account-26@example.com" }),
    ).not.toBeInTheDocument();
    expect(screen.getByText("Showing 1–25 of 52 accounts")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Next" }));
    expect(screen.getByText("Page 2 of 3")).toBeInTheDocument();
    expect(visibleAccounts()).toHaveLength(25);
    expect(
      screen.getByRole("heading", { name: "account-26@example.com" }),
    ).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Next" }));
    expect(screen.getByText("Page 3 of 3")).toBeInTheDocument();
    expect(visibleAccounts()).toHaveLength(2);
    expect(screen.getByRole("button", { name: "Next" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Previous" })).toBeEnabled();
  });

  it("reveals validation guidance without collapsing when the error resolves", async () => {
    const candidate = { ...createCandidate(1, "ready"), concurrency: 0 };
    const { rerenderWithValidationErrors } = renderReview({
      candidates: [candidate],
      decisions: [createDecision(candidate, "create")],
      expiresAt: "expiry unavailable",
      validationErrors: new Map([
        [
          candidate.candidate_id,
          { field: "providerId", message: "Provider ID is already in use." },
        ],
      ]),
    });

    const expiry = screen.getByText("expiry unavailable");
    expect(expiry.tagName).toBe("TIME");
    expect(expiry).toHaveAttribute("datetime", "expiry unavailable");
    expect(
      screen.getByText(
        "Priority 1 · Weight 1 · Concurrency unlimited · Retries none",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/Provider settings/).closest("details"),
    ).toHaveAttribute("open");
    expect(
      screen.getByRole("textbox", {
        name: "Provider ID for account-1@example.com",
      }),
    ).toHaveAttribute("aria-invalid", "true");
    expect(
      screen.getByText("Provider ID is already in use."),
    ).toBeInTheDocument();

    await screen.findByText("Needs attention");
    rerenderWithValidationErrors(new Map());
    expect(
      screen.getByText(/Provider settings/).closest("details"),
    ).toHaveAttribute("open");
  });
});
