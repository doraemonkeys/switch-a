package errorruleapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"
)

const (
	ErrorCodeValidation           = "VALIDATION_ERROR"
	ErrorCodeInternal             = "INTERNAL_ERROR"
	ErrorCodeNotFound             = "NOT_FOUND"
	ErrorCodeConflict             = "CONFLICT"
	ErrorCodePreconditionRequired = "PRECONDITION_REQUIRED"
	ErrorCodeRevisionMismatch     = "REVISION_MISMATCH"
	ErrorCodeRequestTooLarge      = "REQUEST_TOO_LARGE"
)

const jsonContentType = "application/json"

type errorResponse struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
}

type apiError struct {
	Status  int
	Code    string
	Message string
	Details map[string]any
	Cause   error
}

func (e *apiError) Error() string {
	if e == nil {
		return "internal-error API failure"
	}
	return e.Message
}

func (e *apiError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func validationError(field, message string, cause error) *apiError {
	return &apiError{
		Status:  http.StatusBadRequest,
		Code:    ErrorCodeValidation,
		Message: message,
		Details: map[string]any{"field": field},
		Cause:   cause,
	}
}

func requestTooLargeError(limit int64) *apiError {
	return &apiError{
		Status:  http.StatusRequestEntityTooLarge,
		Code:    ErrorCodeRequestTooLarge,
		Message: "request body exceeds the endpoint limit",
		Details: map[string]any{"limit_bytes": limit},
	}
}

func internalError(message string, cause error) *apiError {
	return &apiError{
		Status:  http.StatusInternalServerError,
		Code:    ErrorCodeInternal,
		Message: message,
		Details: map[string]any{},
		Cause:   cause,
	}
}

func writeAPIError(w http.ResponseWriter, apiErr *apiError) {
	if apiErr == nil {
		apiErr = internalError("internal-error API failed", nil)
	}
	writeJSON(w, apiErr.Status, errorResponse{
		Code: apiErr.Code, Message: apiErr.Message, Details: nonNilDetails(apiErr.Details),
	})
}

func nonNilDetails(details map[string]any) map[string]any {
	if details == nil {
		return map[string]any{}
	}
	return details
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		fallback, _ := json.Marshal(errorResponse{
			Code: ErrorCodeInternal, Message: "failed to encode internal-error API response", Details: map[string]any{},
		})
		w.Header().Set("Content-Type", jsonContentType)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write(append(fallback, '\n'))
		return
	}
	w.Header().Set("Content-Type", jsonContentType)
	w.WriteHeader(status)
	_, _ = w.Write(append(payload, '\n'))
}

func decodeRequest(r *http.Request, limit int64, target any) *apiError {
	if r == nil || r.Body == nil {
		return validationError("request", "request body is required", nil)
	}
	if r.ContentLength > limit {
		return requestTooLargeError(limit)
	}

	// Reading one byte beyond the endpoint limit distinguishes an exact-boundary
	// request from an overflow without allowing an attacker-controlled allocation.
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return validationError("request", "request body could not be read", err)
	}
	if int64(len(body)) > limit {
		return requestTooLargeError(limit)
	}
	if len(body) == 0 {
		return validationError("request", "request body is required", nil)
	}
	if !utf8.Valid(body) {
		return validationError("request", "request body must be valid UTF-8 JSON", nil)
	}
	if fieldErr := validateJSONFields(body, requestJSONShape(target)); fieldErr != nil {
		return fieldErr
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return decodeValidationError(err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return validationError("request", "request body must contain exactly one JSON value", nil)
		}
		return validationError("request", "request body contains trailing JSON", err)
	}
	return nil
}

func decodeValidationError(err error) *apiError {
	const unknownPrefix = "json: unknown field "
	if suffix, ok := strings.CutPrefix(err.Error(), unknownPrefix); ok {
		field := strings.Trim(suffix, `"`)
		return validationError(field, "request contains an unknown field", err)
	}
	var typeError *json.UnmarshalTypeError
	if errors.As(err, &typeError) {
		field := typeError.Field
		if field == "" {
			field = "request"
		}
		return validationError(field, "request field has an invalid JSON type", err)
	}
	return validationError("request", "request body is invalid JSON", err)
}

