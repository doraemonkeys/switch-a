package h2ingress

import (
	"errors"
	"io"
)

// net/http validates trailing names only when an initial Trailer declaration
// exists. Undeclared trailers therefore need the same validation at our EOF
// boundary; a partial/invalid header list must never become a successful input.
type validatedBody struct {
	io.ReadCloser
	trailers *trailers
}

func (body *validatedBody) Read(p []byte) (int, error) {
	n, err := body.ReadCloser.Read(p)
	if errors.Is(err, io.EOF) {
		body.trailers.mu.Lock()
		failure := body.trailers.failure
		body.trailers.mu.Unlock()
		if failure != nil {
			return n, failure
		}
	}
	return n, err
}
