// Package logger handles log initialization using doraemonkeys/mylog/zap.
package logger

import (
	"go.uber.org/zap"
)

// New creates a new logger instance.
// In production, logs to file; in development, logs to console only.
func New(isDev bool) *zap.Logger {
	// TODO: Implement using doraemonkeys/mylog/zap
	// Production:
	//   logger := mylog.NewBuilder().
	//     LogPath("./logs/switch-a.log").
	//     Level(zapcore.InfoLevel).
	//     MaxLogSize(100).
	//     MaxKeepDays(7).
	//     JSONFormatFile().
	//     Build()
	//
	// Development:
	//   logger := mylog.NewBuilder().
	//     NoLogFile().
	//     Level(zapcore.DebugLevel).
	//     Build()

	if isDev {
		logger, _ := zap.NewDevelopment()
		return logger
	}
	logger, _ := zap.NewProduction()
	return logger
}
