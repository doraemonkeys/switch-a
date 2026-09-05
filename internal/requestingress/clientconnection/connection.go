// Package clientconnection keeps physical peer closure observable after an HTTP/1
// request-body read has deliberately been interrupted.
package clientconnection

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

const readBufferBytes = 32 * 1024

type connectionKey struct{}

// Connection owns one socket read pump. Read deadlines apply to the HTTP parser,
// independently of the socket observer, because net/http cancels its request
// context permanently when an interrupted body read reports a timeout.
type Connection struct {
	net.Conn
	mu       sync.Mutex
	changed  chan struct{}
	closed   chan struct{}
	data     []byte
	terminal error
	deadline time.Time
	discard  bool
	once     sync.Once
}

func Wrap(conn net.Conn) *Connection {
	c := &Connection{Conn: conn, changed: make(chan struct{}), closed: make(chan struct{})}
	go c.pump()
	return c
}

func (c *Connection) signalLocked() { close(c.changed); c.changed = make(chan struct{}) }

func (c *Connection) pump() {
	buffer := make([]byte, readBufferBytes)
	for {
		c.mu.Lock()
		for len(c.data) > 0 && !c.discard && c.terminal == nil {
			changed := c.changed
			c.mu.Unlock()
			<-changed
			c.mu.Lock()
		}
		stopped := c.terminal != nil
		c.mu.Unlock()
		if stopped {
			return
		}
		n, err := c.Conn.Read(buffer)
		c.mu.Lock()
		if n > 0 && !c.discard {
			c.data = append(c.data[:0], buffer[:n]...)
		}
		if err != nil {
			c.terminal = err
			c.once.Do(func() { close(c.closed) })
		}
		c.signalLocked()
		c.mu.Unlock()
		if err != nil {
			return
		}
	}
}

func (c *Connection) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		c.mu.Lock()
		if !c.deadline.IsZero() && !time.Now().Before(c.deadline) {
			c.mu.Unlock()
			return 0, readDeadlineError{}
		}
		if len(c.data) > 0 {
			n := copy(p, c.data)
			c.data = c.data[n:]
			c.signalLocked()
			c.mu.Unlock()
			return n, nil
		}
		if c.terminal != nil {
			err := c.terminal
			c.mu.Unlock()
			return 0, err
		}
		changed, deadline := c.changed, c.deadline
		c.mu.Unlock()
		if deadline.IsZero() {
			<-changed
			continue
		}
		timer := time.NewTimer(time.Until(deadline))
		select {
		case <-changed:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
		}
	}
}

func (c *Connection) SetReadDeadline(deadline time.Time) error {
	c.mu.Lock()
	c.deadline = deadline
	c.signalLocked()
	c.mu.Unlock()
	return nil
}

func (c *Connection) SetDeadline(deadline time.Time) error {
	if err := c.SetReadDeadline(deadline); err != nil {
		return err
	}
	return c.SetWriteDeadline(deadline)
}

func (c *Connection) Close() error {
	err := c.Conn.Close()
	c.mu.Lock()
	if c.terminal == nil {
		c.terminal = net.ErrClosed
	}
	c.once.Do(func() { close(c.closed) })
	c.signalLocked()
	c.mu.Unlock()
	return err
}

// DiscardInput is only used when this HTTP/1 connection will close after its
// current response; unread bytes cannot then become a subsequent request.
func (c *Connection) DiscardInput() {
	c.mu.Lock()
	c.discard = true
	c.data = nil
	c.signalLocked()
	c.mu.Unlock()
}

func (c *Connection) Done() <-chan struct{} { return c.closed }

type readDeadlineError struct{}

func (readDeadlineError) Error() string     { return "client request read deadline exceeded" }
func (readDeadlineError) Timeout() bool     { return true }
func (readDeadlineError) Temporary() bool   { return true }
func (readDeadlineError) Is(err error) bool { return errors.Is(err, context.DeadlineExceeded) }

func Listen(listener net.Listener) net.Listener { return &listenerWrapper{Listener: listener} }

type listenerWrapper struct{ net.Listener }

func (l *listenerWrapper) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return Wrap(c), nil
}

func Context(ctx context.Context, conn net.Conn) context.Context {
	for conn != nil {
		if observed, ok := conn.(*Connection); ok {
			return context.WithValue(ctx, connectionKey{}, observed)
		}
		underlying, ok := conn.(interface{ NetConn() net.Conn })
		if !ok {
			break
		}
		conn = underlying.NetConn()
	}
	return ctx
}

func FromContext(ctx context.Context) *Connection {
	observed, _ := ctx.Value(connectionKey{}).(*Connection)
	return observed
}

var _ net.Conn = (*Connection)(nil)
var _ io.Reader = (*Connection)(nil)
