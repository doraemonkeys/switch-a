package requestcapture

import "github.com/doraemonkeys/switch-a/internal/requestcapture/redaction"

// IngressRecorder borrows identity only. Session stop invalidates the handle,
// so capture cannot keep replay storage alive or interfere with upload cleanup.
type IngressRecorder struct{ gateway GatewayRecorder }

func (r GatewayRecorder) BeginIngress(head IngressHead) IngressRecorder {
	access := r.acquire()
	defer access.release()
	g := access.gateway
	if g == nil || g.finished || g.sharedRequestInitialized {
		return IngressRecorder{}
	}
	// Logical ownership must survive a failed metadata allocation: later quota
	// recovery cannot reinterpret the attempt's absent body as complete evidence.
	g.sharedRequestInitialized = true
	snapshot := (redaction.Sanitizer{}).Ingress(head)
	charge := estimateIngressCharge(&snapshot)
	if !access.session.reserveLocked(charge, true) {
		return IngressRecorder{}
	}
	g.charge += charge
	g.ingress = &snapshot
	g.sharedRequest, g.sharedRequestComplete = newBlobLocked(access.session)
	g.ingressBuilder = blobBuilder{value: g.sharedRequest, overflowed: !g.sharedRequestComplete}
	g.ingress.CaptureTruncated = g.ingress.CaptureTruncated || !g.sharedRequestComplete
	return IngressRecorder{gateway: r}
}

// ObserveChunk copies at most the available capture budget before returning;
// the caller retains ownership and can immediately reuse its ingestion buffer.
func (r IngressRecorder) ObserveChunk(chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	access := r.gateway.acquire()
	defer access.release()
	g := access.gateway
	if g == nil || g.finished || g.ingress == nil || g.ingress.State != "receiving" {
		return
	}
	g.ingress.ReceivedBytes += int64(len(chunk))
	g.sharedRequestExpected = g.ingress.ReceivedBytes
	if g.ingressBuilder.appendLocked(access.session, chunk) != len(chunk) {
		g.sharedRequestComplete = false
		g.ingress.CaptureTruncated = true
		g.markIngressTruncatedLocked()
	}
}

func (r IngressRecorder) FinishIngress(input IngressFinish) {
	access := r.gateway.acquire()
	defer access.release()
	g := access.gateway
	if g == nil || g.finished || g.ingress == nil || g.ingress.State != "receiving" {
		return
	}
	snapshot := (redaction.Sanitizer{}).FinishIngress(*g.ingress, input.State, input.ReceivedBytes, input.Trailers, input.Reason)
	if input.ReceivedBytes > g.ingress.ReceivedBytes {
		// Source validation can reject the tail of the last read before observers
		// see it. Preserve the actual received count without claiming full evidence.
		snapshot.CaptureTruncated = true
		g.sharedRequestComplete = false
	}
	extra := estimateIngressCharge(&snapshot) - estimateIngressCharge(g.ingress)
	if extra > 0 && !access.session.reserveLocked(extra, true) {
		snapshot.Trailers = nil
		snapshot.Reason = ""
		snapshot.CaptureTruncated = true
		extra = estimateIngressCharge(&snapshot) - estimateIngressCharge(g.ingress)
	}
	if extra > 0 {
		g.charge += extra
	} else if extra < 0 {
		access.session.releaseLocked(-extra)
		g.charge += extra
	}
	*g.ingress = snapshot
	g.sharedRequestExpected = input.ReceivedBytes
	if snapshot.CaptureTruncated {
		g.markIngressTruncatedLocked()
	}
}

func (g *gatewayState) markIngressTruncatedLocked() {
	for entry := g.entryFirst; entry != nil; entry = entry.after {
		if entry.record != nil && entry.record.protocol == ProtocolHTTP {
			entry.record.markOverflowLocked()
		}
	}
}

// ObserveFailure records source usability independently of pump completion.
// A replay disk fault can occur after a valid client EOF and must not erase it.
func (r IngressRecorder) ObserveFailure(input IngressFailure) {
	access := r.gateway.acquire()
	defer access.release()
	g := access.gateway
	if g == nil || g.finished || g.ingress == nil || g.ingressFailureObserved {
		return
	}
	g.ingressFailureObserved = true
	failure, truncated := (redaction.Sanitizer{}).IngressFailure(input)
	charge := estimateIngressFailureCharge(&failure)
	if access.session.reserveLocked(charge, true) {
		g.charge += charge
		g.ingress.SourceFailure = &failure
	} else {
		truncated = true
	}
	if truncated {
		g.ingress.CaptureTruncated = true
		g.markIngressTruncatedLocked()
	}
}
