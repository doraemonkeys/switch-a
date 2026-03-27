package admin

import (
	"context"
	"errors"
	"net/http"

	"switch-a/internal/model"
	"switch-a/internal/providerauth"
	"switch-a/internal/store"

	"go.uber.org/zap"
)

// StartChatGPTProviderLogin handles POST /admin/api/provider-auth/chatgpt/start.
func (h *Handler) StartChatGPTProviderLogin(w http.ResponseWriter, _ *http.Request) {
	if h.auth == nil {
		writeError(w, http.StatusNotImplemented, ErrCodeInternal, "GPT login is unavailable in this build")
		return
	}

	start, err := h.auth.StartChatGPTLogin()
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, start)
}

// GetChatGPTProviderLoginStatus handles GET /admin/api/provider-auth/chatgpt/sessions/{login_id}.
func (h *Handler) GetChatGPTProviderLoginStatus(w http.ResponseWriter, r *http.Request) {
	if h.auth == nil {
		writeError(w, http.StatusNotImplemented, ErrCodeInternal, "GPT login is unavailable in this build")
		return
	}

	loginID := r.PathValue("login_id")
	if loginID == "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "login_id is required")
		return
	}

	status, err := h.auth.GetChatGPTLoginStatus(loginID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, status)
}

// RefreshProviderCredential handles POST /admin/api/providers/{id}/refresh-credential.
func (h *Handler) RefreshProviderCredential(w http.ResponseWriter, r *http.Request) {
	h.runProviderAuthAction(
		w,
		r,
		"refresh provider credential",
		func(ctx context.Context, provider *model.Provider) (bool, error) {
			return h.auth.RefreshProviderCredentials(ctx, provider)
		},
	)
}

// RefreshProviderUsage handles POST /admin/api/providers/{id}/refresh-usage.
func (h *Handler) RefreshProviderUsage(w http.ResponseWriter, r *http.Request) {
	h.runProviderAuthAction(
		w,
		r,
		"refresh provider usage",
		func(ctx context.Context, provider *model.Provider) (bool, error) {
			return h.auth.RefreshProviderUsage(ctx, provider)
		},
	)
}

type providerAuthAction func(ctx context.Context, provider *model.Provider) (bool, error)

func (h *Handler) runProviderAuthAction(
	w http.ResponseWriter,
	r *http.Request,
	action string,
	run providerAuthAction,
) {
	if h.auth == nil {
		writeError(w, http.StatusNotImplemented, ErrCodeInternal, "GPT login is unavailable in this build")
		return
	}

	provider := h.getProviderForAuthAction(w, r, action)
	if provider == nil {
		return
	}

	supported, err := run(r.Context(), provider)
	if !supported {
		writeError(
			w,
			http.StatusConflict,
			ErrCodeConflict,
			action+" is not supported for credential_type "+string(model.NormalizeProviderCredentialType(provider.CredentialType)),
		)
		return
	}
	if err != nil {
		h.writeProviderAuthActionError(w, provider, action, err)
		return
	}

	if state, stateErr := h.store.GetHealthState(r.Context(), provider.ID); stateErr != nil {
		h.logger.Warn("failed to get health state after provider auth action",
			zap.String("provider_id", provider.ID),
			zap.String("action", action),
			zap.Error(stateErr),
		)
	} else {
		provider.Health = state
	}

	writeJSON(w, http.StatusOK, h.providerPayload(provider))
}

func (h *Handler) getProviderForAuthAction(w http.ResponseWriter, r *http.Request, action string) *model.Provider {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Provider ID is required")
		return nil
	}

	provider, err := h.store.GetProvider(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrCodeNotFound, "Provider not found: "+id)
			return nil
		}
		h.logger.Error("failed to get provider", zap.String("id", id), zap.String("action", action), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to "+action)
		return nil
	}
	return provider
}

func (h *Handler) writeProviderAuthActionError(
	w http.ResponseWriter,
	provider *model.Provider,
	action string,
	err error,
) {
	var stateErr *providerauth.ProviderAuthStateError
	if errors.As(err, &stateErr) {
		writeError(w, http.StatusConflict, ErrCodeConflict, stateErr.Error())
		return
	}

	h.logger.Warn("provider auth action failed",
		zap.String("provider_id", provider.ID),
		zap.String("action", action),
		zap.Error(err),
	)
	writeError(w, http.StatusBadGateway, ErrCodeInternal, "Failed to "+action)
}
