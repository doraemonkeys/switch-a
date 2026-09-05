package upstreamtransport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestTransmissionObserverSeparatesRedirectAndNativeReopen(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{false: "redirect", true: "native retry"}[native], func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/warm" {
					_, _ = io.WriteString(w, "warm")
					return
				}
				_, _ = io.Copy(io.Discard, r.Body)
				calls++
				if calls == 1 {
					if native {
						connection, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						_ = connection.Close()
						return
					}
					w.Header().Set("Location", "/next")
					w.WriteHeader(http.StatusTemporaryRedirect)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			transport := New(Config{})
			defer transport.CloseIdleConnections()
			if native {
				request, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/warm", nil)
				response, _, err := transport.Fetch(t.Context(), request, ExecutionOptions{})
				if err != nil {
					t.Fatal(err)
				}
				closeResponse(t, response)
			}
			var mu sync.Mutex
			var events []TransmissionEvent
			observer := func(event TransmissionEvent) { mu.Lock(); events = append(events, event); mu.Unlock() }
			source := testBodySource([]byte("wire"))
			request, err := BuildRequest(t.Context(), http.MethodGet, server.URL+"/start", source, httptest.NewRequest(http.MethodGet, "http://gateway.test", nil))
			if err != nil {
				t.Fatal(err)
			}
			response, _, err := transport.Fetch(t.Context(), request, ExecutionOptions{Observe: observer})
			if err != nil {
				t.Fatal(err)
			}
			closeResponse(t, response)
			_ = request.Body.Close()
			mu.Lock()
			snapshot := append([]TransmissionEvent(nil), events...)
			mu.Unlock()
			var opened, closed, decisions []TransmissionEvent
			for _, event := range snapshot {
				switch event.Kind {
				case TransmissionOpened:
					opened = append(opened, event)
				case TransmissionClosed:
					closed = append(closed, event)
				case TransmissionRetryDecision:
					decisions = append(decisions, event)
				}
			}
			if len(opened) != 2 || len(closed) != 2 {
				t.Fatalf("events=%+v", snapshot)
			}
			if opened[0].TransmissionIndex != 1 || opened[0].HopIndex != 1 || opened[0].ReopenReason != TransmissionInitial || opened[1].TransmissionIndex != 2 {
				t.Fatalf("opened=%+v", opened)
			}
			for _, event := range closed {
				if event.BodyReadBytes != 4 {
					t.Fatalf("closed=%+v", closed)
				}
			}
			if native {
				if opened[1].HopIndex != 1 || opened[1].ReopenReason != TransmissionNativeRetry || len(decisions) != 1 || !decisions[0].RetryEligible {
					t.Fatalf("events=%+v", snapshot)
				}
				if decisions[0].Disclosure.DefinitelyNotDisclosed() {
					t.Fatalf("lost disclosure: %+v", decisions[0])
				}
			} else if opened[1].HopIndex != 2 || opened[1].ReopenReason != TransmissionRedirect || len(decisions) != 0 || opened[1].Disclosure != RequestDisclosureConfirmed {
				t.Fatalf("events=%+v", snapshot)
			}
		})
	}
}

func TestNativeHTTP2ReopenBudgetAndCancellation(t *testing.T) {
	if err := waitNativeReopen(t.Context(), false, maxHTTP2Reopens+1); err != nil {
		t.Fatal(err)
	}
	if err := waitNativeReopen(t.Context(), true, 0); err != nil {
		t.Fatal(err)
	}
	if err := waitNativeReopen(t.Context(), true, maxHTTP2Reopens); !errors.Is(err, ErrReopenLimit) {
		t.Fatalf("limit=%v", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := waitNativeReopen(canceled, true, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait=%v", err)
	}
	if err := waitReopenDelay(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	if got := http2ReopenDelay(1, 0); got != time.Second {
		t.Fatalf("first delayed reopen=%v", got)
	}
	if got := http2ReopenDelay(6, 0); got != 32*time.Second {
		t.Fatalf("last delayed reopen=%v", got)
	}
	if got := http2ReopenDelay(6, 0.99); got != 35*time.Second {
		t.Fatalf("native jitter=%v", got)
	}
}

func TestInjectedTransportKeepsSourceProjectionAndUnknownDisclosure(t *testing.T) {
	var nilRoundTripper http.RoundTripper
	if NewWithRoundTripper(nilRoundTripper).followClient.Transport != http.DefaultTransport {
		t.Fatal("nil transport did not select default")
	}
	injected := &recordingRoundTripper{}
	transport := NewWithRoundTripper(injected)
	transport.CloseIdleConnections()
	if injected.closed.Load() != 1 {
		t.Fatalf("closed=%d", injected.closed.Load())
	}
}
