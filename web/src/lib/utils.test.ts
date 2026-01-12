import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { generateRandomId, slugify, isValidId } from "./utils";

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

    it("converts string to lowercase slug with random suffix", () => {
      const result = slugify("OpenAI Production");
      expect(result).toMatch(/^openai-production-[a-z0-9]+$/);
    });

    it("replaces spaces with hyphens", () => {
      const result = slugify("hello world");
      expect(result).toMatch(/^hello-world-[a-z0-9]+$/);
    });

    it("removes special characters", () => {
      const result = slugify("hello@world#test!");
      expect(result).toMatch(/^helloworldtest-[a-z0-9]+$/);
    });

    it("collapses multiple hyphens", () => {
      const result = slugify("hello   world");
      expect(result).toMatch(/^hello-world-[a-z0-9]+$/);
    });

    it("removes leading and trailing hyphens", () => {
      const result = slugify("-hello world-");
      expect(result).toMatch(/^hello-world-[a-z0-9]+$/);
    });

    it("handles empty string", () => {
      const result = slugify("");
      // Empty string returns just the random suffix
      expect(result).toMatch(/^[a-z0-9]+$/);
    });

    it("handles whitespace-only string", () => {
      const result = slugify("   ");
      // Whitespace-only returns just the random suffix
      expect(result).toMatch(/^[a-z0-9]+$/);
    });

    it("handles Chinese characters (non-ASCII)", () => {
      const result = slugify("中文测试");
      // Non-ASCII characters get removed, so fallback to item-{suffix}
      expect(result).toMatch(/^item-[a-z0-9]+$/);
    });

    it("handles mixed Chinese and English", () => {
      const result = slugify("测试 test 中文");
      expect(result).toMatch(/^test-[a-z0-9]+$/);
    });

    it("handles numbers in input", () => {
      const result = slugify("test123");
      expect(result).toMatch(/^test123-[a-z0-9]+$/);
    });

    it("preserves hyphens in input", () => {
      const result = slugify("my-api-key");
      expect(result).toMatch(/^my-api-key-[a-z0-9]+$/);
    });

    it("handles special characters only", () => {
      const result = slugify("@#$%^&*");
      // All special chars removed, but input is not whitespace-only, so fallback to item-{suffix}
      expect(result).toMatch(/^item-[a-z0-9]+$/);
    });
  });

  describe("isValidId", () => {
    it("returns true for valid lowercase ID", () => {
      expect(isValidId("hello")).toBe(true);
    });

    it("returns true for ID with numbers", () => {
      expect(isValidId("hello123")).toBe(true);
    });

    it("returns true for ID with hyphens", () => {
      expect(isValidId("hello-world")).toBe(true);
    });

    it("returns true for empty string", () => {
      expect(isValidId("")).toBe(true);
    });

    it("returns true for complex valid ID", () => {
      expect(isValidId("my-api-key-123-abc")).toBe(true);
    });

    it("returns false for uppercase letters", () => {
      expect(isValidId("Hello")).toBe(false);
      expect(isValidId("HELLO")).toBe(false);
    });

    it("returns false for spaces", () => {
      expect(isValidId("hello world")).toBe(false);
    });

    it("returns false for special characters", () => {
      expect(isValidId("hello@world")).toBe(false);
      expect(isValidId("hello_world")).toBe(false);
      expect(isValidId("hello.world")).toBe(false);
    });

    it("returns false for unicode characters", () => {
      expect(isValidId("hello中文")).toBe(false);
      expect(isValidId("héllo")).toBe(false);
    });
  });
});
