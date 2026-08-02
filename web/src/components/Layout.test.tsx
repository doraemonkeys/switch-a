import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router";
import { ApiProvider } from "@/api/ApiContext";
import type { DebugCaptureStatus } from "@/api";
import { DebugCaptureContext } from "@/features/debug-capture/context";
import { Layout } from "./Layout";

const NAVIGATION_CASES = [
  { name: "Dashboard", href: "/", icon: "📊" },
  { name: "Monitor", href: "/monitor", icon: "📡" },
  { name: "Providers", href: "/providers", icon: "🔌" },
  { name: "Groups", href: "/groups", icon: "📁" },
  { name: "Routing", href: "/routing", icon: "🧭" },
  { name: "Config", href: "/config", icon: "⚙️" },
  { name: "Logs", href: "/logs", icon: "📋" },
  { name: "Debug Capture", href: "/debug-capture", icon: "🐞" },
] as const;

const stoppedStatus: DebugCaptureStatus = {
  state: "stopped",
  process_memory: {
    ceiling_bytes: 512 * 1_024 * 1_024,
    charged_bytes: 0,
    retained_bytes: 0,
    pinned_bytes: 0,
    releasing_bytes: 0,
    temporary_bytes: 0,
  },
  pending_export_count: 0,
  active_download_count: 0,
  session: null,
};

function renderWithRouter(status: DebugCaptureStatus = stoppedStatus) {
  return render(
    <ApiProvider>
      <DebugCaptureContext.Provider
        value={{
          status,
          loading: false,
          error: null,
          operation: null,
          refreshStatus: vi.fn(),
          startCapture: vi.fn(),
          stopCapture: vi.fn(),
        }}
      >
        <MemoryRouter>
          <Layout />
        </MemoryRouter>
      </DebugCaptureContext.Provider>
    </ApiProvider>,
  );
}

describe("Layout", () => {
  it.each(["Switch-A", "AI Provider Proxy", "Online", "Version 0.1.0", "⚡"])(
    "renders static shell content: %s",
    (text) => {
      renderWithRouter();
      expect(screen.getByText(text)).toBeInTheDocument();
    },
  );

  it.each(NAVIGATION_CASES)(
    "renders $name navigation with its route and icon",
    ({ name, href, icon }) => {
      renderWithRouter();

      expect(
        screen.getByRole("link", { name: new RegExp(name, "i") }),
      ).toHaveAttribute("href", href);
      expect(screen.getByText(icon)).toBeInTheDocument();
    },
  );

  it("shows the global badge only while capture is active", () => {
    const { rerender } = renderWithRouter();
    expect(screen.queryByText("Active")).not.toBeInTheDocument();

    rerender(
      <ApiProvider>
        <DebugCaptureContext.Provider
          value={{
            status: {
              ...stoppedStatus,
              state: "active",
              session: {
                session_id: "session-active",
                generation: 1,
                started_at: "2026-08-01T00:00:00Z",
                providers: [{ id: "provider-a", name: "Provider A" }],
                provider_ids: ["provider-a"],
                completed_records_per_provider: 10,
                retained_bytes_limit: 256 * 1_024 * 1_024,
                retained_bytes: 0,
                active_record_count: 0,
                completed_record_count: 0,
                gateway_trace_count: 0,
                evicted_record_count: 0,
                overflowed_record_count: 0,
                history_truncated_trace_count: 0,
                dropped_trace_count: 0,
                dropped_exchange_count: 0,
                dropped_transition_count: 0,
              },
            },
            loading: false,
            error: null,
            operation: null,
            refreshStatus: vi.fn(),
            startCapture: vi.fn(),
            stopCapture: vi.fn(),
          }}
        >
          <MemoryRouter>
            <Layout />
          </MemoryRouter>
        </DebugCaptureContext.Provider>
      </ApiProvider>,
    );

    expect(screen.getByText("Active")).toBeInTheDocument();
  });

  it("renders the logout action", () => {
    renderWithRouter();
    expect(screen.getByRole("button", { name: /Logout/i })).toBeInTheDocument();
  });
});
