import { beforeEach, describe, expect, it } from "vitest";
import { createApiClient } from "./client";
import { createMockHttpClient, createMockStorage } from "./test-mocks";
import type { NormalizedRequestLog, RequestAttempt } from "./types";

const normalizedLog = {
  id: 1,
  request_id: "request-1",
  provider_id: "provider-1",
  api_type: "claude",
  model: "claude-3-7-sonnet",
  client_ip: "127.0.0.1",
  user_id: "user-1",
  semantics_version: "normalized_v1",
  client_transport_status_code: 101,
  completion_state: "completed",
  service_outcome: "completed",
  termination_actor: null,
  termination_reason: null,
  client_action: "none",
  session_evidence_json: null,
  latency_ms: 150,
  is_sse: false,
  is_websocket: true,
  retry_count: 0,
  is_sticky: false,
  created_at: "2026-04-01T00:00:00Z",
} satisfies NormalizedRequestLog;

const normalizedAttempt = {
  id: 10,
  request_id: "request-1",
  provider_id: "provider-2",
  semantics_version: "normalized_v1",
  attempt: 1,
  status_code: 502,
  error: "provider unavailable",
  attempt_evidence_json: null,
  latency_ms: 80,
  created_at: "2026-04-01T00:00:01Z",
} satisfies RequestAttempt;

function omitProperty<T extends object, K extends keyof T>(
  value: T,
  key: K,
): Omit<T, K> {
  const nextValue: Partial<T> = { ...value };
  delete nextValue[key];
  return nextValue as Omit<T, K>;
}

describe("createApiClient logs API", () => {
  let mockStorage: ReturnType<typeof createMockStorage>;
  let mockHttpClient: ReturnType<typeof createMockHttpClient>;
  let api: ReturnType<typeof createApiClient>;

  beforeEach(() => {
    mockStorage = createMockStorage();
    mockHttpClient = createMockHttpClient();
    api = createApiClient({
      storage: mockStorage,
      httpClient: mockHttpClient,
      baseUrl: "https://test-api.example.com",
    });
  });

  it("lists logs without params", async () => {
    const logsResponse = {
      logs: [normalizedLog],
      total: 1,
      limit: 20,
      offset: 0,
      sort_by: "created_at",
      sort_order: "desc",
    };
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(logsResponse),
    });

    const result = await api.logs.list();

    expect(result).toEqual(logsResponse);
    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/logs",
      expect.any(Object),
    );
  });

  it("serializes pagination and metadata filters, including custom API types", async () => {
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve({ logs: [], total: 0, limit: 20, offset: 0 }),
    });

    await api.logs.list({
      limit: 50,
      offset: 100,
      provider_id: "provider-1",
      api_type: "custom:mytool",
      is_websocket: true,
      user_id: "user-1",
    });

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/logs?limit=50&offset=100&provider_id=provider-1&api_type=custom%3Amytool&is_websocket=true&user_id=user-1",
      expect.any(Object),
    );
  });

  it("serializes normalized semantic filters", async () => {
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve({ logs: [], total: 0, limit: 20, offset: 0 }),
    });

    await api.logs.list({
      semantics_version: "normalized_v1",
      completion_state: "completed",
      service_outcome: "interrupted",
      client_action: "reconnect_required",
      termination_actor: "upstream",
      termination_reason: "usage_limit_reached",
      client_transport_status_code: 101,
      session_committed: true,
      client_visible: true,
      commit_source: "semantic_event",
    });

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/logs?semantics_version=normalized_v1&completion_state=completed&service_outcome=interrupted&client_action=reconnect_required&termination_actor=upstream&termination_reason=usage_limit_reached&client_transport_status_code=101&session_committed=true&client_visible=true&commit_source=semantic_event",
      expect.any(Object),
    );
  });

  it("omits undefined and empty semantic filters", async () => {
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve({ logs: [], total: 0, limit: 20, offset: 0 }),
    });

    await api.logs.list({
      semantics_version: undefined,
      service_outcome: undefined,
      client_action: undefined,
      termination_reason: undefined,
    });

    expect(mockHttpClient.fetch).toHaveBeenCalledWith(
      "https://test-api.example.com/logs",
      expect.any(Object),
    );
  });

  it("rejects normalized rows that omit required assessment fields", async () => {
    const logWithoutClientAction = omitProperty(normalizedLog, "client_action");

    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(logWithoutClientAction),
    });

    await expect(api.logs.get(1)).rejects.toThrow(
      "logs/1.client_action is required for normalized_v1 rows",
    );
  });

  it("rejects normalized rows that omit nullable canonical fields entirely", async () => {
    const logWithoutTerminationReason = omitProperty(
      normalizedLog,
      "termination_reason",
    );

    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () => Promise.resolve(logWithoutTerminationReason),
    });

    await expect(api.logs.get(1)).rejects.toThrow(
      "logs/1.termination_reason must be present for normalized_v1 rows",
    );
  });

  it("accepts nested attempts when their nullable evidence is explicit", async () => {
    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () =>
        Promise.resolve({
          ...normalizedLog,
          attempts: [normalizedAttempt],
        }),
    });

    const result = await api.logs.get(1);

    expect(result.attempts).toEqual([normalizedAttempt]);
  });

  it("rejects nested attempts that omit semantics_version", async () => {
    const attemptWithoutSemanticsVersion = omitProperty(
      normalizedAttempt,
      "semantics_version",
    );

    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () =>
        Promise.resolve({
          ...normalizedLog,
          attempts: [attemptWithoutSemanticsVersion],
        }),
    });

    await expect(api.logs.get(1)).rejects.toThrow(
      "logs/1.attempts[0].semantics_version is required",
    );
  });

  it("rejects nested attempts that omit nullable evidence fields entirely", async () => {
    const attemptWithoutEvidence = omitProperty(
      normalizedAttempt,
      "attempt_evidence_json",
    );

    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () =>
        Promise.resolve({
          ...normalizedLog,
          attempts: [attemptWithoutEvidence],
        }),
    });

    await expect(api.logs.get(1)).rejects.toThrow(
      "logs/1.attempts[0].attempt_evidence_json must be present",
    );
  });

  // The HTTP normalization path (classifyHTTPAttemptOutcome) emits outcome
  // values beyond the websocket-only legacy four; every one of them must parse
  // or the whole log detail is silently dropped by the list-row fallback.
  it("accepts nested attempts with every HTTP-normalized outcome value", async () => {
    const outcomes = [
      "upstream_completed",
      "upstream_http_status_error",
      "upstream_incomplete",
      "gateway_error",
    ] as const;

    mockHttpClient.mockResponse({
      ok: true,
      status: 200,
      json: () =>
        Promise.resolve({
          ...normalizedLog,
          attempts: outcomes.map((outcome, index) => ({
            ...normalizedAttempt,
            id: 10 + index,
            attempt: 1 + index,
            outcome,
          })),
        }),
    });

    const result = await api.logs.get(1);

    expect(result.attempts?.map((attempt) => attempt.outcome)).toEqual([
      ...outcomes,
    ]);
  });
});
