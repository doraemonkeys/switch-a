// Refresh interval configuration constants
export const REFRESH_INTERVALS = [
  { value: 0, label: "Off" },
  { value: 5000, label: "5s" },
  { value: 10000, label: "10s" },
  { value: 30000, label: "30s" },
] as const;

// Default refresh intervals for different pages
export const DEFAULT_REFRESH_INTERVAL: {
  dashboard: number;
  providers: number;
} = {
  dashboard: 10000, // Dashboard auto-refreshes by default
  providers: 0, // Providers page is off by default
};
