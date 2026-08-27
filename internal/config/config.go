// Package config handles configuration loading and management.
package config

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/viper"
)

const bytesPerMiB int64 = 1 << 20

// configureViperPaths sets up config file paths for a viper instance.
// If configPath is provided, it uses that exact path.
// Otherwise, it searches in standard locations.
func configureViperPaths(v *viper.Viper, configPath string) {
	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		v.SetConfigName(ConfigFileName)
		v.SetConfigType(ConfigFileType)
		v.AddConfigPath(".")
		v.AddConfigPath("./config")
		// Unix-only system config path
		if runtime.GOOS != "windows" {
			v.AddConfigPath("/etc/switch-a")
		}
	}
}

// Config holds the startup configuration loaded from environment variables or config file.
// Debug-capture values use runtime-native units so conversion cannot be repeated or
// interpreted differently by the composition root and the capture manager.
type Config struct {
	Port             string
	AdminPort        string
	DBPath           string
	AdminToken       string
	LogPath          string
	LogMaxSizeMB     int
	LogMaxKeepDays   int
	LogLevel         string
	CodexKeyringFile string

	DebugCaptureMemoryCeilingBytes     int64
	DebugCaptureMaxActiveRecords       int
	DebugCaptureMaxActiveTraces        int
	DebugCaptureMaxTransitionsPerTrace int
	DebugCaptureMaxPendingExports      int
	DebugCaptureMaxConcurrentDownloads int
	DebugCaptureDetailPreviewBytes     int
	DebugCaptureDetailEventLimit       int
	DebugCaptureDownloadTokenTTL       time.Duration
	DebugCaptureMaxRecordsPerProvider  int
	DebugCaptureChunkBytes             int
	DebugCaptureExportLineBytes        int

	ConfigFileUsed string
}

// serializedConfig preserves operator-facing units until every value has been
// validated. Keeping this representation private prevents MiB and seconds from
// leaking beyond the configuration boundary.
type serializedConfig struct {
	Port             string `mapstructure:"port"`
	AdminPort        string `mapstructure:"admin_port"`
	DBPath           string `mapstructure:"db_path"`
	AdminToken       string `mapstructure:"admin_token"`
	LogPath          string `mapstructure:"log_path"`
	LogMaxSizeMB     int    `mapstructure:"log_max_size_mb"`
	LogMaxKeepDays   int    `mapstructure:"log_max_keep_days"`
	LogLevel         string `mapstructure:"log_level"`
	CodexKeyringFile string `mapstructure:"codex_keyring_file"`

	DebugCaptureMemoryCeilingMiB        int64 `mapstructure:"debug_capture_memory_ceiling_mib"`
	DebugCaptureMaxActiveRecords        int64 `mapstructure:"debug_capture_max_active_records"`
	DebugCaptureMaxActiveTraces         int64 `mapstructure:"debug_capture_max_active_traces"`
	DebugCaptureMaxTransitionsPerTrace  int64 `mapstructure:"debug_capture_max_transitions_per_trace"`
	DebugCaptureMaxPendingExports       int64 `mapstructure:"debug_capture_max_pending_exports"`
	DebugCaptureMaxConcurrentDownloads  int64 `mapstructure:"debug_capture_max_concurrent_downloads"`
	DebugCaptureDetailPreviewBytes      int64 `mapstructure:"debug_capture_detail_preview_bytes"`
	DebugCaptureDetailEventLimit        int64 `mapstructure:"debug_capture_detail_event_limit"`
	DebugCaptureDownloadTokenTTLSeconds int64 `mapstructure:"debug_capture_download_token_ttl_seconds"`
	DebugCaptureMaxRecordsPerProvider   int64 `mapstructure:"debug_capture_max_records_per_provider"`
	DebugCaptureChunkBytes              int64 `mapstructure:"debug_capture_chunk_bytes"`
	DebugCaptureExportLineBytes         int64 `mapstructure:"debug_capture_export_line_bytes"`
}

// Load loads configuration from config file and/or environment variables.
// Priority (highest to lowest):
//  1. Environment variables (SWITCHA_*)
//  2. Config file (config.yaml, config.json, etc.)
//  3. Default values
func Load() (*Config, error) {
	return LoadWithPath("")
}

