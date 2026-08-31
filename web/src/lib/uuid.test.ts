import { describe, expect, it, vi } from "vitest";
import { generateUUIDv4 } from "./uuid";

describe("generateUUIDv4", () => {
  it("generates the RFC 4122 version and variant from random bytes", () => {
    const fillRandomValues = vi.fn((bytes: Uint8Array) => {
      bytes.set(Array.from({ length: bytes.length }, (_, index) => index));
      return bytes;
    });

    expect(generateUUIDv4(fillRandomValues)).toBe(
      "00010203-0405-4607-8809-0a0b0c0d0e0f",
    );
    expect(fillRandomValues).toHaveBeenCalledTimes(1);
  });

  it("works with the random-value capability available on LAN HTTP", () => {
    const lanHttpCrypto = {
      getRandomValues(bytes: Uint8Array) {
        bytes.fill(0xff);
        return bytes;
      },
    };

    expect(generateUUIDv4(lanHttpCrypto.getRandomValues)).toBe(
      "ffffffff-ffff-4fff-bfff-ffffffffffff",
    );
  });
});
