// Log table constants
export const LOG_TABLE_COLUMNS = 8;
export const PROVIDER_ID_PREVIEW_LENGTH = 8;

// Date formatter for consistent time display
export const dateFormatter = new Intl.DateTimeFormat("en-US", {
  month: "short",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
});