func validateJSONFields(data []byte, shape *jsonShape) *apiError {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := walkJSONValue(decoder, "request", shape); err != nil {
		var duplicate *duplicateFieldError
		if errors.As(err, &duplicate) {
			return validationError(duplicate.field, "request contains a duplicate field", err)
		}
		var unknown *unknownFieldError
		if errors.As(err, &unknown) {
			return validationError(unknown.field, "request contains an unknown field", err)
		}
		return validationError("request", "request body is invalid JSON", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil // The strict decoder reports the more useful trailing-value error.
	}
	return nil
}

func walkJSONValue(decoder *json.Decoder, path string, shape *jsonShape) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delim {
	case '{':
		return walkJSONObject(decoder, path, shape)
	case '[':
		return walkJSONArray(decoder, path, shape)
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
}

func walkJSONObject(decoder *json.Decoder, path string, shape *jsonShape) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return fmt.Errorf("object key at %s is not a string", path)
		}
		fieldPath := key
		if path != "request" {
			fieldPath = path + "." + key
		}
		if _, duplicate := seen[key]; duplicate {
			return &duplicateFieldError{field: fieldPath}
		}
		seen[key] = struct{}{}

		var childShape *jsonShape
		if shape != nil && shape.fields != nil {
			var allowed bool
			childShape, allowed = shape.fields[key]
			if !allowed {
				return &unknownFieldError{field: fieldPath}
			}
		}
		if err := walkJSONValue(decoder, fieldPath, childShape); err != nil {
			return err
		}
	}
	_, err := decoder.Token()
	return err
}

func walkJSONArray(decoder *json.Decoder, path string, shape *jsonShape) error {
	var elementShape *jsonShape
	if shape != nil {
		elementShape = shape.element
	}
	for index := 0; decoder.More(); index++ {
		if err := walkJSONValue(decoder, fmt.Sprintf("%s[%d]", path, index), elementShape); err != nil {
			return err
		}
	}
	_, err := decoder.Token()
	return err
}

type duplicateFieldError struct {
	field string
}

func (e *duplicateFieldError) Error() string {
	return "duplicate JSON field " + e.field
}

type unknownFieldError struct {
	field string
}

func (e *unknownFieldError) Error() string {
	return "unknown JSON field " + e.field
}

type jsonShape struct {
	fields  map[string]*jsonShape
	element *jsonShape
}

var (
	backoffJSONShape = &jsonShape{fields: map[string]*jsonShape{
		"initial_delay": nil,
		"max_delay":     nil,
		"multiplier":    nil,
		"jitter":        nil,
	}}
	actionJSONShape = &jsonShape{fields: map[string]*jsonShape{
		"type":        nil,
		"max_retries": nil,
		"backoff":     backoffJSONShape,
	}}
	targetJSONShape = &jsonShape{fields: map[string]*jsonShape{
		"kind":        nil,
		"provider_id": nil,
	}}
	ruleSpecJSONShape = &jsonShape{fields: map[string]*jsonShape{
		"name":       nil,
		"enabled":    nil,
		"target":     targetJSONShape,
		"api_type":   nil,
		"keywords":   {},
		"match_mode": nil,
		"action":     actionJSONShape,
	}}
	mutationJSONShape = &jsonShape{fields: map[string]*jsonShape{
		"schema_version": nil,
		"rule":           ruleSpecJSONShape,
	}}
	reorderJSONShape = &jsonShape{fields: map[string]*jsonShape{
		"schema_version":   nil,
		"ordered_rule_ids": {},
	}}
	messageBodyJSONShape = &jsonShape{fields: map[string]*jsonShape{
		"encoding": nil,
		"value":    nil,
	}}
	testMessageJSONShape = &jsonShape{fields: map[string]*jsonShape{
		"schema_version":   nil,
		"api_type":         nil,
		"provider_id":      nil,
		"content_type":     nil,
		"content_encoding": nil,
		"body":             messageBodyJSONShape,
	}}
)

func requestJSONShape(target any) *jsonShape {
	switch target.(type) {
	case *mutationRequest:
		return mutationJSONShape
	case *reorderRequest:
		return reorderJSONShape
	case *testMessageRequest:
		return testMessageJSONShape
	default:
		return nil
	}
}
