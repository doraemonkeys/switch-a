import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import type { Provider } from "../../api/client";
import { ProvidersTableBody } from "./ProvidersTableBody";

function buildProvider(): Provider {
  return {
    id: "provider-gpt",
    name: "GPT Provider",
    api_key: "",
    api_types: [
      {
        provider_id: "provider-gpt",
        api_type: "codex",
        base_url: "https://chatgpt.com/backend-api/codex",
        api_key: "",
      },
    ],
    auth_mode: "auto",
    credential_type: "chatgpt",
    group_id: null,
    weight: 1,
    priority: 1,
    concurrency: 1,
    rate_limit_rpm: 0,
    rate_limit_window: 0,
    max_retries: 0,
    strategy: "round_robin",
    vendor: "",
    failover_scope: "any",
    accept_failover: "any",
    enabled: true,
    created_at: "2026-03-22T12:00:00Z",
    updated_at: "2026-03-22T12:00:00Z",
    auth: {
      type: "chatgpt",
      status: "active",
      email: "user@example.com",
      account_id: "acct_test",
      plan_type: "plus",
      usage: {
        fetched_at: "2026-03-22T12:05:00Z",
        plan_type: "plus",
        five_hour: {
          used_percent: 18,
          window_seconds: 18000,
          reset_at: "2026-03-22T17:00:00Z",
        },
        one_week: {
          used_percent: 42,
          window_seconds: 604800,
          reset_at: "2026-03-29T00:00:00Z",
        },
      },
    },
  } as Provider;
}

describe("ProvidersTableBody", () => {
  it("renders GPT plan and usage summary in the provider row", () => {
    const provider = buildProvider();

    render(
      <table>
        <tbody>
          <ProvidersTableBody
            loading={false}
            providers={[provider]}
            filteredProviders={[provider]}
            onToggle={vi.fn()}
            onEdit={vi.fn()}
            onDelete={vi.fn()}
            onReset={vi.fn()}
            onAddClick={vi.fn()}
            onImportClick={vi.fn()}
            getGroupName={() => "Ungrouped"}
            getGroupEnabled={() => undefined}
          />
        </tbody>
      </table>,
    );

    expect(screen.getByText("GPT Provider")).toBeInTheDocument();
    expect(screen.getByText("active")).toBeInTheDocument();
    expect(screen.getByText(/Plus/)).toBeInTheDocument();
    expect(screen.getByText(/5h 18% used/)).toBeInTheDocument();
    expect(screen.getByText(/1w 42% used/)).toBeInTheDocument();
  });

  it("shows reconnect-required auth state without relying on usage readiness", () => {
    const provider = {
      ...buildProvider(),
      auth: {
        type: "chatgpt",
        status: "reauth_required",
        reason: "invalid_grant",
        last_error: "refresh_token_reused",
        email: "user@example.com",
      },
    } as Provider;

    render(
      <table>
        <tbody>
          <ProvidersTableBody
            loading={false}
            providers={[provider]}
            filteredProviders={[provider]}
            onToggle={vi.fn()}
            onEdit={vi.fn()}
            onDelete={vi.fn()}
            onReset={vi.fn()}
            onAddClick={vi.fn()}
            onImportClick={vi.fn()}
            getGroupName={() => "Ungrouped"}
            getGroupEnabled={() => undefined}
          />
        </tbody>
      </table>,
    );

    expect(screen.getByText("reauth_required")).toBeInTheDocument();
    expect(screen.getByText("invalid_grant")).toBeInTheDocument();
    expect(screen.getByText("refresh_token_reused")).toBeInTheDocument();
    expect(screen.queryByText(/5h 18% used/)).not.toBeInTheDocument();
  });

  it("marks providers whose assigned group is disabled", () => {
    const provider = {
      ...buildProvider(),
      group_id: "group-disabled",
    } as Provider;

    render(
      <table>
        <tbody>
          <ProvidersTableBody
            loading={false}
            providers={[provider]}
            filteredProviders={[provider]}
            onToggle={vi.fn()}
            onEdit={vi.fn()}
            onDelete={vi.fn()}
            onReset={vi.fn()}
            onAddClick={vi.fn()}
            onImportClick={vi.fn()}
            getGroupName={() => "GPT Account"}
            getGroupEnabled={() => false}
          />
        </tbody>
      </table>,
    );

    expect(screen.getByText("GPT Account")).toBeInTheDocument();
    expect(screen.getByLabelText("Group disabled")).toBeInTheDocument();
    expect(
      screen.getByTitle("Filter by group: GPT Account (group disabled)"),
    ).toBeInTheDocument();
  });
});
