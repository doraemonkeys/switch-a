package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth"
	"github.com/doraemonkeys/switch-a/internal/store"

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
	ID                          string                            `json:"id"`
	Name                        string                            `json:"name"`
	APIKey                      string                            `json:"api_key"`
	APITypes                    []APITypeInput                    `json:"api_types"`
	AuthMode                    string                            `json:"auth_mode"`
	CredentialType              model.ProviderCredentialType      `json:"credential_type"`
	UsageLimitPolicy            model.ProviderUsageLimitPolicy    `json:"usage_limit_policy"`
	CredentialLoginID           string                            `json:"credential_login_id,omitempty"`
	CredentialBindingResolution model.CredentialBindingResolution `json:"credential_binding_resolution,omitempty"`
	GroupID                     *string                           `json:"group_id"`
	Weight                      int                               `json:"weight"`
	Priority                    int                               `json:"priority"`
	Concurrency                 int                               `json:"concurrency"`
	MaxRetries                  *int                              `json:"max_retries"`     // Pointer to distinguish unset (nil) from explicit 0
	Backoff                     *model.BackoffPolicy              `json:"backoff"`         // Exponential backoff for same-provider retries
	Vendor                      string                            `json:"vendor"`          // Empty = no isolation, "*" = wildcard (see model.Provider.Vendor)
	FailoverScope               *model.Scope                      `json:"failover_scope"`  // Pointer to distinguish unset (nil) from explicit empty
	AcceptFailover              *model.Scope                      `json:"accept_failover"` // Governs true failover only; pre-visible replacement stays allowed
	Enabled                     *bool                             `json:"enabled"`
}

