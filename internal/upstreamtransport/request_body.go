package upstreamtransport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
)

// BodySource separates logical input retention from an individual transmission.
// Close on an opened reader must interrupt and join pending reads without closing
// the logical source; redirects and retries may reopen a healthy live source.
type BodySource interface {
	Open() (io.ReadCloser, error)
	Framing() BodyFraming
	Trailers() http.Header
}

// BodyFraming preserves the inbound declaration independently of retained bytes.
type BodyFraming struct {
	ProtocolMajor int
	ContentLength int64
	HasBody       bool
	TrailerKeys   []string
	Complete      bool
}

type sourceBody struct {
	source BodySource
	mu     sync.Mutex
	active io.ReadCloser
}

func (b *sourceBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	if b.active == nil {
		reader, err := b.source.Open()
		if err != nil {
			b.mu.Unlock()
			return 0, err
		}
		b.active = reader
	}
	active := b.active
	b.mu.Unlock()
	return active.Read(p)
}

func (b *sourceBody) Close() error {
	b.mu.Lock()
	active := b.active
	b.active = nil
	b.mu.Unlock()
	if active != nil {
		return active.Close()
	}
	return nil
}

func (b *sourceBody) install(active io.ReadCloser) {
	b.mu.Lock()
	b.active = active
	b.mu.Unlock()
}

// bodyTransmission owns the only mutable trailer map used by this transmission.
// net/http inspects it after EOF, so publishing from Read keeps ownership ordered.
type bodyTransmission struct {
	io.ReadCloser
	source      BodySource
	trailer     http.Header
	finalized   bool
	readFailure error
	closeOnce   sync.Once
	closeMu     sync.Mutex
	closed      bool
	onClosed    []func()
	bytesRead   atomic.Int64
	closeErr    error
}

func (b *bodyTransmission) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.bytesRead.Add(int64(n))
	b.closeMu.Lock()
	readFailure := b.readFailure
	b.closeMu.Unlock()
	if err != nil && readFailure != nil {
		return n, readFailure
	}
	if err == io.EOF && !b.finalized {
		b.finalized = true
		for key, values := range b.source.Trailers() {
			b.trailer[http.CanonicalHeaderKey(key)] = append([]string(nil), values...)
		}
	}
	return n, err
}

func (b *bodyTransmission) abortRead(reason error) {
	b.closeMu.Lock()
	b.readFailure = reason
	b.closeMu.Unlock()
	_ = b.Close()
}

func (b *bodyTransmission) Close() error {
	b.closeOnce.Do(func() {
		b.closeErr = b.ReadCloser.Close()
		b.closeMu.Lock()
		b.closed = true
		onClosed := b.onClosed
		b.onClosed = nil
		b.closeMu.Unlock()
		for _, callback := range onClosed {
			callback()
		}
	})
	return b.closeErr
}

func (b *bodyTransmission) afterClose(callback func()) {
	b.closeMu.Lock()
	if b.closed {
		b.closeMu.Unlock()
		callback()
		return
	}
	b.onClosed = append(b.onClosed, callback)
	b.closeMu.Unlock()
}

func projectBody(request *http.Request, body *sourceBody) error {
	framing := body.source.Framing()
	request.GetBody = func() (io.ReadCloser, error) { return nil, errTransmissionReopen }
	request.Header.Del("Content-Length")
	request.Header.Del("Transfer-Encoding")
	request.Header.Del("Trailer")
	request.ContentLength = framing.ContentLength
	request.TransferEncoding = nil
	request.Trailer = make(http.Header)
	for _, key := range framing.TrailerKeys {
		request.Trailer[http.CanonicalHeaderKey(key)] = nil
	}
	if framing.Complete {
		for key := range body.source.Trailers() {
			request.Trailer[http.CanonicalHeaderKey(key)] = nil
		}
	}
	// HTTP/2 may introduce trailer keys at EOF. Reserving chunked framing for a
	// still-receiving H2 source keeps those keys representable on HTTP/1 hops.
	if len(request.Trailer) > 0 || framing.ProtocolMajor >= 2 && !framing.Complete {
		request.TransferEncoding = []string{"chunked"}
	}
	if !framing.HasBody && request.ContentLength == 0 && len(request.TransferEncoding) == 0 {
		request.Body = http.NoBody
		return nil
	}
	reader, err := body.source.Open()
	if err != nil {
		return err
	}
	transmission := &bodyTransmission{ReadCloser: reader, source: body.source, trailer: request.Trailer}
	request.Body = transmission
	body.install(transmission)
	return nil
}

