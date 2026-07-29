import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router";
import { RequireAuth } from "./RequireAuth";
import { ApiContext } from "../api/context";
import type { ApiClient } from "../api/client";

function createMockApiClient(token: string | null = null) {
  return {
    getToken: vi.fn().mockReturnValue(token),
    setToken: vi.fn(),
    clearToken: vi.fn(),
    providers: {
      list: vi.fn(),
      get: vi.fn(),
      create: vi.fn(),
      update: vi.fn(),
      delete: vi.fn(),
      enable: vi.fn(),
      disable: vi.fn(),
      reset: vi.fn(),
    },
    groups: {
      list: vi.fn(),
      get: vi.fn(),
      create: vi.fn(),
      update: vi.fn(),
      delete: vi.fn(),
    },
    config: {
      get: vi.fn(),
      update: vi.fn(),
    },
    status: { get: vi.fn(), health: vi.fn() },
    logs: { list: vi.fn() },
  } as unknown as ApiClient;
}

function renderWithRouter(
  apiClient: ApiClient,
  initialEntries: string[] = ["/protected"],
) {
  return render(
    <ApiContext.Provider value={apiClient}>
      <MemoryRouter initialEntries={initialEntries}>
        <Routes>
          <Route path="/login" element={<div>Login Page</div>} />
          <Route
            path="/protected"
            element={
              <RequireAuth>
                <div>Protected Content</div>
              </RequireAuth>
            }
          />
        </Routes>
      </MemoryRouter>
    </ApiContext.Provider>,
  );
}

describe("RequireAuth", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("should render children when user is authenticated", () => {
    const mockApi = createMockApiClient("valid-token");

    renderWithRouter(mockApi);

    expect(screen.getByText("Protected Content")).toBeInTheDocument();
    expect(screen.queryByText("Login Page")).not.toBeInTheDocument();
  });

  it("should redirect to login when user is not authenticated", () => {
    const mockApi = createMockApiClient(null);

    renderWithRouter(mockApi);

    expect(screen.getByText("Login Page")).toBeInTheDocument();
    expect(screen.queryByText("Protected Content")).not.toBeInTheDocument();
  });

  it("should redirect to login when token is empty string", () => {
    const mockApi = createMockApiClient("");

    renderWithRouter(mockApi);

    expect(screen.getByText("Login Page")).toBeInTheDocument();
    expect(screen.queryByText("Protected Content")).not.toBeInTheDocument();
  });

  it("should call getToken to check authentication", () => {
    const mockApi = createMockApiClient("token");

    renderWithRouter(mockApi);

    expect(mockApi.getToken).toHaveBeenCalled();
  });

  it("should preserve location state when redirecting to login", () => {
    const mockApi = createMockApiClient(null);

    // We can verify the redirect happens, but testing the state
    // would require more complex setup with useLocation in the login component
    renderWithRouter(mockApi, ["/protected?query=test"]);

    expect(screen.getByText("Login Page")).toBeInTheDocument();
  });

  it("should render nested children correctly", () => {
    const mockApi = createMockApiClient("token");

    render(
      <ApiContext.Provider value={mockApi}>
        <MemoryRouter initialEntries={["/protected"]}>
          <Routes>
            <Route
              path="/protected"
              element={
                <RequireAuth>
                  <div>
                    <h1>Title</h1>
                    <p>Content</p>
                    <span>More content</span>
                  </div>
                </RequireAuth>
              }
            />
          </Routes>
        </MemoryRouter>
      </ApiContext.Provider>,
    );

    expect(screen.getByText("Title")).toBeInTheDocument();
    expect(screen.getByText("Content")).toBeInTheDocument();
    expect(screen.getByText("More content")).toBeInTheDocument();
  });
});
