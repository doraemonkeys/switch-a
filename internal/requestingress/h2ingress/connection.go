package h2ingress

import (
	"bytes"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"

	"go.uber.org/zap"
	"golang.org/x/net/http/httpguts"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

const (
	frameHeaderBytes        = 9
	hpackTableBytes         = 4096
	hpackFieldOverheadBytes = 32
	typicalHeaderFields     = 10
	// HPACK literal overhead and continuation framing can exceed decoded bytes.
	encodedHeaderExpansion = 2
	maxPendingStreams      = 2*maxConcurrentStreams + 1
)

var (
	errIngressBudget   = errors.New("HTTP/2 ingress metadata budget exceeded")
	errInvalidTrailers = errors.New("HTTP/2 request contains forbidden trailer fields")
)

type connection struct {
	net.Conn
	reader           *http2.Framer
	captured         boundedCapture
	pending          []byte
	terminal         error
	prefaceRemaining int
	highestStream    uint32
	headerListLimit  uint32
	mu               sync.Mutex
	streams          map[uint32]*trailers
	logger           *zap.Logger
	writeMu          sync.Mutex
	output           frameCompletion
}

func newConnection(conn net.Conn, maxHeaderBytes int, logger *zap.Logger) *connection {
	c := &connection{Conn: conn, prefaceRemaining: len(http2.ClientPreface), streams: make(map[uint32]*trailers), logger: logger}
	c.captured.limit = maxReadFrameBytes + encodedHeaderExpansion*maxHeaderBytes + frameHeaderBytes
	c.reader = http2.NewFramer(nil, io.TeeReader(conn, &c.captured))
	c.reader.SetMaxReadFrameSize(maxReadFrameBytes)
	c.reader.ReadMetaHeaders = hpack.NewDecoder(hpackTableBytes, nil)
	c.headerListLimit = uint32(maxHeaderBytes + typicalHeaderFields*hpackFieldOverheadBytes)
	// Match the native decoder's string limit; evaluate the original client's
	// smaller header-list budget separately after decoding.
	c.reader.MaxHeaderListSize = c.headerListLimit + associationBudgetBytes
	return c
}

// ConnectionState keeps ServeConn's TLS verification and Request.TLS intact.
func (c *connection) ConnectionState() tls.ConnectionState {
	if conn, ok := c.Conn.(interface{ ConnectionState() tls.ConnectionState }); ok {
		return conn.ConnectionState()
	}
	return tls.ConnectionState{}
}

func (c *connection) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if c.prefaceRemaining > 0 {
		n, err := c.Conn.Read(p[:min(len(p), c.prefaceRemaining)])
		c.prefaceRemaining -= n
		return n, err
	}
	if len(c.pending) == 0 {
		if err := c.readFrame(); err != nil {
			return 0, err
		}
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

func (c *connection) readFrame() error {
	if c.terminal != nil {
		return c.terminal
	}
	c.captured.Reset()
	frame, err := c.reader.ReadFrame()
	c.pending = c.captured.Bytes()
	if err != nil {
		return c.preserveFrameError(err)
	}
	switch frame := frame.(type) {
	case *http2.MetaHeadersFrame:
		if err := c.observeHeaders(frame); err != nil {
			c.pending = nil
			c.terminal = err
			c.logger.Debug("HTTP/2 ingress metadata failed", zap.Uint32("stream_id", frame.StreamID), zap.Error(err))
			return err
		}
	case *http2.RSTStreamFrame:
		c.release(frame.StreamID)
	}
	return nil
}

func (c *connection) preserveFrameError(err error) error {
	c.logger.Debug("HTTP/2 ingress frame read failed", zap.Error(err))
	// Both decoders must consume the same original HPACK bytes even
	// when a malformed stream is rejected. Let the native parser
	// produce its normal RST_STREAM and retain its dynamic table.
	var streamError http2.StreamError
	if !errors.As(err, &streamError) {
		c.terminal = err
	}
	if len(c.pending) == 0 {
		return err
	}
	return nil
}

func (c *connection) observeHeaders(frame *http2.MetaHeadersFrame) error {
	streamID := frame.StreamID
	size := uint64(0)
	for _, field := range frame.Fields {
		size += uint64(field.Size())
	}
	tooLarge := frame.Truncated || size > uint64(c.headerListLimit)
	c.mu.Lock()
	if streamID > c.highestStream {
		c.highestStream = streamID
		if len(c.streams) >= maxPendingStreams {
			c.mu.Unlock()
			return errIngressBudget
		}
		state := &trailers{headTooLarge: tooLarge}
		c.streams[streamID] = state
		c.mu.Unlock()
		c.pending = associate(c.pending, streamID)
		c.logger.Debug("HTTP/2 ingress stream attached", zap.Uint32("stream_id", streamID))
		return nil
	}
	state := c.streams[streamID]
	c.mu.Unlock()
	if state != nil {
		actual := make(http.Header)
		var failure error
		if tooLarge {
			failure = errIngressBudget
		}
		for _, field := range frame.RegularFields() {
			if !httpguts.ValidTrailerHeader(http.CanonicalHeaderKey(field.Name)) {
				failure = errInvalidTrailers
			}
			actual.Add(field.Name, field.Value)
		}
		state.mu.Lock()
		state.values, state.failure = actual, failure
		state.mu.Unlock()
		c.logger.Debug("HTTP/2 ingress trailers observed", zap.Uint32("stream_id", streamID), zap.Int("trailer_keys", len(actual)), zap.Error(failure))
	}
	return nil
}

// The literal is never indexed: both HPACK decoders retain identical dynamic
// tables. Original compressed headers, declarations, DATA and control frames
// remain byte-for-byte intact; only the in-process parser sees this marker.
func associate(raw []byte, streamID uint32) []byte {
	for offset := 0; offset < len(raw); {
		size := int(raw[offset])<<16 | int(raw[offset+1])<<8 | int(raw[offset+2])
		raw[offset+4] &^= byte(http2.FlagHeadersEndHeaders)
		offset += frameHeaderBytes + size
	}
	var block bytes.Buffer
	encoder := hpack.NewEncoder(&block)
	_ = encoder.WriteField(hpack.HeaderField{Name: associationHeaderLower, Value: strconv.FormatUint(uint64(streamID), 10), Sensitive: true})
	var added bytes.Buffer
	_ = http2.NewFramer(&added, nil).WriteContinuation(streamID, true, block.Bytes())
	return append(raw, added.Bytes()...)
}

func (c *connection) release(streamID uint32) {
	c.mu.Lock()
	delete(c.streams, streamID)
	c.mu.Unlock()
}

// A response END_STREAM can precede upload EOF, for example after flushing a
// HEAD response. Such a stream still needs its trailer association until its
// handler returns; only rejected requests can be retired by output alone.
func (c *connection) releaseUnattached(streamID uint32) {
	c.mu.Lock()
	if state := c.streams[streamID]; state != nil && !state.attached {
		delete(c.streams, streamID)
	}
	c.mu.Unlock()
}

func (c *connection) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	n, err := c.Conn.Write(p)
	c.output.observe(p[:n], c.releaseUnattached)
	return n, err
}

type boundedCapture struct {
	bytes.Buffer
	limit int
}

func (b *boundedCapture) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, errIngressBudget
	}
	return b.Buffer.Write(p)
}

