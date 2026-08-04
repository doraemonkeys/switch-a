// Package errorruleapi provides the strict administrative HTTP surface for
// internal-error rules without expanding the broad root admin Store interface.
package errorruleapi

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/doraemonkeys/switch-a/internal/errorrule"
	errorrulesqlite "github.com/doraemonkeys/switch-a/internal/errorrule/sqlite"
	"go.uber.org/zap"
)

const createdRuleLocationPrefix = "/admin/api/internal-error-rules/"

type Config struct {
	Rules        RuleService
	Stats        RuleStatsReader
	StatsOverlay RuleStatsOverlay
	Providers    ProviderCatalog
	Analyzer     MessageAnalyzer
	OperationIDs errorrule.IDGenerator
	Logger       *zap.Logger
}

type Handler struct {
	service      *service
	operationIDs errorrule.IDGenerator
	logger       *zap.Logger
}

func NewHandler(config Config) (*Handler, error) {
	if config.Rules == nil {
		return nil, fmt.Errorf("internal-error rule service is required")
	}
	if config.Stats == nil {
		return nil, fmt.Errorf("internal-error rule statistics reader is required")
	}
	if config.StatsOverlay == nil {
		return nil, fmt.Errorf("internal-error rule statistics overlay is required")
	}
	if config.Providers == nil {
		return nil, fmt.Errorf("provider catalog is required")
	}
	if config.Analyzer == nil {
		config.Analyzer = NewRegistryAnalyzer()
	}
	if config.OperationIDs == nil {
		config.OperationIDs = errorrule.UUIDGenerator{}
	}
	if config.Logger == nil {
		config.Logger = zap.NewNop()
	}
	return &Handler{
		service: &service{
			rules: config.Rules, stats: config.Stats, overlay: config.StatsOverlay,
			providers: config.Providers, analyzer: config.Analyzer,
		},
		operationIDs: config.OperationIDs,
		logger:       config.Logger,
	}, nil
}

func (h *Handler) ListRules(w http.ResponseWriter, _ *http.Request) {
	if !h.available(w) {
		return
	}
	revision, rules := h.service.listRules()
	w.Header().Set("ETag", FormatRuleSetETag(revision))
	writeJSON(w, http.StatusOK, newRuleListResponse(revision, rules))
}

func (h *Handler) GetRule(w http.ResponseWriter, r *http.Request) {
	if !h.available(w) {
		return
	}
	id, apiErr := ruleIDFromRequest(r)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	revision, rule, err := h.service.getRule(id)
	if err != nil {
		h.writeServiceError(w, "get", "", id, err)
		return
	}
	w.Header().Set("ETag", FormatRuleSetETag(revision))
	writeJSON(w, http.StatusOK, newRuleResponse(revision, rule))
}

func (h *Handler) CreateRule(w http.ResponseWriter, r *http.Request) {
	if !h.available(w) {
		return
	}
	operationID := h.operationIDs.NewID()
	expected, apiErr := parseIfMatch(r.Header, true)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	var request mutationRequest
	if apiErr := decodeRequest(w, r, MaxRuleMutationRequestBytes, &request); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	spec, apiErr := request.domainRule()
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	result, rule, err := h.service.createRule(r.Context(), *expected, spec)
	if err != nil {
		h.writeServiceError(w, "create", operationID, "", err)
		return
	}
	w.Header().Set("ETag", FormatRuleSetETag(result.Revision))
	w.Header().Set("Location", createdRuleLocationPrefix+string(rule.ID))
	h.logMutation("create", operationID, rule.ID, result)
	writeJSON(w, http.StatusCreated, newRuleResponse(result.Revision, rule))
}

func (h *Handler) UpdateRule(w http.ResponseWriter, r *http.Request) {
	if !h.available(w) {
		return
	}
	operationID := h.operationIDs.NewID()
	id, apiErr := ruleIDFromRequest(r)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	expected, apiErr := parseIfMatch(r.Header, true)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	var request mutationRequest
	if apiErr := decodeRequest(w, r, MaxRuleMutationRequestBytes, &request); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	spec, apiErr := request.domainRule()
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	result, rule, err := h.service.updateRule(r.Context(), *expected, id, spec)
	if err != nil {
		h.writeServiceError(w, "update", operationID, id, err)
		return
	}
	w.Header().Set("ETag", FormatRuleSetETag(result.Revision))
	h.logMutation("update", operationID, id, result)
	writeJSON(w, http.StatusOK, newRuleResponse(result.Revision, rule))
}

