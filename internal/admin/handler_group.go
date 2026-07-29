package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/store"

	"go.uber.org/zap"
)

// Group API handlers

// CreateGroupRequest represents the request to create a group.
type CreateGroupRequest struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Strategy string `json:"strategy"`
	Priority int    `json:"priority"`
	Weight   int    `json:"weight"`
	Enabled  *bool  `json:"enabled"`
}

// UpdateGroupRequest represents the request to update a group.
type UpdateGroupRequest struct {
	Name     *string `json:"name"`
	Strategy *string `json:"strategy"`
	Priority *int    `json:"priority"`
	Weight   *int    `json:"weight"`
	Enabled  *bool   `json:"enabled"`
}

// ListGroups handles GET /admin/api/groups.
func (h *Handler) ListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := h.store.ListGroups(r.Context())
	if err != nil {
		h.logger.Error("failed to list groups", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to list groups")
		return
	}

	writeJSON(w, http.StatusOK, groups)
}

// GetGroup handles GET /admin/api/groups/{id}.
func (h *Handler) GetGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Group ID is required")
		return
	}

	group, err := h.store.GetGroup(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "Group not found: "+id)
			return
		}
		h.logger.Error("failed to get group", zap.String("id", id), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to get group")
		return
	}

	writeJSON(w, http.StatusOK, group)
}

// CreateGroup handles POST /admin/api/groups.
func (h *Handler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	limitRequestBody(w, r)
	var req CreateGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid request body")
		return
	}

	if req.ID == "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Group ID is required")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Group name is required")
		return
	}

	// Check if group already exists
	_, err := h.store.GetGroup(r.Context(), req.ID)
	if err == nil {
		writeError(w, http.StatusConflict, ErrCodeConflict, "Group already exists: "+req.ID)
		return
	}
	if !errors.Is(err, store.ErrNotFound) {
		h.logger.Error("failed to check group existence", zap.String("id", req.ID), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to create group")
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	group := &model.Group{
		ID:       req.ID,
		Name:     req.Name,
		Strategy: req.Strategy,
		Priority: req.Priority,
		Weight:   req.Weight,
		Enabled:  enabled,
	}

	// Set defaults and validate
	if group.Strategy == "" {
		group.Strategy = DefaultStrategy
	} else if !IsValidStrategy(group.Strategy) {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid strategy: must be 'priority', 'random', or 'weight'")
		return
	}
	if group.Weight <= 0 {
		group.Weight = DefaultWeight
	}
	// Validate priority is not reserved for ungrouped providers
	if group.Priority == ReservedGroupPriority {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, fmt.Sprintf("Priority value %d is reserved for ungrouped providers", ReservedGroupPriority))
		return
	}

	if err := h.store.CreateGroup(r.Context(), group); err != nil {
		h.logger.Error("failed to create group", zap.String("id", req.ID), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to create group")
		return
	}

	writeJSON(w, http.StatusCreated, group)
}

// UpdateGroup handles PUT /admin/api/groups/{id}.
func (h *Handler) UpdateGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Group ID is required")
		return
	}

	limitRequestBody(w, r)
	var req UpdateGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid request body")
		return
	}

	// Validate fields before fetching group
	if req.Name != nil && *req.Name == "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Name cannot be empty")
		return
	}
	if req.Weight != nil && *req.Weight <= 0 {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Weight must be positive")
		return
	}

	group, err := h.store.GetGroup(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "Group not found: "+id)
			return
		}
		h.logger.Error("failed to get group", zap.String("id", id), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to update group")
		return
	}

	// Update fields if provided
	if req.Name != nil {
		group.Name = *req.Name
	}
	if req.Strategy != nil {
		if !IsValidStrategy(*req.Strategy) {
			writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid strategy: must be 'priority', 'random', or 'weight'")
			return
		}
		group.Strategy = *req.Strategy
	}
	if req.Priority != nil {
		// Validate priority is not reserved for ungrouped providers
		if *req.Priority == ReservedGroupPriority {
			writeError(w, http.StatusBadRequest, ErrCodeValidation, fmt.Sprintf("Priority value %d is reserved for ungrouped providers", ReservedGroupPriority))
			return
		}
		group.Priority = *req.Priority
	}
	if req.Weight != nil {
		group.Weight = *req.Weight
	}
	if req.Enabled != nil {
		group.Enabled = *req.Enabled
	}

	if err := h.store.UpdateGroup(r.Context(), group); err != nil {
		h.logger.Error("failed to update group", zap.String("id", id), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to update group")
		return
	}

	writeJSON(w, http.StatusOK, group)
}

// DeleteGroup handles DELETE /admin/api/groups/{id}.
func (h *Handler) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	h.handleDelete(w, r, deleteConfig{
		resourceType: "Group",
		getFunc: func(ctx context.Context, id string) error {
			_, err := h.store.GetGroup(ctx, id)
			return err
		},
		deleteFunc: h.store.DeleteGroup,
	})
}

// EnableGroup handles POST /admin/api/groups/{id}/enable.
func (h *Handler) EnableGroup(w http.ResponseWriter, r *http.Request) {
	h.setGroupEnabled(w, r, true)
}

// DisableGroup handles POST /admin/api/groups/{id}/disable.
func (h *Handler) DisableGroup(w http.ResponseWriter, r *http.Request) {
	h.setGroupEnabled(w, r, false)
}

// setGroupEnabled is a helper to enable or disable a group.
func (h *Handler) setGroupEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	action := "enable"
	if !enabled {
		action = "disable"
	}

	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Group ID is required")
		return
	}

	group, err := h.store.GetGroup(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "Group not found: "+id)
			return
		}
		h.logger.Error("failed to get group", zap.String("id", id), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to "+action+" group")
		return
	}

	group.Enabled = enabled
	if err := h.store.UpdateGroup(r.Context(), group); err != nil {
		h.logger.Error("failed to update group", zap.String("id", id), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to "+action+" group")
		return
	}

	writeJSON(w, http.StatusOK, group)
}
