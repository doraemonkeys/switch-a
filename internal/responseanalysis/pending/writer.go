package pending

import (
	"fmt"
	"io"
	"net/http"
)

type responseFlusher interface {
	Flush()
}

func writeResponseHead(writer ResponseWriter, statusCode int, header http.Header) {
	target := writer.Header()
	for key := range target {
		delete(target, key)
	}
	for key, values := range header {
		for _, value := range values {
			target.Add(key, value)
		}
	}
	writer.WriteHeader(statusCode)
}

func writeFull(writer ResponseWriter, body []byte) (int, error) {
	written := 0
	for len(body) > 0 {
		n, err := writer.Write(body)
		if n < 0 || n > len(body) {
			return written, fmt.Errorf("client writer returned invalid byte count %d for %d bytes", n, len(body))
		}
		written += n
		body = body[n:]
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, io.ErrShortWrite
		}
	}
	return written, nil
}

func flushResponse(writer ResponseWriter, enabled bool) {
	if !enabled {
		return
	}
	if flusher, ok := writer.(responseFlusher); ok {
		flusher.Flush()
	}
}

func writeTrailers(writer ResponseWriter, trailer http.Header) {
	if writer == nil || len(trailer) == 0 {
		return
	}
	target := writer.Header()
	for key, values := range trailer {
		trailerKey := http.TrailerPrefix + key
		delete(target, trailerKey)
		for _, value := range values {
			target.Add(trailerKey, value)
		}
	}
}
