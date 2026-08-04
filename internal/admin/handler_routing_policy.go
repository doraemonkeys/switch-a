package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/store"

	"go.uber.org/zap"
)

// RoutingPolicyPayload keeps the admin API contract flat so the frontend does
// not depend on storage-only join tables.
type RoutingPolicyPayload struct {
	ID               string                            `json:"id"`
	APIType          string                            `json:"api_type"`
	ModelMatchType   model.RoutingPolicyModelMatchType `json:"model_match_type,omitempty"`
	ModelMatchValue  string                            `json:"model_match_value,omitempty"`
	Enabled          bool                              `json:"enabled"`
	TargetProviderID *string                           `json:"target_provider_id,omitempty"`
	AllowedGroupIDs  []string                          `json:"allowed_group_ids"`
	AllowedVendors   []string                          `json:"allowed_vendors"`
	CreatedAt        time.Time                         `json:"created_at,omitzero"`
	UpdatedAt        time.Time                         `json:"updated_at,omitzero"`
}

type routingPolicyOptionalString struct {
	set   bool
	value *string
}

func (field *routingPolicyOptionalString) UnmarshalJSON(data []byte) error {
	field.set = true
	if string(data) == "null" {
		field.value = nil
		return nil
	}

	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	field.value = &value
	return nil
}

func (field routingPolicyOptionalString) IsSet() bool {
	return field.set
}

func (field routingPolicyOptionalString) Value() *string {
	return field.value
}

// RoutingPolicyRequest mirrors the frontend write contract for full-rule upserts.
type RoutingPolicyRequest struct {
	APIType          string                             `json:"api_type"`
	ModelMatchType   *model.RoutingPolicyModelMatchType `json:"model_match_type"`
	ModelMatchValue  *string                            `json:"model_match_value"`
	Enabled          *bool                              `json:"enabled"`
	TargetProviderID routingPolicyOptionalString        `json:"target_provider_id"`
	AllowedGroupIDs  []string                           `json:"allowed_group_ids"`
	AllowedVendors   []string                           `json:"allowed_vendors"`
}

type routingPolicyValidationError struct {
	message string
}

func (e *routingPolicyValidationError) Error() string {
	return e.message
}

func invalidRoutingPolicy(message string) error {
	return &routingPolicyValidationError{message: message}
}

func formatRoutingPolicyID(id uint) string {
	return strconv.FormatUint(uint64(id), 10)
}

func parseRoutingPolicyID(raw string) (uint, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, invalidRoutingPolicy("Routing policy ID is required")
	}
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 {
		return 0, invalidRoutingPolicy("Routing policy ID must be a positive integer")
	}
	return uint(id), nil
}

func normalizeRoutingPolicyStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	sort.Strings(normalized)
	if len(normalized) == 0 {
		return []string{}
	}
	return normalized
}

func routingPolicyPayload(policy *model.RoutingPolicy) RoutingPolicyPayload {
	groupIDs := make([]string, 0, len(policy.Groups))
	for _, group := range policy.Groups {
		groupIDs = append(groupIDs, group.GroupID)
	}
	vendors := make([]string, 0, len(policy.Vendors))
	for _, vendor := range policy.Vendors {
		vendors = append(vendors, vendor.Vendor)
	}
	var targetProviderID *string
	if policy.TargetProviderID != nil {
		trimmed := strings.TrimSpace(*policy.TargetProviderID)
		if trimmed != "" {
			targetProviderID = &trimmed
		}
	}

	return RoutingPolicyPayload{
		ID:               formatRoutingPolicyID(policy.ID),
		APIType:          policy.APIType,
		ModelMatchType:   policy.ModelMatchType,
		ModelMatchValue:  policy.ModelMatchValue,
		Enabled:          policy.Enabled,
		TargetProviderID: targetProviderID,
		AllowedGroupIDs:  normalizeRoutingPolicyStrings(groupIDs),
		AllowedVendors:   normalizeRoutingPolicyStrings(vendors),
		CreatedAt:        policy.CreatedAt,
		UpdatedAt:        policy.UpdatedAt,
	}
}

func routingPolicyPayloads(policies []model.RoutingPolicy) []RoutingPolicyPayload {
	payloads := make([]RoutingPolicyPayload, 0, len(policies))
	for i := range policies {
		payloads = append(payloads, routingPolicyPayload(&policies[i]))
	}
	return payloads
}

func (h *Handler) buildRoutingPolicy(
	ctx context.Context,
	req RoutingPolicyRequest,
	current *model.RoutingPolicy,
) (*model.RoutingPolicy, error) {
	catalog, err := h.loadRoutingPolicyCatalog(ctx)
	if err != nil {
		return nil, err
	}
	return buildRoutingPolicyFromCatalog(routingPolicySpec{
		APIType:             req.APIType,
		ModelMatchType:      req.ModelMatchType,
		ModelMatchValue:     req.ModelMatchValue,
		Enabled:             req.Enabled,
		TargetProviderID:    req.TargetProviderID.Value(),
		TargetProviderIDSet: req.TargetProviderID.IsSet(),
		AllowedGroupIDs:     req.AllowedGroupIDs,
		AllowedVendors:      req.AllowedVendors,
	}, catalog, current)
}

