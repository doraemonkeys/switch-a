import { describe, it, expect, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { AppRoutes } from "./App";
import { ApiContext } from "@/api/context";
import { ToastProvider } from "@/components/Toast";
import type { ApiClient } from "@/api/client";

function createMockApiClient(): ApiClient {
  return {
    setToken: vi.fn(),
    clearToken: vi.fn(),
    getToken: vi.fn().mockReturnValue("admin-token"),
    validateToken: vi.fn().mockResolvedValue(true),
    apiCatalog: {
      get: vi.fn().mockResolvedValue({
        schema_version: 1,
        custom_api_type_prefix: "custom:",
        api_types: [
          {
            api_type: "codex",
            label: "Codex",
            description: "OpenAI Responses API",
            display_order: 0,
            semantic_error_supported: true,
            response_protocol_ids: ["openai.responses.json.v1"],
          },
        ],
      }),
    },
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
    errorDetection: {
      rules: {
        list: vi.fn().mockResolvedValue({
          value: {
            schema_version: 1,
            rule_set_revision: "0",
            rules: [],
          },
          etag: '"internal-error-rules/0"',
        }),
        get: vi.fn(),
        create: vi.fn(),
        update: vi.fn(),
        delete: vi.fn(),
        reorder: vi.fn(),
      },
      stats: {
        get: vi.fn().mockResolvedValue({
          schema_version: 1,
          rule_set_revision: "0",
          stats: [],
        }),
      },
      testMessage: vi.fn(),
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
    requests: {
      active: vi.fn().mockResolvedValue({ requests: [], count: 0 }),
    },
    debugCapture: {
      status: vi.fn().mockResolvedValue({
        state: "stopped",
        process_memory: {
          ceiling_bytes: 536_870_912,
          charged_bytes: 0,
          retained_bytes: 0,
          pinned_bytes: 0,
          releasing_bytes: 0,
          temporary_bytes: 0,
        },
        pending_export_count: 0,
        active_download_count: 0,
        session: null,
      }),
      start: vi.fn(),
      stop: vi.fn(),
      listRecords: vi.fn(),
      getRecord: vi.fn(),
      createExport: vi.fn(),
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
          <AppRoutes />
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
    expect(screen.getByRole("link", { name: /Monitor/i })).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: /Providers/i }),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Groups/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Routing/i })).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: /Error Detection/i }),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Config/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Logs/i })).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: /Debug Capture/i }),
    ).toBeInTheDocument();
  });

  it("should navigate to Debug Capture", async () => {
    render(<TestApp initialPath="/debug-capture" />);

    expect(
      await screen.findByRole("heading", { name: "Debug Capture" }),
    ).toBeInTheDocument();
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

  it("opens the sole editor route with decoded provider/API prefill", async () => {
    render(
      <TestApp initialPath="/error-detection?scope=provider&provider_id=deleted-provider&api_type=custom%3Aprivate" />,
    );

    expect(
      await screen.findByRole("heading", { name: "Internal Error Detection" }),
    ).toBeInTheDocument();
    const editorHeading = await screen.findByRole("heading", {
      name: "Create detection rule",
    });
    const editor = editorHeading.closest("section");
    expect(editor).not.toBeNull();
    const editorQueries = within(editor as HTMLElement);
    expect(editorQueries.getByLabelText("API type")).toHaveValue(
      "custom:private",
    );
    expect(
      editorQueries.getByRole("option", {
        name: /Deleted provider.*deleted-provider/,
      }),
    ).toBeInTheDocument();
    const unsupportedState = screen
      .getByText(/Custom API types do not support structured error detection/i)
      .closest('[role="status"]');
    expect(unsupportedState).not.toBeNull();
  });

  it("should redirect unknown routes to dashboard", async () => {
    render(<TestApp initialPath="/unknown-route" />);

    // Should redirect to dashboard
    expect(
      await screen.findByRole("heading", { name: /Dashboard/i }),
    ).toBeInTheDocument();
  });
});
