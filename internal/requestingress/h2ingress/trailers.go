package h2ingress

import (
	"context"
	"net/http"
	"strconv"
	"sync"
)

type trailerKey struct{}
type trailers struct {
	mu           sync.Mutex
	values       http.Header
	failure      error
	headTooLarge bool
	attached     bool // protected by connection.mu
}

// Trailers returns a detached snapshot of all actual trailer fields. Call only
// after a successful request Body EOF; framing declarations remain in the
// original Request.Trailer map. The bool distinguishes an adapted HTTP/2 input.
func Trailers(request *http.Request) (http.Header, bool) {
	state, ok := request.Context().Value(trailerKey{}).(*trailers)
	if !ok {
		return nil, false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.values.Clone(), true
}

func (c *connection) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		values := request.Header.Values(associationHeader)
		if len(values) == 0 {
			http.Error(w, "missing HTTP/2 ingress association", http.StatusInternalServerError)
			return
		}
		id, err := strconv.ParseUint(values[len(values)-1], 10, 32)
		c.mu.Lock()
		state := c.streams[uint32(id)]
		if err == nil && state != nil {
			state.attached = true
		}
		c.mu.Unlock()
		if err != nil || state == nil {
			http.Error(w, "invalid HTTP/2 ingress association", http.StatusInternalServerError)
			return
		}
		// Preserve a real client field with the same name, including repeated values.
		// The final value alone belongs to this adapter and never reaches capture or
		// upstream request construction.
		if len(values) == 1 {
			request.Header.Del(associationHeader)
		} else {
			request.Header[associationHeader] = values[:len(values)-1]
		}
		defer c.release(uint32(id))
		if state.headTooLarge {
			http.Error(w, "request header list too large", http.StatusRequestHeaderFieldsTooLarge)
			return
		}
		request.Body = &validatedBody{ReadCloser: request.Body, trailers: state}
		request = request.WithContext(context.WithValue(request.Context(), trailerKey{}, state))
		next.ServeHTTP(w, request)
	})
}
