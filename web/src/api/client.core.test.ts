import { describe, it, expect, vi, beforeEach } from "vitest";
import { ApiError, createTokenManager, createApiClient } from "./client";
import type { ApiClientDeps } from "./interfaces";
import { createMockStorage, createMockHttpClient } from "./test-mocks";

describe("ApiError", () => {
  it("should create an error with code, message, and status", () => {
    const error = new ApiError("AUTH_FAILED", "Authentication failed", 401);

    expect(error).toBeInstanceOf(Error);
    expect(error.name).toBe("ApiError");
    expect(error.code).toBe("AUTH_FAILED");
    expect(error.message).toBe("Authentication failed");
    expect(error.status).toBe(401);
  });
});

describe("createTokenManager", () => {
  let mockStorage: ReturnType<typeof createMockStorage>;
  let tokenManager: ReturnType<typeof createTokenManager>;

  beforeEach(() => {
    mockStorage = createMockStorage();
    tokenManager = createTokenManager(mockStorage);
  });

  it("should get token from storage", () => {
    mockStorage.data.set("admin_token", "test-token");

    expect(tokenManager.get()).toBe("test-token");
    expect(mockStorage.getItem).toHaveBeenCalledWith("admin_token");
  });

  it("should return null when no token exists", () => {
    expect(tokenManager.get()).toBeNull();
  });

  it("should set token in storage", () => {
    tokenManager.set("new-token");

    expect(mockStorage.setItem).toHaveBeenCalledWith(
      "admin_token",
      "new-token",
    );
    expect(mockStorage.data.get("admin_token")).toBe("new-token");
  });

  it("should clear token from storage", () => {
    mockStorage.data.set("admin_token", "test-token");

    tokenManager.clear();

    expect(mockStorage.removeItem).toHaveBeenCalledWith("admin_token");
    expect(mockStorage.data.has("admin_token")).toBe(false);
  });
});

