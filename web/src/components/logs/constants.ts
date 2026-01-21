// Log table constants
// Note: Includes Time, Provider, API Type, Model, Status, Tokens, Retries, Latency, Client
export const LOG_TABLE_COLUMNS = 9;
export const PROVIDER_ID_PREVIEW_LENGTH = 8;

// Date formatter for consistent time display (compact format: MM/DD HH:mm:ss)
export const dateFormatter = new Intl.DateTimeFormat("en-US", {
  month: "2-digit",
  day: "2-digit",
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
  hour12: false,
});

// Claude cache billing rates
// See: https://docs.anthropic.com/claude/docs/prompt-caching
export const CLAUDE_CACHE_BILLING = {
  /** Cache read tokens are billed at 10% of standard input token cost */
  READ_RATE: 0.1,
  /** Cache creation tokens are billed at 125% of standard input token cost */
  WRITE_RATE: 1.25,
} as const;
