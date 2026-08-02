package debugcapture

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/requestcapture"

	"go.uber.org/zap"
)

func (h *Handler) CreateExport(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	operation := operationContext{name: "create_export"}
	if h.exports == nil {
		h.writeUnavailable(w, operation)
		return
	}
	if !requestcapture.IsCanonicalSessionID(sessionID) {
		h.writeError(w, http.StatusBadRequest, errorCodeValidation, "session_id must be a non-empty canonical identifier", map[string]string{"field": "session_id"}, operation)
		return
	}
	// Path values are attacker-controlled. Only canonical capture identifiers may
	// cross into structured logs, otherwise a credential-shaped path can become
	// observable when the response writer itself fails.
	operation.sessionID = sessionID

	var input requestcapture.ExportRequest
	if err := decodeJSONBody(w, r, &input); err != nil {
		h.writeBodyDecodeError(w, err, operation)
		return
	}
	if validation := normalizeAndValidateExportRequest(&input); validation != nil {
		h.writeError(w, http.StatusBadRequest, errorCodeValidation, validation.message, validation.details, operation)
		return
	}
	ticket, err := h.exports.CreateExport(r.Context(), sessionID, input)
	if err != nil {
		if requestContextEnded(r.Context(), err) {
			return
		}
		h.writeServiceError(w, err, operation)
		return
	}
	if requestContextEnded(r.Context(), nil) {
		return
	}
	grant, valid := exportDownloadGrant(sessionID, ticket)
	if !valid {
		h.logger.Error("debug capture core returned an invalid export ticket",
			zap.String("operation", operation.name),
			zap.String("session_id", operation.sessionID),
			zap.String("failure_reason", "invalid_export_ticket"),
		)
		h.writeError(w, http.StatusInternalServerError, errorCodeInternal, "Debug capture operation failed", nil, operation)
		return
	}
	operation.exportID = grant.ExportID
	w.Header().Set("Location", grant.DownloadPath)
	h.logger.Info("debug capture export created through admin API",
		zap.String("session_id", grant.SessionID),
		zap.String("export_id", grant.ExportID),
		zap.Int("record_count", grant.RecordCount),
	)
	h.writeJSON(w, http.StatusCreated, grant, operation)
}

func exportDownloadGrant(expectedSessionID string, ticket requestcapture.ExportTicket) (ExportDownloadGrant, bool) {
	if !requestcapture.IsCanonicalExportID(ticket.ExportID) ||
		!requestcapture.IsCanonicalSessionID(ticket.SessionID) ||
		ticket.SessionID != expectedSessionID ||
		ticket.RecordCount <= 0 ||
		ticket.ExpiresAt.IsZero() ||
		ticket.DownloadToken == "" ||
		len(ticket.DownloadToken) > int(maxDownloadFormBytes) ||
		ticket.DownloadToken != strings.TrimSpace(ticket.DownloadToken) {
		return ExportDownloadGrant{}, false
	}
	downloadPath := exportDownloadPathPrefix + ticket.ExportID + "/download"
	return ExportDownloadGrant{
		ExportID:      ticket.ExportID,
		SessionID:     ticket.SessionID,
		RecordCount:   ticket.RecordCount,
		ExpiresAt:     ticket.ExpiresAt,
		DownloadPath:  downloadPath,
		DownloadToken: ticket.DownloadToken,
	}, true
}

func normalizeAndValidateExportRequest(input *requestcapture.ExportRequest) *inputValidation {
	switch input.Scope {
	case requestcapture.ExportScopeAll:
		if len(input.RecordIDs) != 0 {
			return &inputValidation{message: "record_ids must be omitted when scope is all", details: map[string]string{"field": "record_ids"}}
		}
	case requestcapture.ExportScopeRecords:
		if len(input.RecordIDs) == 0 {
			return &inputValidation{message: "record_ids is required when scope is records", details: map[string]string{"field": "record_ids"}}
		}
		seen := make(map[string]struct{}, len(input.RecordIDs))
		for _, recordID := range input.RecordIDs {
			if !requestcapture.IsCanonicalRecordID(recordID) {
				return &inputValidation{message: "record_ids must contain canonical record identifiers", details: map[string]string{"field": "record_ids"}}
			}
			if _, duplicate := seen[recordID]; duplicate {
				return &inputValidation{message: "record_ids must contain unique values", details: map[string]string{"field": "record_ids", "record_id": recordID}}
			}
			seen[recordID] = struct{}{}
		}
	default:
		return &inputValidation{message: "scope must be all or records", details: map[string]string{"field": "scope"}}
	}
	return nil
}

