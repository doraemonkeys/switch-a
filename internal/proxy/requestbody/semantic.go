// Package requestbody provides bounded semantic views over HTTP request bodies.
// Wire bytes remain owned by the proxy so retries and upstream forwarding stay exact.
package requestbody

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/klauspost/compress/zstd"
)

const maxContentCodingLayers = 4

// Failure is a stable classification for semantic decoding failures.
type Failure string

const (
	// FailureInvalidLimit means the caller supplied an unusable decoded-body bound.
	FailureInvalidLimit Failure = "invalid_limit"
	// FailureInvalidEncoding means the Content-Encoding field is structurally invalid.
	FailureInvalidEncoding Failure = "invalid_content_encoding"
	// FailureUnsupportedEncoding means no semantic decoder exists for a content coding.
	FailureUnsupportedEncoding Failure = "unsupported_content_encoding"
	// FailureContentDecoding means encoded bytes do not form a valid coded representation.
	FailureContentDecoding Failure = "content_decoding"
	// FailureDecodedBodyTooLarge means decoded output exceeded the semantic body bound.
	FailureDecodedBodyTooLarge Failure = "decoded_body_too_large"
	// FailureInternal classifies errors from an injected decoder outside this package.
	FailureInternal Failure = "internal"
)

// DecodeError preserves a machine-readable failure and the relevant coding.
type DecodeError struct {
	Failure Failure
	Coding  string
	Cause   error
}

func (e *DecodeError) Error() string {
	return fmt.Sprintf("request body semantic decoding failed: failure=%s coding=%q: %v", e.Failure, e.Coding, e.Cause)
}

func (e *DecodeError) Unwrap() error { return e.Cause }

// Decoder creates a decoded view without changing the wire representation.
// Its zero value is ready for use.
type Decoder struct{}

// NewDecoder returns a stateless semantic request-body decoder.
func NewDecoder() Decoder { return Decoder{} }

// Decode returns a bounded semantic body while leaving wire untouched.
func (Decoder) Decode(wire []byte, contentEncodingValues []string, maxDecodedBytes int64) ([]byte, error) {
	codings, err := parseContentCodings(contentEncodingValues)
	if err != nil {
		return nil, err
	}
	if !hasEncodedCoding(codings) || len(wire) == 0 {
		return wire, nil
	}
	if maxDecodedBytes <= 0 {
		return nil, newDecodeError(FailureInvalidLimit, strings.Join(codings, ","), errors.New("decoded body limit must be positive"))
	}

	reader := io.Reader(bytes.NewReader(wire))
	closers := make([]func(), 0, len(codings))
	defer func() { closeDecoders(closers) }()
	for index := len(codings) - 1; index >= 0; index-- {
		reader, err = openDecoder(codings[index], reader, maxDecodedBytes, &closers)
		if err != nil {
			return nil, err
		}
	}

	readLimit := maxDecodedBytes
	if readLimit < math.MaxInt64 {
		readLimit++
	}
	decoded, err := io.ReadAll(io.LimitReader(reader, readLimit))
	if err != nil {
		if errors.Is(err, zstd.ErrDecoderSizeExceeded) {
			return nil, newDecodeError(FailureDecodedBodyTooLarge, strings.Join(codings, ","), err)
		}
		return nil, newDecodeError(FailureContentDecoding, strings.Join(codings, ","), err)
	}
	if int64(len(decoded)) > maxDecodedBytes {
		return nil, newDecodeError(
			FailureDecodedBodyTooLarge,
			strings.Join(codings, ","),
			fmt.Errorf("decoded body exceeds %d-byte limit", maxDecodedBytes),
		)
	}
	return decoded, nil
}

func hasEncodedCoding(codings []string) bool {
	for _, coding := range codings {
		if coding != "identity" {
			return true
		}
	}
	return false
}

func parseContentCodings(values []string) ([]string, error) {
	joined := strings.TrimSpace(strings.Join(values, ","))
	if joined == "" {
		return nil, nil
	}

	parts := strings.Split(joined, ",")
	if len(parts) > maxContentCodingLayers {
		return nil, newDecodeError(
			FailureInvalidEncoding,
			joined,
			fmt.Errorf("content encoding has more than %d layers", maxContentCodingLayers),
		)
	}
	codings := make([]string, 0, len(parts))
	for _, part := range parts {
		coding := strings.ToLower(strings.TrimSpace(part))
		if coding == "" {
			return nil, newDecodeError(FailureInvalidEncoding, joined, errors.New("content encoding contains an empty coding"))
		}
		codings = append(codings, coding)
	}
	return codings, nil
}

func openDecoder(coding string, source io.Reader, maxDecodedBytes int64, closers *[]func()) (io.Reader, error) {
	switch coding {
	case "identity":
		return source, nil
	case "gzip":
		reader, err := gzip.NewReader(source)
		if err != nil {
			return nil, newDecodeError(FailureContentDecoding, coding, err)
		}
		*closers = append(*closers, func() { _ = reader.Close() })
		return reader, nil
	case "deflate":
		reader, err := zlib.NewReader(source)
		if err != nil {
			return nil, newDecodeError(FailureContentDecoding, coding, err)
		}
		*closers = append(*closers, func() { _ = reader.Close() })
		return reader, nil
	case "zstd":
		// Streaming avoids DecodeAll's eager allocation. The configured semantic
		// body limit also caps hostile frame windows before output is materialized.
		decoderMemoryLimit := max(uint64(maxDecodedBytes), uint64(zstd.MinWindowSize))
		reader, err := zstd.NewReader(
			source,
			zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderLowmem(true),
			zstd.WithDecoderMaxMemory(decoderMemoryLimit),
			zstd.WithDecodeBuffersBelow(0),
		)
		if err != nil {
			return nil, newDecodeError(FailureContentDecoding, coding, err)
		}
		*closers = append(*closers, reader.Close)
		return reader, nil
	default:
		return nil, newDecodeError(FailureUnsupportedEncoding, coding, errors.New("content coding is not supported for semantic inspection"))
	}
}

func closeDecoders(closers []func()) {
	for index := len(closers) - 1; index >= 0; index-- {
		closers[index]()
	}
}

func newDecodeError(failure Failure, coding string, cause error) *DecodeError {
	return &DecodeError{Failure: failure, Coding: coding, Cause: cause}
}
