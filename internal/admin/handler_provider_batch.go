package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"switch-a/internal/store"

	"go.uber.org/zap"
)

// Batch operation types

// BatchProviderRequest represents a batch operation request for providers.
type BatchProviderRequest struct {
	Action string   `json:"action"` // "reset" | "enable" | "disable" | "delete"
	IDs    []string `json:"ids"`
}

// BatchProviderResult represents the result of a single provider operation.
type BatchProviderResult struct {
	ID      string `json:"id"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// BatchProviderResponse represents the response for batch provider operations.
type BatchProviderResponse struct {
	Success  bool                  `json:"success"`
	Affected int                   `json:"affected"`
	Results  []BatchProviderResult `json:"results"`
}

// validBatchActions contains the allowed batch action values.
// This is unexported to prevent external mutation of global state.
var validBatchActions = map[string]bool{
	"reset":   true,
	"enable":  true,
	"disable": true,
	"delete":  true,
}

// BatchProviderAction handles POST /admin/api/providers/batch.
// Performs batch operations on multiple providers.
func (h *Handler) BatchProviderAction(w http.ResponseWriter, r *http.Request) {
	limitRequestBody(w, r)
	var req BatchProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid request body")
		return
	}

	// Validate action
	if !validBatchActions[req.Action] {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid action: must be 'reset', 'enable', 'disable', or 'delete'")
		return
	}

	// Validate IDs
	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "At least one provider ID is required")
		return
	}

	// Execute batch operation
	results := make([]BatchProviderResult, len(req.IDs))
	successCount := 0
	conflictCount := 0
	ctx := r.Context()

	for i, id := range req.IDs {
		result := BatchProviderResult{ID: id}
		var err error

		switch req.Action {
		case "reset":
			err = h.batchReset(ctx, id)
		case "enable":
			err = h.batchEnable(ctx, id)
		case "disable":
			err = h.batchDisable(ctx, id)
		case "delete":
			err = h.batchDelete(ctx, id)
		}

		if err != nil {
			result.Success = false
			result.Error = err.Error()
			if errors.Is(err, store.ErrRoutingPolicyReferenceConflict) {
				conflictCount++
			}
		} else {
			result.Success = true
			successCount++
		}
		results[i] = result
	}

	// Determine response
	resp := BatchProviderResponse{
		Success:  successCount == len(req.IDs),
		Affected: successCount,
		Results:  results,
	}

	// Use 207 Multi-Status for partial success
	status := http.StatusOK
	if successCount > 0 && successCount < len(req.IDs) {
		status = http.StatusMultiStatus
	} else if successCount == 0 {
		if conflictCount > 0 {
			status = http.StatusConflict
		} else {
			status = http.StatusBadRequest
		}
	}

	writeJSON(w, status, resp)
}

// batchReset resets a provider's circuit breaker state.
func (h *Handler) batchReset(ctx context.Context, id string) error {
	if _, err := h.getProviderOrError(ctx, id); err != nil {
		return err
	}

	// Reset via health manager
	if h.health != nil {
		if err := h.health.ManualEnable(ctx, id); err != nil {
			return errors.New("failed to reset provider: " + id)
		}
	}
	return nil
}

// batchEnable enables a provider.
func (h *Handler) batchEnable(ctx context.Context, id string) error {
	provider, err := h.getProviderOrError(ctx, id)
	if err != nil {
		return err
	}

	provider.Enabled = true
	if err := h.store.UpdateProvider(ctx, provider); err != nil {
		return errors.New("failed to enable provider: " + id)
	}

	// Update health manager state
	if h.health != nil {
		if err := h.health.ManualEnable(ctx, id); err != nil {
			h.logger.Warn("failed to enable provider in health manager", zap.String("id", id), zap.Error(err))
		}
	}
	return nil
}

// batchDisable disables a provider.
func (h *Handler) batchDisable(ctx context.Context, id string) error {
	provider, err := h.getProviderOrError(ctx, id)
	if err != nil {
		return err
	}

	provider.Enabled = false
	if err := h.store.UpdateProvider(ctx, provider); err != nil {
		return errors.New("failed to disable provider: " + id)
	}

	// Update health manager state
	if h.health != nil {
		if err := h.health.ManualDisable(ctx, id, "disabled via batch API"); err != nil {
			h.logger.Warn("failed to disable provider in health manager", zap.String("id", id), zap.Error(err))
		}
	}
	return nil
}

// batchDelete deletes a provider.
func (h *Handler) batchDelete(ctx context.Context, id string) error {
	if _, err := h.getProviderOrError(ctx, id); err != nil {
		return err
	}

	if err := h.store.DeleteProvider(ctx, id); err != nil {
		if errors.Is(err, store.ErrRoutingPolicyReferenceConflict) {
			return err
		}
		return errors.New("failed to delete provider: " + id)
	}

	// Clear concurrency counter
	if h.cleaner != nil {
		h.cleaner.ClearConcurrency(id)
	}

	// Clear circuit breaker failure history
	if h.health != nil {
		h.health.ResetCircuitBreaker(id)
	}

	return nil
}
