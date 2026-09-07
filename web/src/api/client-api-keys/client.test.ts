import { describe, expect, it, vi } from "vitest";
import { createClientApiKeysApi } from "./client";
import {
  parseClientAPIKeysState,
  parseClientAPIKey,
  parseClientAccessPolicy,
} from "./types";

const item = {
  id: "key/id",
  name: "Laptop",
  key: "existing-value",
  created_at: "2026-09-06T12:00:00Z",
  updated_at: "2026-09-06T12:00:00Z",
};

describe("client API keys contract", () => {
  it("reads state and writes the explicit policy", async () => {
    const request = vi
      .fn()
      .mockResolvedValueOnce({ mode: "permissive", keys: [item] })
      .mockResolvedValueOnce({ mode: "restricted" });
    const api = createClientApiKeysApi(request);
    expect(await api.get()).toEqual({ mode: "permissive", keys: [item] });
    expect(await api.setPolicy("restricted")).toEqual({ mode: "restricted" });
    expect(request).toHaveBeenNthCalledWith(1, "/client-api-keys");
    expect(request).toHaveBeenNthCalledWith(2, "/client-api-keys/policy", {
      method: "PUT",
      body: '{"mode":"restricted"}',
    });
  });

  it("preserves supplied values and uses separate generate and escaped item paths", async () => {
    const request = vi.fn().mockResolvedValue(item);
    const api = createClientApiKeysApi(request);
    await api.add("Laptop", " exact key ");
    await api.generate("Generated");
    await api.rename("key/id", "New");
    request.mockResolvedValueOnce(undefined);
    await api.delete("key/id");
    expect(request.mock.calls).toEqual([
      [
        "/client-api-keys",
        {
          method: "POST",
          body: JSON.stringify({ name: "Laptop", key: " exact key " }),
        },
      ],
      [
        "/client-api-keys/generate",
        { method: "POST", body: '{"name":"Generated"}' },
      ],
      [
        "/client-api-keys/keys/key%2Fid",
        { method: "PUT", body: '{"name":"New"}' },
      ],
      ["/client-api-keys/keys/key%2Fid", { method: "DELETE" }],
    ]);
  });

  it("keeps reserved-looking imported IDs in the item namespace", async () => {
    const request = vi.fn().mockResolvedValue(item);
    const api = createClientApiKeysApi(request);
    await api.rename("policy", "Renamed");
    await api.delete("generate");
    expect(request).toHaveBeenCalledWith("/client-api-keys/keys/policy", {
      method: "PUT",
      body: '{"name":"Renamed"}',
    });
    expect(request).toHaveBeenCalledWith("/client-api-keys/keys/generate", {
      method: "DELETE",
    });
  });

  it("rejects malformed responses and propagates server errors", async () => {
    for (const value of [
      null,
      [],
      {},
      { mode: "other", keys: [] },
      { mode: "restricted", keys: null },
    ]) {
      expect(() => parseClientAPIKeysState(value)).toThrow();
    }
    expect(() => parseClientAPIKey({ ...item, key: null })).toThrow();
    expect(() => parseClientAccessPolicy({ mode: false })).toThrow();
    const api = createClientApiKeysApi(
      vi.fn().mockRejectedValue(new Error("Duplicate key")),
    );
    await expect(api.add("Laptop", "x")).rejects.toThrow("Duplicate key");
  });
});
