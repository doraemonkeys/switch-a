package proxy

import proxyusage "switch-a/internal/proxy/usage"

// Token accounting moved into a dedicated subpackage because HTTP interception
// and WebSocket observation both consume the same usage semantics, while the
// proxy root package should only own transport orchestration.
type TokenUsage = proxyusage.TokenUsage
type CacheCreation = proxyusage.CacheCreation
type UsageDetailsJSON = proxyusage.UsageDetailsJSON
type Logger = proxyusage.Logger
type ZapLoggerAdapter = proxyusage.ZapLoggerAdapter
type ZapSugaredLogger = proxyusage.ZapSugaredLogger
type captureBuffer = proxyusage.CaptureBuffer

const (
	maxSSEBuffer             = proxyusage.MaxSSEBuffer
	minBufferReallocCapacity = proxyusage.MinBufferReallocCapacity
)

func newCaptureBuffer(contentLength int64) captureBuffer {
	return proxyusage.NewCaptureBuffer(contentLength)
}

func parseTokenUsageWithLogger(data []byte, logger Logger) *TokenUsage {
	return proxyusage.ParseWithLogger(data, logger)
}

func truncateForLog(data []byte, maxLen int) string {
	return proxyusage.TruncateForLog(data, maxLen)
}

func NewZapLoggerAdapter(logger ZapSugaredLogger) *ZapLoggerAdapter {
	return proxyusage.NewZapLoggerAdapter(logger)
}
