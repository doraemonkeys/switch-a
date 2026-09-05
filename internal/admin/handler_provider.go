package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/store"
	"github.com/doraemonkeys/switch-a/internal/upstreamtarget"

	"go.uber.org/zap"
)

// Provider API handlers

// ListProviders handles GET /admin/api/providers.
func (h *Handler) ListProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := h.store.ListProviders(r.Context())
	if err != nil {
		h.logger.Error("failed to list providers", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to list providers")
		return
	}

	// Collect provider IDs for batch health state fetch
	providerIDs := make([]string, len(providers))
	for i := range providers {
		providerIDs[i] = providers[i].ID
	}

	// Batch fetch health states to avoid N+1 queries.
	// Using partial failure design: if batch retrieval fails,
	// we log and continue without health states rather than failing the entire request.
	healthStates, err := h.store.GetHealthStatesByProviderIDs(r.Context(), providerIDs)
	if err != nil {
		h.logger.Warn("failed to batch fetch health states", zap.Error(err))
	} else {
		for i := range providers {
			if state, ok := healthStates[providers[i].ID]; ok {
				providers[i].Health = state
			}
		}
	}

	writeJSON(w, http.StatusOK, h.providerPayloads(providers))
}

// GetProvider handles GET /admin/api/providers/{id}.
func (h *Handler) GetProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Provider ID is required")
		return
	}

	provider, err := h.store.GetProvider(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "Provider not found: "+id)
			return
		}
		h.logger.Error("failed to get provider", zap.String("id", id), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to get provider")
		return
	}

	// Populate health state.
	// Using partial failure design: if health retrieval fails,
	// we log and continue rather than failing the entire request.
	state, err := h.store.GetHealthState(r.Context(), id)
	if err != nil {
		h.logger.Warn("failed to get health state", zap.String("id", id), zap.Error(err))
	} else {
		provider.Health = state
	}

	writeJSON(w, http.StatusOK, h.providerPayload(provider))
}

// CreateProviderRequest represents the request to create a provider.
type CreateProviderRequest struct {
	ClientDisguise        clientdisguise.Policy               `json:"client_disguise"`
	ID                    string                              `json:"id"`
	Name                  string                              `json:"name"`
	APITypes              []APITypeInput                      `json:"api_types"`
	NewCredentialSessions []NewProviderCredentialSessionInput `json:"new_credential_sessions,omitempty"`
	AuthMode              string                              `json:"auth_mode"`
	UsageLimitPolicy      model.ProviderUsageLimitPolicy      `json:"usage_limit_policy"`
	GroupID               *string                             `json:"group_id"`
	Weight                int                                 `json:"weight"`
	Priority              int                                 `json:"priority"`
	Concurrency           int                                 `json:"concurrency"`
	MaxRetries            *int                                `json:"max_retries"`
	Backoff               *model.BackoffPolicy                `json:"backoff"`
	Vendor                string                              `json:"vendor"`
	FailoverScope         *model.Scope                        `json:"failover_scope"`
	AcceptFailover        *model.Scope                        `json:"accept_failover"`
	Enabled               *bool                               `json:"enabled"`
}

// APITypeInput represents an API type entry with endpoint details.
type APITypeInput struct {
	APIType             string `json:"api_type"`
	BaseURL             string `json:"base_url"`
	CredentialSessionID string `json:"credential_session_id"`
}

func isValidBaseURL(raw string) bool {
	return upstreamtarget.ValidateBaseURL(raw) == nil
}

func validateAPITypeInputs(apiTypes []APITypeInput) string {
	if len(apiTypes) == 0 {
		return "At least one api_type is required"
	}
	seen := make(map[string]struct{}, len(apiTypes))
	for _, at := range apiTypes {
		if !IsValidAPIType(at.APIType) {
			return "Invalid api_type: " + at.APIType
		}
		if _, exists := seen[at.APIType]; exists {
			return "Duplicate api_type: " + at.APIType
		}
		seen[at.APIType] = struct{}{}
		if at.BaseURL == "" {
			return "base_url is required for api_type: " + at.APIType
		}
		if !isValidBaseURL(at.BaseURL) {
			return "Invalid base_url for api_type " + at.APIType + ": must be a valid absolute URL without a fragment"
		}
		if at.CredentialSessionID == "" {
			return "credential_session_id is required for api_type: " + at.APIType
		}
	}
	return ""
}

