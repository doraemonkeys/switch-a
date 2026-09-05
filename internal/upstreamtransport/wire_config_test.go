package upstreamtransport

import (
	"crypto/tls"
	"net/http"
	"reflect"
	"testing"
	"time"
)

func TestSampledTransportValidationAndPoolIsolation(t *testing.T) {
	valid := []string{"", " {}", `{"tls_min_version":771,"tls_max_version":772,"cipher_suites":[49199],"curve_preferences":[29,23],"alpn":["http/1.1"],"http_protocol":"http1"}`, `{"http_protocol":"http2","alpn":["h2"]}`, `{"tls_min_version":769,"tls_max_version":770}`}
	pool := NewPool()
	defer pool.CloseIdleConnections()
	for _, raw := range valid {
		config, err := ParseWireConfig([]byte(raw))
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		transport, err := pool.Get(Config{}, config)
		if err != nil {
			t.Fatal(err)
		}
		again, err := pool.Get(Config{}, config)
		if err != nil || again != transport {
			t.Fatal("same actual configuration lost pool", err)
		}
		if transport.WebSocketClient() == nil || transport.WebSocketClient().Transport != transport.followClient.Transport {
			t.Fatal("WS transport mismatch")
		}
		if !transport.followClient.Transport.(*http.Transport).DisableCompression {
			t.Fatal("sample enabled implicit encoding")
		}
	}
	empty, _ := pool.Get(Config{}, WireConfig{})
	normalized, _ := pool.Get(Config{}, WireConfig{CipherSuites: []uint16{}, ALPN: []string{}, CurvePreferences: []uint16{}})
	if empty != normalized {
		t.Fatal("nil and empty config did not normalize")
	}
	timeout, _ := pool.Get(Config{ConnectTimeout: time.Second}, WireConfig{})
	if timeout == empty {
		t.Fatal("timeouts not in pool key")
	}
	sample := WireConfig{TLSMinVersion: tls.VersionTLS12, CurvePreferences: []uint16{uint16(tls.X25519)}, ALPN: []string{"h2", "http/1.1"}}
	transport, err := pool.Get(Config{}, sample)
	if err != nil {
		t.Fatal(err)
	}
	sample.ALPN[0] = "changed"
	sample.CurvePreferences[0] = 0
	base := transport.followClient.Transport.(*http.Transport)
	if !reflect.DeepEqual(base.TLSClientConfig.NextProtos, []string{"h2", "http/1.1"}) || base.TLSClientConfig.CurvePreferences[0] != tls.X25519 || !base.Protocols.HTTP2() || !base.Protocols.HTTP1() {
		t.Fatal("sample mutated live config")
	}
	var zero Pool
	if _, err = zero.Get(Config{}, WireConfig{}); err != nil {
		t.Fatal(err)
	}
	zero.CloseIdleConnections()
	var absent *Pool
	absent.CloseIdleConnections()
	var missing *Transport
	if missing.WebSocketClient() != nil {
		t.Fatal("missing transport has client")
	}
	if (&Transport{}).WebSocketClient() != nil {
		t.Fatal("empty transport has client")
	}
}
func TestSampledTransportRejectsUnsupportedClaims(t *testing.T) {
	invalid := []string{`{"chrome":true}`, `[]`, `{} {}`, `{"tls_min_version":1}`, `{"tls_min_version":772,"tls_max_version":771}`, `{"cipher_suites":[4865]}`, `{"cipher_suites":[1]}`, `{"curve_preferences":[1]}`, `{"alpn":["h3"]}`, `{"http_protocol":"http3"}`, `{"http_protocol":"http1","alpn":["h2"]}`, `{"http_protocol":"http2","alpn":["http/1.1"]}`}
	for _, raw := range invalid {
		if _, err := ParseWireConfig([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if _, err := NewPool().Get(Config{}, WireConfig{HTTPProtocol: "chrome"}); err == nil {
		t.Fatal("unchecked direct config")
	}
}
