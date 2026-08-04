package websocketproxy

import tokenusage "github.com/doraemonkeys/switch-a/internal/responseanalysis/tokenusage"

// WebSocket lifecycle results keep their native usage vocabulary while parsing,
// buffering, and billing semantics remain owned by the shared tokenusage module.
type TokenUsage = tokenusage.TokenUsage
type CacheCreation = tokenusage.CacheCreation
type Logger = tokenusage.Logger
type ZapLoggerAdapter = tokenusage.ZapLoggerAdapter
type ZapSugaredLogger = tokenusage.ZapSugaredLogger

func ParseWithLogger(data []byte, logger Logger) *TokenUsage {
	return tokenusage.ParseWithLogger(data, logger)
}

func NewZapLoggerAdapter(logger ZapSugaredLogger) *ZapLoggerAdapter {
	return tokenusage.NewZapLoggerAdapter(logger)
}
