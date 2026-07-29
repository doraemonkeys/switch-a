import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { MemoryRouter } from "react-router";
import { ApiProvider } from "@/api/ApiContext";
import { Layout } from "./Layout";

const NAVIGATION_CASES = [
  { name: "Dashboard", href: "/", icon: "📊" },
  { name: "Providers", href: "/providers", icon: "🔌" },
  { name: "Groups", href: "/groups", icon: "📁" },
  { name: "Routing", href: "/routing", icon: "🧭" },
  { name: "Config", href: "/config", icon: "⚙️" },
  { name: "Logs", href: "/logs", icon: "📋" },
] as const;

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

  it("renders the logout action", () => {
    renderWithRouter();
    expect(screen.getByRole("button", { name: /Logout/i })).toBeInTheDocument();
  });
});