describe("createApiClient", () => {
  let mockStorage: ReturnType<typeof createMockStorage>;
  let mockHttpClient: ReturnType<typeof createMockHttpClient>;
  let deps: ApiClientDeps;
  let api: ReturnType<typeof createApiClient>;

  beforeEach(() => {
    mockStorage = createMockStorage();
    mockHttpClient = createMockHttpClient();
    deps = {
      storage: mockStorage,
      httpClient: mockHttpClient,
      baseUrl: "https://test-api.example.com",
    };
    api = createApiClient(deps);
  });

  describe("token management", () => {
    it("should set and clear token", () => {
      api.setToken("test-token");
      expect(mockStorage.data.get("admin_token")).toBe("test-token");

      api.clearToken();
      expect(mockStorage.data.has("admin_token")).toBe(false);
    });
  });

  describe("request handling", () => {
    it("should make request without auth header when no token", async () => {
      mockHttpClient.mockResponse({
        ok: true,
        status: 200,
        json: () => Promise.resolve([{ id: "1", name: "Test" }]),
      });

      await api.providers.list();

      expect(mockHttpClient.fetch).toHaveBeenCalledWith(
        "https://test-api.example.com/providers",
        expect.objectContaining({
          headers: { "Content-Type": "application/json" },
        }),
      );
    });

    it("should include auth header when token exists", async () => {
      mockStorage.data.set("admin_token", "bearer-token");
      mockHttpClient.mockResponse({
        ok: true,
        status: 200,
        json: () => Promise.resolve([]),
      });

      await api.providers.list();

      expect(mockHttpClient.fetch).toHaveBeenCalledWith(
        "https://test-api.example.com/providers",
        expect.objectContaining({
          headers: {
            "Content-Type": "application/json",
            Authorization: "Bearer bearer-token",
          },
        }),
      );
    });

    it("should handle 204 No Content response", async () => {
      mockHttpClient.mockResponse({
        ok: true,
        status: 204,
        json: () => Promise.reject(new Error("No content")),
      });

      const result = await api.providers.delete("123");

      expect(result).toBeUndefined();
    });

    it("should throw ApiError on non-ok response", async () => {
      mockHttpClient.mockResponse({
        ok: false,
        status: 401,
        statusText: "Unauthorized",
        json: () =>
          Promise.resolve({ code: "AUTH_REQUIRED", message: "Please login" }),
      });

      await expect(api.providers.list()).rejects.toThrow(ApiError);
      await expect(api.providers.list()).rejects.toMatchObject({
        code: "AUTH_REQUIRED",
        message: "Please login",
        status: 401,
      });
    });

    it("should handle JSON parse error in error response", async () => {
      mockHttpClient.mockResponse({
        ok: false,
        status: 500,
        statusText: "Internal Server Error",
        json: () => Promise.reject(new Error("Invalid JSON")),
      });

      await expect(api.providers.list()).rejects.toMatchObject({
        code: "UNKNOWN_ERROR",
        message: "Internal Server Error",
        status: 500,
      });
    });

    it("should clear token and call onUnauthorized on 401 response", async () => {
      const onUnauthorized = vi.fn();
      const customApi = createApiClient({
        storage: mockStorage,
        httpClient: mockHttpClient,
        baseUrl: "https://test-api.example.com",
        onUnauthorized,
      });

      mockStorage.data.set("admin_token", "expired-token");
      mockHttpClient.mockResponse({
        ok: false,
        status: 401,
        statusText: "Unauthorized",
        json: () =>
          Promise.resolve({ code: "AUTH_EXPIRED", message: "Token expired" }),
      });

      await expect(customApi.providers.list()).rejects.toMatchObject({
        code: "AUTH_EXPIRED",
        status: 401,
      });

      // Token should be cleared
      expect(mockStorage.data.has("admin_token")).toBe(false);
      // onUnauthorized callback should be called
      expect(onUnauthorized).toHaveBeenCalledTimes(1);
    });
  });

  describe("validateToken", () => {
    it("should return true when token is valid", async () => {
      mockHttpClient.mockResponse({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ providers: [] }),
      });

      const result = await api.validateToken("valid-token");

      expect(result).toBe(true);
      expect(mockHttpClient.fetch).toHaveBeenCalledWith(
        "https://test-api.example.com/status",
        expect.objectContaining({
          headers: expect.objectContaining({
            Authorization: "Bearer valid-token",
          }),
        }),
      );
    });

    it("should return false when token is invalid", async () => {
      mockHttpClient.mockResponse({
        ok: false,
        status: 401,
        statusText: "Unauthorized",
        json: () => Promise.resolve({ code: "AUTH_REQUIRED" }),
      });

      const result = await api.validateToken("invalid-token");

      expect(result).toBe(false);
    });

    it("should return false on network error", async () => {
      (mockHttpClient.fetch as ReturnType<typeof vi.fn>).mockRejectedValue(
        new Error("Network error"),
      );

      const result = await api.validateToken("any-token");

      expect(result).toBe(false);
    });

    it("should not call onUnauthorized when validating token fails", async () => {
      const onUnauthorized = vi.fn();
      const customApi = createApiClient({
        storage: mockStorage,
        httpClient: mockHttpClient,
        baseUrl: "https://test-api.example.com",
        onUnauthorized,
      });

      mockHttpClient.mockResponse({
        ok: false,
        status: 401,
        statusText: "Unauthorized",
        json: () => Promise.resolve({ code: "AUTH_REQUIRED" }),
      });

      await customApi.validateToken("invalid-token");

      // onUnauthorized should NOT be called during validateToken
      expect(onUnauthorized).not.toHaveBeenCalled();
    });

    it("should not store token during validation", async () => {
      mockHttpClient.mockResponse({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ providers: [] }),
      });

      await api.validateToken("test-token");

      // Token should NOT be stored after validation
      expect(mockStorage.data.has("admin_token")).toBe(false);
    });
  });

  describe("auth flow", () => {
    it("should include Authorization header after setting token", async () => {
      // Initially no token
      mockHttpClient.mockResponse({
        ok: true,
        status: 200,
        json: () => Promise.resolve([]),
      });

      await api.providers.list();
      expect(mockHttpClient.fetch).toHaveBeenCalledWith(
        expect.any(String),
        expect.objectContaining({
          headers: expect.not.objectContaining({
            Authorization: expect.any(String),
          }),
        }),
      );

      // Set token
      api.setToken("new-auth-token");

      // Now request should include Authorization header
      await api.providers.list();
      expect(mockHttpClient.fetch).toHaveBeenLastCalledWith(
        expect.any(String),
        expect.objectContaining({
          headers: expect.objectContaining({
            Authorization: "Bearer new-auth-token",
          }),
        }),
      );
    });

    it("should not include Authorization header after clearing token", async () => {
      // Set and then clear token
      api.setToken("temp-token");
      api.clearToken();

      mockHttpClient.mockResponse({
        ok: true,
        status: 200,
        json: () => Promise.resolve([]),
      });

      await api.providers.list();

      expect(mockHttpClient.fetch).toHaveBeenCalledWith(
        expect.any(String),
        expect.objectContaining({
          headers: { "Content-Type": "application/json" },
        }),
      );
    });
  });
});