func validateProviderAPITypeConfiguration(provider *model.Provider) string {
	if len(provider.APITypes) == 0 {
		return "At least one api_type is required"
	}
	seen := make(map[string]struct{}, len(provider.APITypes))
	for _, at := range provider.APITypes {
		if !IsValidAPIType(at.APIType) {
			return "Invalid api_type: " + at.APIType
		}
		if _, exists := seen[at.APIType]; exists {
			return "Duplicate api_type: " + at.APIType
		}
		seen[at.APIType] = struct{}{}
		if at.BaseURL == "" {
			return "base_url is required for api_type: " + at.APIType
		}
		if !isValidBaseURL(at.BaseURL) {
			return "Invalid base_url for api_type " + at.APIType + ": must be a valid absolute URL without a fragment"
		}
	}
	return ""
}

func validateProviderConfiguration(provider *model.Provider) string {
	if errMsg := validateProviderAPITypeConfiguration(provider); errMsg != "" {
		return errMsg
	}

	if len(provider.CredentialSessions) != len(provider.APITypes) {
		return "every api_type requires one credential session reference"
	}
	return ""
}

// validate checks that all required fields are present and all provided fields have valid values.
// Returns an error message if validation fails, empty string otherwise.
func (req *CreateProviderRequest) validate() string {
	if err := req.ClientDisguise.Validate(); err != nil {
		return err.Error()
	}
	if req.ID == "" {
		return "Provider ID is required"
	}
	if req.Name == "" {
		return "Provider name is required"
	}
	if errMsg := validateAPITypeInputs(req.APITypes); errMsg != "" {
		return errMsg
	}
	if errMsg := validateNewProviderCredentialSessions(req.APITypes, req.NewCredentialSessions); errMsg != "" {
		return errMsg
	}
	if req.MaxRetries != nil && *req.MaxRetries < 0 {
		return "MaxRetries must be non-negative"
	}
	if req.Backoff != nil {
		if err := req.Backoff.Validate(); err != nil {
			return "Invalid backoff: " + err.Error()
		}
	}
	if req.FailoverScope != nil && !model.IsValidScope(*req.FailoverScope) {
		return "Invalid failover_scope: must be 'none', 'vendor', or 'any'"
	}
	if req.AcceptFailover != nil && !model.IsValidScope(*req.AcceptFailover) {
		return "Invalid accept_failover: must be 'none', 'vendor', or 'any'"
	}
	if !model.IsValidProviderUsageLimitPolicy(req.UsageLimitPolicy) {
		return "Invalid usage_limit_policy: must be 'switch_provider' or 'suspend'"
	}
	if req.AuthMode != "" && !IsValidAuthMode(req.AuthMode) {
		return "Invalid auth_mode: must be 'auto', 'bearer', or 'x-api-key'"
	}
	return ""
}

// toProvider converts the request to a Provider model with appropriate defaults applied.
func (req *CreateProviderRequest) toProvider() *model.Provider {
	apiTypes := make([]model.ProviderAPIType, len(req.APITypes))
	for i, at := range req.APITypes {
		apiTypes[i] = model.ProviderAPIType{
			ProviderID: req.ID,
			APIType:    at.APIType,
			BaseURL:    at.BaseURL,
		}
	}

	provider := &model.Provider{
		ClientDisguise:   req.ClientDisguise,
		ID:               req.ID,
		Name:             req.Name,
		APITypes:         apiTypes,
		AuthMode:         req.AuthMode,
		UsageLimitPolicy: req.UsageLimitPolicy,
		GroupID:          req.GroupID,
		Weight:           req.Weight,
		Priority:         req.Priority,
		Concurrency:      req.Concurrency,
		MaxRetries:       DefaultProviderMaxRetries,
		Backoff:          model.BackoffPolicy{},
		Vendor:           req.Vendor,
		FailoverScope:    model.ScopeAny,
		AcceptFailover:   model.ScopeAny,
		Enabled:          true,
	}
	provider.CredentialSessions = make([]credentialsession.RouteSnapshot, len(req.APITypes))
	for index := range req.APITypes {
		provider.CredentialSessions[index] = credentialsession.RouteSnapshot{
			RouteTargetID: req.ID,
			APIType:       req.APITypes[index].APIType,
			VendorScope:   req.Vendor,
			Credential: credentialsession.Snapshot{
				SessionID: req.APITypes[index].CredentialSessionID,
			},
		}
	}

	// Apply explicit values where provided
	if req.Enabled != nil {
		provider.Enabled = *req.Enabled
	}
	if req.MaxRetries != nil {
		provider.MaxRetries = *req.MaxRetries
	}
	if req.FailoverScope != nil {
		provider.FailoverScope = *req.FailoverScope
	}
	if req.AcceptFailover != nil {
		provider.AcceptFailover = *req.AcceptFailover
	}
	if req.Backoff != nil {
		provider.Backoff = *req.Backoff
	}
	if provider.Weight <= 0 {
		provider.Weight = DefaultWeight
	}
	if provider.AuthMode == "" {
		provider.AuthMode = DefaultAuthMode
	}

	return provider
}

