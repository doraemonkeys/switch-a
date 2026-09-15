package upstreamtransport

import (
	"net/http"
	"net/http/httptrace"
	"sync/atomic"
)

// RequestDisclosure describes whether request-owned identity or continuity
// data may have crossed the upstream transport boundary. Ownership policy uses
// these facts separately from whether the client has seen a response.
type RequestDisclosure uint8

const (
	RequestDisclosureUnknown RequestDisclosure = iota
	RequestDisclosureNone
	RequestDisclosurePossible
	RequestDisclosureConfirmed
)

func (d RequestDisclosure) DefinitelyNotDisclosed() bool {
	return d == RequestDisclosureNone
}

func (d RequestDisclosure) String() string {
	switch d {
	case RequestDisclosureNone:
		return "none"
	case RequestDisclosurePossible:
		return "possible"
	case RequestDisclosureConfirmed:
		return "confirmed"
	default:
		return "unknown"
	}
}

// RequestDisclosureObservation belongs to one HTTP exchange or WebSocket dial,
// including all of its redirects and native transport retries.
type RequestDisclosureObservation struct {
	state atomic.Uint32
}

// ObserveRequestDisclosure copies the client while sharing its connection pool
// and policies. Observing inside RoundTrip keeps a custom WebSocket dialer that
// bypasses this client unknown instead of assuming it transmitted nothing.
func ObserveRequestDisclosure(client *http.Client) (*http.Client, *RequestDisclosureObservation) {
	observation := &RequestDisclosureObservation{}
	return observation.client(client), observation
}

func (o *RequestDisclosureObservation) client(client *http.Client) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	observed := *client
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	observed.Transport = disclosureRoundTripper{base: base, observation: o}
	return &observed
}

func (o *RequestDisclosureObservation) trace() *httptrace.ClientTrace {
	markPossible := func() {
		for {
			state := o.state.Load()
			if state >= uint32(RequestDisclosurePossible) || o.state.CompareAndSwap(state, uint32(RequestDisclosurePossible)) {
				return
			}
		}
	}
	return &httptrace.ClientTrace{
		// A failed header write may expose a prefix, so the first field must
		// establish possible disclosure before WroteHeaders/WroteRequest.
		WroteHeaderField: func(string, []string) { markPossible() },
		WroteHeaders:     markPossible,
		WroteRequest:     func(httptrace.WroteRequestInfo) { markPossible() },
	}
}

func (o *RequestDisclosureObservation) confirm() {
	o.state.Store(uint32(RequestDisclosureConfirmed))
}

// Result includes responses returned by a dialer even if it used its own client.
// A later failed redirect cannot erase an earlier response or possible write.
func (o *RequestDisclosureObservation) Result(responseReceived bool) RequestDisclosure {
	if responseReceived {
		return RequestDisclosureConfirmed
	}
	return RequestDisclosure(o.state.Load())
}

type disclosureRoundTripper struct {
	base        http.RoundTripper
	observation *RequestDisclosureObservation
}

func (t disclosureRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if supportsDisclosureTrace(t.base) {
		// Only net/http's trace contract can prove that a connection failure
		// preceded the request write. Silence from an opaque adapter proves nothing.
		t.observation.state.CompareAndSwap(uint32(RequestDisclosureUnknown), uint32(RequestDisclosureNone))
	}
	ctx := httptrace.WithClientTrace(request.Context(), t.observation.trace())
	response, err := t.base.RoundTrip(request.WithContext(ctx))
	if response != nil {
		t.observation.confirm()
	}
	return response, err
}

// The source adapter preserves net/http traces, including when input preparation
// fails before dispatch. Opaque third-party adapters retain unknown disclosure.
func supportsDisclosureTrace(base http.RoundTripper) bool {
	switch transport := base.(type) {
	case *http.Transport:
		return true
	case sourceRoundTripper:
		return supportsDisclosureTrace(transport.base)
	default:
		return false
	}
}

func (t disclosureRoundTripper) CloseIdleConnections() {
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
