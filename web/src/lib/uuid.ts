const UUID_BYTE_LENGTH = 16;
const UUID_VERSION_BYTE_INDEX = 6;
const UUID_VARIANT_BYTE_INDEX = 8;
const UUID_VERSION_CLEAR_MASK = 0x0f;
const UUID_V4_VERSION_BITS = 0x40;
const UUID_VARIANT_CLEAR_MASK = 0x3f;
const UUID_RFC_4122_VARIANT_BITS = 0x80;
const UUID_BYTE_GROUP_LENGTHS = [4, 2, 2, 2, 6] as const;
const HEX_RADIX = 16;
const HEX_BYTE_WIDTH = 2;

type FillRandomValues = (bytes: Uint8Array) => Uint8Array;

/**
 * Generate an RFC 4122 UUID v4 without relying on Crypto.randomUUID, which
 * browsers may hide when the admin UI is served over LAN HTTP.
 */
export function generateUUIDv4(
  fillRandomValues: FillRandomValues = (bytes) => crypto.getRandomValues(bytes),
): string {
  const bytes = fillRandomValues(new Uint8Array(UUID_BYTE_LENGTH));
  bytes[UUID_VERSION_BYTE_INDEX] =
    (bytes[UUID_VERSION_BYTE_INDEX] & UUID_VERSION_CLEAR_MASK) |
    UUID_V4_VERSION_BITS;
  bytes[UUID_VARIANT_BYTE_INDEX] =
    (bytes[UUID_VARIANT_BYTE_INDEX] & UUID_VARIANT_CLEAR_MASK) |
    UUID_RFC_4122_VARIANT_BITS;

  const byteGroups: string[] = [];
  let groupStart = 0;
  for (const groupLength of UUID_BYTE_GROUP_LENGTHS) {
    byteGroups.push(
      Array.from(bytes.slice(groupStart, groupStart + groupLength), (byte) =>
        byte.toString(HEX_RADIX).padStart(HEX_BYTE_WIDTH, "0"),
      ).join(""),
    );
    groupStart += groupLength;
  }
  return byteGroups.join("-");
}
