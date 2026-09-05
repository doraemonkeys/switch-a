package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

// A redirect is a real additional body read, and a caller retry must accumulate
// on the logical operation without contaminating WebSocket payload counters.
func TestPlan10ReviewHTTPBodyReadMetricsAcrossRetryAndRedirect(t *testing.T) {
	payload := []byte("logical-request-body")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != string(payload) {
			t.Errorf("upstream body=%q error=%v", body, err)
		}
		if r.URL.Path == "/redirect" {
			w.Header().Set("Location", "/final")
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()
	tracker := &LiveBytesTracker{}
	source := &ingressUpload{ingress: newTestIngress(t, payload), tracker: tracker}
	transport := NewTransport(TransportConfig{})
	defer transport.CloseIdleConnections()
	for attempt := range 2 {
		request, err := BuildUpstreamRequest(t.Context(), http.MethodPost, upstream.URL+"/redirect", source, httptest.NewRequest(http.MethodPost, "http://client/upload", nil))
		if err != nil {
			t.Fatal(err)
		}
		if got, want := tracker.UpstreamBodyReadBytes.Load(), int64(attempt*2*len(payload)); got != want {
			t.Fatalf("request construction counted reads: got=%d want=%d", got, want)
		}
		response, _, err := transport.FetchUpstream(t.Context(), request, upstreamtransport.ExecutionOptions{})
		if err != nil {
			t.Fatal(err)
		}
		body, err := response.TakeBody()
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, body)
		_ = body.Close()
		_ = request.Body.Close()
		if got, want := tracker.UpstreamBodyReadBytes.Load(), int64((attempt+1)*2*len(payload)); got != want {
			t.Fatalf("retry/redirect body reads: got=%d want=%d", got, want)
		}
	}
	if got := tracker.BytesSent.Load(); got != 0 {
		t.Fatalf("HTTP mutated WebSocket bytes_sent=%d", got)
	}
}
