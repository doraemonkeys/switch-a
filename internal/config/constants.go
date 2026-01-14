// Package config handles configuration loading and management.
package config

import "switch-a/internal/defaults"

// Environment variable names.
const (
	EnvPrefix         = "SWITCHA"
	EnvPort           = "SWITCHA_PORT"
	EnvAdminPort      = "SWITCHA_ADMIN_PORT"
	EnvDBPath         = "SWITCHA_DB_PATH"
	EnvAdminToken     = "SWITCHA_ADMIN_TOKEN"
	EnvLogPath        = "SWITCHA_LOG_PATH"
	EnvLogMaxSizeMB   = "SWITCHA_LOG_MAX_SIZE_MB"
	EnvLogMaxKeepDays = "SWITCHA_LOG_MAX_KEEP_DAYS"
	EnvLogLevel       = "SWITCHA_LOG_LEVEL"
)

// Config file settings.
const (
	ConfigFileName = "config"
	ConfigFileType = "yaml"
)

// Config keys for viper.
const (
	KeyPort           = "port"
	KeyAdminPort      = "admin_port"
	KeyDBPath         = "db_path"
	KeyAdminToken     = "admin_token"
	KeyLogPath        = "log_path"
	KeyLogMaxSizeMB   = "log_max_size_mb"
	KeyLogMaxKeepDays = "log_max_keep_days"
	KeyLogLevel       = "log_level"
)

// Default configuration values.
const (
	DefaultPort           = "28080"
	DefaultAdminPort      = "28081"
	DefaultDBPath         = "./data.db"
	DefaultLogPath        = defaults.LogPath
	DefaultLogMaxSizeMB   = defaults.LogMaxSizeMB
	DefaultLogMaxKeepDays = defaults.LogMaxKeepDays
	DefaultLogLevel       = defaults.LogLevel
)