// The native protocol engine owns eligibility; its body factory hands an
// eligible reopen back here before it can shallow-copy a mutable trailer map.
var errTransmissionReopen = errors.New("upstream transmission reopen requested")

type sourceRoundTripper struct {
	base     http.RoundTripper
	observer *executionObserver
}

func (t sourceRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	hop := t.observer.hops.Add(1)
	body, ok := request.Body.(*sourceBody)
	if !ok {
		event := t.observer.newTransmission(hop, 0)
		t.observer.emit(event, TransmissionOpened, nil, nil)
		response, err := t.base.RoundTrip(request)
		if response != nil {
			t.observer.disclosure.confirm()
		}
		t.observer.emit(event, TransmissionClosed, nil, err)
		return response, err
	}
	for reopens := 0; ; reopens++ {
		if err := request.Context().Err(); err != nil {
			return nil, err
		}
		transmission := request.Clone(request.Context())
		if err := projectBody(transmission, body); err != nil {
			return nil, err
		}
		event := t.observer.newTransmission(hop, reopens)
		reader, _ := transmission.Body.(*bodyTransmission)
		t.observer.emit(event, TransmissionOpened, reader, nil)
		if reader != nil {
			reader.afterClose(func() { t.observer.emit(event, TransmissionClosed, reader, nil) })
		}
		observation := newTransmissionObservation()
		transmission = transmission.WithContext(observation.context(transmission.Context(), transmission.Body))
		response, err := t.base.RoundTrip(transmission)
		if response != nil {
			t.observer.disclosure.confirm()
		}
		if reader == nil {
			t.observer.emit(event, TransmissionClosed, nil, err)
		}
		if err == nil {
			return response, nil
		}
		// RoundTripper may close asynchronously. Join before opening the replacement
		// so an old reader cannot race new framing or consume the live tail.
		_ = transmission.Body.Close()
		// A converter failure is terminal even if the protocol engine concurrently
		// requests a native reopen after seeing a connection failure.
		terminalConversion := false
		if source, ok := body.source.(interface{ terminalError() error }); ok {
			if failure := source.terminalError(); failure != nil {
				err = failure
				terminalConversion = true
			}
		}
		event.RetryEligible = !terminalConversion && errors.Is(err, errTransmissionReopen)
		if !event.RetryEligible {
			t.observer.emit(event, TransmissionRetryDecision, reader, err)
			return response, err
		}
		err = waitNativeReopen(request.Context(), observation.isHTTP2(), reopens)
		if err != nil {
			event.RetryEligible = false
		}
		t.observer.emit(event, TransmissionRetryDecision, reader, err)
		if err != nil {
			return nil, err
		}
	}
}

func requestBodySource(request *http.Request) *sourceBody {
	if request == nil {
		return nil
	}
	body, _ := request.Body.(*sourceBody)
	return body
}

func prepareSourceRequest(ctx context.Context, method, upstreamURL string, source BodySource) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, upstreamURL, nil)
	if err != nil {
		return nil, err
	}
	if source == nil {
		return request, nil
	}
	framing := source.Framing()
	if !framing.HasBody && framing.ContentLength == 0 && len(framing.TrailerKeys) == 0 && (framing.ProtocolMajor < 2 || framing.Complete) && len(source.Trailers()) == 0 {
		request.Body = http.NoBody
		return request, nil
	}
	body := &sourceBody{source: source}
	request.Body = body
	request.ContentLength = framing.ContentLength
	request.GetBody = func() (io.ReadCloser, error) { return body, nil }
	return request, nil
}
