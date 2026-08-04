export const SECTION_CARD_CLASS =
  "rounded-lg border border-border-light bg-bg-tertiary/60 p-3";
export const FIELD_LABEL_CLASS =
  "text-[11px] uppercase tracking-wide text-text-muted";
export const SNIPPET_CLASS =
  "mt-1 rounded border border-border-light bg-bg-secondary p-2 text-xs font-mono text-text-secondary whitespace-pre-wrap break-words";

export function formatEvidenceToken(value: string): string {
  return value.replaceAll("_", " ");
}
