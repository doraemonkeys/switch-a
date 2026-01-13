/** Generate a random ID string (6 characters, alphanumeric) */
export function generateRandomId(length: number = 6): string {
  const chars = "abcdefghijklmnopqrstuvwxyz0123456789";
  const randomValues = new Uint32Array(length);
  crypto.getRandomValues(randomValues);
  return Array.from(randomValues, (v) => chars[v % chars.length]).join("");
}

/** Convert string to URL-friendly slug with random suffix (e.g., "OpenAI Production" → "openai-production-abc123") */
export function slugify(str: string): string {
  const randomSuffix = generateRandomId(6);

  const slug = str
    .toLowerCase()
    .trim()
    .replace(/\s+/g, "-")
    .replace(/[^a-z0-9-]/g, "")
    .replace(/-+/g, "-")
    .replace(/(^-)|(-$)/g, "");

  // If the result is empty (e.g., input was Chinese characters),
  // generate a fallback ID based on random suffix only
  if (!slug && str.trim()) {
    return `item-${randomSuffix}`;
  }

  // Append random suffix to make ID unique
  return slug ? `${slug}-${randomSuffix}` : randomSuffix;
}

/** Validate ID: only lowercase letters, numbers, and hyphens allowed */
export const isValidId = (id: string): boolean => /^[a-z0-9-]*$/.test(id);

/** Generate a consistent pastel color from a string */
export function stringToColor(str: string) {
  let hash = 0;
  for (let i = 0; i < str.length; i++) {
    hash = str.charCodeAt(i) + ((hash << 5) - hash);
  }
  const hue = Math.abs(hash % 360);
  return {
    bg: `hsl(${hue}, 85%, 96%)`,
    text: `hsl(${hue}, 70%, 35%)`,
    border: `hsl(${hue}, 60%, 85%)`,
  };
}

// =============================================================================
// Badge Style Utilities
// =============================================================================

/** Badge style classes for success state */
export const BADGE_STYLES = {
  SUCCESS:
    "bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-300",
  DANGER: "bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-300",
  WARNING:
    "bg-yellow-100 text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-300",
  INFO: "bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-300",
} as const;

/** Get badge class based on success boolean */
export function getSuccessBadgeClass(success: boolean): string {
  return success ? BADGE_STYLES.SUCCESS : BADGE_STYLES.DANGER;
}

/** Get badge class based on HTTP status code */
export function getStatusCodeBadgeClass(statusCode: number): string {
  if (statusCode >= 200 && statusCode < 300) {
    return BADGE_STYLES.SUCCESS;
  }
  if (statusCode >= 400) {
    return BADGE_STYLES.DANGER;
  }
  return BADGE_STYLES.WARNING;
}
