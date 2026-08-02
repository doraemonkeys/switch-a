package debugcapture

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"

	"go.uber.org/zap"
)

const (
	errorCodeValidation          = "VALIDATION_ERROR"
	errorCodeInternal            = "INTERNAL_ERROR"
	errorCodeUnavailable         = "DEBUG_CAPTURE_UNAVAILABLE"
	errorCodeSessionActive       = "DEBUG_CAPTURE_SESSION_ACTIVE"
	errorCodeSessionNotFound     = "DEBUG_CAPTURE_SESSION_NOT_FOUND"
	errorCodeRecordNotFound      = "DEBUG_CAPTURE_RECORD_NOT_FOUND"
	errorCodeRecordEvicted       = "DEBUG_CAPTURE_RECORD_EVICTED"
	errorCodeSnapshotChanged     = "DEBUG_CAPTURE_SNAPSHOT_CHANGED"
	errorCodeExportLimit         = "DEBUG_CAPTURE_EXPORT_LIMIT_REACHED"
	errorCodeDownloadLimit       = "DEBUG_CAPTURE_DOWNLOAD_LIMIT_REACHED"
	errorCodeDownloadUnavailable = "DEBUG_CAPTURE_DOWNLOAD_UNAVAILABLE"
	errorCodeCapacityExceeded    = "DEBUG_CAPTURE_CAPACITY_EXCEEDED"
	errorCodeStatusBusy          = "DEBUG_CAPTURE_STATUS_BUSY"

	failureReasonQueryCanceled          = "query_canceled"
	failureReasonRequestCanceled        = "request_canceled"
	failureReasonRequestDeadline        = "request_deadline_exceeded"
	failureReasonResponseWrite          = "response_write_failed"
	failureReasonUnexpectedDomain       = "unexpected_domain_error"
	failureReasonProviderCatalogFailure = "provider_catalog_unavailable"
)

type operationContext struct {
	name      string
	sessionID string
	recordID  string
	exportID  string
}

type bodyDecodeError struct {
	status  int
	message string
}

type queryJSONLease interface {
	WriteJSON(context.Context, io.Writer) error
}

type responseCommitTracker struct {
	http.ResponseWriter
	committed bool
}

func (w *responseCommitTracker) Write(payload []byte) (int, error) {
	// net/http commits a successful status before attempting the body write, so
	// no structured error envelope is safe once the lease reaches this boundary.
	w.committed = true
	return w.ResponseWriter.Write(payload)
}

func (e *bodyDecodeError) Error() string { return e.message }

// ApplySensitiveResponseHeaders is exported so the server can establish the
// boundary before ServeMux performs canonical-path redirects. Keeping the
// policy here prevents the normal and pre-routing paths from drifting apart.
func ApplySensitiveResponseHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

// SensitiveResponses remains useful around the nested capture router so it can
// be composed independently in handler tests and alternate server surfaces.
func SensitiveResponses(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ApplySensitiveResponseHeaders(w)
		next.ServeHTTP(w, r)
	})
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return &bodyDecodeError{status: http.StatusRequestEntityTooLarge, message: "Request body exceeds the configured limit"}
		}
		return &bodyDecodeError{status: http.StatusBadRequest, message: "Invalid request body"}
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return &bodyDecodeError{status: http.StatusRequestEntityTooLarge, message: "Request body exceeds the configured limit"}
		}
		return &bodyDecodeError{status: http.StatusBadRequest, message: "Request body must contain exactly one JSON value"}
	}
	return nil
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, value any, operation operationContext) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		h.logger.Error("failed to encode debug capture response",
			zap.String("operation", operation.name),
			zap.String("session_id", operation.sessionID),
			zap.String("record_id", operation.recordID),
			zap.String("export_id", operation.exportID),
			zap.String("failure_reason", failureReasonResponseWrite),
		)
	}
}

func (h *Handler) writeQueryJSON(w http.ResponseWriter, ctx context.Context, lease queryJSONLease, operation operationContext) {
	w.Header().Set("Content-Type", contentTypeJSON)
	tracked := &responseCommitTracker{ResponseWriter: w}
	err := lease.WriteJSON(ctx, tracked)
	if err == nil {
		return
	}
	if tracked.committed {
		h.logger.Warn("debug capture query response ended after body streaming began",
			zap.String("operation", operation.name),
			zap.String("session_id", operation.sessionID),
			zap.String("record_id", operation.recordID),
			zap.String("failure_reason", stableQueryWriteFailureReason(ctx, err)),
		)
		return
	}
	if requestContextEnded(ctx, err) {
		return
	}
	h.writeServiceError(w, err, operation)
}

func (h *Handler) writeStatusJSON(
	w http.ResponseWriter,
	ctx context.Context,
	lease requestcapture.StatusLease,
	operation operationContext,
) {
	w.Header().Set("Content-Type", contentTypeJSON)
	tracked := &responseCommitTracker{ResponseWriter: w}
	err := lease.WriteJSON(ctx, tracked)
	if err == nil {
		return
	}
	if tracked.committed || requestContextEnded(ctx, err) {
		if tracked.committed {
			h.logger.Warn("debug capture status response ended after body streaming began",
				zap.String("operation", operation.name),
				zap.String("failure_reason", stableQueryWriteFailureReason(ctx, err)),
			)
		}
		return
	}
	h.writeServiceError(w, err, operation)
}

