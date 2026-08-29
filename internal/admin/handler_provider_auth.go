package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/providerauth"

	"go.uber.org/zap"
)

const codexAuthExportFilename = "auth.json"

const codexAuthExportBlockedDetailKind = "credential_session_export_blocked"

type codexAuthExportBlockedDetails struct {
	Kind                   string   `json:"kind"`
	CredentialSessionID    string   `json:"credential_session_id"`
	BlockingRouteTargetIDs []string `json:"blocking_route_target_ids"`
}

// StartChatGPTProviderLogin starts a temporary login that can be consumed only
// by credential-session creation; route targets never own the resulting secret.
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

type ImportChatGPTProviderCredentialRequest struct {
	AuthData string `json:"auth_data"`
}

// ImportChatGPTProviderCredential stages pasted auth data as a temporary login;
// callers then create a CredentialSession with the returned login identifier.
func (h *Handler) ImportChatGPTProviderCredential(w http.ResponseWriter, r *http.Request) {
	if h.auth == nil {
		writeError(w, http.StatusNotImplemented, ErrCodeInternal, "GPT login is unavailable in this build")
		return
	}
	limitRequestBody(w, r)
	var req ImportChatGPTProviderCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid request body")
		return
	}
	if strings.TrimSpace(req.AuthData) == "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "auth_data is required")
		return
	}
	status, err := h.auth.ImportChatGPTLogin(r.Context(), req.AuthData)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (h *Handler) ExportCredentialSessionCodexAuth(w http.ResponseWriter, r *http.Request) {
	repository := h.credentialStore()
	if repository == nil {
		writeError(w, http.StatusNotImplemented, ErrCodeInternal, "Credential sessions are unavailable in this build")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	session, err := repository.GetCredentialSession(r.Context(), id)
	if err != nil {
		h.writeCredentialSessionError(w, "export", id, err)
		return
	}
	blockingRouteTargetIDs, err := repository.CredentialSessionEnabledRouteTargetIDs(r.Context(), id)
	if err != nil {
		h.writeCredentialSessionError(w, "export", id, err)
		return
	}
	snapshot, err := session.Snapshot()
	if err != nil {
		h.writeCredentialSessionError(w, "export", id, err)
		return
	}
	document, err := providerauth.BuildCodexAuthDocument(&snapshot, len(blockingRouteTargetIDs) != 0)
	if err != nil {
		switch {
		case errors.Is(err, providerauth.ErrCodexAuthExportRequiresChatGPT):
			writeError(w, http.StatusConflict, ErrCodeConflict, "Codex auth export is only supported for GPT credential sessions")
		case errors.Is(err, providerauth.ErrCodexAuthExportRequiresPaused):
			writeJSON(w, http.StatusConflict, struct {
				Code    string                        `json:"code"`
				Message string                        `json:"message"`
				Details codexAuthExportBlockedDetails `json:"details"`
			}{
				Code:    ErrCodeConflict,
				Message: "Disable every referencing route target before exporting Codex auth.json",
				Details: codexAuthExportBlockedDetails{
					Kind:                   codexAuthExportBlockedDetailKind,
					CredentialSessionID:    id,
					BlockingRouteTargetIDs: blockingRouteTargetIDs,
				},
			})
		default:
			h.logger.Warn("credential session unavailable for Codex auth export", zap.String("session_id", id), zap.Error(err))
			writeError(w, http.StatusConflict, ErrCodeConflict, "Credential session does not have active GPT credentials to export")
		}
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="`+codexAuthExportFilename+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	writeJSON(w, http.StatusOK, document)
	h.logger.Info("exported paused credential session", zap.String("session_id", id), zap.String("format", "codex_auth_json"))
}
