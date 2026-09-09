package upstreamtransport

import (
	"crypto/tls"
	"github.com/coder/websocket"
	"net/http"
	"net/http/httptest"
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
		client := transport.WebSocketClient()
		upgrade := client.Transport.(*http.Transport)
		if upgrade != transport.WebSocketClient().Transport {
			t.Fatal("WS client lost its pooled transport")
		}
		if upgrade == transport.followClient.Transport || !upgrade.Protocols.HTTP1() || upgrade.Protocols.HTTP2() {
			t.Fatal("WS handshake must use an independent HTTP/1.1 transport")
		}
		if upgrade.TLSClientConfig != nil && !reflect.DeepEqual(upgrade.TLSClientConfig.NextProtos, []string{"http/1.1"}) {
			t.Fatal("WS ALPN must agree with its upgrade protocol")
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

func TestWebSocketUpgradeRemainsHTTP1OnHTTP2Server(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 1 {
			t.Errorf("upgrade protocol = %s", r.Proto)
		}
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.CloseNow()
		if err := connection.Write(r.Context(), websocket.MessageText, []byte("ready")); err != nil {
			t.Error(err)
		}
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	transport := New(Config{})
	defer transport.CloseIdleConnections()
	base := transport.followClient.Transport.(*http.Transport)
	base.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	connection, response, err := websocket.Dial(t.Context(), server.URL, &websocket.DialOptions{HTTPClient: transport.WebSocketClient()})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	if response.StatusCode != http.StatusSwitchingProtocols || response.ProtoMajor != 1 {
		t.Fatalf("upgrade response = %d %s", response.StatusCode, response.Proto)
	}
	_, data, err := connection.Read(t.Context())
	if err != nil || string(data) != "ready" {
		t.Fatalf("message = %q, error = %v", data, err)
	}
}