func (h *Handler) writeMissingQueryLease(w http.ResponseWriter, operation operationContext) {
	h.logger.Error("debug capture query returned no lease",
		zap.String("operation", operation.name),
		zap.String("session_id", operation.sessionID),
		zap.String("record_id", operation.recordID),
	)
	h.writeError(w, http.StatusInternalServerError, errorCodeInternal, "Debug capture operation failed", nil, operation)
}

func requestContextEnded(ctx context.Context, err error) bool {
	return (ctx != nil && ctx.Err() != nil) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func stableQueryWriteFailureReason(ctx context.Context, err error) string {
	switch {
	case ctx != nil && errors.Is(ctx.Err(), context.Canceled):
		return failureReasonRequestCanceled
	case ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
		return failureReasonRequestDeadline
	case errors.Is(err, requestcapture.ErrQueryCanceled):
		return failureReasonQueryCanceled
	case errors.Is(err, context.Canceled):
		return failureReasonRequestCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return failureReasonRequestDeadline
	default:
		return failureReasonResponseWrite
	}
}

func (h *Handler) writeError(w http.ResponseWriter, status int, code, message string, details map[string]string, operation operationContext) {
	h.writeJSON(w, status, model.ErrorResponse{Code: code, Message: message, Details: details}, operation)
}

func (h *Handler) writeBodyDecodeError(w http.ResponseWriter, err error, operation operationContext) {
	var decodeErr *bodyDecodeError
	if !errors.As(err, &decodeErr) {
		h.writeError(w, http.StatusBadRequest, errorCodeValidation, "Invalid request body", nil, operation)
		return
	}
	h.writeError(w, decodeErr.status, errorCodeValidation, decodeErr.message, nil, operation)
}

func (h *Handler) writeServiceError(w http.ResponseWriter, err error, operation operationContext) {
	var validationErr *requestcapture.ValidationError
	switch {
	case errors.As(err, &validationErr):
		h.writeError(w, http.StatusBadRequest, errorCodeValidation, "Invalid debug capture request", map[string]string{
			"field":  validationErr.Field,
			"reason": validationErr.Reason,
		}, operation)
	case errors.Is(err, requestcapture.ErrSessionActive):
		h.writeError(w, http.StatusConflict, errorCodeSessionActive, "A debug capture session is already active", nil, operation)
	case errors.Is(err, requestcapture.ErrNoActiveSession), errors.Is(err, requestcapture.ErrSessionMismatch), errors.Is(err, requestcapture.ErrQueryCanceled):
		h.writeError(w, http.StatusNotFound, errorCodeSessionNotFound, "Debug capture session not found", nil, operation)
	case errors.Is(err, requestcapture.ErrInvalidCursor):
		h.writeError(w, http.StatusBadRequest, errorCodeValidation, "Invalid record cursor or snapshot watermark", nil, operation)
	case errors.Is(err, requestcapture.ErrRecordNotFound):
		h.writeError(w, http.StatusNotFound, errorCodeRecordNotFound, "Debug capture record not found", nil, operation)
	case errors.Is(err, requestcapture.ErrRecordEvicted):
		h.writeError(w, http.StatusGone, errorCodeRecordEvicted, "Debug capture record was evicted", nil, operation)
	case errors.Is(err, requestcapture.ErrSnapshotChanged):
		h.writeError(w, http.StatusConflict, errorCodeSnapshotChanged, "Debug capture snapshot changed; refresh the record list", nil, operation)
	case errors.Is(err, requestcapture.ErrExportLimitReached):
		h.writeError(w, http.StatusTooManyRequests, errorCodeExportLimit, "Too many pending debug capture exports", nil, operation)
	case errors.Is(err, requestcapture.ErrStatusBusy):
		h.writeError(w, http.StatusTooManyRequests, errorCodeStatusBusy, "Debug capture status is already being read", nil, operation)
	case errors.Is(err, requestcapture.ErrDownloadLimitReached):
		h.writeError(w, http.StatusTooManyRequests, errorCodeDownloadLimit, "Too many active debug capture downloads", nil, operation)
	case errors.Is(err, requestcapture.ErrDownloadUnavailable), errors.Is(err, requestcapture.ErrExportCanceled):
		// A uniform response prevents the capability endpoint from becoming an
		// oracle for token validity, expiry, replay, or export existence.
		h.writeError(w, http.StatusGone, errorCodeDownloadUnavailable, "Debug capture download is unavailable", nil, operation)
	case errors.Is(err, requestcapture.ErrCapacityExceeded):
		h.writeError(w, http.StatusInsufficientStorage, errorCodeCapacityExceeded, "Debug capture memory capacity is exhausted", nil, operation)
	case errors.Is(err, requestcapture.ErrManagerClosed), errors.Is(err, requestcapture.ErrGenerationExhausted):
		h.writeError(w, http.StatusServiceUnavailable, errorCodeUnavailable, "Debug capture is unavailable", nil, operation)
	default:
		h.logger.Error("debug capture admin operation failed",
			zap.String("operation", operation.name),
			zap.String("session_id", operation.sessionID),
			zap.String("record_id", operation.recordID),
			zap.String("export_id", operation.exportID),
			zap.String("failure_reason", failureReasonUnexpectedDomain),
		)
		h.writeError(w, http.StatusInternalServerError, errorCodeInternal, "Debug capture operation failed", nil, operation)
	}
}

func (h *Handler) writeUnavailable(w http.ResponseWriter, operation operationContext) {
	h.writeError(w, http.StatusServiceUnavailable, errorCodeUnavailable, "Debug capture is unavailable", nil, operation)
}
