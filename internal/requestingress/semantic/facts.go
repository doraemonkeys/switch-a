// Package semantic projects existing request consumers without retaining request payloads.
package semantic

import (
	"context"
	"io"

	codexheaders "github.com/doraemonkeys/switch-a/internal/codex/headers"
	"github.com/doraemonkeys/switch-a/internal/model"
)

type State string

const (
	Known       State = "known"
	Unavailable State = "unavailable"
)

const (
	ReasonMissing                    = "missing"
	ReasonInvalidJSON                = "invalid_json"
	ReasonInvalidLimit               = "invalid_limit"
	ReasonInvalidContentEncoding     = "invalid_content_encoding"
	ReasonUnsupportedContentEncoding = "unsupported_content_encoding"
	ReasonContentDecoding            = "content_decoding"
	ReasonDecodedBodyTooLarge        = "decoded_body_too_large"
)

type Fact[T any] struct {
	State  State
	Value  T
	Reason string
}
type ReasoningContract uint8

const (
	ReasoningUnsupported ReasoningContract = iota
	ReasoningCodex
	ReasoningClaude
	ReasoningChat
)

type Options struct {
	ContentEncodingValues []string
	MaxDecodedBytes       int64
	ReasoningContract     ReasoningContract
}
type Result struct {
	Model        Fact[string]
	Reasoning    Fact[model.RequestedReasoningObservation]
	Codex        Fact[codexheaders.ClientEvidence]
	DecodedBytes int64
}

const unknownModel = "unknown"

// Project owns only its semantic reader. Acquisition failures do not close the wire source.
func Project(ctx context.Context, source io.Reader, options Options) Result {
	return project(ctx, source, options)
}
