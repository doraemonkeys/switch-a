package upstreamtransport

import (
	"sync/atomic"
)

type TransmissionEventKind string

const (
	TransmissionOpened        TransmissionEventKind = "opened"
	TransmissionClosed        TransmissionEventKind = "closed"
	TransmissionRetryDecision TransmissionEventKind = "retry_decision"
)

type TransmissionReopenReason string

const (
	TransmissionInitial     TransmissionReopenReason = "initial"
	TransmissionRedirect    TransmissionReopenReason = "redirect"
	TransmissionNativeRetry TransmissionReopenReason = "native_transport_retry"
)

// TransmissionEvent describes transport-owned execution. The caller binds its
// operation/attempt identifiers in Observe, keeping transport independent of logs.
type TransmissionEvent struct {
	Kind              TransmissionEventKind
	TransmissionIndex int64
	HopIndex          int64
	ReopenReason      TransmissionReopenReason
	RetryEligible     bool
	PreviousReopens   int
	BodyReadBytes     int64
	Disclosure        RequestDisclosure
	Err               error
}

type executionObserver struct {
	observe       func(TransmissionEvent)
	disclosure    *requestDisclosureTracker
	hops          atomic.Int64
	transmissions atomic.Int64
}

func (o *executionObserver) newTransmission(hop int64, reopens int) TransmissionEvent {
	reason := TransmissionInitial
	if reopens > 0 {
		reason = TransmissionNativeRetry
	} else if hop > 1 {
		reason = TransmissionRedirect
	}
	return TransmissionEvent{TransmissionIndex: o.transmissions.Add(1), HopIndex: hop, ReopenReason: reason, PreviousReopens: reopens}
}

func (o *executionObserver) emit(event TransmissionEvent, kind TransmissionEventKind, body *bodyTransmission, err error) {
	if o.observe == nil {
		return
	}
	event.Kind = kind
	event.Err = err
	event.Disclosure = o.disclosure.disclosure(false)
	if body != nil {
		event.BodyReadBytes = body.bytesRead.Load()
	}
	o.observe(event)
}
