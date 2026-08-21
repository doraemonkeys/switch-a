import { describe, expect, it } from "vitest";
import {
  calculateTokenPercent,
  formatTokenCompact,
  formatTokenLocale,
  isGranularityAllowed,
  parseTokenBigInt,
} from "./token-format";

describe("token-format utilities", () => {
  describe("parseTokenBigInt", () => {
    it("parses valid decimal strings into BigInt", () => {
      expect(parseTokenBigInt("0")).toBe(0n);
      expect(parseTokenBigInt("12345678901234567890")).toBe(
        12345678901234567890n,
      );
      expect(parseTokenBigInt(" 500 ")).toBe(500n);
    });

    it("parses numbers and bigints", () => {
      expect(parseTokenBigInt(42)).toBe(42n);
      expect(parseTokenBigInt(100n)).toBe(100n);
    });

    it("returns 0n for invalid, negative, or missing values", () => {
      expect(parseTokenBigInt(null)).toBe(0n);
      expect(parseTokenBigInt(undefined)).toBe(0n);
      expect(parseTokenBigInt("")).toBe(0n);
      expect(parseTokenBigInt("abc")).toBe(0n);
      expect(parseTokenBigInt("-50")).toBe(0n);
      expect(parseTokenBigInt(-10)).toBe(0n);
    });
  });

  describe("formatTokenCompact", () => {
    it("formats small numbers directly", () => {
      expect(formatTokenCompact("0")).toBe("0");
      expect(formatTokenCompact("450")).toBe("450");
      expect(formatTokenCompact("999")).toBe("999");
    });

    it("formats thousands (K)", () => {
      expect(formatTokenCompact("1000")).toBe("1K");
      expect(formatTokenCompact("1500")).toBe("1.5K");
      expect(formatTokenCompact("420000")).toBe("420K");
      expect(formatTokenCompact("820400")).toBe("820.4K");
    });

    it("formats millions (M)", () => {
      expect(formatTokenCompact("1000000")).toBe("1M");
      expect(formatTokenCompact("1420000")).toBe("1.42M");
      expect(formatTokenCompact("12451200")).toBe("12.45M");
      expect(formatTokenCompact("7420000")).toBe("7.42M");
    });

    it("formats billions (B)", () => {
      expect(formatTokenCompact("1000000000")).toBe("1B");
      expect(formatTokenCompact("2500000000")).toBe("2.5B");
      expect(formatTokenCompact("12450000000")).toBe("12.45B");
    });
  });

  describe("formatTokenLocale", () => {
    it("formats full decimal numbers with locale thousand separators", () => {
      expect(formatTokenLocale("0")).toBe("0");
      expect(formatTokenLocale("12451200")).toBe("12,451,200");
      expect(formatTokenLocale(8204110)).toBe("8,204,110");
    });
  });

  describe("calculateTokenPercent", () => {
    it("calculates percentages safely", () => {
      expect(calculateTokenPercent("50", "100")).toBe(50);
      expect(calculateTokenPercent("3803", "10000")).toBeCloseTo(38.03);
      expect(calculateTokenPercent("0", "100")).toBe(0);
      expect(calculateTokenPercent("100", "0")).toBe(0);
      expect(calculateTokenPercent("150", "100")).toBe(100);
    });

    it("handles large BigInt totals accurately", () => {
      const part = "5000000000000000";
      const total = "10000000000000000";
      expect(calculateTokenPercent(part, total)).toBe(50);
    });
  });

  describe("isGranularityAllowed", () => {
    it("verifies allowed granularities per period", () => {
      expect(isGranularityAllowed("24h", "5m")).toBe(true);
      expect(isGranularityAllowed("24h", "15m")).toBe(true);
      expect(isGranularityAllowed("24h", "1h")).toBe(true);
      expect(isGranularityAllowed("24h", "1d")).toBe(false);

      expect(isGranularityAllowed("7d", "1h")).toBe(true);
      expect(isGranularityAllowed("7d", "6h")).toBe(true);
      expect(isGranularityAllowed("7d", "1d")).toBe(true);
      expect(isGranularityAllowed("7d", "5m")).toBe(false);

      expect(isGranularityAllowed("30d", "6h")).toBe(true);
      expect(isGranularityAllowed("30d", "1d")).toBe(true);
      expect(isGranularityAllowed("30d", "1h")).toBe(false);

      expect(isGranularityAllowed("all", "1d")).toBe(true);
      expect(isGranularityAllowed("all", "6h")).toBe(false);
      expect(isGranularityAllowed("all", undefined)).toBe(false);
    });
  });
});
