import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import type { ApiClient, Provider } from "../api/client";
import { ApiContext } from "../api/context";
import { ProviderDetailDrawer } from "./ProviderDetailDrawer";

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
    auth_profile: {
      type: "chatgpt",
      ready: true,
      email: "user@example.com",
      account_id: "acct_test",
      plan_type: "team",
      usage: {
        fetched_at: "2026-03-22T12:05:00Z",
        plan_type: "team",
        five_hour: {
          used_percent: 22,
          window_seconds: 18000,
          reset_at: "2026-03-22T17:00:00Z",
        },
        one_week: {
          used_percent: 58,
          window_seconds: 604800,
          reset_at: "2026-03-29T00:00:00Z",
        },
      },
    },
  } as Provider;
}

describe("ProviderDetailDrawer", () => {
  it("renders GPT plan and quota windows for chatgpt providers", async () => {
    const listLogs = vi.fn().mockResolvedValue({ logs: [] });
    const mockApi = {
      logs: {
        list: listLogs,
      },
    } as unknown as ApiClient;

    render(
      <MemoryRouter>
        <ApiContext.Provider value={mockApi}>
          <ProviderDetailDrawer
            provider={buildProvider()}
            onClose={vi.fn()}
            onEdit={vi.fn()}
            onDelete={vi.fn()}
            onToggle={vi.fn()}
            onReset={vi.fn()}
            getGroupName={() => "Ungrouped"}
          />
        </ApiContext.Provider>
      </MemoryRouter>,
    );

    await waitFor(() => expect(listLogs).toHaveBeenCalled());
    await screen.findByText("No recent requests");

    expect(screen.getByText("GPT Account")).toBeInTheDocument();
    expect(screen.getByText("Team")).toBeInTheDocument();
    expect(screen.getByText("5 Hours")).toBeInTheDocument();
    expect(screen.getByText("1 Week")).toBeInTheDocument();
    expect(screen.getByText(/22% used/)).toBeInTheDocument();
    expect(screen.getByText(/58% used/)).toBeInTheDocument();
    expect(screen.getByText("Usage Updated")).toBeInTheDocument();
  });
});
