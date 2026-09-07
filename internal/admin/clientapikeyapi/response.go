package clientapikeyapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/doraemonkeys/switch-a/internal/clientaccess"
	"go.uber.org/zap"
)

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func problem(w http.ResponseWriter, status int, code, message string) {
	respond(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func fail(w http.ResponseWriter, log *zap.Logger, err error) {
	status, code, message := http.StatusInternalServerError, "internal_error", "Failed to manage client API keys"
	switch {
	case errors.Is(err, clientaccess.ErrValidation):
		status, code, message = http.StatusBadRequest, "validation_error", err.Error()
	case errors.Is(err, clientaccess.ErrDuplicate):
		status, code, message = http.StatusConflict, "duplicate_key", "This API key already exists"
	case errors.Is(err, clientaccess.ErrNotFound):
		status, code, message = http.StatusNotFound, "not_found", "Client API key not found"
	}
	log.Warn("client API key administration failed", zap.Int("status", status), zap.String("error_code", code), zap.Error(err))
	problem(w, status, code, message)
}

func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(value); err != nil {
		problem(w, http.StatusBadRequest, "validation_error", "Invalid JSON")
		return false
	}
	// A mutation must describe one document; accepting trailing values hides
	// mistakes made by clients composing administrative requests.
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		problem(w, http.StatusBadRequest, "validation_error", "Expected one JSON document")
		return false
	}
	return true
}
