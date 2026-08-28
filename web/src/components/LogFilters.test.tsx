import {
  fireEvent,
  render as testingLibraryRender,
  screen,
} from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactElement, ReactNode } from "react";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import type { LogFilter, Provider } from "../api/types";
import { parseAPICatalog } from "../api/api-catalog";
import { APICatalogContext } from "../api/context";
import { LogFilters } from "./LogFilters";

const testAPICatalog = parseAPICatalog(
  JSON.parse(
    readFileSync(
      resolve(process.cwd(), "../contracts/internal-error/v1/api-catalog.json"),
      "utf8",
    ),
  ) as unknown,
);

function APICatalogTestProvider({ children }: { children: ReactNode }) {
  return (
    <APICatalogContext.Provider
      value={{
        catalog: testAPICatalog,
        loading: false,
        error: null,
        refetch: () => Promise.resolve(),
      }}
    >
      {children}
    </APICatalogContext.Provider>
  );
}

function render(element: ReactElement) {
  return testingLibraryRender(element, { wrapper: APICatalogTestProvider });
}

function createMockProviders(): Provider[] {
  return [
    {
      id: "provider-1",
      name: "Provider One",
      api_types: [],
      auth_mode: "bearer",
      credential_sessions: [],
      group_id: null,
      weight: 100,
      priority: 1,
      concurrency: 10,
      max_retries: 3,
      vendor: "",
      failover_scope: "any",
      accept_failover: "any",
      enabled: true,
      created_at: "2026-04-01T00:00:00Z",
      updated_at: "2026-04-01T00:00:00Z",
    },
  ];
}

const mockOnFilterChange = vi.fn();
const mockOnClear = vi.fn();
const mockProviders = createMockProviders();

function renderFilters(filter: LogFilter = {}) {
  return render(
    <LogFilters
      filter={filter}
      onFilterChange={mockOnFilterChange}
      providers={mockProviders}
      onClear={mockOnClear}
    />,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("LogFilters", () => {
  it("renders normalized semantic filter controls", () => {
    renderFilters();

    expect(screen.getByText("Semantics")).toBeInTheDocument();
    expect(screen.getByText("Service Outcome")).toBeInTheDocument();
    expect(screen.getByText("Client Action")).toBeInTheDocument();
    expect(screen.getByText("Termination Reason")).toBeInTheDocument();
    expect(screen.getByText("Termination Actor")).toBeInTheDocument();
    expect(screen.getByText("Completion State")).toBeInTheDocument();
    expect(screen.getByText("Transport Code")).toBeInTheDocument();
  });

  it("updates normalized semantic filters", () => {
    renderFilters();

    fireEvent.change(screen.getByRole("combobox", { name: /semantics/i }), {
      target: { value: "legacy_pre_assessment" },
    });
    fireEvent.change(
      screen.getByRole("combobox", { name: /service outcome/i }),
      {
        target: { value: "interrupted" },
      },
    );
    fireEvent.change(screen.getByRole("combobox", { name: /client action/i }), {
      target: { value: "reconnect_required" },
    });

    expect(mockOnFilterChange).toHaveBeenNthCalledWith(1, {
      semantics_version: "legacy_pre_assessment",
    });
    expect(mockOnFilterChange).toHaveBeenNthCalledWith(2, {
      service_outcome: "interrupted",
    });
    expect(mockOnFilterChange).toHaveBeenNthCalledWith(3, {
      client_action: "reconnect_required",
    });
  });

  it("updates transport code filter from numeric input", () => {
    renderFilters();

    fireEvent.change(screen.getByLabelText(/transport code/i), {
      target: { value: "101" },
    });

    expect(mockOnFilterChange).toHaveBeenCalledWith({
      client_transport_status_code: 101,
    });
  });

  it("keeps request type controls coupled for regular requests", () => {
    renderFilters();

    fireEvent.change(screen.getByRole("combobox", { name: /request type/i }), {
      target: { value: "regular" },
    });

    expect(mockOnFilterChange).toHaveBeenCalledWith({
      is_sse: false,
      is_websocket: false,
    });
  });

  it("accepts custom API type filters", () => {
    renderFilters();

    fireEvent.change(screen.getByLabelText(/api type/i), {
      target: { value: "custom:mytool" },
    });

    expect(mockOnFilterChange).toHaveBeenCalledWith({
      api_type: "custom:mytool",
    });
  });

  it("shows active normalized filters and supports badge removal", () => {
    renderFilters({
      semantics_version: "legacy_pre_assessment",
      service_outcome: "unknown",
      termination_reason: "transport_error",
      client_transport_status_code: 101,
    });

    expect(screen.getByText("Active filters:")).toBeInTheDocument();
    expect(screen.getByText("Semantics: Legacy")).toBeInTheDocument();
    expect(screen.getByText("Service Outcome: Unknown")).toBeInTheDocument();
    expect(
      screen.getByText("Termination Reason: Transport Error"),
    ).toBeInTheDocument();
    expect(screen.getByText("Transport Code: 101")).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText("Remove Semantics: Legacy filter"));
    expect(mockOnFilterChange).toHaveBeenCalledWith({
      semantics_version: undefined,
    });
  });

  it("shows clear filters button only when filters are active", () => {
    const { rerender } = renderFilters();

    expect(screen.queryByText("Clear Filters")).not.toBeInTheDocument();

    rerender(
      <LogFilters
        filter={{ service_outcome: "completed" }}
        onFilterChange={mockOnFilterChange}
        providers={mockProviders}
        onClear={mockOnClear}
      />,
    );

    expect(screen.getByText("Clear Filters")).toBeInTheDocument();
    fireEvent.click(screen.getByText("Clear Filters"));
    expect(mockOnClear).toHaveBeenCalledTimes(1);
  });
});
