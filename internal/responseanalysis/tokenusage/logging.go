package tokenusage

// Logger interface for debug logging during parsing.
type Logger interface {
	Debug(msg string, keysAndValues ...any)
}

// ZapLoggerAdapter adapts *zap.SugaredLogger to the Logger interface.
// This allows the token parsing code to use zap for structured logging.
type ZapLoggerAdapter struct {
	logger ZapSugaredLogger
}

// ZapSugaredLogger defines the minimal interface needed from *zap.SugaredLogger.
// Using an interface allows for easier testing and decoupling.
type ZapSugaredLogger interface {
	// Debugw logs a message with key-value pairs at debug level.
	Debugw(msg string, keysAndValues ...any)
}

// NewZapLoggerAdapter creates a new adapter for a zap sugared logger.
func NewZapLoggerAdapter(logger ZapSugaredLogger) *ZapLoggerAdapter {
	if logger == nil {
		return nil
	}
	return &ZapLoggerAdapter{logger: logger}
}

// Debug implements the Logger interface for ZapLoggerAdapter.
func (a *ZapLoggerAdapter) Debug(msg string, keysAndValues ...any) {
	if a == nil || a.logger == nil {
		return
	}
	// Use Debugw to log message with key-value pairs
	a.logger.Debugw(msg, keysAndValues...)
}
