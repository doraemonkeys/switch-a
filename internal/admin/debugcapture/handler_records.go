package debugcapture

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/doraemonkeys/switch-a/internal/requestcapture"
)

var recordListQueryParameters = map[string]struct{}{
	"limit":              {},
	"cursor":             {},
	"snapshot_watermark": {},
}

func (h *Handler) ListRecords(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	operation := operationContext{name: "list_records"}
	if h.queries == nil {
		h.writeUnavailable(w, operation)
		return
	}
	if !requestcapture.IsCanonicalSessionID(sessionID) {
		h.writeError(w, http.StatusBadRequest, errorCodeValidation, "session_id must be a non-empty canonical identifier", map[string]string{"field": "session_id"}, operation)
		return
	}
	operation.sessionID = sessionID

	query, validation := parseListQuery(r)
	if validation != nil {
		h.writeError(w, http.StatusBadRequest, errorCodeValidation, validation.message, validation.details, operation)
		return
	}
	lease, err := h.queries.OpenRecordPage(r.Context(), sessionID, query)
	if err != nil {
		if requestContextEnded(r.Context(), err) {
			return
		}
		h.writeServiceError(w, err, operation)
		return
	}
	if lease == nil {
		h.writeMissingQueryLease(w, operation)
		return
	}
	defer lease.Close()
	h.writeQueryJSON(w, r.Context(), lease, operation)
}

func parseListQuery(r *http.Request) (requestcapture.ListQuery, *inputValidation) {
	if len(r.URL.RawQuery) > maxRecordListQueryBytes {
		return requestcapture.ListQuery{}, &inputValidation{message: "Query parameters exceed the configured limit", details: map[string]string{"field": "query"}}
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return requestcapture.ListQuery{}, &inputValidation{message: "Query parameters are malformed", details: map[string]string{"field": "query"}}
	}
	for key, entries := range values {
		if _, allowed := recordListQueryParameters[key]; !allowed {
			return requestcapture.ListQuery{}, &inputValidation{message: "Unknown query parameter: " + key, details: map[string]string{"field": key}}
		}
		if len(entries) != 1 {
			return requestcapture.ListQuery{}, &inputValidation{message: "Query parameters must not be repeated", details: map[string]string{"field": key}}
		}
	}

	limit := requestcapture.DefaultListLimit
	if rawLimits, provided := values["limit"]; provided {
		rawLimit := rawLimits[0]
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed < 1 || parsed > requestcapture.DefaultMaxListLimit {
			return requestcapture.ListQuery{}, &inputValidation{message: "limit is outside the allowed range", details: map[string]string{"field": "limit"}}
		}
		limit = parsed
	}
	cursors, cursorProvided := values["cursor"]
	watermarks, watermarkProvided := values["snapshot_watermark"]
	if cursorProvided != watermarkProvided {
		return requestcapture.ListQuery{}, &inputValidation{message: "cursor and snapshot_watermark must be provided together", details: map[string]string{"field": "cursor"}}
	}
	var cursor, watermark string
	if cursorProvided {
		cursor, watermark = cursors[0], watermarks[0]
		if cursor == "" || watermark == "" {
			return requestcapture.ListQuery{}, &inputValidation{message: "cursor and snapshot_watermark must not be empty", details: map[string]string{"field": "cursor"}}
		}
	}
	return requestcapture.ListQuery{Limit: limit, Cursor: cursor, SnapshotWatermark: watermark}, nil
}

func (h *Handler) GetRecord(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	recordID := r.PathValue("record_id")
	operation := operationContext{
		name: "get_record",
	}
	if h.queries == nil {
		h.writeUnavailable(w, operation)
		return
	}
	if !requestcapture.IsCanonicalSessionID(sessionID) || !requestcapture.IsCanonicalRecordID(recordID) {
		h.writeError(w, http.StatusBadRequest, errorCodeValidation, "session_id and record_id must be non-empty canonical identifiers", nil, operation)
		return
	}
	operation.sessionID = sessionID
	operation.recordID = recordID
	if r.URL.ForceQuery || r.URL.RawQuery != "" {
		h.writeError(w, http.StatusBadRequest, errorCodeValidation, "Record detail does not accept query parameters", nil, operation)
		return
	}
	lease, err := h.queries.OpenRecordDetail(r.Context(), sessionID, recordID, 0)
	if err != nil {
		if requestContextEnded(r.Context(), err) {
			return
		}
		h.writeServiceError(w, err, operation)
		return
	}
	if lease == nil {
		h.writeMissingQueryLease(w, operation)
		return
	}
	defer lease.Close()
	h.writeQueryJSON(w, r.Context(), lease, operation)
}
