package providercookie

import "strings"

const MaxOperationIDBytes = 256

type OperationID string

func NewOperationID(value string) (OperationID, error) {
	if value == "" || value != strings.TrimSpace(value) || len(value) > MaxOperationIDBytes {
		return "", &ConfigurationError{Field: "operation_id", Reason: "must be canonical and within the size limit"}
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return "", &ConfigurationError{Field: "operation_id", Reason: "must not contain control characters"}
		}
	}
	return OperationID(value), nil
}

type TraceEvent struct {
	OperationID OperationID
	Milestone   string
	Decision    string
	Reason      string
	Count       int
	Rejected    int
	Evicted     int
}

// TraceSink receives deliberately secret-free workflow decisions. Integrators
// can bridge this narrow contract to zap without giving the Cookie domain a
// logger dependency or an opportunity to format sensitive values.
type TraceSink interface {
	RecordProviderCookieTrace(TraceEvent)
}

type TraceSinkFunc func(TraceEvent)

func (f TraceSinkFunc) RecordProviderCookieTrace(event TraceEvent) { f(event) }

type discardTrace struct{}

func (discardTrace) RecordProviderCookieTrace(TraceEvent) {}
