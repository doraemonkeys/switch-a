import type { DebugCaptureBlobPreview, DebugCaptureHeaders } from "@/api";

const BYTE_UNITS = ["B", "KiB", "MiB", "GiB"] as const;
const BYTES_PER_UNIT = 1_024;
const TEXTUAL_CONTENT_MARKERS = [
  "json",
  "text/",
  "xml",
  "javascript",
  "x-www-form-urlencoded",
] as const;

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";

  let value = bytes;
  let unitIndex = 0;
  while (value >= BYTES_PER_UNIT && unitIndex < BYTE_UNITS.length - 1) {
    value /= BYTES_PER_UNIT;
    unitIndex += 1;
  }
  const precision = unitIndex === 0 || value >= 10 ? 0 : 1;
  return `${value.toFixed(precision)} ${BYTE_UNITS[unitIndex]}`;
}

export function formatCaptureValue(value: string): string {
  return value
    .split("_")
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

export function getContentType(headers: DebugCaptureHeaders): string {
  const entry = Object.entries(headers).find(
    ([name]) => name.toLowerCase() === "content-type",
  );
  return entry?.[1]?.[0] ?? "";
}

export function isTextualContentType(contentType: string): boolean {
  const normalized = contentType.toLowerCase();
  return TEXTUAL_CONTENT_MARKERS.some((marker) => normalized.includes(marker));
}

export function presentBlobPreview(
  preview: DebugCaptureBlobPreview,
  preferText: boolean,
): string {
  if (!preview.data_base64) return "(empty payload)";
  if (!preferText) return preview.data_base64;

  try {
    const binary = atob(preview.data_base64);
    const bytes = Uint8Array.from(binary, (character) =>
      character.charCodeAt(0),
    );
    return new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  } catch {
    return preview.data_base64;
  }
}
