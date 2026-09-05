package upstreamtransport

import (
	"bytes"
	"io"
	"net/http"
)

type memoryBodySource struct {
	payload  []byte
	framing  BodyFraming
	trailers http.Header
	openErr  error
	opened   int
}

func testBodySource(payload []byte) *memoryBodySource {
	return &memoryBodySource{payload: payload, framing: BodyFraming{ProtocolMajor: 1, ContentLength: int64(len(payload)), HasBody: len(payload) > 0, Complete: true}}
}
func (s *memoryBodySource) Open() (io.ReadCloser, error) {
	if s.openErr != nil {
		return nil, s.openErr
	}
	s.opened++
	return io.NopCloser(bytes.NewReader(s.payload)), nil
}
func (s *memoryBodySource) Framing() BodyFraming  { return s.framing }
func (s *memoryBodySource) Trailers() http.Header { return s.trailers.Clone() }