func (h *Handler) handleProviderPersistenceError(
	w http.ResponseWriter,
	id string,
	action string,
	err error,
) bool {
	if errors.Is(err, store.ErrRoutingPolicyReferenceConflict) {
		writeError(w, http.StatusConflict, ErrCodeConflict, err.Error())
		return true
	}

	h.logger.Error("failed to "+action+" provider", zap.String("id", id), zap.Error(err))
	writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to "+action+" provider")
	return false
}

// CreateProvider handles POST /admin/api/providers.
func (h *Handler) CreateProvider(w http.ResponseWriter, r *http.Request) {
	limitRequestBody(w, r)
	var req CreateProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid request body")
		return
	}

	if errMsg := req.validate(); errMsg != "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, errMsg)
		return
	}

	// Check if provider already exists
	_, err := h.store.GetProvider(r.Context(), req.ID)
	if err == nil {
		writeError(w, http.StatusConflict, ErrCodeConflict, "Provider already exists: "+req.ID)
		return
	}
	if !errors.Is(err, store.ErrNotFound) {
		h.logger.Error("failed to check provider existence", zap.String("id", req.ID), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to create provider")
		return
	}

	provider := req.toProvider()
	newSessions, err := buildNewProviderCredentialSessions(req.NewCredentialSessions)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, err.Error())
		return
	}
	if errMsg := validateProviderConfiguration(provider); errMsg != "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, errMsg)
		return
	}

	// Validate GroupID exists if provided
	if provider.GroupID != nil {
		groupID, ok := h.validateAndResolveGroupID(w, r, *provider.GroupID)
		if !ok {
			return
		}
		provider.GroupID = groupID
	}

	// Check for contradictory failover configuration
	warnings := model.HasContradictoryConfig(provider)
	for _, warning := range warnings {
		h.logger.Warn("provider has contradictory failover config",
			zap.String("id", req.ID),
			zap.String("warning", warning))
	}
	if len(newSessions) > 0 {
		h.logger.Info("provider credential materialization started",
			zap.String("operation", providerCredentialMaterializationOperation),
			zap.String("provider_id", req.ID),
			zap.Strings("credential_session_ids", providerCredentialSessionIDs(newSessions)))
	}

	if err := h.mutateProviderGeneration(req.ID, func() error {
		if len(newSessions) == 0 {
			return h.store.CreateProvider(r.Context(), provider)
		}
		writer := h.providerCredentials
		if writer == nil {
			return fmt.Errorf("atomic provider credential writes are unavailable")
		}
		return writer.CreateProviderWithCredentialSessions(r.Context(), provider, newSessions)
	}); err != nil {
		h.handleProviderPersistenceError(w, req.ID, "create", err)
		return
	}
	if len(newSessions) > 0 {
		h.logger.Info("provider credential materialization completed",
			zap.String("operation", providerCredentialMaterializationOperation),
			zap.String("provider_id", req.ID),
			zap.Strings("credential_session_ids", providerCredentialSessionIDs(newSessions)))
	}
	persisted, err := h.store.GetProvider(r.Context(), req.ID)
	if err != nil {
		h.logger.Error("failed to reload created provider", zap.String("id", req.ID), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to create provider")
		return
	}

	writeJSON(w, http.StatusCreated, ProviderResponse{
		ProviderPayload: h.providerPayload(persisted),
		Warnings:        warnings,
	})
}