// APITypeInput represents an API type entry with endpoint details.
type APITypeInput struct {
	APIType string `json:"api_type"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
}

// isValidBaseURL checks that a base URL has a scheme and host,
// rejecting bare strings that url.Parse would accept silently.
func isValidBaseURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return u.Scheme != "" && u.Host != ""
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
			return "Invalid base_url for api_type " + at.APIType + ": must be a valid URL with scheme and host"
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
			return "Invalid base_url for api_type " + at.APIType + ": must be a valid URL with scheme and host"
		}
	}
	return ""
}

func validateProviderConfiguration(provider *model.Provider) string {
	if errMsg := validateProviderAPITypeConfiguration(provider); errMsg != "" {
		return errMsg
	}

	switch model.NormalizeProviderCredentialType(provider.CredentialType) {
	case model.ProviderCredentialTypeChatGPT:
		if !providerauth.HasCompleteChatGPTCredential(provider) {
			return "GPT login is incomplete or invalid for this provider"
		}
	default:
		for _, at := range provider.APITypes {
			if !model.HasAPIKey(provider.APIKey) && !model.HasAPIKey(at.APIKey) {
				return "api_key is required for api_type: " + at.APIType + " when provider default api_key is empty"
			}
		}
	}
	return ""
}

// validate checks that all required fields are present and all provided fields have valid values.
// Returns an error message if validation fails, empty string otherwise.
func (req *CreateProviderRequest) validate() string {
	if req.ID == "" {
		return "Provider ID is required"
	}
	if req.Name == "" {
		return "Provider name is required"
	}
	if model.NormalizeProviderCredentialType(req.CredentialType) == model.ProviderCredentialTypeChatGPT {
		if req.CredentialLoginID == "" {
			return "credential_login_id is required for chatgpt providers"
		}
	} else if errMsg := validateAPITypeInputs(req.APITypes); errMsg != "" {
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
	if !IsValidProviderCredentialType(req.CredentialType) {
		return "Invalid credential_type: must be 'api_key' or 'chatgpt'"
	}
	if !model.IsValidProviderUsageLimitPolicy(req.UsageLimitPolicy) {
		return "Invalid usage_limit_policy: must be 'switch_provider' or 'suspend'"
	}
	if !model.IsValidCredentialBindingResolution(req.CredentialBindingResolution) {
		return "Invalid credential_binding_resolution: must be 'reject' or 'replace'"
	}
	if req.CredentialBindingResolution == model.CredentialBindingResolutionReplace &&
		model.NormalizeProviderCredentialType(req.CredentialType) != model.ProviderCredentialTypeChatGPT {
		return "credential_binding_resolution=replace is only valid for chatgpt providers"
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
			APIKey:     model.NormalizeAPIKey(at.APIKey),
		}
	}

	credentialType := model.NormalizeProviderCredentialType(req.CredentialType)
	provider := &model.Provider{
		ID:               req.ID,
		Name:             req.Name,
		APIKey:           model.NormalizeAPIKey(req.APIKey),
		APITypes:         apiTypes,
		AuthMode:         req.AuthMode,
		CredentialType:   credentialType,
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

type providerPersistencePlan struct {
	chatGPTLoginID string
}

func (h *Handler) prepareProviderForPersistence(provider *model.Provider, credentialLoginID string) (providerPersistencePlan, string) {
	plan := providerPersistencePlan{}
	providerauth.NormalizeProviderForPersistence(provider)
	switch model.NormalizeProviderCredentialType(provider.CredentialType) {
	case model.ProviderCredentialTypeChatGPT:
		if credentialLoginID != "" {
			if h.auth == nil {
				return plan, "GPT login is unavailable in this build"
			}
			if err := h.auth.ApplyChatGPTLogin(provider, credentialLoginID); err != nil {
				return plan, err.Error()
			}
			plan.chatGPTLoginID = credentialLoginID
		}
		if !providerauth.HasCompleteChatGPTCredential(provider) {
			return plan, "GPT login is required for chatgpt credential providers"
		}
	default:
		provider.Credential = nil
	}
	return plan, ""
}

func (h *Handler) commitProviderPersistencePlan(plan providerPersistencePlan) {
	if plan.chatGPTLoginID == "" || h.auth == nil {
		return
	}
	// The provider write has already succeeded, so surfacing a cleanup failure to the
	// caller would misreport the durable result. Log and keep the login reusable.
	if err := h.auth.FinalizeChatGPTLogin(plan.chatGPTLoginID); err != nil {
		h.logger.Warn("failed to finalize chatgpt login after provider persistence",
			zap.String("login_id", plan.chatGPTLoginID),
			zap.Error(err))
	}
}

func (h *Handler) handleProviderPersistenceError(
	w http.ResponseWriter,
	id string,
	action string,
	err error,
) bool {
	var conflict *store.CredentialBindingConflictError
	if errors.As(err, &conflict) {
		h.logger.Warn("rejected provider persistence due to duplicate GPT credential binding",
			zap.String("id", id),
			zap.String("account_id", conflict.AccountID),
			zap.String("bound_provider_id", conflict.ProviderID),
		)
		writeErrorWithDetails(w, http.StatusConflict, ErrCodeConflict, conflict.Error(), map[string]string{
			"kind":        "credential_binding",
			"account_id":  conflict.AccountID,
			"provider_id": conflict.ProviderID,
		})
		return true
	}
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
	plan, errMsg := h.prepareProviderForPersistence(provider, req.CredentialLoginID)
	if errMsg != "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, errMsg)
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

	if err := h.mutateProviderGeneration(req.ID, func() error {
		return h.store.CreateProvider(r.Context(), provider, store.ProviderWriteOptions{
			CredentialBindingResolution: req.CredentialBindingResolution,
		})
	}); err != nil {
		h.handleProviderPersistenceError(w, req.ID, "create", err)
		return
	}

	h.commitProviderPersistencePlan(plan)
	writeJSON(w, http.StatusCreated, ProviderResponse{
		ProviderPayload: h.providerPayload(provider),
		Warnings:        warnings,
	})
}

// UpdateProviderRequest represents the request to update a provider.
type UpdateProviderRequest struct {
	Name                        *string                           `json:"name"`
	APIKey                      *string                           `json:"api_key"`
	APITypes                    []APITypeInput                    `json:"api_types"`
	AuthMode                    *string                           `json:"auth_mode"`
	CredentialType              *model.ProviderCredentialType     `json:"credential_type"`
	UsageLimitPolicy            *model.ProviderUsageLimitPolicy   `json:"usage_limit_policy"`
	CredentialLoginID           string                            `json:"credential_login_id,omitempty"`
	CredentialBindingResolution model.CredentialBindingResolution `json:"credential_binding_resolution,omitempty"`
	GroupID                     *string                           `json:"group_id"`
	Weight                      *int                              `json:"weight"`
	Priority                    *int                              `json:"priority"`
	Concurrency                 *int                              `json:"concurrency"`
	MaxRetries                  *int                              `json:"max_retries"`
	Backoff                     *model.BackoffPolicy              `json:"backoff"` // Exponential backoff for same-provider retries
	Vendor                      *string                           `json:"vendor"`
	FailoverScope               *model.Scope                      `json:"failover_scope"`
	AcceptFailover              *model.Scope                      `json:"accept_failover"` // Governs true failover only; pre-visible replacement stays allowed
	Enabled                     *bool                             `json:"enabled"`
}

// validate checks that all provided fields have valid values.
// Returns an error message if validation fails, empty string otherwise.
func (req *UpdateProviderRequest) validate() string {
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
	}
	if req.CredentialType != nil && !IsValidProviderCredentialType(*req.CredentialType) {
		return "Invalid credential_type: must be 'api_key' or 'chatgpt'"
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
	if !model.IsValidCredentialBindingResolution(req.CredentialBindingResolution) {
		return "Invalid credential_binding_resolution: must be 'reject' or 'replace'"
	}
	if req.CredentialBindingResolution == model.CredentialBindingResolutionReplace &&
		(req.CredentialType == nil || model.NormalizeProviderCredentialType(*req.CredentialType) != model.ProviderCredentialTypeChatGPT) {
		return "credential_binding_resolution=replace is only valid when updating a chatgpt provider"
	}
	return ""
}

// applyTo updates the provider fields from the request.
func (req *UpdateProviderRequest) applyTo(provider *model.Provider) {
	if req.Name != nil {
		provider.Name = *req.Name
	}
	if req.APIKey != nil {
		provider.APIKey = model.NormalizeAPIKey(*req.APIKey)
	}
	if req.APITypes != nil {
		apiTypes := make([]model.ProviderAPIType, len(req.APITypes))
		for i, at := range req.APITypes {
			apiTypes[i] = model.ProviderAPIType{
				ProviderID: provider.ID,
				APIType:    at.APIType,
				BaseURL:    at.BaseURL,
				APIKey:     model.NormalizeAPIKey(at.APIKey),
			}
		}
		provider.APITypes = apiTypes
	}
	if req.AuthMode != nil {
		provider.AuthMode = *req.AuthMode
	}
	if req.CredentialType != nil {
		provider.CredentialType = model.NormalizeProviderCredentialType(*req.CredentialType)
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
	plan, errMsg := h.prepareProviderForPersistence(provider, req.CredentialLoginID)
	if errMsg != "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, errMsg)
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

	if err := h.mutateProviderGeneration(id, func() error {
		return h.store.UpdateProvider(r.Context(), provider, store.ProviderWriteOptions{
			CredentialBindingResolution: req.CredentialBindingResolution,
		})
	}); err != nil {
		h.handleProviderPersistenceError(w, id, "update", err)
		return
	}

	h.commitProviderPersistencePlan(plan)
	if provider.Enabled != originalEnabled {
		h.syncHealthManagerState(r.Context(), id, provider.Enabled)
	}

	writeJSON(w, http.StatusOK, ProviderResponse{
		ProviderPayload: h.providerPayload(provider),
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