// LoadWithPath loads configuration from the specified config file path.
// If configPath is empty, it searches for config file in default locations.
func LoadWithPath(configPath string) (*Config, error) {
	v := viper.New()
	setDefaults(v)
	bindEnvironment(v)
	configureViperPaths(v, configPath)

	var configFileUsed string
	var fileDebugCaptureIntegers map[string]int64
	if err := v.ReadInConfig(); err != nil {
		if configPath != "" {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
		// A malformed discovered file must remain fatal; silently ignoring it
		// would make the running safety limits differ from operator intent.
		var configFileNotFoundError viper.ConfigFileNotFoundError
		if !errors.As(err, &configFileNotFoundError) {
			return nil, fmt.Errorf("failed to parse config file: %w", err)
		}
	} else {
		configFileUsed = v.ConfigFileUsed()
		fileDebugCaptureIntegers, err = decodeDebugCaptureFileIntegers(configFileUsed)
		if err != nil {
			return nil, fmt.Errorf("failed to validate config file: %w", err)
		}
	}

	var serialized serializedConfig
	if err := v.Unmarshal(&serialized); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}
	if err := decodeDebugCaptureIntegers(v, &serialized, fileDebugCaptureIntegers); err != nil {
		return nil, fmt.Errorf("failed to decode debug capture config: %w", err)
	}

	cfg, err := serialized.runtimeConfig()
	if err != nil {
		return nil, err
	}
	if cfg.AdminToken == "" {
		return nil, fmt.Errorf("%s is required (set via environment variable or config file)", EnvAdminToken)
	}
	cfg.ConfigFileUsed = configFileUsed
	return cfg, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault(KeyPort, DefaultPort)
	v.SetDefault(KeyAdminPort, DefaultAdminPort)
	v.SetDefault(KeyDBPath, DefaultDBPath)
	v.SetDefault(KeyLogPath, DefaultLogPath)
	v.SetDefault(KeyLogMaxSizeMB, DefaultLogMaxSizeMB)
	v.SetDefault(KeyLogMaxKeepDays, DefaultLogMaxKeepDays)
	v.SetDefault(KeyLogLevel, DefaultLogLevel)
	v.SetDefault(KeyDebugCaptureMemoryCeilingMiB, DefaultDebugCaptureMemoryCeilingMiB)
	v.SetDefault(KeyDebugCaptureMaxActiveRecords, DefaultDebugCaptureMaxActiveRecords)
	v.SetDefault(KeyDebugCaptureMaxActiveTraces, DefaultDebugCaptureMaxActiveTraces)
	v.SetDefault(KeyDebugCaptureMaxTransitionsPerTrace, DefaultDebugCaptureMaxTransitionsPerTrace)
	v.SetDefault(KeyDebugCaptureMaxPendingExports, DefaultDebugCaptureMaxPendingExports)
	v.SetDefault(KeyDebugCaptureMaxConcurrentDownloads, DefaultDebugCaptureMaxConcurrentDownloads)
	v.SetDefault(KeyDebugCaptureDetailPreviewBytes, DefaultDebugCaptureDetailPreviewBytes)
	v.SetDefault(KeyDebugCaptureDetailEventLimit, DefaultDebugCaptureDetailEventLimit)
	v.SetDefault(KeyDebugCaptureDownloadTokenTTLSeconds, DefaultDebugCaptureDownloadTokenTTLSeconds)
	v.SetDefault(KeyDebugCaptureMaxRecordsPerProvider, DefaultDebugCaptureMaxRecordsPerProvider)
	v.SetDefault(KeyDebugCaptureChunkBytes, DefaultDebugCaptureChunkBytes)
	v.SetDefault(KeyDebugCaptureExportLineBytes, DefaultDebugCaptureExportLineBytes)
}

