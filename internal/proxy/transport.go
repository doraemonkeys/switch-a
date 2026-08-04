package proxy

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

var (
	ErrReadTimeout    = errors.New("upstream response idle timeout")
	ErrSSEIdleTimeout = errors.New("upstream SSE idle timeout")
)

// TransportConfig separates exchange-establishment deadlines from body idle
// deadlines. The latter are enforced by responseanalysis after body ownership
// moves out of the transport.
type TransportConfig struct {
	ConnectTimeout   time.Duration
	FirstByteTimeout time.Duration
	ReadTimeout      time.Duration
	SSEIdleTimeout   time.Duration
}

// Transport is intentionally fetch-only. Client writing, draining, decoding,
// and response classification belong to the pending-response coordinator.
type Transport struct {
	upstream *upstreamtransport.Transport
}

func NewTransport(config TransportConfig) *Transport {
	return &Transport{upstream: upstreamtransport.New(upstreamtransport.Config{
		ConnectTimeout:   config.ConnectTimeout,
		FirstByteTimeout: config.FirstByteTimeout,
	})}
}

func (t *Transport) FetchUpstream(ctx context.Context, request *http.Request) (*upstreamtransport.Response, error) {
	return t.upstream.Fetch(ctx, request)
}

func (t *Transport) CloseIdleConnections() {
	if t != nil && t.upstream != nil {
		t.upstream.CloseIdleConnections()
	}
}

func BuildUpstreamRequest(
	ctx context.Context,
	method string,
	upstreamURL string,
	body []byte,
	originalRequest *http.Request,
) (*http.Request, error) {
	return upstreamtransport.BuildRequest(ctx, method, upstreamURL, body, originalRequest)
}
