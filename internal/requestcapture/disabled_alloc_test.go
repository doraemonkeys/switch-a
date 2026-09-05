package requestcapture

import (
	"net/http"
	"testing"
)

type captureProbe interface {
	Enabled() bool
	BeginGateway(GatewayStart) GatewayRecorder
}

func TestDisabledPathAllocs(t *testing.T) {
	manager := newTestManager(t, nil)
	payload := make([]byte, 1<<20)
	input := RawHTTPStart{
		Attempt: AttemptMetadata{Provider: ProviderIdentity{ID: "provider"}},
		URL:     testParsedURL("https://example.test"),
		Request: RawRequest{
			Method:  http.MethodPost,
			Headers: http.Header{"X-Test": {"value"}},
			Body:    payload,
		},
	}
	allocations := testing.AllocsPerRun(10_000, func() {
		gateway := manager.BeginGateway(GatewayStart{})
		ingress := gateway.BeginIngress(IngressHead{Protocol: "HTTP/2.0", ContentLength: -1})
		ingress.ObserveChunk(payload)
		ingress.FinishIngress(IngressFinish{State: "complete", ReceivedBytes: int64(len(payload))})
		recorder := gateway.BeginHTTP(input)
		recorder.ObserveUpstream(payload)
		recorder.ObserveClientWrite(len(payload))
		recorder.Finish(Outcome{})
		gateway.Finish(GatewayOutcome{})
	})
	if allocations != 0 {
		t.Fatalf("disabled probe allocations = %f, want 0", allocations)
	}

	var consumer captureProbe = manager
	interfaceAllocations := testing.AllocsPerRun(10_000, func() {
		if consumer.Enabled() {
			panic("disabled manager became enabled")
		}
	})
	if interfaceAllocations != 0 {
		t.Fatalf("disabled interface check allocations = %f, want 0", interfaceAllocations)
	}
	if manager.Status().ProcessMemory.ChargedBytes != 0 {
		t.Fatal("disabled probe retained memory")
	}
}

func BenchmarkDisabledPath(b *testing.B) {
	manager, err := NewManager(Config{})
	if err != nil {
		b.Fatal(err)
	}
	payload := make([]byte, 1<<20)
	input := RawHTTPStart{
		Request: RawRequest{Body: payload},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		gateway := manager.BeginGateway(GatewayStart{})
		recorder := gateway.BeginHTTP(input)
		recorder.ObserveUpstream(payload)
	}
}