func (h *Handler) DownloadExport(w http.ResponseWriter, r *http.Request) {
	exportID := r.PathValue("export_id")
	operation := operationContext{name: "download_export"}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		h.writeError(w, http.StatusMethodNotAllowed, errorCodeValidation, "Debug capture downloads require POST", nil, operation)
		return
	}
	if h.exports == nil {
		h.writeUnavailable(w, operation)
		return
	}
	if !requestcapture.IsCanonicalExportID(exportID) {
		h.writeError(w, http.StatusBadRequest, errorCodeValidation, "export_id must be a non-empty canonical identifier", map[string]string{"field": "export_id"}, operation)
		return
	}
	operation.exportID = exportID
	if r.URL.ForceQuery || r.URL.RawQuery != "" {
		h.writeError(w, http.StatusBadRequest, errorCodeValidation, "Download tokens must be submitted in the form body", map[string]string{"field": downloadTokenField}, operation)
		return
	}
	if len(r.Header.Values("Content-Encoding")) != 0 {
		h.writeError(w, http.StatusUnsupportedMediaType, errorCodeValidation, "Download form must not use content encoding", nil, operation)
		return
	}
	contentTypes := r.Header.Values("Content-Type")
	if len(contentTypes) != 1 {
		h.writeError(w, http.StatusUnsupportedMediaType, errorCodeValidation, "Download requires exactly one application/x-www-form-urlencoded Content-Type", nil, operation)
		return
	}
	mediaType, parameters, err := mime.ParseMediaType(contentTypes[0])
	if err != nil || mediaType != "application/x-www-form-urlencoded" || len(parameters) != 0 {
		h.writeError(w, http.StatusUnsupportedMediaType, errorCodeValidation, "Download requires an application/x-www-form-urlencoded body", nil, operation)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxDownloadFormBytes)
	if err := r.ParseForm(); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			h.writeError(w, http.StatusRequestEntityTooLarge, errorCodeValidation, "Download form exceeds the configured limit", nil, operation)
			return
		}
		h.writeError(w, http.StatusBadRequest, errorCodeValidation, "Invalid download form", nil, operation)
		return
	}
	if len(r.PostForm) != 1 || len(r.PostForm[downloadTokenField]) != 1 {
		h.writeError(w, http.StatusBadRequest, errorCodeValidation, "Download form must contain exactly one download_token", map[string]string{"field": downloadTokenField}, operation)
		return
	}
	token := r.PostForm.Get(downloadTokenField)
	if token == "" || token != strings.TrimSpace(token) {
		h.writeError(w, http.StatusBadRequest, errorCodeValidation, "download_token is required", map[string]string{"field": downloadTokenField}, operation)
		return
	}

	download, err := h.exports.AcceptDownload(exportID, token)
	if err != nil {
		// The unauthenticated capability boundary must not reveal whether a token
		// was valid before an internal claim limit or memory fault occurred. Core
		// diagnostics retain the precise reason; every pre-stream failure is the
		// same public result so expiry, replay, existence, and pressure are
		// indistinguishable to callers.
		h.writeError(w, http.StatusGone, errorCodeDownloadUnavailable, "Debug capture download is unavailable", nil, operation)
		return
	}
	if !download.Valid() {
		h.logger.Error("debug capture core returned an invalid claimed download",
			zap.String("operation", operation.name),
			zap.String("export_id", operation.exportID),
			zap.String("failure_reason", "invalid_download_handle"),
		)
		h.writeError(w, http.StatusGone, errorCodeDownloadUnavailable, "Debug capture download is unavailable", nil, operation)
		return
	}
	// Claiming consumes the capability and reserves a download slot. Transport
	// failures, alternate streamers, and panics must not strand that lease.
	defer download.Close()
	filename := "switch-a-debug-capture-" + safeFilenameComponent(exportID) + ".ndjson"
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": filename})
	w.Header().Set("Content-Type", contentTypeNDJSON)
	w.Header().Set("Content-Disposition", disposition)
	w.WriteHeader(http.StatusOK)
	if err := h.streamer.Stream(r.Context(), download, w); err != nil {
		// Streaming errors cannot be converted into a second HTTP response. The
		// missing export_end event is the on-disk truncation signal.
		h.logger.Warn("debug capture download ended before export completion",
			zap.String("export_id", exportID),
			zap.String("reason", stableDownloadFailureReason(err)),
		)
	}
}

func safeFilenameComponent(value string) string {
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			builder.WriteRune(r)
		}
	}
	if builder.Len() == 0 {
		return "export"
	}
	return builder.String()
}

func stableDownloadFailureReason(err error) string {
	switch {
	case errors.Is(err, requestcapture.ErrExportCanceled):
		return "export_canceled"
	case errors.Is(err, requestcapture.ErrDownloadUnavailable):
		return "download_unavailable"
	case errors.Is(err, context.Canceled):
		return "request_canceled"
	default:
		return "stream_error"
	}
}
