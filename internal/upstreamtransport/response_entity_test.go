package upstreamtransport

import (
	"context"
	"crypto/tls"
	"net/http"
	"reflect"
	"testing"
)

func TestResponseEntitySemanticsPreserveBodylessMetadata(t *testing.T) {
	for _, scenario := range []struct {
		method  string
		status  int
		allowed bool
	}{
		{http.MethodHead, http.StatusOK, false}, {http.MethodGet, http.StatusNotModified, false}, {http.MethodGet, http.StatusNoContent, false}, {http.MethodGet, http.StatusResetContent, false}, {http.MethodGet, http.StatusEarlyHints, false}, {http.MethodConnect, http.StatusOK, false}, {http.MethodConnect, http.StatusBadGateway, true}, {http.MethodPost, http.StatusCreated, true}, {"", 0, true},
	} {
		original := ResponseHead{RequestMethod: scenario.method, StatusCode: scenario.status, ContentLength: 128, Header: http.Header{"Content-Type": {"text/event-stream"}, "Content-Encoding": {"gzip"}, "Content-Length": {"128"}, "Etag": {"representation"}}}
		if original.AllowsBody() != scenario.allowed {
			t.Fatalf("%+v", scenario)
		}
		if scenario.allowed {
			continue
		}
		derived := DerivedResponseHead(original)
		if !reflect.DeepEqual(original, derived) {
			t.Fatalf("bodyless representation changed: %+v", derived)
		}
		normalized, err := NormalizeEventStream(original, http.NoBody)
		if err != nil || normalized.Body != http.NoBody || normalized.Transformed || !reflect.DeepEqual(normalized.Head, original) {
			t.Fatal(normalized, err)
		}
	}
	transport := NewWithRoundTripper(conversionRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}, "Content-Encoding": {"gzip"}}, Body: http.NoBody, ContentLength: 128}, nil
	}))
	request, err := http.NewRequest(http.MethodHead, "http://example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, _, err := transport.Fetch(context.Background(), request, ExecutionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	head, body, err := response.Take()
	if err != nil {
		t.Fatal(err)
	}
	_ = body.Close()
	if head.RequestMethod != http.MethodHead || head.AllowsBody() {
		t.Fatal(head)
	}
}

func TestPoolNormalizesTLSSetOrderButRetainsALPNOrder(t *testing.T) {
	pool := NewPool()
	defer pool.CloseIdleConnections()
	firstSample := WireConfig{CipherSuites: []uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256, tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384}, CurvePreferences: []uint16{uint16(tls.CurveP256), uint16(tls.X25519)}}
	first, err := pool.Get(Config{}, firstSample)
	if err != nil {
		t.Fatal(err)
	}
	second, err := pool.Get(Config{}, WireConfig{CipherSuites: []uint16{tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384, tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256, tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256}, CurvePreferences: []uint16{uint16(tls.X25519), uint16(tls.CurveP256), uint16(tls.X25519)}})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("TLS sets allocated separate pools")
	}
	firstSample.ALPN = []string{"h2", "http/1.1"}
	first, err = pool.Get(Config{}, firstSample)
	if err != nil {
		t.Fatal(err)
	}
	firstSample.ALPN = []string{"http/1.1", "h2"}
	second, err = pool.Get(Config{}, firstSample)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("ordered ALPN was treated as a set")
	}
}