func bindEnvironment(v *viper.Viper) {
	v.SetEnvPrefix(EnvPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	_ = v.BindEnv(KeyPort, EnvPort)
	_ = v.BindEnv(KeyAdminPort, EnvAdminPort)
	_ = v.BindEnv(KeyDBPath, EnvDBPath)
	_ = v.BindEnv(KeyAdminToken, EnvAdminToken)
	_ = v.BindEnv(KeyLogPath, EnvLogPath)
	_ = v.BindEnv(KeyLogMaxSizeMB, EnvLogMaxSizeMB)
	_ = v.BindEnv(KeyLogMaxKeepDays, EnvLogMaxKeepDays)
	_ = v.BindEnv(KeyLogLevel, EnvLogLevel)
	_ = v.BindEnv(KeyCodexKeyringFile, EnvCodexKeyringFile)
	_ = v.BindEnv(KeyDebugCaptureMemoryCeilingMiB, EnvDebugCaptureMemoryCeilingMiB)
	_ = v.BindEnv(KeyDebugCaptureMaxActiveRecords, EnvDebugCaptureMaxActiveRecords)
	_ = v.BindEnv(KeyDebugCaptureMaxActiveTraces, EnvDebugCaptureMaxActiveTraces)
	_ = v.BindEnv(KeyDebugCaptureMaxTransitionsPerTrace, EnvDebugCaptureMaxTransitionsPerTrace)
	_ = v.BindEnv(KeyDebugCaptureMaxPendingExports, EnvDebugCaptureMaxPendingExports)
	_ = v.BindEnv(KeyDebugCaptureMaxConcurrentDownloads, EnvDebugCaptureMaxConcurrentDownloads)
	_ = v.BindEnv(KeyDebugCaptureDetailPreviewBytes, EnvDebugCaptureDetailPreviewBytes)
	_ = v.BindEnv(KeyDebugCaptureDetailEventLimit, EnvDebugCaptureDetailEventLimit)
	_ = v.BindEnv(KeyDebugCaptureDownloadTokenTTLSeconds, EnvDebugCaptureDownloadTokenTTLSeconds)
	_ = v.BindEnv(KeyDebugCaptureMaxRecordsPerProvider, EnvDebugCaptureMaxRecordsPerProvider)
	_ = v.BindEnv(KeyDebugCaptureChunkBytes, EnvDebugCaptureChunkBytes)
	_ = v.BindEnv(KeyDebugCaptureExportLineBytes, EnvDebugCaptureExportLineBytes)
}

func (serialized serializedConfig) runtimeConfig() (*Config, error) {
	memoryCeilingBytes, err := debugCaptureMemoryCeilingBytes(serialized.DebugCaptureMemoryCeilingMiB)
	if err != nil {
		return nil, err
	}
	maxActiveRecords, err := positiveRuntimeInt(KeyDebugCaptureMaxActiveRecords, serialized.DebugCaptureMaxActiveRecords)
	if err != nil {
		return nil, err
	}
	maxActiveTraces, err := positiveRuntimeInt(KeyDebugCaptureMaxActiveTraces, serialized.DebugCaptureMaxActiveTraces)
	if err != nil {
		return nil, err
	}
	maxTransitionsPerTrace, err := positiveRuntimeInt(KeyDebugCaptureMaxTransitionsPerTrace, serialized.DebugCaptureMaxTransitionsPerTrace)
	if err != nil {
		return nil, err
	}
	maxPendingExports, err := positiveRuntimeInt(KeyDebugCaptureMaxPendingExports, serialized.DebugCaptureMaxPendingExports)
	if err != nil {
		return nil, err
	}
	maxConcurrentDownloads, err := positiveRuntimeInt(KeyDebugCaptureMaxConcurrentDownloads, serialized.DebugCaptureMaxConcurrentDownloads)
	if err != nil {
		return nil, err
	}
	detailPreviewBytes, err := positiveRuntimeInt(KeyDebugCaptureDetailPreviewBytes, serialized.DebugCaptureDetailPreviewBytes)
	if err != nil {
		return nil, err
	}
	detailEventLimit, err := positiveRuntimeInt(KeyDebugCaptureDetailEventLimit, serialized.DebugCaptureDetailEventLimit)
	if err != nil {
		return nil, err
	}
	downloadTokenTTL, err := positiveDurationSeconds(KeyDebugCaptureDownloadTokenTTLSeconds, serialized.DebugCaptureDownloadTokenTTLSeconds)
	if err != nil {
		return nil, err
	}
	maxRecordsPerProvider, err := positiveRuntimeInt(KeyDebugCaptureMaxRecordsPerProvider, serialized.DebugCaptureMaxRecordsPerProvider)
	if err != nil {
		return nil, err
	}
	chunkBytes, err := positiveRuntimeInt(KeyDebugCaptureChunkBytes, serialized.DebugCaptureChunkBytes)
	if err != nil {
		return nil, err
	}
	// The lower bound caps chunk metadata and lock amplification, while the upper
	// bound prevents tiny observations from reserving disproportionate capacity.
	if chunkBytes < MinimumDebugCaptureChunkBytes || chunkBytes > MaximumDebugCaptureChunkBytes {
		return nil, fmt.Errorf(
			"%s must be between %d and %d bytes",
			KeyDebugCaptureChunkBytes,
			MinimumDebugCaptureChunkBytes,
			MaximumDebugCaptureChunkBytes,
		)
	}
	exportLineBytes, err := positiveRuntimeInt(KeyDebugCaptureExportLineBytes, serialized.DebugCaptureExportLineBytes)
	if err != nil {
		return nil, err
	}

	return &Config{
		Port:                               serialized.Port,
		AdminPort:                          serialized.AdminPort,
		DBPath:                             serialized.DBPath,
		AdminToken:                         serialized.AdminToken,
		LogPath:                            serialized.LogPath,
		LogMaxSizeMB:                       serialized.LogMaxSizeMB,
		LogMaxKeepDays:                     serialized.LogMaxKeepDays,
		LogLevel:                           serialized.LogLevel,
		CodexKeyringFile:                   serialized.CodexKeyringFile,
		DebugCaptureMemoryCeilingBytes:     memoryCeilingBytes,
		DebugCaptureMaxActiveRecords:       maxActiveRecords,
		DebugCaptureMaxActiveTraces:        maxActiveTraces,
		DebugCaptureMaxTransitionsPerTrace: maxTransitionsPerTrace,
		DebugCaptureMaxPendingExports:      maxPendingExports,
		DebugCaptureMaxConcurrentDownloads: maxConcurrentDownloads,
		DebugCaptureDetailPreviewBytes:     detailPreviewBytes,
		DebugCaptureDetailEventLimit:       detailEventLimit,
		DebugCaptureDownloadTokenTTL:       downloadTokenTTL,
		DebugCaptureMaxRecordsPerProvider:  maxRecordsPerProvider,
		DebugCaptureChunkBytes:             chunkBytes,
		DebugCaptureExportLineBytes:        exportLineBytes,
	}, nil
}

func debugCaptureMemoryCeilingBytes(valueMiB int64) (int64, error) {
	if valueMiB < DefaultDebugCaptureSessionQuotaMiB {
		return 0, fmt.Errorf(
			"%s must be at least %d MiB because the default session quota is %d MiB",
			KeyDebugCaptureMemoryCeilingMiB,
			DefaultDebugCaptureSessionQuotaMiB,
			DefaultDebugCaptureSessionQuotaMiB,
		)
	}
	const maxInt64 = int64(^uint64(0) >> 1)
	if valueMiB > maxInt64/bytesPerMiB {
		return 0, fmt.Errorf("%s is too large to represent in bytes", KeyDebugCaptureMemoryCeilingMiB)
	}
	return valueMiB * bytesPerMiB, nil
}

func positiveRuntimeInt(key string, value int64) (int, error) {
	if value <= 0 {
		return 0, fmt.Errorf("%s must be positive", key)
	}
	converted := int(value)
	if int64(converted) != value {
		return 0, fmt.Errorf("%s is too large for this platform", key)
	}
	return converted, nil
}

func positiveDurationSeconds(key string, seconds int64) (time.Duration, error) {
	if seconds <= 0 {
		return 0, fmt.Errorf("%s must be positive", key)
	}
	const maxDuration = time.Duration(1<<63 - 1)
	if seconds > int64(maxDuration/time.Second) {
		return 0, fmt.Errorf("%s is too large to represent as a duration", key)
	}
	return time.Duration(seconds) * time.Second, nil
}
