import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { Layout } from "./Layout";
import { ApiProvider } from "@/api/ApiContext";

// Wrapper to provide routing and API context
function renderWithRouter(initialPath = "/") {
  return render(
    <ApiProvider>
      <MemoryRouter initialEntries={[initialPath]}>
        <Layout />
      </MemoryRouter>
    </ApiProvider>,
  );
}

describe("Layout", () => {
  it("should render the header with app title", () => {
    renderWithRouter();

    expect(screen.getByText("Switch-A")).toBeInTheDocument();
    expect(screen.getByText("AI Provider Proxy")).toBeInTheDocument();
  });

  it("should render online status indicator", () => {
    renderWithRouter();

    expect(screen.getByText("Online")).toBeInTheDocument();
  });

  it("should render all navigation items", () => {
    renderWithRouter();

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

  it("should render version info", () => {
    renderWithRouter();

    expect(screen.getByText("Version 0.1.0")).toBeInTheDocument();
  });

  it("should have correct navigation links", () => {
    renderWithRouter();

    const dashboardLink = screen.getByRole("link", { name: /Dashboard/i });
    const providersLink = screen.getByRole("link", { name: /Providers/i });
    const groupsLink = screen.getByRole("link", { name: /Groups/i });
    const configLink = screen.getByRole("link", { name: /Config/i });
    const logsLink = screen.getByRole("link", { name: /Logs/i });

    expect(dashboardLink).toHaveAttribute("href", "/");
    expect(providersLink).toHaveAttribute("href", "/providers");
    expect(groupsLink).toHaveAttribute("href", "/groups");
    expect(configLink).toHaveAttribute("href", "/config");
    expect(logsLink).toHaveAttribute("href", "/logs");
  });

  it("should display navigation icons", () => {
    renderWithRouter();

    // Check for emoji icons in navigation
    expect(screen.getByText("📊")).toBeInTheDocument();
    expect(screen.getByText("🔌")).toBeInTheDocument();
    expect(screen.getByText("📁")).toBeInTheDocument();
    expect(screen.getByText("⚙️")).toBeInTheDocument();
    expect(screen.getByText("📋")).toBeInTheDocument();
  });

  it("should display header icon", () => {
    renderWithRouter();

    expect(screen.getByText("⚡")).toBeInTheDocument();
  });

  it("should render logout button", () => {
    renderWithRouter();
    expect(screen.getByRole("button", { name: /Logout/i })).toBeInTheDocument();
  });
});
