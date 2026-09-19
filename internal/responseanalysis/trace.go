package responseanalysis

import "go.uber.org/zap"

type LogTrace struct{ logger *zap.Logger }

func NewLogTrace(logger *zap.Logger) LogTrace {
	return LogTrace{logger: logger}
}

func (t LogTrace) Trace(event TraceEvent) {
	log := t.logger.Debug
	if event.AnalysisFailure != "" {
		log = t.logger.Warn
	}
	log(event.Name,
		zap.String("operation_id", event.OperationID),
		zap.String("state", string(event.State)),
		zap.String("reason", string(event.Reason)),
		zap.String("analysis_failure", string(event.AnalysisFailure)),
		zap.Int64("upstream_bytes_read", event.UpstreamBytesRead),
		zap.Int64("client_bytes_written", event.ClientBytesWritten),
		zap.Int("request_bytes", event.RequestBytes),
		zap.Int("peak_request_bytes", event.PeakRequestBytes),
		zap.Int("probe_memory_limit", event.ProbeMemoryLimit),
		zap.Int("process_bytes", event.ProcessBytes),
		zap.Int("process_memory_limit", event.ProcessMemoryLimit),
	)
}
