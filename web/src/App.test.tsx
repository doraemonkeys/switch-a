import { describe, it, expect, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Routes, Route, Navigate } from "react-router-dom";
import { Layout } from "@/components/Layout";
import { Dashboard } from "@/pages/Dashboard";
import { Providers } from "@/pages/providers";
import { Groups } from "@/pages/Groups";
import { RoutingPolicies } from "@/pages/RoutingPolicies";
import { Config } from "@/pages/Config";
import { Logs } from "@/pages/Logs";
import { ApiContext } from "@/api/context";
import { ToastProvider } from "@/components/Toast";
import type { ApiClient } from "@/api/client";

function createMockApiClient(): ApiClient {
  return {
    setToken: vi.fn(),
    clearToken: vi.fn(),
    providers: {
      list: vi.fn().mockResolvedValue([]),
      get: vi.fn(),
      create: vi.fn(),
      update: vi.fn(),
      delete: vi.fn(),
      enable: vi.fn(),
      disable: vi.fn(),
      reset: vi.fn(),
      refreshCredential: vi.fn(),
      refreshUsage: vi.fn(),
    },
    routingPolicies: {
      list: vi.fn().mockResolvedValue([]),
      get: vi.fn(),
      create: vi.fn(),
      update: vi.fn(),
      delete: vi.fn(),
    },
    groups: {
      list: vi.fn().mockResolvedValue([]),
      get: vi.fn(),
      create: vi.fn(),
      update: vi.fn(),
      delete: vi.fn(),
    },
    config: {
      get: vi.fn().mockResolvedValue({
        defaults: {
          log_retention_days: "7",
          upstream_connect_timeout: "30",
          global_max_attempts: "3",
        },
        values: {},
      }),
      update: vi.fn(),
    },
    status: {
      get: vi.fn().mockResolvedValue({ providers: [] }),
      health: vi.fn().mockResolvedValue([]),
    },
    logs: {
      list: vi.fn().mockResolvedValue({
        logs: [],
        total: 0,
        limit: 20,
        offset: 0,
        sort_by: "created_at",
        sort_order: "desc",
      }),
      get: vi.fn(),
    },
    stats: {
      get: vi.fn().mockResolvedValue({
        total_requests: 0,
        avg_latency_ms: 0,
        outcome_counts: {},
        providers: {
          total: 0,
          healthy: 0,
          unhealthy: 0,
          disabled: 0,
        },
        requests_by_api_type: {},
        requests_by_provider_outcome: [],
        time_range: {
          start: "2026-04-01T00:00:00Z",
          end: "2026-04-02T00:00:00Z",
        },
        outcome_timeseries: [],
      }),
    },
  } as unknown as ApiClient;
}

// Test component that replicates App routing without BrowserRouter basename issues
function TestApp({ initialPath = "/" }: { initialPath?: string }) {
  const mockApi = createMockApiClient();
  return (
    <ApiContext.Provider value={mockApi}>
      <ToastProvider>
        <MemoryRouter initialEntries={[initialPath]}>
          <Routes>
            <Route path="/" element={<Layout />}>
              <Route index element={<Dashboard />} />
              <Route path="providers" element={<Providers />} />
              <Route path="groups" element={<Groups />} />
              <Route path="routing" element={<RoutingPolicies />} />
              <Route path="config" element={<Config />} />
              <Route path="logs" element={<Logs />} />
              <Route path="*" element={<Navigate to="/" replace />} />
            </Route>
          </Routes>
        </MemoryRouter>
      </ToastProvider>
    </ApiContext.Provider>
  );
}

describe("App", () => {
  it("should render the layout with navigation", async () => {
    render(<TestApp />);

    // Check for main app title
    expect(await screen.findByText("Switch-A")).toBeInTheDocument();
    expect(screen.getByText("AI Provider Proxy")).toBeInTheDocument();
  });

  it("should render dashboard by default", async () => {
    render(<TestApp />);

    // Dashboard should be the default route
    expect(
      await screen.findByRole("heading", { name: /Dashboard/i }),
    ).toBeInTheDocument();
  });

  it("should render all navigation links", async () => {
    render(<TestApp />);

    expect(
      await screen.findByRole("link", { name: /Dashboard/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: /Providers/i }),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Groups/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Routing/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Config/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Logs/i })).toBeInTheDocument();
  });

  it("should navigate to providers page", async () => {
    render(<TestApp initialPath="/providers" />);

    await waitFor(() => {
      expect(
        screen.getByRole("heading", { name: /Providers/i }),
      ).toBeInTheDocument();
    });
  });

  it("should navigate to groups page", async () => {
    render(<TestApp initialPath="/groups" />);

    await waitFor(() => {
      expect(
        screen.getByRole("heading", { name: /Groups/i }),
      ).toBeInTheDocument();
    });
  });

  it("should navigate to config page", async () => {
    render(<TestApp initialPath="/config" />);

    await waitFor(() => {
      expect(
        screen.getByRole("heading", { name: /Configuration/i }),
      ).toBeInTheDocument();
    });
  });

  it("should navigate to routing policies page", async () => {
    render(<TestApp initialPath="/routing" />);

    await waitFor(() => {
      expect(
        screen.getByRole("heading", { name: /Routing Policies/i }),
      ).toBeInTheDocument();
    });
  });

  it("should navigate to logs page", async () => {
    render(<TestApp initialPath="/logs" />);

    expect(
      await screen.findByRole("heading", { name: /Request Logs/i }),
    ).toBeInTheDocument();
  });

  it("should redirect unknown routes to dashboard", async () => {
    render(<TestApp initialPath="/unknown-route" />);

    // Should redirect to dashboard
    expect(
      await screen.findByRole("heading", { name: /Dashboard/i }),
    ).toBeInTheDocument();
  });
});
