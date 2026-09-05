package upstreamtransport

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"net/http/httptrace"
	"sync"
	"time"
)

const (
	upstreamHTTP2Protocol = "h2"
	maxHTTP2Reopens       = 7
	http2ReopenJitter     = 0.1
)

var ErrReopenLimit = errors.New("upstream HTTP/2 transmission reopen limit reached")

// observedConnection only coordinates upload cancellation. Native net/http
// remains the authority on whether the failed exchange qualifies for replay.
type observedConnection struct {
	net.Conn
	mu                  sync.Mutex
	readFailureHandlers map[uint64]func(error)
	watchGeneration     uint64
}

func (c *observedConnection) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if err != nil {
		c.mu.Lock()
		handlers := make([]func(error), 0, len(c.readFailureHandlers))
		for _, handler := range c.readFailureHandlers {
			handlers = append(handlers, handler)
		}
		c.mu.Unlock()
		for _, onFailure := range handlers {
			onFailure(err)
		}
	}
	return n, err
}

// net/http joins its write loop before returning some read failures. Closing the
// upload reader here prevents a live-tail wait from trapping that join forever.
func (c *observedConnection) watchReadFailure(body *bodyTransmission) {
	c.mu.Lock()
	c.watchGeneration++
	generation := c.watchGeneration
	if c.readFailureHandlers == nil {
		c.readFailureHandlers = make(map[uint64]func(error))
	}
	c.readFailureHandlers[generation] = body.abortRead
	c.mu.Unlock()
	body.afterClose(func() { c.mu.Lock(); delete(c.readFailureHandlers, generation); c.mu.Unlock() })
}

type transmissionObservation struct {
	mu    sync.Mutex
	http2 bool
}

func newTransmissionObservation() *transmissionObservation { return &transmissionObservation{} }

func (o *transmissionObservation) context(ctx context.Context, body io.ReadCloser) context.Context {
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		connection := info.Conn
		if secure, ok := connection.(*tls.Conn); ok {
			o.mu.Lock()
			o.http2 = secure.ConnectionState().NegotiatedProtocol == upstreamHTTP2Protocol
			o.mu.Unlock()
			connection = secure.NetConn()
		}
		if observed, ok := connection.(*observedConnection); ok {
			if transmission, ok := body.(*bodyTransmission); ok {
				observed.watchReadFailure(transmission)
			}
		}
	}})
}

func (o *transmissionObservation) isHTTP2() bool { o.mu.Lock(); defer o.mu.Unlock(); return o.http2 }

// The sentinel exits native HTTP/2's loop before its delay. Carry the same
// bounded seven reopens and exponential jitter here while rebuilding each map.
func waitNativeReopen(ctx context.Context, http2 bool, previousReopens int) error {
	if !http2 {
		return ctx.Err()
	}
	if previousReopens >= maxHTTP2Reopens {
		return ErrReopenLimit
	}
	if previousReopens == 0 {
		return ctx.Err()
	}
	return waitReopenDelay(ctx, http2ReopenDelay(previousReopens, rand.Float64()))
}

func http2ReopenDelay(previousReopens int, jitter float64) time.Duration {
	seconds := float64(uint(1) << (uint(previousReopens) - 1))
	return time.Second * time.Duration(seconds+seconds*http2ReopenJitter*jitter)
}

func waitReopenDelay(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
