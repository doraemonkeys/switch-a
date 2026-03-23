package admin

import "net/http"

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
