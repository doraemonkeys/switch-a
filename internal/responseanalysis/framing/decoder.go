package framing

import (
	"compress/gzip"
	"errors"
	"io"
	"sync"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
)

// ContentCoding is parsed before decoder construction so chained or ambiguous
// encodings never reach a best-effort decoder.
type ContentCoding uint8

const (
	CodingIdentity ContentCoding = iota + 1
	CodingGzip
	CodingBrotli
)

// GzipDecoderWorkingMemoryBytes covers the RFC-maximum extra header, the
// 32-KiB DEFLATE window, Go's Huffman tables and reader buffers, plus headroom
// for member transitions. The grant is conservative and remains live for the
// decoder lifetime because the standard library exposes no allocation hooks.
const GzipDecoderWorkingMemoryBytes = 256 * 1024

// NewDecoder constructs a streaming decoder without taking ownership of the
// source. Body closure remains the sole responsibility of the response
// coordinator, which prevents decoder cleanup from racing raw-byte forwarding.
func NewDecoder(coding ContentCoding, source io.Reader) (io.ReadCloser, error) {
	decoder, err := NewDecoderWithReserver(coding, source, allocation.NoopReserver{})
	if err != nil {
		// Converting a nil *Decoder directly to io.ReadCloser would manufacture a
		// non-nil interface and make fail-open callers believe a decoder exists.
		return nil, err
	}
	return decoder, nil
}

func NewDecoderWithReserver(coding ContentCoding, source io.Reader, reserver allocation.Reserver) (*Decoder, error) {
	if reserver == nil {
		return nil, &Error{Reason: FailureInternal, Cause: allocation.ErrNilReserver}
	}
	if source == nil {
		return nil, &Error{Reason: FailureInternal, Cause: errors.New("decoder source is nil")}
	}
	switch coding {
	case CodingIdentity:
		return &Decoder{reader: source}, nil
	case CodingGzip:
		grant, err := reserver.Reserve(allocation.ClassDecoderWorkingSet, GzipDecoderWorkingMemoryBytes)
		if err != nil {
			return nil, err
		}
		if grant == nil {
			return nil, &Error{Reason: FailureInternal, Cause: allocation.ErrNilGrant}
		}
		reader, err := gzip.NewReader(source)
		if err != nil {
			grant.Release()
			return nil, &Error{Reason: FailureContentDecoding, Cause: err}
		}
		return &Decoder{reader: reader, closer: reader, grant: grant}, nil
	case CodingBrotli:
		// The available pure-Go decoder has no allocation hook. A read limit
		// bounds output but cannot prevent attacker-controlled Huffman/context
		// allocations, so treating Brotli as unsupported is the only honest
		// fail-open behavior until a budget-aware decoder is owned here.
		return nil, &Error{Reason: FailureUnsupportedEncoding, Cause: errors.New("bounded Brotli decoder unavailable")}
	default:
		return nil, &Error{Reason: FailureUnsupportedEncoding, Cause: errors.New("unknown content coding")}
	}
}

// Decoder borrows its source and owns only decoder state plus its working-set
// grant. Close is idempotent and never closes the source body.
type Decoder struct {
	reader io.Reader
	closer io.Closer
	grant  allocation.Grant

	closeOnce sync.Once
	closeErr  error
}

func (r *Decoder) Read(buffer []byte) (int, error) {
	if r == nil || r.reader == nil {
		return 0, &Error{Reason: FailureInternal, Cause: errors.New("decoder is nil")}
	}
	n, err := r.reader.Read(buffer)
	if err == nil || errors.Is(err, io.EOF) {
		return n, err
	}
	return n, &Error{Reason: FailureContentDecoding, Cause: err}
}

func (r *Decoder) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		if r.closer != nil {
			r.closeErr = r.closer.Close()
		}
		if r.grant != nil {
			r.grant.Release()
			r.grant = nil
		}
	})
	return r.closeErr
}
