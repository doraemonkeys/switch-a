package responseanalysis

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestLogTracePreservesAnalysisFailureAndMemoryContext(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	trace := NewLogTrace(zap.New(core))
	event := TraceEvent{
		Name: "response_analysis.stopped", OperationID: "request/attempt/1",
		State: StateForwarding, Reason: BoundaryProcessMemoryExhausted,
		AnalysisFailure: BoundaryProcessMemoryExhausted,
		RequestBytes:    100, PeakRequestBytes: 200, ProbeMemoryLimit: DefaultProbeMemoryLimit,
		ProcessBytes: 1000, ProcessMemoryLimit: 1000,
		UpstreamBytesRead: 300, ClientBytesWritten: 300,
	}
	trace.Trace(event)
	entry := logs.All()[0]
	fields := entry.ContextMap()
	if entry.Level != zap.WarnLevel || entry.Message != event.Name ||
		fields["operation_id"] != event.OperationID || fields["analysis_failure"] != string(event.AnalysisFailure) ||
		fields["peak_request_bytes"] != int64(200) || fields["probe_memory_limit"] != int64(DefaultProbeMemoryLimit) ||
		fields["process_memory_limit"] != int64(1000) {
		t.Fatalf("trace lost diagnostic context: %+v", entry)
	}
	event.AnalysisFailure = ""
	trace.Trace(event)
	if logs.All()[1].Level != zap.DebugLevel {
		t.Fatal("normal trace is not debug level")
	}
}
