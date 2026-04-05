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

const MILLISECONDS_PER_SECOND = 1000;
const SECONDS_PER_MINUTE = 60;
const MINUTES_PER_HOUR = 60;
const MILLISECONDS_PER_MINUTE =
  MILLISECONDS_PER_SECOND * SECONDS_PER_MINUTE;
const MILLISECONDS_PER_HOUR = MILLISECONDS_PER_MINUTE * MINUTES_PER_HOUR;
const SECOND_FRACTION_DIGITS = 1;

interface FormatDurationOptions {
  smallestUnit?: "ms" | "s";
}

function trimTrailingFractionZeros(value: string): string {
  return value.replace(/\.0$/, "");
}

/**
 * Keep duration copy readable by switching to larger units when the next smaller
 * unit stops adding signal. Real-time surfaces can still opt into second-only
 * output to avoid millisecond noise.
 */
export function formatDuration(
  ms: number,
  options: FormatDurationOptions = {},
): string {
  const { smallestUnit = "s" } = options;
  const normalizedMs = Math.max(0, ms);

  if (normalizedMs < MILLISECONDS_PER_SECOND) {
    if (smallestUnit === "ms") {
      return `${Math.round(normalizedMs)}ms`;
    }
    return `${Math.floor(normalizedMs / MILLISECONDS_PER_SECOND)}s`;
  }

  if (normalizedMs < MILLISECONDS_PER_MINUTE) {
    if (smallestUnit === "s") {
      return `${Math.floor(normalizedMs / MILLISECONDS_PER_SECOND)}s`;
    }

    const seconds = normalizedMs / MILLISECONDS_PER_SECOND;
    return `${trimTrailingFractionZeros(seconds.toFixed(SECOND_FRACTION_DIGITS))}s`;
  }

  const totalSeconds = Math.floor(normalizedMs / MILLISECONDS_PER_SECOND);
  const totalMinutes = Math.floor(totalSeconds / SECONDS_PER_MINUTE);

  if (normalizedMs < MILLISECONDS_PER_HOUR) {
    return `${totalMinutes}m ${totalSeconds % SECONDS_PER_MINUTE}s`;
  }

  const hours = Math.floor(totalMinutes / MINUTES_PER_HOUR);
  return `${hours}h ${totalMinutes % MINUTES_PER_HOUR}m`;
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