// UpdateProviderRequest represents the request to update a provider.
type UpdateProviderRequest struct {
	ClientDisguise        *clientdisguise.Policy              `json:"client_disguise"`
	Name                  *string                             `json:"name"`
	APITypes              []APITypeInput                      `json:"api_types"`
	NewCredentialSessions []NewProviderCredentialSessionInput `json:"new_credential_sessions,omitempty"`
	AuthMode              *string                             `json:"auth_mode"`
	UsageLimitPolicy      *model.ProviderUsageLimitPolicy     `json:"usage_limit_policy"`
	GroupID               *string                             `json:"group_id"`
	Weight                *int                                `json:"weight"`
	Priority              *int                                `json:"priority"`
	Concurrency           *int                                `json:"concurrency"`
	MaxRetries            *int                                `json:"max_retries"`
	Backoff               *model.BackoffPolicy                `json:"backoff"`
	Vendor                *string                             `json:"vendor"`
	FailoverScope         *model.Scope                        `json:"failover_scope"`
	AcceptFailover        *model.Scope                        `json:"accept_failover"`
	Enabled               *bool                               `json:"enabled"`
}

// validate checks that all provided fields have valid values.
// Returns an error message if validation fails, empty string otherwise.
func (req *UpdateProviderRequest) validate() string {
	if req.ClientDisguise != nil {
		if err := req.ClientDisguise.Validate(); err != nil {
			return err.Error()
		}
	}
	if req.Name != nil && *req.Name == "" {
		return "Name cannot be empty"
	}
	if req.Weight != nil && *req.Weight <= 0 {
		return "Weight must be positive"
	}
	if req.Concurrency != nil && *req.Concurrency < 0 {
		return "Concurrency cannot be negative"
	}
	if req.MaxRetries != nil && *req.MaxRetries < 0 {
		return "MaxRetries must be non-negative"
	}
	if req.APITypes != nil {
		if errMsg := validateAPITypeInputs(req.APITypes); errMsg != "" {
			return errMsg
		}
		if errMsg := validateNewProviderCredentialSessions(req.APITypes, req.NewCredentialSessions); errMsg != "" {
			return errMsg
		}
	} else if len(req.NewCredentialSessions) != 0 {
		return "new credential sessions require api_types"
	}
	if req.UsageLimitPolicy != nil && !model.IsValidProviderUsageLimitPolicy(*req.UsageLimitPolicy) {
		return "Invalid usage_limit_policy: must be 'switch_provider' or 'suspend'"
	}
	if req.AuthMode != nil && !IsValidAuthMode(*req.AuthMode) {
		return "Invalid auth_mode: must be 'auto', 'bearer', or 'x-api-key'"
	}
	if req.Backoff != nil {
		if err := req.Backoff.Validate(); err != nil {
			return "Invalid backoff: " + err.Error()
		}
	}
	if req.FailoverScope != nil && !model.IsValidScope(*req.FailoverScope) {
		return "Invalid failover_scope: must be 'none', 'vendor', or 'any'"
	}
	if req.AcceptFailover != nil && !model.IsValidScope(*req.AcceptFailover) {
		return "Invalid accept_failover: must be 'none', 'vendor', or 'any'"
	}
	return ""
}

// applyTo updates the provider fields from the request.
func (req *UpdateProviderRequest) applyTo(provider *model.Provider) {
	if req.ClientDisguise != nil {
		provider.ClientDisguise = *req.ClientDisguise
	}
	if req.Name != nil {
		provider.Name = *req.Name
	}
	if req.APITypes != nil {
		apiTypes := make([]model.ProviderAPIType, len(req.APITypes))
		for i, at := range req.APITypes {
			apiTypes[i] = model.ProviderAPIType{
				ProviderID: provider.ID,
				APIType:    at.APIType,
				BaseURL:    at.BaseURL,
			}
		}
		provider.APITypes = apiTypes
		provider.CredentialSessions = make([]credentialsession.RouteSnapshot, len(req.APITypes))
		for index := range req.APITypes {
			provider.CredentialSessions[index] = credentialsession.RouteSnapshot{
				RouteTargetID: provider.ID,
				APIType:       req.APITypes[index].APIType,
				Credential: credentialsession.Snapshot{
					SessionID: req.APITypes[index].CredentialSessionID,
				},
			}
		}
	}
	if req.AuthMode != nil {
		provider.AuthMode = *req.AuthMode
	}
	if req.UsageLimitPolicy != nil {
		provider.UsageLimitPolicy = *req.UsageLimitPolicy
	}
	if req.Weight != nil {
		provider.Weight = *req.Weight
	}
	if req.Priority != nil {
		provider.Priority = *req.Priority
	}
	if req.Concurrency != nil {
		provider.Concurrency = *req.Concurrency
	}
	if req.MaxRetries != nil {
		provider.MaxRetries = *req.MaxRetries
	}
	if req.Backoff != nil {
		provider.Backoff = *req.Backoff
	}
	if req.Vendor != nil {
		provider.Vendor = *req.Vendor
	}
	for index := range provider.CredentialSessions {
		provider.CredentialSessions[index].VendorScope = provider.Vendor
	}
	if req.FailoverScope != nil {
		provider.FailoverScope = *req.FailoverScope
	}
	if req.AcceptFailover != nil {
		provider.AcceptFailover = *req.AcceptFailover
	}
	if req.Enabled != nil {
		provider.Enabled = *req.Enabled
	}
}

