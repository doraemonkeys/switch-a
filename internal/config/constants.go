// Package config handles configuration loading and management.
package config

import "github.com/doraemonkeys/switch-a/internal/defaults"

// Environment variable names.
const (
	EnvPrefix                              = "SWITCHA"
	EnvPort                                = "SWITCHA_PORT"
	EnvAdminPort                           = "SWITCHA_ADMIN_PORT"
	EnvDBPath                              = "SWITCHA_DB_PATH"
	EnvAdminToken                          = "SWITCHA_ADMIN_TOKEN"
	EnvLogPath                             = "SWITCHA_LOG_PATH"
	EnvLogMaxSizeMB                        = "SWITCHA_LOG_MAX_SIZE_MB"
	EnvLogMaxKeepDays                      = "SWITCHA_LOG_MAX_KEEP_DAYS"
	EnvLogLevel                            = "SWITCHA_LOG_LEVEL"
	EnvCodexKeyringFile                    = "SWITCHA_CODEX_KEYRING_FILE"
	EnvDebugCaptureMemoryCeilingMiB        = "SWITCHA_DEBUG_CAPTURE_MEMORY_CEILING_MIB"
	EnvDebugCaptureMaxActiveRecords        = "SWITCHA_DEBUG_CAPTURE_MAX_ACTIVE_RECORDS"
	EnvDebugCaptureMaxActiveTraces         = "SWITCHA_DEBUG_CAPTURE_MAX_ACTIVE_TRACES"
	EnvDebugCaptureMaxTransitionsPerTrace  = "SWITCHA_DEBUG_CAPTURE_MAX_TRANSITIONS_PER_TRACE"
	EnvDebugCaptureMaxPendingExports       = "SWITCHA_DEBUG_CAPTURE_MAX_PENDING_EXPORTS"
	EnvDebugCaptureMaxConcurrentDownloads  = "SWITCHA_DEBUG_CAPTURE_MAX_CONCURRENT_DOWNLOADS"
	EnvDebugCaptureDetailPreviewBytes      = "SWITCHA_DEBUG_CAPTURE_DETAIL_PREVIEW_BYTES"
	EnvDebugCaptureDetailEventLimit        = "SWITCHA_DEBUG_CAPTURE_DETAIL_EVENT_LIMIT"
	EnvDebugCaptureDownloadTokenTTLSeconds = "SWITCHA_DEBUG_CAPTURE_DOWNLOAD_TOKEN_TTL_SECONDS"
	EnvDebugCaptureMaxRecordsPerProvider   = "SWITCHA_DEBUG_CAPTURE_MAX_RECORDS_PER_PROVIDER"
	EnvDebugCaptureChunkBytes              = "SWITCHA_DEBUG_CAPTURE_CHUNK_BYTES"
	EnvDebugCaptureExportLineBytes         = "SWITCHA_DEBUG_CAPTURE_EXPORT_LINE_BYTES"
)

// Config file settings.
const (
	ConfigFileName = "config"
	ConfigFileType = "yaml"
)

// Config keys for viper.
const (
	KeyPort                                = "port"
	KeyAdminPort                           = "admin_port"
	KeyDBPath                              = "db_path"
	KeyAdminToken                          = "admin_token"
	KeyLogPath                             = "log_path"
	KeyLogMaxSizeMB                        = "log_max_size_mb"
	KeyLogMaxKeepDays                      = "log_max_keep_days"
	KeyLogLevel                            = "log_level"
	KeyCodexKeyringFile                    = "codex_keyring_file"
	KeyDebugCaptureMemoryCeilingMiB        = "debug_capture_memory_ceiling_mib"
	KeyDebugCaptureMaxActiveRecords        = "debug_capture_max_active_records"
	KeyDebugCaptureMaxActiveTraces         = "debug_capture_max_active_traces"
	KeyDebugCaptureMaxTransitionsPerTrace  = "debug_capture_max_transitions_per_trace"
	KeyDebugCaptureMaxPendingExports       = "debug_capture_max_pending_exports"
	KeyDebugCaptureMaxConcurrentDownloads  = "debug_capture_max_concurrent_downloads"
	KeyDebugCaptureDetailPreviewBytes      = "debug_capture_detail_preview_bytes"
	KeyDebugCaptureDetailEventLimit        = "debug_capture_detail_event_limit"
	KeyDebugCaptureDownloadTokenTTLSeconds = "debug_capture_download_token_ttl_seconds"
	KeyDebugCaptureMaxRecordsPerProvider   = "debug_capture_max_records_per_provider"
	KeyDebugCaptureChunkBytes              = "debug_capture_chunk_bytes"
	KeyDebugCaptureExportLineBytes         = "debug_capture_export_line_bytes"
)

// Default configuration values.
const (
	DefaultPort                                = "28080"
	DefaultAdminPort                           = "28081"
	DefaultDBPath                              = "./data.db"
	DefaultLogPath                             = defaults.LogPath
	DefaultLogMaxSizeMB                        = defaults.LogMaxSizeMB
	DefaultLogMaxKeepDays                      = defaults.LogMaxKeepDays
	DefaultLogLevel                            = defaults.LogLevel
	DefaultDebugCaptureMemoryCeilingMiB        = defaults.DebugCaptureMemoryCeilingMiB
	DefaultDebugCaptureSessionQuotaMiB         = defaults.DebugCaptureSessionQuotaMiB
	DefaultDebugCaptureMaxActiveRecords        = defaults.DebugCaptureMaxActiveRecords
	DefaultDebugCaptureMaxActiveTraces         = defaults.DebugCaptureMaxActiveTraces
	DefaultDebugCaptureMaxTransitionsPerTrace  = defaults.DebugCaptureMaxTransitionsPerTrace
	DefaultDebugCaptureMaxPendingExports       = defaults.DebugCaptureMaxPendingExports
	DefaultDebugCaptureMaxConcurrentDownloads  = defaults.DebugCaptureMaxConcurrentDownloads
	DefaultDebugCaptureDetailPreviewBytes      = defaults.DebugCaptureDetailPreviewBytes
	DefaultDebugCaptureDetailEventLimit        = defaults.DebugCaptureDetailEventLimit
	DefaultDebugCaptureDownloadTokenTTLSeconds = defaults.DebugCaptureDownloadTokenTTLSeconds
	DefaultDebugCaptureMaxRecordsPerProvider   = defaults.DebugCaptureMaxRecordsPerProvider
	MinimumDebugCaptureChunkBytes              = defaults.DebugCaptureMinimumChunkBytes
	MaximumDebugCaptureChunkBytes              = defaults.DebugCaptureMaximumChunkBytes
	DefaultDebugCaptureChunkBytes              = defaults.DebugCaptureChunkBytes
	DefaultDebugCaptureExportLineBytes         = defaults.DebugCaptureExportLineBytes
)
