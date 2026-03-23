import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { LogFilters } from "./LogFilters";
import type { LogFilter, Provider } from "../api/types";
import { PROVIDER_CREDENTIAL_TYPES } from "../config/constants";

// Helper to create mock providers
function createMockProviders(): Provider[] {
  return [
    {
      id: "provider-1",
      name: "Provider One",
      api_key: "key-1",
      api_types: [],
      auth_mode: "bearer",
      credential_type: PROVIDER_CREDENTIAL_TYPES.API_KEY,
      group_id: null,
      weight: 100,
      priority: 1,
      concurrency: 10,
      max_retries: 3,
      vendor: "",
      failover_scope: "any",
      accept_failover: "any",
      enabled: true,
      created_at: "2024-01-01T00:00:00Z",
      updated_at: "2024-01-01T00:00:00Z",
    },
    {
      id: "provider-2",
      name: "Provider Two",
      api_key: "key-2",
      api_types: [],
      auth_mode: "bearer",
      credential_type: PROVIDER_CREDENTIAL_TYPES.API_KEY,
      group_id: null,
      weight: 100,
      priority: 2,
      concurrency: 10,
      max_retries: 3,
      vendor: "",
      failover_scope: "any",
      accept_failover: "any",
      enabled: true,
      created_at: "2024-01-01T00:00:00Z",
      updated_at: "2024-01-01T00:00:00Z",
    },
  ];
}

// Shared test setup
const mockOnFilterChange = vi.fn();
const mockOnClear = vi.fn();
const mockProviders = createMockProviders();
const defaultFilter: LogFilter = {};

beforeEach(() => {
  vi.clearAllMocks();
});