// The outbound observer retains only a frame header, regardless of DATA size.
// Rejected streams never reach a Handler, so peer and server resets must retire
// their association as well as the ordinary handler-return path.
type frameCompletion struct {
	header      [frameHeaderBytes]byte
	headerBytes int
	remaining   int
}

func (f *frameCompletion) observe(p []byte, release func(uint32)) {
	for len(p) > 0 {
		if f.headerBytes < frameHeaderBytes {
			n := copy(f.header[f.headerBytes:], p)
			f.headerBytes += n
			p = p[n:]
			if f.headerBytes < frameHeaderBytes {
				continue
			}
			f.remaining = int(f.header[0])<<16 | int(f.header[1])<<8 | int(f.header[2])
		}
		n := min(len(p), f.remaining)
		f.remaining -= n
		p = p[n:]
		if f.remaining == 0 {
			kind, flags := http2.FrameType(f.header[3]), http2.Flags(f.header[4])
			if kind == http2.FrameRSTStream || ((kind == http2.FrameHeaders || kind == http2.FrameData) && flags.Has(http2.FlagDataEndStream)) {
				id := uint32(f.header[5]&0x7f)<<24 | uint32(f.header[6])<<16 | uint32(f.header[7])<<8 | uint32(f.header[8])
				release(id)
			}
			f.headerBytes = 0
		}
	}
}
