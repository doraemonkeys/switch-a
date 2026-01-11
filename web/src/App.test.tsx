import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Routes, Route, Navigate } from "react-router-dom";
import { Layout } from "@/components/Layout";
import { Dashboard } from "@/pages/Dashboard";
import { Providers } from "@/pages/Providers";
import { Groups } from "@/pages/Groups";
import { Config } from "@/pages/Config";
import { Logs } from "@/pages/Logs";

// Test component that replicates App routing without BrowserRouter basename issues
function TestApp({ initialPath = "/" }: { initialPath?: string }) {
  return (
    <MemoryRouter initialEntries={[initialPath]}>
      <Routes>
        <Route path="/" element={<Layout />}>
          <Route index element={<Dashboard />} />
          <Route path="providers" element={<Providers />} />
          <Route path="groups" element={<Groups />} />
          <Route path="config" element={<Config />} />
          <Route path="logs" element={<Logs />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Route>
      </Routes>
    </MemoryRouter>
  );
}

describe("App", () => {
  it("should render the layout with navigation", () => {
    render(<TestApp />);

    // Check for main app title
    expect(screen.getByText("Switch-A")).toBeInTheDocument();
    expect(screen.getByText("AI Provider Proxy")).toBeInTheDocument();
  });

  it("should render dashboard by default", () => {
    render(<TestApp />);

    // Dashboard should be the default route
    expect(
      screen.getByRole("heading", { name: /Dashboard/i }),
    ).toBeInTheDocument();
  });

  it("should render all navigation links", () => {
    render(<TestApp />);

    expect(
      screen.getByRole("link", { name: /Dashboard/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: /Providers/i }),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Groups/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Config/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Logs/i })).toBeInTheDocument();
  });

  it("should navigate to providers page", () => {
    render(<TestApp initialPath="/providers" />);

    expect(
      screen.getByRole("heading", { name: /Providers/i }),
    ).toBeInTheDocument();
  });

  it("should navigate to groups page", () => {
    render(<TestApp initialPath="/groups" />);

    expect(
      screen.getByRole("heading", { name: /Groups/i }),
    ).toBeInTheDocument();
  });

  it("should navigate to config page", () => {
    render(<TestApp initialPath="/config" />);

    expect(
      screen.getByRole("heading", { name: /Configuration/i }),
    ).toBeInTheDocument();
  });

  it("should navigate to logs page", () => {
    render(<TestApp initialPath="/logs" />);

    expect(
      screen.getByRole("heading", { name: /Request Logs/i }),
    ).toBeInTheDocument();
  });

  it("should redirect unknown routes to dashboard", () => {
    render(<TestApp initialPath="/unknown-route" />);

    // Should redirect to dashboard
    expect(
      screen.getByRole("heading", { name: /Dashboard/i }),
    ).toBeInTheDocument();
  });
});