func (h *Handler) DeleteRule(w http.ResponseWriter, r *http.Request) {
	if !h.available(w) {
		return
	}
	operationID := h.operationIDs.NewID()
	id, apiErr := ruleIDFromRequest(r)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	expected, apiErr := parseIfMatch(r.Header, true)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	result, err := h.service.deleteRule(r.Context(), *expected, id)
	if err != nil {
		h.writeServiceError(w, "delete", operationID, id, err)
		return
	}
	w.Header().Set("ETag", FormatRuleSetETag(result.Revision))
	h.logMutation("delete", operationID, id, result)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ReorderRules(w http.ResponseWriter, r *http.Request) {
	if !h.available(w) {
		return
	}
	operationID := h.operationIDs.NewID()
	expected, apiErr := parseIfMatch(r.Header, true)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	var request reorderRequest
	if apiErr := decodeRequest(w, r, MaxRuleReorderRequestBytes, &request); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	ordered, apiErr := request.ruleIDs()
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	result, err := h.service.reorderRules(r.Context(), *expected, ordered)
	if err != nil {
		h.writeServiceError(w, "reorder", operationID, "", err)
		return
	}
	w.Header().Set("ETag", FormatRuleSetETag(result.Revision))
	h.logMutation("reorder", operationID, "", result)
	writeJSON(w, http.StatusOK, newRuleListResponse(result.Revision, result.Rules))
}

func (h *Handler) GetStats(w http.ResponseWriter, r *http.Request) {
	if !h.available(w) {
		return
	}
	revision, stats, err := h.service.listStats(r.Context())
	if err != nil {
		h.writeServiceError(w, "stats", "", "", err)
		return
	}
	writeJSON(w, http.StatusOK, newStatsResponse(revision, stats))
}

func (h *Handler) TestMessage(w http.ResponseWriter, r *http.Request) {
	if !h.available(w) {
		return
	}
	expected, apiErr := parseIfMatch(r.Header, false)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	var request testMessageRequest
	if apiErr := decodeRequest(w, r, MaxTestMessageRequestBytes, &request); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	input, apiErr := request.input()
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	response, err := h.service.testMessage(r.Context(), expected, input)
	if err != nil {
		h.writeServiceError(w, "test_message", "", "", err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) available(w http.ResponseWriter) bool {
	if h != nil && h.service != nil {
		return true
	}
	writeAPIError(w, internalError("internal-error API is unavailable", nil))
	return false
}

func ruleIDFromRequest(r *http.Request) (errorrule.RuleID, *apiError) {
	if r == nil {
		return "", validationError("rule_id", "rule_id is required", nil)
	}
	id := errorrule.RuleID(r.PathValue("id"))
	if err := id.Validate(); err != nil {
		return "", validationError("rule_id", "rule_id must be a lowercase canonical UUIDv4", err)
	}
	return id, nil
}

func (h *Handler) writeServiceError(
	w http.ResponseWriter,
	operation, operationID string,
	ruleID errorrule.RuleID,
	err error,
) {
	apiErr := serviceAPIError(ruleID, err)
	if apiErr.Status >= http.StatusInternalServerError {
		if operationID == "" {
			operationID = h.operationIDs.NewID()
		}
		fields := []zap.Field{zap.String("operation", operation), zap.Error(err)}
		fields = append(fields, zap.String("operation_id", operationID))
		if ruleID != "" {
			fields = append(fields, zap.String("rule_id", string(ruleID)))
		}
		h.logger.Error("internal-error admin operation failed", fields...)
	}
	writeAPIError(w, apiErr)
}

func serviceAPIError(ruleID errorrule.RuleID, err error) *apiError {
	var direct *apiError
	if errors.As(err, &direct) {
		return direct
	}
	var mismatch *errorrulesqlite.RevisionMismatchError
	if errors.As(err, &mismatch) {
		return &apiError{
			Status: http.StatusPreconditionFailed, Code: ErrorCodeRevisionMismatch,
			Message: "internal error rule revision changed",
			Details: map[string]any{"current_revision": mismatch.Current.String()}, Cause: err,
		}
	}
	var missingProvider *providerNotFoundError
	if errors.As(err, &missingProvider) {
		return providerNotFoundAPIError(missingProvider.providerID, err)
	}
	var repositoryMissingProvider *errorrulesqlite.ProviderNotFoundError
	if errors.As(err, &repositoryMissingProvider) {
		return providerNotFoundAPIError(repositoryMissingProvider.ProviderID, err)
	}
	switch {
	case errors.Is(err, errorrulesqlite.ErrRuleNotFound):
		return &apiError{
			Status: http.StatusNotFound, Code: ErrorCodeNotFound,
			Message: "internal error rule not found", Details: map[string]any{"rule_id": string(ruleID)}, Cause: err,
		}
	case errors.Is(err, errorrulesqlite.ErrRuleCapacity):
		return &apiError{
			Status: http.StatusConflict, Code: ErrorCodeConflict,
			Message: "internal error rule capacity reached", Details: map[string]any{"limit": errorrule.MaxRuleCount}, Cause: err,
		}
	case errors.Is(err, errorrulesqlite.ErrRevisionOverflow):
		return &apiError{
			Status: http.StatusConflict, Code: ErrorCodeConflict,
			Message: "internal error rule revision is exhausted", Details: map[string]any{}, Cause: err,
		}
	default:
		return internalError("failed to compile internal error rules", err)
	}
}

func providerNotFoundAPIError(providerID string, cause error) *apiError {
	return &apiError{
		Status: http.StatusNotFound, Code: ErrorCodeNotFound,
		Message: "provider not found", Details: map[string]any{"provider_id": providerID}, Cause: cause,
	}
}

func (h *Handler) logMutation(
	operation, operationID string,
	ruleID errorrule.RuleID,
	result errorrulesqlite.MutationResult,
) {
	fields := []zap.Field{
		zap.String("operation_id", operationID),
		zap.String("operation", operation),
		zap.String("rule_set_revision", result.Revision.String()),
		zap.Bool("changed", result.Changed),
	}
	if ruleID != "" {
		fields = append(fields, zap.String("rule_id", string(ruleID)))
	}
	h.logger.Info("internal-error rule mutation completed", fields...)
}
