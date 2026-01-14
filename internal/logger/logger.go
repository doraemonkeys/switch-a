// Package logger handles log initialization using doraemonkeys/mylog/zap.
package logger

import (
	"strings"

	"switch-a/internal/defaults"

	mylog "github.com/doraemonkeys/mylog/zap"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Default configuration values derived from centralized defaults.
const (
	DefaultLogPath     = defaults.LogPath
	DefaultMaxSizeMB   = defaults.LogMaxSizeMB
	DefaultMaxKeepDays = defaults.LogMaxKeepDays
	DefaultLogLevel    = defaults.LogLevel
)

// Config holds logger configuration options.
type Config struct {
	LogPath     string
	MaxSizeMB   int
	MaxKeepDays int
	Level       string // Log level: debug, info, warn, error
	IsDev       bool
}

func DefaultConfig() Config {
	return Config{
		LogPath:     DefaultLogPath,
		MaxSizeMB:   DefaultMaxSizeMB,
		MaxKeepDays: DefaultMaxKeepDays,
		Level:       DefaultLogLevel,
		IsDev:       false,
	}
}

// ParseLevel converts a string log level to zapcore.Level.
// Supported levels: debug, info, warn, error.
// Defaults to InfoLevel if the level string is not recognized.
func ParseLevel(level string) zapcore.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return zapcore.DebugLevel
	case "info":
		return zapcore.InfoLevel
	case "warn", "warning":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}

// New creates a new logger instance based on the provided configuration.
// In production, logs to both file and console; in development, logs to console only.
func New(cfg Config) *zap.Logger {
	level := ParseLevel(cfg.Level)

	if cfg.IsDev {
		return mylog.NewBuilder().
			NoLogFile().
			Level(zapcore.DebugLevel).
			Build()
	}

	return mylog.NewBuilder().
		LogPath(cfg.LogPath).
		Level(level).
		MaxLogSize(cfg.MaxSizeMB).
		MaxKeepDays(cfg.MaxKeepDays).
		JSONFormatFile().
		Build()
}
