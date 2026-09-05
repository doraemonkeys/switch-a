package upstreamtransport

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"
)

// WireConfig contains only transport knobs represented by an independent sample.
// It makes no claim to reproduce a browser fingerprint or infer TLS from a UA.
type WireConfig struct {
	TLSMinVersion    uint16   `json:"tls_min_version,omitempty"`
	TLSMaxVersion    uint16   `json:"tls_max_version,omitempty"`
	CipherSuites     []uint16 `json:"cipher_suites,omitempty"`
	CurvePreferences []uint16 `json:"curve_preferences,omitempty"`
	ALPN             []string `json:"alpn,omitempty"`
	HTTPProtocol     string   `json:"http_protocol,omitempty"`
}

func ParseWireConfig(raw []byte) (WireConfig, error) {
	var config WireConfig
	if len(bytes.TrimSpace(raw)) == 0 {
		return config, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, fmt.Errorf("decode sampled transport configuration: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return config, fmt.Errorf("sampled transport configuration must contain one JSON object")
	}
	return config.normalized()
}
func (c WireConfig) normalized() (WireConfig, error) {
	for _, version := range []uint16{c.TLSMinVersion, c.TLSMaxVersion} {
		if version != 0 && version != tls.VersionTLS10 && version != tls.VersionTLS11 && version != tls.VersionTLS12 && version != tls.VersionTLS13 {
			return c, fmt.Errorf("unsupported sampled TLS version %d", version)
		}
	}
	if c.TLSMinVersion != 0 && c.TLSMaxVersion != 0 && c.TLSMinVersion > c.TLSMaxVersion {
		return c, fmt.Errorf("sampled TLS version range is inverted")
	}
	switch c.HTTPProtocol {
	case "", "http1", "http2":
	default:
		return c, fmt.Errorf("unsupported sampled HTTP protocol %q", c.HTTPProtocol)
	}
	if err := c.validateTLSAlgorithms(); err != nil {
		return c, err
	}
	for _, protocol := range c.ALPN {
		if protocol != "h2" && protocol != "http/1.1" {
			return c, fmt.Errorf("unsupported sampled ALPN %q", protocol)
		}
		if c.HTTPProtocol == "http1" && protocol == "h2" || c.HTTPProtocol == "http2" && protocol == "http/1.1" {
			return c, fmt.Errorf("sampled ALPN conflicts with HTTP protocol")
		}
	}
	c.CipherSuites = slices.Clone(c.CipherSuites)
	c.CurvePreferences = slices.Clone(c.CurvePreferences)
	// Go treats these TLS knobs as supported sets; sample input order cannot
	// create a distinct physical configuration. ALPN remains ordered.
	slices.Sort(c.CipherSuites)
	c.CipherSuites = slices.Compact(c.CipherSuites)
	slices.Sort(c.CurvePreferences)
	c.CurvePreferences = slices.Compact(c.CurvePreferences)
	c.ALPN = slices.Clone(c.ALPN)
	if len(c.CipherSuites) == 0 {
		c.CipherSuites = nil
	}
	if len(c.CurvePreferences) == 0 {
		c.CurvePreferences = nil
	}
	if len(c.ALPN) == 0 {
		c.ALPN = nil
	}
	return c, nil
}

type transportPoolKey struct {
	config Config
	wire   string
}
type Pool struct {
	mu      sync.Mutex
	entries map[transportPoolKey]*Transport
}

func NewPool() *Pool { return &Pool{entries: make(map[transportPoolKey]*Transport)} }
func (p *Pool) Get(config Config, sample WireConfig) (*Transport, error) {
	normalized, err := sample.normalized()
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return nil, err
	}
	key := transportPoolKey{config: config, wire: string(encoded)}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.entries == nil {
		p.entries = make(map[transportPoolKey]*Transport)
	}
	if transport := p.entries[key]; transport != nil {
		return transport, nil
	}
	transport := New(config)
	base := transport.followClient.Transport.(*http.Transport)
	if normalized.TLSMinVersion != 0 || normalized.TLSMaxVersion != 0 || len(normalized.CipherSuites) > 0 || len(normalized.CurvePreferences) > 0 || len(normalized.ALPN) > 0 {
		curves := make([]tls.CurveID, len(normalized.CurvePreferences))
		for i, curve := range normalized.CurvePreferences {
			curves[i] = tls.CurveID(curve)
		}
		base.TLSClientConfig = &tls.Config{MinVersion: normalized.TLSMinVersion, MaxVersion: normalized.TLSMaxVersion, CipherSuites: normalized.CipherSuites, CurvePreferences: curves, NextProtos: normalized.ALPN}
	}
	if normalized.HTTPProtocol != "" || len(normalized.ALPN) > 0 {
		protocols := new(http.Protocols)
		protocols.SetHTTP1(normalized.HTTPProtocol != "http2" && (len(normalized.ALPN) == 0 || slices.Contains(normalized.ALPN, "http/1.1")))
		protocols.SetHTTP2(normalized.HTTPProtocol != "http1" && (len(normalized.ALPN) == 0 || slices.Contains(normalized.ALPN, "h2")))
		base.Protocols = protocols
	}
	p.entries[key] = transport
	return transport, nil
}
func (p *Pool) CloseIdleConnections() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, transport := range p.entries {
		transport.CloseIdleConnections()
	}
}
func (t *Transport) WebSocketClient() *http.Client {
	if t == nil || t.rawClient == nil {
		return nil
	}
	client := *t.rawClient
	return &client
}

func (c WireConfig) validateTLSAlgorithms() error {
	supported := append(tls.CipherSuites(), tls.InsecureCipherSuites()...)
	for _, suite := range c.CipherSuites {
		found := false
		for _, candidate := range supported {
			if candidate.ID == suite && !slices.Contains(candidate.SupportedVersions, tls.VersionTLS13) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("TLS cipher suite %d cannot be configured by this adapter", suite)
		}
	}
	for _, curve := range c.CurvePreferences {
		switch tls.CurveID(curve) {
		case tls.CurveP256, tls.CurveP384, tls.CurveP521, tls.X25519, tls.X25519MLKEM768:
		default:
			return fmt.Errorf("unsupported sampled TLS curve %d", curve)
		}
	}

	return nil
}
