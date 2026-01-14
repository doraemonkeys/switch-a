import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { Monitor } from "./Monitor";
import type { ActiveRequest, Provider, SystemStatus } from "../api/types";

// Mock data
// eslint-disable-next-line sonarjs/no-hardcoded-ip -- safe for testing
const TEST_CLIENT_IP = "192.168.1.1";

const mockActiveRequests: ActiveRequest[] = [
  {
    request_id: "req-1",
    provider_id: "provider-1",
    model: "claude-3-opus",
    api_type: "claude",
    user_id: "user-1",
    client_ip: TEST_CLIENT_IP,
    is_sse: false,
    started_at: new Date().toISOString(),
  },
  {
    request_id: "req-2",
    provider_id: "provider-2",
    model: "gpt-4",
    api_type: "codex",
    user_id: "user-2",
    client_ip: TEST_CLIENT_IP,
    is_sse: true,
    started_at: new Date().toISOString(),
  },
];

const mockProviders: Provider[] = [
  {
    id: "provider-1",
    name: "Anthropic",
    base_url: "https://api.anthropic.com",
    api_key: "key-1",
    api_types: [{ provider_id: "provider-1", api_type: "claude" }],
    auth_mode: "bearer",
    group_id: null,
    weight: 1,
    priority: 1,
    concurrency: 10,
    max_retries: 3,
    enabled: true,
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
  },
  {
    id: "provider-2",
    name: "OpenAI",
    base_url: "https://api.openai.com",
    api_key: "key-2",
    api_types: [{ provider_id: "provider-2", api_type: "codex" }],
    auth_mode: "bearer",
    group_id: null,
    weight: 1,
    priority: 1,
    concurrency: 10,
    max_retries: 3,
    enabled: true,
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
  },
];

const mockStatus: SystemStatus = {
  providers: [
    {
      id: "provider-1",
      name: "Anthropic",
      enabled: true,
      current_requests: 1,
      health: {
        provider_id: "provider-1",
        available: true,
        success_count: 100,
        fail_count: 5,
        last_success: new Date().toISOString(),
        last_failure: null,
        last_error: null,
        disabled_until: null,
        disabled_reason: null,
      },
    },
    {
      id: "provider-2",
      name: "OpenAI",
      enabled: true,
      current_requests: 0,
      health: {
        provider_id: "provider-2",
        available: false,
        success_count: 50,
        fail_count: 10,
        last_success: null,
        last_failure: new Date().toISOString(),
        last_error: "Rate limited",
        disabled_until: null,
        disabled_reason: null,
      },
    },
    {
      id: "provider-3",
      name: "Disabled Provider",
      enabled: false,
      current_requests: 0,
      health: null,
    },
  ],
};

// Mock hook returns
const mockUseLiveRequests: {
  requests: ActiveRequest[];
  count: number;
  loading: boolean;
  error: Error | null;
  refetch: ReturnType<typeof vi.fn>;
  isPolling: boolean;
} = {
  requests: mockActiveRequests,
  count: 2,
  loading: false,
  error: null,
  refetch: vi.fn(),
  isPolling: true,
};

const mockUseStatus: {
  status: SystemStatus | null;
  summary: null;
  loading: boolean;
  error: Error | null;
  refetch: ReturnType<typeof vi.fn>;
} = {
  status: mockStatus,
  summary: null,
  loading: false,
  error: null,
  refetch: vi.fn(),
};

const mockUseProviders = {
  providers: mockProviders,
  loading: false,
  error: null,
  refetch: vi.fn(),
  createProvider: vi.fn(),
  updateProvider: vi.fn(),
  deleteProvider: vi.fn(),
  enableProvider: vi.fn(),
  disableProvider: vi.fn(),
  resetProvider: vi.fn(),
};

// Mock the hooks
vi.mock("../hooks", () => ({
  useLiveRequests: () => mockUseLiveRequests,
  useStatus: () => mockUseStatus,
  useProviders: () => mockUseProviders,
}));

