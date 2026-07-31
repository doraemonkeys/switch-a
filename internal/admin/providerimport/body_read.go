package providerimport

import (
	"errors"
	"io"
	"net/http"
	"os"
	"time"

	"go.uber.org/zap"
)

var (
	errProviderImportBodyReadTimeout             = errors.New("provider import body read timed out")
	errProviderImportBodyReadDeadlineUnavailable = errors.New("provider import body read deadline unavailable")
)

type providerImportBodyReadDeadlineError struct {
	cause error
}

func (e *providerImportBodyReadDeadlineError) Error() string {
	return errProviderImportBodyReadDeadlineUnavailable.Error() + ": " + e.cause.Error()
}

func (e *providerImportBodyReadDeadlineError) Unwrap() error {
	return e.cause
}

func (e *providerImportBodyReadDeadlineError) Is(target error) bool {
	return target == errProviderImportBodyReadDeadlineUnavailable
}

func setHTTPResponseReadDeadline(w http.ResponseWriter, deadline time.Time) error {
	return http.NewResponseController(w).SetReadDeadline(deadline)
}

func (h *Handler) tryAcquireProviderImportBodyRead() bool {
	if h.providerImportReadSlots == nil {
		return true
	}
	select {
	case h.providerImportReadSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (h *Handler) releaseProviderImportBodyRead() {
	if h.providerImportReadSlots != nil {
		<-h.providerImportReadSlots
	}
}

func (h *Handler) readProviderImportBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	timeout := h.providerImportReadTimeout
	if timeout <= 0 {
		timeout = providerImportBodyReadTimeout
	}
	setDeadline := h.providerImportSetReadDeadline
	if setDeadline == nil {
		setDeadline = setHTTPResponseReadDeadline
	}
	if err := setDeadline(w, time.Now().Add(timeout)); err != nil {
		return nil, &providerImportBodyReadDeadlineError{cause: err}
	}
	defer func() {
		if err := setDeadline(w, time.Time{}); err != nil && h.logger != nil {
			h.logger.Warn("failed to clear provider import body read deadline", zap.Error(err))
		}
	}()

	r.Body = http.MaxBytesReader(w, r.Body, MaxProviderImportBodySize)
	raw, err := io.ReadAll(r.Body)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return nil, errProviderImportBodyReadTimeout
	}
	return raw, err
}