describe("LogFilters - Basic Rendering", () => {
  it("renders all filter dropdowns", () => {
    render(
      <LogFilters
        filter={defaultFilter}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    expect(screen.getByText("Provider")).toBeInTheDocument();
    expect(screen.getByText("Status")).toBeInTheDocument();
    expect(screen.getByText("Request Type")).toBeInTheDocument();
    expect(screen.getByText("Commit State")).toBeInTheDocument();
    expect(screen.getByText("Sticky Write")).toBeInTheDocument();
    expect(screen.getByText("Terminal Cause")).toBeInTheDocument();
    expect(screen.getByText("API Type")).toBeInTheDocument();
    expect(screen.getByText("Time Range")).toBeInTheDocument();
  });

  it("renders provider options", () => {
    render(
      <LogFilters
        filter={defaultFilter}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    expect(screen.getByText("All Providers")).toBeInTheDocument();
    expect(screen.getByText("Provider One")).toBeInTheDocument();
    expect(screen.getByText("Provider Two")).toBeInTheDocument();
  });
});

describe("LogFilters - Provider Filter", () => {
  it("handles provider filter change", () => {
    render(
      <LogFilters
        filter={defaultFilter}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    const providerSelect = screen.getByDisplayValue("All Providers");
    fireEvent.change(providerSelect, { target: { value: "provider-1" } });

    expect(mockOnFilterChange).toHaveBeenCalledWith({
      provider_id: "provider-1",
    });
  });

  it("clears provider filter when All Providers is selected", () => {
    render(
      <LogFilters
        filter={{ provider_id: "provider-1" }}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    const providerSelect = screen.getByDisplayValue("Provider One");
    fireEvent.change(providerSelect, { target: { value: "" } });

    expect(mockOnFilterChange).toHaveBeenCalledWith({ provider_id: undefined });
  });
});

describe("LogFilters - Status Filter", () => {
  it("handles status filter change to Success", () => {
    render(
      <LogFilters
        filter={defaultFilter}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    const statusSelect = screen.getByDisplayValue("All Status");
    fireEvent.change(statusSelect, { target: { value: "true" } });

    expect(mockOnFilterChange).toHaveBeenCalledWith({ success: true });
  });

  it("handles status filter change to Failed", () => {
    render(
      <LogFilters
        filter={defaultFilter}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    const statusSelect = screen.getByDisplayValue("All Status");
    fireEvent.change(statusSelect, { target: { value: "false" } });

    expect(mockOnFilterChange).toHaveBeenCalledWith({ success: false });
  });

  it("clears status filter when All Status is selected", () => {
    render(
      <LogFilters
        filter={{ success: true }}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    const statusSelect = screen.getByDisplayValue("Success");
    fireEvent.change(statusSelect, { target: { value: "" } });

    expect(mockOnFilterChange).toHaveBeenCalledWith({ success: undefined });
  });
});

describe("LogFilters - Lifecycle Filters", () => {
  it("handles commit state filter changes", () => {
    render(
      <LogFilters
        filter={defaultFilter}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    const commitStateSelect = screen.getByRole("combobox", {
      name: /commit state/i,
    });
    fireEvent.change(commitStateSelect, { target: { value: "true" } });

    expect(mockOnFilterChange).toHaveBeenCalledWith({
      session_committed: true,
    });
  });

  it("handles sticky write filter changes", () => {
    render(
      <LogFilters
        filter={defaultFilter}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    const stickyWriteSelect = screen.getByRole("combobox", {
      name: /sticky write/i,
    });
    fireEvent.change(stickyWriteSelect, { target: { value: "false" } });

    expect(mockOnFilterChange).toHaveBeenCalledWith({
      sticky_written: false,
    });
  });

  it("handles terminal cause filter changes", () => {
    render(
      <LogFilters
        filter={defaultFilter}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    const terminalCauseSelect = screen.getByRole("combobox", {
      name: /terminal cause/i,
    });
    fireEvent.change(terminalCauseSelect, {
      target: { value: "upstream_semantic_error" },
    });

    expect(mockOnFilterChange).toHaveBeenCalledWith({
      terminal_cause: "upstream_semantic_error",
    });
  });
});

describe("LogFilters - API Type Filter", () => {
  it("handles API type filter change", () => {
    render(
      <LogFilters
        filter={defaultFilter}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    const apiTypeSelect = screen.getByRole("combobox", { name: /api type/i });
    fireEvent.change(apiTypeSelect, { target: { value: "claude" } });

    expect(mockOnFilterChange).toHaveBeenCalledWith({ api_type: "claude" });
  });

  it("clears API type filter when All Types is selected", () => {
    render(
      <LogFilters
        filter={{ api_type: "claude" }}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    const apiTypeSelect = screen.getByDisplayValue("claude");
    fireEvent.change(apiTypeSelect, { target: { value: "" } });

    expect(mockOnFilterChange).toHaveBeenCalledWith({ api_type: undefined });
  });
});

describe("LogFilters - Time Range Filter", () => {
  it("handles date preset change to Last 1 Hour", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2024-01-15T12:00:00Z"));

    render(
      <LogFilters
        filter={defaultFilter}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    const timeRangeSelect = screen.getByDisplayValue("All Time");
    fireEvent.change(timeRangeSelect, { target: { value: "1h" } });

    expect(mockOnFilterChange).toHaveBeenCalledWith({
      start_time: "2024-01-15T11:00:00.000Z",
      end_time: "2024-01-15T12:00:00.000Z",
    });

    vi.useRealTimers();
  });

  it("handles date preset change to Last 24 Hours", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2024-01-15T12:00:00Z"));

    render(
      <LogFilters
        filter={defaultFilter}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    const timeRangeSelect = screen.getByDisplayValue("All Time");
    fireEvent.change(timeRangeSelect, { target: { value: "24h" } });

    expect(mockOnFilterChange).toHaveBeenCalledWith({
      start_time: "2024-01-14T12:00:00.000Z",
      end_time: "2024-01-15T12:00:00.000Z",
    });

    vi.useRealTimers();
  });

  it("handles date preset change to Last 7 Days", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2024-01-15T12:00:00Z"));

    render(
      <LogFilters
        filter={defaultFilter}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    const timeRangeSelect = screen.getByDisplayValue("All Time");
    fireEvent.change(timeRangeSelect, { target: { value: "7d" } });

    expect(mockOnFilterChange).toHaveBeenCalledWith({
      start_time: "2024-01-08T12:00:00.000Z",
      end_time: "2024-01-15T12:00:00.000Z",
    });

    vi.useRealTimers();
  });

  it("handles date preset change to Last 30 Days", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2024-01-31T12:00:00Z"));

    render(
      <LogFilters
        filter={defaultFilter}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    const timeRangeSelect = screen.getByDisplayValue("All Time");
    fireEvent.change(timeRangeSelect, { target: { value: "30d" } });

    expect(mockOnFilterChange).toHaveBeenCalledWith({
      start_time: "2024-01-01T12:00:00.000Z",
      end_time: "2024-01-31T12:00:00.000Z",
    });

    vi.useRealTimers();
  });

  it("clears date filter when All Time is selected", () => {
    render(
      <LogFilters
        filter={{
          start_time: "2024-01-15T11:00:00Z",
          end_time: "2024-01-15T12:00:00Z",
        }}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    const timeRangeSelect = screen.getByRole("combobox", {
      name: /time range/i,
    });
    fireEvent.change(timeRangeSelect, { target: { value: "" } });

    expect(mockOnFilterChange).toHaveBeenCalledWith({
      start_time: undefined,
      end_time: undefined,
    });
  });
});

describe("LogFilters - Clear Filters", () => {
  it("does not show clear filters button when no filters are active", () => {
    render(
      <LogFilters
        filter={defaultFilter}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    expect(screen.queryByText("Clear Filters")).not.toBeInTheDocument();
  });

  it("shows clear filters button when filters are active", () => {
    render(
      <LogFilters
        filter={{ provider_id: "provider-1" }}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    expect(screen.getByText("Clear Filters")).toBeInTheDocument();
  });

  it("calls onClear when clear filters button is clicked", () => {
    render(
      <LogFilters
        filter={{ provider_id: "provider-1" }}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    fireEvent.click(screen.getByText("Clear Filters"));
    expect(mockOnClear).toHaveBeenCalledTimes(1);
  });

  it("shows active filters summary when filters are active", () => {
    render(
      <LogFilters
        filter={{ provider_id: "provider-1", success: true }}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    expect(screen.getByText("Active filters:")).toBeInTheDocument();
    expect(screen.getByText("Provider: Provider One")).toBeInTheDocument();
    expect(screen.getByText("Status: Success")).toBeInTheDocument();
  });

  it("shows api_type filter badge", () => {
    render(
      <LogFilters
        filter={{ api_type: "claude" }}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    expect(screen.getByText("Type: claude")).toBeInTheDocument();
  });

  it("shows lifecycle filter badges", () => {
    render(
      <LogFilters
        filter={{
          session_committed: true,
          sticky_written: false,
          terminal_cause: "client_disconnect",
        }}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    expect(screen.getByText("Commit: Committed")).toBeInTheDocument();
    expect(screen.getByText("Sticky Write: Not Written")).toBeInTheDocument();
    expect(
      screen.getByText("Terminal Cause: Client Disconnect"),
    ).toBeInTheDocument();
  });

  it("shows start_time filter badge with formatted date", () => {
    render(
      <LogFilters
        filter={{ start_time: "2024-01-15T12:00:00Z" }}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    expect(screen.getByText(/Since:/)).toBeInTheDocument();
  });
});

describe("LogFilters - Filter Badge Removal", () => {
  it("removes provider filter when badge remove button is clicked", () => {
    render(
      <LogFilters
        filter={{ provider_id: "provider-1" }}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    const removeButton = screen.getByLabelText(
      "Remove Provider: Provider One filter",
    );
    fireEvent.click(removeButton);

    expect(mockOnFilterChange).toHaveBeenCalledWith({ provider_id: undefined });
  });

  it("removes status filter when badge remove button is clicked", () => {
    render(
      <LogFilters
        filter={{ success: false }}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    const removeButton = screen.getByLabelText("Remove Status: Failed filter");
    fireEvent.click(removeButton);

    expect(mockOnFilterChange).toHaveBeenCalledWith({ success: undefined });
  });

  it("removes api_type filter when badge remove button is clicked", () => {
    render(
      <LogFilters
        filter={{ api_type: "gemini" }}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    const removeButton = screen.getByLabelText("Remove Type: gemini filter");
    fireEvent.click(removeButton);

    expect(mockOnFilterChange).toHaveBeenCalledWith({ api_type: undefined });
  });

  it("removes lifecycle filters when their badge buttons are clicked", () => {
    render(
      <LogFilters
        filter={{
          session_committed: false,
          sticky_written: true,
          terminal_cause: "clean_close",
        }}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    fireEvent.click(screen.getByLabelText("Remove Commit: Uncommitted filter"));
    fireEvent.click(
      screen.getByLabelText("Remove Sticky Write: Written filter"),
    );
    fireEvent.click(
      screen.getByLabelText("Remove Terminal Cause: Clean Close filter"),
    );

    expect(mockOnFilterChange).toHaveBeenNthCalledWith(1, {
      session_committed: undefined,
    });
    expect(mockOnFilterChange).toHaveBeenNthCalledWith(2, {
      sticky_written: undefined,
    });
    expect(mockOnFilterChange).toHaveBeenNthCalledWith(3, {
      terminal_cause: undefined,
    });
  });

  it("removes time filter when badge remove button is clicked", () => {
    render(
      <LogFilters
        filter={{
          start_time: "2024-01-15T12:00:00Z",
          end_time: "2024-01-15T13:00:00Z",
        }}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    const removeButton = screen.getByLabelText(/Remove Since:/);
    fireEvent.click(removeButton);

    expect(mockOnFilterChange).toHaveBeenCalledWith({
      start_time: undefined,
      end_time: undefined,
    });
  });

  it("shows provider_id as fallback when provider name is not found", () => {
    render(
      <LogFilters
        filter={{ provider_id: "unknown-provider" }}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    expect(screen.getByText("Provider: unknown-provider")).toBeInTheDocument();
  });
});

describe("LogFilters - Date Preset Detection", () => {
  it("detects 1h date preset correctly", () => {
    vi.useFakeTimers();
    const now = new Date("2024-01-15T12:00:00Z");
    vi.setSystemTime(now);

    const startTime = new Date(now.getTime() - 60 * 60 * 1000); // 1 hour ago

    render(
      <LogFilters
        filter={{ start_time: startTime.toISOString() }}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    const timeRangeSelect = screen.getByRole("combobox", {
      name: /time range/i,
    });
    expect(timeRangeSelect).toHaveValue("1h");

    vi.useRealTimers();
  });

  it("detects 24h date preset correctly", () => {
    vi.useFakeTimers();
    const now = new Date("2024-01-15T12:00:00Z");
    vi.setSystemTime(now);

    const startTime = new Date(now.getTime() - 24 * 60 * 60 * 1000); // 24 hours ago

    render(
      <LogFilters
        filter={{ start_time: startTime.toISOString() }}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    const timeRangeSelect = screen.getByRole("combobox", {
      name: /time range/i,
    });
    expect(timeRangeSelect).toHaveValue("24h");

    vi.useRealTimers();
  });

  it("detects 7d date preset correctly", () => {
    vi.useFakeTimers();
    const now = new Date("2024-01-15T12:00:00Z");
    vi.setSystemTime(now);

    const startTime = new Date(now.getTime() - 7 * 24 * 60 * 60 * 1000); // 7 days ago

    render(
      <LogFilters
        filter={{ start_time: startTime.toISOString() }}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    const timeRangeSelect = screen.getByRole("combobox", {
      name: /time range/i,
    });
    expect(timeRangeSelect).toHaveValue("7d");

    vi.useRealTimers();
  });

  it("detects 30d date preset correctly", () => {
    vi.useFakeTimers();
    const now = new Date("2024-01-15T12:00:00Z");
    vi.setSystemTime(now);

    const startTime = new Date(now.getTime() - 30 * 24 * 60 * 60 * 1000); // 30 days ago

    render(
      <LogFilters
        filter={{ start_time: startTime.toISOString() }}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    const timeRangeSelect = screen.getByRole("combobox", {
      name: /time range/i,
    });
    expect(timeRangeSelect).toHaveValue("30d");

    vi.useRealTimers();
  });

  it("returns empty preset for custom time ranges", () => {
    vi.useFakeTimers();
    const now = new Date("2024-01-15T12:00:00Z");
    vi.setSystemTime(now);

    // 60 days ago - beyond any preset
    const startTime = new Date(now.getTime() - 60 * 24 * 60 * 60 * 1000);

    render(
      <LogFilters
        filter={{ start_time: startTime.toISOString() }}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    const timeRangeSelect = screen.getByRole("combobox", {
      name: /time range/i,
    });
    expect(timeRangeSelect).toHaveValue("");

    vi.useRealTimers();
  });

  it("returns empty preset when start_time is not set", () => {
    render(
      <LogFilters
        filter={{}}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    const timeRangeSelect = screen.getByRole("combobox", {
      name: /time range/i,
    });
    expect(timeRangeSelect).toHaveValue("");
  });

  it("handles invalid date preset gracefully by returning early", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2024-01-15T12:00:00Z"));

    render(
      <LogFilters
        filter={defaultFilter}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    // First set a valid preset
    const timeRangeSelect = screen.getByRole("combobox", {
      name: /time range/i,
    });
    fireEvent.change(timeRangeSelect, { target: { value: "1h" } });
    expect(mockOnFilterChange).toHaveBeenCalledTimes(1);

    mockOnFilterChange.mockClear();

    // Now simulate selecting All Time (empty value)
    fireEvent.change(timeRangeSelect, { target: { value: "" } });
    expect(mockOnFilterChange).toHaveBeenCalledWith({
      start_time: undefined,
      end_time: undefined,
    });

    vi.useRealTimers();
  });

  it("considers end_time as active filter indicator", () => {
    render(
      <LogFilters
        filter={{ end_time: "2024-01-15T12:00:00Z" }}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    expect(screen.getByText("Clear Filters")).toBeInTheDocument();
  });

  it("considers lifecycle filters as active filter indicators", () => {
    render(
      <LogFilters
        filter={{ terminal_cause: "unknown" }}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    expect(screen.getByText("Clear Filters")).toBeInTheDocument();
  });
});
