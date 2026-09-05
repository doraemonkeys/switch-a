package clientdisguise

import (
	"fmt"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}
func validateTransportSample(sample TransportSample) error {
	if sample.ID == "" || sample.SourceID == "" || sample.CapturedAt.IsZero() {
		return invalid("transport sample ID, source and capture time required")
	}
	if sample.TLSProfile != "" && sample.TLSProfile != "go-standard" {
		return invalid("unsupported sampled TLS adapter %q", sample.TLSProfile)
	}
	if sample.HTTPProfile != "" && sample.HTTPProfile != "go-standard" && sample.HTTPProfile != "http1" && sample.HTTPProfile != "http2" {
		return invalid("unsupported sampled HTTP adapter %q", sample.HTTPProfile)
	}
	config, err := upstreamtransport.ParseWireConfig(sample.Config)
	if err != nil {
		return invalid("transport sample: %v", err)
	}
	if (sample.HTTPProfile == "http1" || sample.HTTPProfile == "http2") && config.HTTPProtocol != "" && config.HTTPProtocol != sample.HTTPProfile {
		return invalid("HTTP sample adapter conflicts with config")
	}
	return nil
}