// UpdateProvider handles PUT /admin/api/providers/{id}.
func (h *Handler) UpdateProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Provider ID is required")
		return
	}

	limitRequestBody(w, r)
	var req UpdateProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid request body")
		return
	}

	if errMsg := req.validate(); errMsg != "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, errMsg)
		return
	}

	provider, err := h.store.GetProvider(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "Provider not found: "+id)
			return
		}
		h.logger.Error("failed to get provider", zap.String("id", id), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to update provider")
		return
	}

	originalEnabled := provider.Enabled

	req.applyTo(provider)
	newSessions, err := buildNewProviderCredentialSessions(req.NewCredentialSessions)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, err.Error())
		return
	}
	if errMsg := validateProviderConfiguration(provider); errMsg != "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, errMsg)
		return
	}

	// Validate and resolve GroupID separately (requires store access)
	if req.GroupID != nil {
		groupID, ok := h.validateAndResolveGroupID(w, r, *req.GroupID)
		if !ok {
			return
		}
		provider.GroupID = groupID
	}

	// Check for contradictory failover configuration
	warnings := model.HasContradictoryConfig(provider)
	for _, warning := range warnings {
		h.logger.Warn("provider has contradictory failover config",
			zap.String("id", id),
			zap.String("warning", warning))
	}
	if len(newSessions) > 0 {
		h.logger.Info("provider credential materialization started",
			zap.String("operation", providerCredentialMaterializationOperation),
			zap.String("provider_id", id),
			zap.Strings("credential_session_ids", providerCredentialSessionIDs(newSessions)))
	}

	if err := h.mutateProviderGeneration(id, func() error {
		if len(newSessions) == 0 {
			return h.store.UpdateProvider(r.Context(), provider)
		}
		writer := h.providerCredentials
		if writer == nil {
			return fmt.Errorf("atomic provider credential writes are unavailable")
		}
		return writer.UpdateProviderWithCredentialSessions(r.Context(), provider, newSessions)
	}); err != nil {
		h.handleProviderPersistenceError(w, id, "update", err)
		return
	}
	if len(newSessions) > 0 {
		h.logger.Info("provider credential materialization completed",
			zap.String("operation", providerCredentialMaterializationOperation),
			zap.String("provider_id", id),
			zap.Strings("credential_session_ids", providerCredentialSessionIDs(newSessions)))
	}

	if provider.Enabled != originalEnabled {
		h.syncHealthManagerState(r.Context(), id, provider.Enabled)
	}
	persisted, err := h.store.GetProvider(r.Context(), id)
	if err != nil {
		h.logger.Error("failed to reload updated provider", zap.String("id", id), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to update provider")
		return
	}

	writeJSON(w, http.StatusOK, ProviderResponse{
		ProviderPayload: h.providerPayload(persisted),
		Warnings:        warnings,
	})
}

// DeleteProvider handles DELETE /admin/api/providers/{id}.
// Generation retirement and deletion share one selector lifecycle boundary so
// no dispatch can cross from the deleted provider snapshot into a recreated ID.
func (h *Handler) DeleteProvider(w http.ResponseWriter, r *http.Request) {
	h.handleDelete(w, r, deleteConfig{
		resourceType: "Provider",
		getFunc: func(ctx context.Context, id string) error {
			_, err := h.store.GetProvider(ctx, id)
			return err
		},
		deleteFunc: func(ctx context.Context, id string) error {
			if err := h.mutateProviderGeneration(id, func() error {
				return h.store.DeleteProvider(ctx, id)
			}); err != nil {
				return err
			}
			// Clear circuit breaker failure history to prevent memory leak.
			// The CircuitBreaker holds failure timestamps in a map that persist
			// until explicitly cleared or cleaned up by the periodic cleanup loop.
			if h.health != nil {
				h.health.ResetCircuitBreaker(id)
			}
			return nil
		},
	})
}
