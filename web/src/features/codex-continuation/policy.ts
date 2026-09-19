export type CodexContinuationBoundary = "none" | "same_identity" | "any";

export interface CodexContinuationPolicy {
  outbound: CodexContinuationBoundary;
  inbound: CodexContinuationBoundary;
}

export const DEFAULT_CODEX_CONTINUATION: CodexContinuationPolicy = {
  outbound: "any",
  inbound: "none",
};

export function parseCodexContinuation(
  value: unknown,
): CodexContinuationPolicy {
  if (value === undefined) return { ...DEFAULT_CODEX_CONTINUATION };
  if (!value || typeof value !== "object")
    throw new Error("Invalid Codex continuation policy");
  const source = value as Record<string, unknown>;
  const parse = (boundary: unknown): CodexContinuationBoundary => {
    if (
      boundary === "none" ||
      boundary === "same_identity" ||
      boundary === "any"
    )
      return boundary;
    throw new Error("Invalid Codex continuation boundary");
  };
  return { outbound: parse(source.outbound), inbound: parse(source.inbound) };
}