describe("Monitor", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2024-01-15T12:00:00.000Z"));

    // Reset mock data to defaults
    mockUseLiveRequests.requests = mockActiveRequests;
    mockUseLiveRequests.count = 2;
    mockUseLiveRequests.loading = false;
    mockUseLiveRequests.error = null;
    mockUseLiveRequests.isPolling = true;

    mockUseStatus.status = mockStatus;
    mockUseStatus.loading = false;
    mockUseStatus.error = null;

    mockUseProviders.providers = mockProviders;
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  describe("page header", () => {
    it("renders page title and description", () => {
      render(<Monitor />);

      expect(screen.getByText("Monitor")).toBeInTheDocument();
      expect(
        screen.getByText("Live request monitoring and system status"),
      ).toBeInTheDocument();
    });

    it("shows auto-refreshing indicator when polling", () => {
      mockUseLiveRequests.isPolling = true;
      render(<Monitor />);

      expect(screen.getByText("Auto-refreshing")).toBeInTheDocument();
    });

    it("hides auto-refreshing indicator when not polling", () => {
      mockUseLiveRequests.isPolling = false;
      render(<Monitor />);

      expect(screen.queryByText("Auto-refreshing")).not.toBeInTheDocument();
    });
  });

  describe("refresh button", () => {
    it("calls refetch functions when clicked", () => {
      render(<Monitor />);

      const refreshButton = screen.getByRole("button", { name: /Refresh/i });
      fireEvent.click(refreshButton);

      expect(mockUseLiveRequests.refetch).toHaveBeenCalled();
      expect(mockUseStatus.refetch).toHaveBeenCalled();
    });

    it("disables button when loading", () => {
      mockUseLiveRequests.loading = true;
      render(<Monitor />);

      const refreshButton = screen.getByRole("button", { name: /Refresh/i });
      expect(refreshButton).toBeDisabled();
    });

    it("disables button when status is loading", () => {
      mockUseStatus.loading = true;
      render(<Monitor />);

      const refreshButton = screen.getByRole("button", { name: /Refresh/i });
      expect(refreshButton).toBeDisabled();
    });
  });

  describe("stats cards", () => {
    it("displays active requests count", () => {
      render(<Monitor />);

      // Find the Active Requests card by its label and verify count
      const activeRequestsLabel = screen.getByText("Active Requests");
      const card = activeRequestsLabel.closest(".card");
      expect(card).toBeInTheDocument();
      // The count should be in a sibling element
      expect(card?.querySelector(".text-3xl")).toHaveTextContent("2");
    });

    it("displays healthy providers count", () => {
      render(<Monitor />);

      // Provider 1 is healthy (enabled + available=true)
      // Provider 2 is unhealthy (enabled but available=false)
      // Provider 3 is disabled
      // So healthy = 1
      expect(screen.getByText("1")).toBeInTheDocument();
      expect(screen.getByText("Healthy Providers")).toBeInTheDocument();
    });

    it("displays unhealthy providers count", () => {
      render(<Monitor />);

      // Total 3 providers - 1 healthy = 2 unhealthy (includes disabled)
      expect(screen.getByText("Unhealthy Providers")).toBeInTheDocument();
    });

    it("shows zero counts when no status data", () => {
      mockUseStatus.status = null;
      render(<Monitor />);

      // When status is null, provider stats should be zero
      // Find the Healthy Providers card and verify count is 0
      const healthyLabel = screen.getByText("Healthy Providers");
      const healthyCard = healthyLabel.closest(".card");
      expect(healthyCard).toBeInTheDocument();
      expect(healthyCard?.querySelector(".text-3xl")).toHaveTextContent("0");

      // Find the Unhealthy Providers card and verify count is 0
      const unhealthyLabel = screen.getByText("Unhealthy Providers");
      const unhealthyCard = unhealthyLabel.closest(".card");
      expect(unhealthyCard).toBeInTheDocument();
      expect(unhealthyCard?.querySelector(".text-3xl")).toHaveTextContent("0");
    });
  });

  describe("live requests panel", () => {
    it("renders Live Requests section header", () => {
      render(<Monitor />);

      expect(screen.getByText("Live Requests")).toBeInTheDocument();
    });

    it("displays active count badge", () => {
      render(<Monitor />);

      expect(screen.getByText("2 active")).toBeInTheDocument();
    });

    it("passes provider names to LiveRequestsPanel", () => {
      render(<Monitor />);

      // Provider names appear in both LiveRequestsPanel and Provider Status sections
      // Just verify that they appear at least once (getAllByText to handle duplicates)
      expect(screen.getAllByText("Anthropic").length).toBeGreaterThan(0);
      expect(screen.getAllByText("OpenAI").length).toBeGreaterThan(0);
    });
  });

  describe("provider status panel", () => {
    it("renders Provider Status section header", () => {
      render(<Monitor />);

      expect(screen.getByText("Provider Status")).toBeInTheDocument();
    });

    it("displays provider names from status", () => {
      render(<Monitor />);

      // All provider names should be visible in provider status list
      const anthropicElements = screen.getAllByText("Anthropic");
      expect(anthropicElements.length).toBeGreaterThan(0);
    });

    it("shows current requests badge when provider has requests", () => {
      render(<Monitor />);

      expect(screen.getByText("1 req")).toBeInTheDocument();
    });

    it("shows error message when status fetch fails", () => {
      mockUseStatus.error = new Error("Network error");
      mockUseStatus.status = null;
      render(<Monitor />);

      expect(screen.getByText(/Failed to load status/)).toBeInTheDocument();
      expect(screen.getByText(/Network error/)).toBeInTheDocument();
    });

    it("shows loading spinner when status is loading", () => {
      mockUseStatus.loading = true;
      mockUseStatus.status = null;
      render(<Monitor />);

      // There should be a loading spinner in the provider status section
      const spinners = document.querySelectorAll(".animate-spin");
      expect(spinners.length).toBeGreaterThan(0);
    });

    it("shows no providers message when list is empty", () => {
      mockUseStatus.status = { providers: [] };
      render(<Monitor />);

      expect(screen.getByText("No providers configured")).toBeInTheDocument();
    });
  });

  describe("StatusDot component", () => {
    it("shows green dot for enabled and available providers", () => {
      render(<Monitor />);

      // Anthropic is enabled and available
      const providerStatusSection = screen
        .getByText("Provider Status")
        .closest("div")?.parentElement;
      const greenDots =
        providerStatusSection?.querySelectorAll(".bg-green-500");
      expect(greenDots?.length).toBeGreaterThan(0);
    });

    it("shows red dot for enabled but unavailable providers", () => {
      render(<Monitor />);

      // OpenAI is enabled but not available
      const providerStatusSection = screen
        .getByText("Provider Status")
        .closest("div")?.parentElement;
      const redDots = providerStatusSection?.querySelectorAll(".bg-red-500");
      expect(redDots?.length).toBeGreaterThan(0);
    });

    it("shows gray dot for disabled providers", () => {
      render(<Monitor />);

      // Provider 3 is disabled
      const providerStatusSection = screen
        .getByText("Provider Status")
        .closest("div")?.parentElement;
      const grayDots = providerStatusSection?.querySelectorAll(".bg-gray-400");
      expect(grayDots?.length).toBeGreaterThan(0);
    });

    it("has title attribute for healthy providers", () => {
      render(<Monitor />);

      expect(document.querySelector('[title="Healthy"]')).toBeInTheDocument();
    });

    it("has title attribute for unhealthy providers", () => {
      render(<Monitor />);

      expect(document.querySelector('[title="Unhealthy"]')).toBeInTheDocument();
    });

    it("has title attribute for disabled providers", () => {
      render(<Monitor />);

      expect(document.querySelector('[title="Disabled"]')).toBeInTheDocument();
    });
  });

  describe("provider name resolution", () => {
    it("uses providers from useProviders to build name map", () => {
      render(<Monitor />);

      // The LiveRequestsPanel should receive provider names
      // which are built from useProviders
      const anthropicText = screen.getAllByText("Anthropic");
      expect(anthropicText.length).toBeGreaterThanOrEqual(1);
    });
  });

  describe("error handling", () => {
    it("handles requests error gracefully", () => {
      mockUseLiveRequests.error = new Error("Failed to fetch");
      mockUseLiveRequests.requests = [];
      render(<Monitor />);

      expect(
        screen.getByText(/Failed to load active requests/),
      ).toBeInTheDocument();
    });
  });
});
