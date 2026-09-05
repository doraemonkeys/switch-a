package requestcapture

import "testing"

// Metadata pressure can clear between ingress start and provider selection. A
// missed logical capture must not become an apparently complete empty request.
func TestPlan10ReviewIngressAdmissionFailureRemainsTruncated(t *testing.T) {
	manager := newTestManager(t, nil)
	session := startTestSession(t, manager, 4, 1<<20, "selected")
	gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "head-budget-recovery"})
	restore := constrainAdditionalCapacity(manager.active.Load(), 0)
	ingress := gateway.BeginIngress(IngressHead{Protocol: "HTTP/1.1", ContentLength: 7})
	restore()
	ingress.ObserveChunk([]byte("payload"))
	ingress.FinishIngress(IngressFinish{State: "complete", ReceivedBytes: 7})
	recorder := beginIngressAttempt(gateway)
	completeHTTP(recorder, nil)
	gateway.Finish(GatewayOutcome{})
	detail, err := readRecordDetailForTest(t, manager, session.SessionID, recorder.ID(), 64)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Summary.CaptureCompletion != CaptureCompletionOverflowed {
		t.Fatalf("lost logical request reported as complete: completion=%q ingress=%+v body=%+v",
			detail.Summary.CaptureCompletion, detail.HTTP.Request.Ingress, detail.HTTP.RequestBody)
	}
}
