// Package logger handles log initialization using doraemonkeys/mylog/zap.
package logger

import (
	mylog "github.com/doraemonkeys/mylog/zap"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Default configuration values.
const (
	DefaultLogPath     = "./logs/switch-a.log"
	DefaultMaxSizeMB   = 100
	DefaultMaxKeepDays = 7
)

// Config holds logger configuration options.
type Config struct {
	LogPath     string
	MaxSizeMB   int
	MaxKeepDays int
	IsDev       bool
}

// DefaultConfig returns the default logger configuration.
func DefaultConfig() Config {
	return Config{
		LogPath:     DefaultLogPath,
		MaxSizeMB:   DefaultMaxSizeMB,
		MaxKeepDays: DefaultMaxKeepDays,
		IsDev:       false,
	}
}

// New creates a new logger instance based on the provided configuration.
// In production, logs to both file and console; in development, logs to console only.
func New(cfg Config) *zap.Logger {
	if cfg.IsDev {
		return mylog.NewBuilder().
			NoLogFile().
			Level(zapcore.DebugLevel).
			Build()
	}

	return mylog.NewBuilder().
		LogPath(cfg.LogPath).
		Level(zapcore.InfoLevel).
		MaxLogSize(cfg.MaxSizeMB).
		MaxKeepDays(cfg.MaxKeepDays).
		JSONFormatFile().
		Build()
}
