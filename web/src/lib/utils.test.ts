import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { formatDuration, generateRandomId, slugify, isValidId } from "./utils";

describe("utils", () => {
  describe("generateRandomId", () => {
    it("generates ID with default length of 6", () => {
      const id = generateRandomId();
      expect(id).toHaveLength(6);
    });

    it("generates ID with custom length", () => {
      const id = generateRandomId(10);
      expect(id).toHaveLength(10);
    });

    it("generates ID with only lowercase letters and numbers", () => {
      const id = generateRandomId(100);
      expect(id).toMatch(/^[a-z0-9]+$/);
    });

    it("generates different IDs on each call", () => {
      const ids = new Set(
        Array.from({ length: 100 }, () => generateRandomId()),
      );
      // With 36^6 possibilities, collisions should be extremely rare
      expect(ids.size).toBeGreaterThan(95);
    });

    it("handles length of 1", () => {
      const id = generateRandomId(1);
      expect(id).toHaveLength(1);
      expect(id).toMatch(/^[a-z0-9]$/);
    });

    it("handles length of 0", () => {
      const id = generateRandomId(0);
      expect(id).toBe("");
    });
  });

  describe("slugify", () => {
    beforeEach(() => {
      vi.spyOn(crypto, "getRandomValues").mockImplementation((array) => {
        const arr = array as Uint32Array;
        for (let i = 0; i < arr.length; i++) {
          arr[i] = i; // Produces predictable "abcdef..." sequence
        }
        return array;
      });
    });

    afterEach(() => {
      vi.restoreAllMocks();
    });

    it.each([
      ["OpenAI Production", /^openai-production-[a-z0-9]+$/],
      ["hello world", /^hello-world-[a-z0-9]+$/],
      ["hello@world#test!", /^helloworldtest-[a-z0-9]+$/],
      ["hello   world", /^hello-world-[a-z0-9]+$/],
      ["-hello world-", /^hello-world-[a-z0-9]+$/],
      ["", /^[a-z0-9]+$/],
      ["   ", /^[a-z0-9]+$/],
      ["中文测试", /^item-[a-z0-9]+$/],
      ["测试 test 中文", /^test-[a-z0-9]+$/],
      ["test123", /^test123-[a-z0-9]+$/],
      ["my-api-key", /^my-api-key-[a-z0-9]+$/],
      ["@#$%^&*", /^item-[a-z0-9]+$/],
    ])("normalizes %j to the expected slug shape", (input, expected) => {
      expect(slugify(input)).toMatch(expected);
    });
  });

  describe("isValidId", () => {
    it.each(["hello", "hello123", "hello-world", "", "my-api-key-123-abc"])(
      "accepts %j",
      (id) => {
        expect(isValidId(id)).toBe(true);
      },
    );

    it.each([
      "Hello",
      "HELLO",
      "hello world",
      "hello@world",
      "hello_world",
      "hello.world",
      "hello中文",
      "héllo",
    ])("rejects %j", (id) => {
      expect(isValidId(id)).toBe(false);
    });
  });

  describe("formatDuration", () => {
    it("keeps live elapsed time at whole-second granularity by default", () => {
      expect(formatDuration(1_500)).toBe("1s");
    });

    it("preserves millisecond output when the caller needs latency precision", () => {
      expect(formatDuration(999, { smallestUnit: "ms" })).toBe("999ms");
    });

    it("switches latency display to seconds once milliseconds stop being readable", () => {
      expect(formatDuration(1_500, { smallestUnit: "ms" })).toBe("1.5s");
      expect(formatDuration(12_000, { smallestUnit: "ms" })).toBe("12s");
    });

    it("formats long durations as minutes and seconds", () => {
      expect(formatDuration(502_279, { smallestUnit: "ms" })).toBe("8m 22s");
    });

    it("formats very long durations as hours and minutes", () => {
      expect(formatDuration(3_780_000, { smallestUnit: "ms" })).toBe("1h 3m");
    });

    it("clamps negative values to zero", () => {
      expect(formatDuration(-1)).toBe("0s");
      expect(formatDuration(-1, { smallestUnit: "ms" })).toBe("0ms");
    });
  });
});
