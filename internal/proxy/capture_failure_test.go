package proxy

import (
	"bytes"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/requestcapture/capturebridge"
)

const (
	hostileCaptureAllocationBytes = 1 << 20
	hostileCaptureDeadline        = 250 * time.Millisecond
)

type proxyHostileCaptureError struct {
	calls atomic.Int32
	block <-chan struct{}
}

func (e *proxyHostileCaptureError) attack() {
	e.calls.Add(1)
	_ = make([]byte, hostileCaptureAllocationBytes)
	<-e.block
	panic("hostile capture error method invoked")
}

func (e *proxyHostileCaptureError) Error() string { e.attack(); return "" }
func (e *proxyHostileCaptureError) As(any) bool   { e.attack(); return false }
func (e *proxyHostileCaptureError) Is(error) bool { e.attack(); return false }
func (e *proxyHostileCaptureError) Unwrap() error { e.attack(); return nil }
func (e *proxyHostileCaptureError) Timeout() bool { e.attack(); return false }

type proxyHostileReadCloser struct {
	payload []byte
	err     error
}

func (r proxyHostileReadCloser) Read(destination []byte) (int, error) {
	return copy(destination, r.payload), r.err
}

func (proxyHostileReadCloser) Close() error { return nil }

type proxyUncomparableCaptureError []byte

func (proxyUncomparableCaptureError) Error() string {
	panic("uncomparable capture error string invoked")
}

type proxyCaptureReadResult struct {
	payload [16]byte
	n       int
	err     error
}

func readProxyCaptureBody(body io.ReadCloser) proxyCaptureReadResult {
	var result proxyCaptureReadResult
	result.n, result.err = body.Read(result.payload[:])
	return result
}

func TestCaptureResponseBodyDoesNotInvokeHostileErrorHooks(t *testing.T) {
	blocked := make(chan struct{})
	hostile := &proxyHostileCaptureError{block: blocked}
	finished := make(chan struct {
		raw      proxyCaptureReadResult
		captured proxyCaptureReadResult
	}, 1)
	go func() {
		raw := readProxyCaptureBody(proxyHostileReadCloser{payload: []byte("raw"), err: hostile})
		captured, _ := capturebridge.WrapHTTPResponseBody(
			proxyHostileReadCloser{payload: []byte("raw"), err: hostile},
			requestcapture.Recorder{},
			-1,
		)
		capturedResult := readProxyCaptureBody(captured)
		finished <- struct {
			raw      proxyCaptureReadResult
			captured proxyCaptureReadResult
		}{raw: raw, captured: capturedResult}
	}()

	select {
	case got := <-finished:
		if got.raw.n != got.captured.n ||
			!bytes.Equal(got.raw.payload[:got.raw.n], got.captured.payload[:got.captured.n]) ||
			got.raw.err != hostile || got.captured.err != hostile {
			t.Fatalf(
				"capture changed reader result: raw=(%q,%T) captured=(%q,%T)",
				got.raw.payload[:got.raw.n],
				got.raw.err,
				got.captured.payload[:got.captured.n],
				got.captured.err,
			)
		}
	case <-time.After(hostileCaptureDeadline):
		close(blocked)
		t.Fatal("capture response wrapper invoked hostile Is/Unwrap hooks")
	}
	if calls := hostile.calls.Load(); calls != 0 {
		t.Fatalf("hostile error method calls = %d, want 0", calls)
	}

	uncomparable := proxyUncomparableCaptureError("slice-backed")
	raw := readProxyCaptureBody(proxyHostileReadCloser{payload: []byte("bytes"), err: uncomparable})
	captured, _ := capturebridge.WrapHTTPResponseBody(
		proxyHostileReadCloser{payload: []byte("bytes"), err: uncomparable},
		requestcapture.Recorder{},
		-1,
	)
	capturedResult := readProxyCaptureBody(captured)
	rawError, rawOK := raw.err.(proxyUncomparableCaptureError)
	capturedError, capturedOK := capturedResult.err.(proxyUncomparableCaptureError)
	if raw.n != capturedResult.n ||
		!bytes.Equal(raw.payload[:raw.n], capturedResult.payload[:capturedResult.n]) ||
		!rawOK || !capturedOK || !bytes.Equal(rawError, capturedError) {
		t.Fatalf(
			"capture changed uncomparable reader result: raw=(%q,%T) captured=(%q,%T)",
			raw.payload[:raw.n],
			raw.err,
			capturedResult.payload[:capturedResult.n],
			capturedResult.err,
		)
	}
}