func writeRoutingPolicyError(w http.ResponseWriter, err error) bool {
	var validationErr *routingPolicyValidationError
	if errors.As(err, &validationErr) {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, validationErr.message)
		return true
	}
	return false
}

// ListRoutingPolicies handles GET /admin/api/routing-policies.
func (h *Handler) ListRoutingPolicies(w http.ResponseWriter, r *http.Request) {
	policies, err := h.store.ListRoutingPolicies(r.Context())
	if err != nil {
		h.logger.Error("failed to list routing policies", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to list routing policies")
		return
	}
	writeJSON(w, http.StatusOK, routingPolicyPayloads(policies))
}

// GetRoutingPolicy handles GET /admin/api/routing-policies/{id}.
func (h *Handler) GetRoutingPolicy(w http.ResponseWriter, r *http.Request) {
	id, err := parseRoutingPolicyID(r.PathValue("id"))
	if err != nil {
		_ = writeRoutingPolicyError(w, err)
		return
	}

	policy, err := h.store.GetRoutingPolicy(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "Routing policy not found: "+formatRoutingPolicyID(id))
			return
		}
		h.logger.Error("failed to get routing policy", zap.Uint("id", id), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to get routing policy")
		return
	}

	writeJSON(w, http.StatusOK, routingPolicyPayload(policy))
}

// CreateRoutingPolicy handles POST /admin/api/routing-policies.
func (h *Handler) CreateRoutingPolicy(w http.ResponseWriter, r *http.Request) {
	limitRequestBody(w, r)
	var req RoutingPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid request body")
		return
	}

	policy, err := h.buildRoutingPolicy(r.Context(), req, nil)
	if err != nil {
		if writeRoutingPolicyError(w, err) {
			return
		}
		h.logger.Error("failed to validate routing policy", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to create routing policy")
		return
	}

	if err := h.mutateAllProviderGenerations(func() error {
		return h.store.CreateRoutingPolicy(r.Context(), policy)
	}); err != nil {
		if errors.Is(err, store.ErrRoutingPolicyConflict) {
			writeError(w, http.StatusConflict, ErrCodeConflict, err.Error())
			return
		}
		h.logger.Error("failed to create routing policy", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to create routing policy")
		return
	}

	writeJSON(w, http.StatusCreated, routingPolicyPayload(policy))
}

// UpdateRoutingPolicy handles PUT /admin/api/routing-policies/{id}.
func (h *Handler) UpdateRoutingPolicy(w http.ResponseWriter, r *http.Request) {
	id, err := parseRoutingPolicyID(r.PathValue("id"))
	if err != nil {
		_ = writeRoutingPolicyError(w, err)
		return
	}

	limitRequestBody(w, r)
	var req RoutingPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid request body")
		return
	}

	current, err := h.store.GetRoutingPolicy(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "Routing policy not found: "+formatRoutingPolicyID(id))
			return
		}
		h.logger.Error("failed to get routing policy before update", zap.Uint("id", id), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to update routing policy")
		return
	}

	policy, err := h.buildRoutingPolicy(r.Context(), req, current)
	if err != nil {
		if writeRoutingPolicyError(w, err) {
			return
		}
		h.logger.Error("failed to validate routing policy", zap.Uint("id", id), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to update routing policy")
		return
	}
	policy.ID = id

	if err := h.mutateAllProviderGenerations(func() error {
		return h.store.UpdateRoutingPolicy(r.Context(), policy)
	}); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "Routing policy not found: "+formatRoutingPolicyID(id))
			return
		case errors.Is(err, store.ErrRoutingPolicyConflict):
			writeError(w, http.StatusConflict, ErrCodeConflict, err.Error())
			return
		default:
			h.logger.Error("failed to update routing policy", zap.Uint("id", id), zap.Error(err))
			writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to update routing policy")
			return
		}
	}

	writeJSON(w, http.StatusOK, routingPolicyPayload(policy))
}

// DeleteRoutingPolicy handles DELETE /admin/api/routing-policies/{id}.
func (h *Handler) DeleteRoutingPolicy(w http.ResponseWriter, r *http.Request) {
	id, err := parseRoutingPolicyID(r.PathValue("id"))
	if err != nil {
		_ = writeRoutingPolicyError(w, err)
		return
	}

	if err := h.mutateAllProviderGenerations(func() error {
		return h.store.DeleteRoutingPolicy(r.Context(), id)
	}); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "Routing policy not found: "+formatRoutingPolicyID(id))
			return
		}
		h.logger.Error("failed to delete routing policy", zap.Uint("id", id), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to delete routing policy")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
