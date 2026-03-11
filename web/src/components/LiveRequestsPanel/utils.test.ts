import { describe, it, expect } from "vitest";
import { formatBytes, formatIdleDuration } from "./utils";

describe("formatBytes", () => {
  it("formats 0 bytes", () => {
    expect(formatBytes(0)).toBe("0 B");
  });

  it("formats bytes under 1 KB", () => {
    expect(formatBytes(512)).toBe("512 B");
  });

  it("formats kilobytes", () => {
    expect(formatBytes(1024)).toBe("1.0 KB");
    expect(formatBytes(1536)).toBe("1.5 KB");
  });

  it("formats megabytes", () => {
    expect(formatBytes(1048576)).toBe("1.0 MB");
    expect(formatBytes(1258291)).toBe("1.2 MB");
  });

  it("formats gigabytes", () => {
    expect(formatBytes(1073741824)).toBe("1.0 GB");
  });

  it("caps at GB for very large values", () => {
    // 2 TB should render as GB since there's no TB unit
    expect(formatBytes(2199023255552)).toBe("2048.0 GB");
  });
});

describe("formatIdleDuration", () => {
  const now = 1700000010000; // some reference time

  it("returns empty string for zero timestamp", () => {
    expect(formatIdleDuration(0, now)).toBe("");
  });

  it("returns empty string for negative idle time", () => {
    expect(formatIdleDuration(now + 5000, now)).toBe("");
  });

  it("formats seconds", () => {
    expect(formatIdleDuration(now - 5000, now)).toBe("idle 5s");
    expect(formatIdleDuration(now - 59000, now)).toBe("idle 59s");
  });

  it("formats minutes", () => {
    expect(formatIdleDuration(now - 120000, now)).toBe("idle 2m");
    expect(formatIdleDuration(now - 3540000, now)).toBe("idle 59m");
  });

  it("formats hours", () => {
    expect(formatIdleDuration(now - 3600000, now)).toBe("idle 1h");
    expect(formatIdleDuration(now - 7200000, now)).toBe("idle 2h");
  });
});
