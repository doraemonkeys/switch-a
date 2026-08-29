package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/providerauth"
	"github.com/doraemonkeys/switch-a/internal/store"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	credentialSessionAuthStateDetailKind       = "credential_session_auth_state"
	credentialSessionSubjectMismatchDetailKind = "credential_session_subject_mismatch"
	credentialSessionReauthenticationOperation = "credential_session_reauthentication"
)

type credentialSessionStore interface {
	CreateCredentialSession(context.Context, *credentialsession.Session) (*credentialsession.Session, error)
	GetCredentialSession(context.Context, string) (*credentialsession.Session, error)
	ListCredentialSessions(context.Context) ([]credentialsession.Session, error)
	CredentialSessionRouteTargetIDs(context.Context, string) ([]string, error)
	DeleteCredentialSession(context.Context, string) error
	WithCredentialSessionMutations(context.Context, []string) (context.Context, func(), error)
	UpdateCredentialSessionCAS(context.Context, string, int64, string, credentialsession.Subject, credentialsession.AuthState) (int64, error)
	CredentialSessionEnabledRouteTargetIDs(context.Context, string) ([]string, error)
}

type CredentialSessionPayload struct {
	ID                     string                      `json:"id"`
	Kind                   credentialsession.Kind      `json:"kind"`
	SecretData             string                      `json:"secret_data,omitempty"`
	Version                int64                       `json:"version"`
	Subject                credentialsession.Subject   `json:"subject"`
	AuthState              credentialsession.AuthState `json:"auth_state"`
	ReferencedRouteTargets []string                    `json:"referenced_route_target_ids"`
	CreatedAt              time.Time                   `json:"created_at"`
	UpdatedAt              time.Time                   `json:"updated_at"`
}

type CreateCredentialSessionRequest struct {
	ID                string                       `json:"id,omitempty"`
	Kind              credentialsession.Kind       `json:"kind"`
	SecretData        string                       `json:"secret_data"`
	AuthState         *credentialsession.AuthState `json:"auth_state,omitempty"`
	CredentialLoginID string                       `json:"credential_login_id,omitempty"`
}

type UpdateCredentialSessionRequest struct {
	ExpectedVersion int64                        `json:"expected_version"`
	SecretData      string                       `json:"secret_data"`
	AuthState       *credentialsession.AuthState `json:"auth_state,omitempty"`
}

type ReauthenticateCredentialSessionRequest struct {
	ExpectedVersion   int64  `json:"expected_version"`
	CredentialLoginID string `json:"credential_login_id"`
}

type credentialSessionAuthService interface {
	RefreshCredentialSession(context.Context, credentialsession.Snapshot) (bool, error)
	RefreshCredentialSessionUsage(context.Context, credentialsession.Snapshot) (bool, error)
}

type chatGPTLoginSessionBuilder interface {
	BuildCredentialSessionFromChatGPTLogin(loginID, sessionID string) (*credentialsession.Session, error)
	FinalizeChatGPTLogin(loginID string) error
}

func (h *Handler) credentialStore() credentialSessionStore {
	value, _ := h.store.(credentialSessionStore)
	return value
}

func (h *Handler) ListCredentialSessions(w http.ResponseWriter, r *http.Request) {
	repository := h.credentialStore()
	if repository == nil {
		writeError(w, http.StatusNotImplemented, ErrCodeInternal, "Credential sessions are unavailable in this build")
		return
	}
	sessions, err := repository.ListCredentialSessions(r.Context())
	if err != nil {
		h.logger.Error("failed to list credential sessions", zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to list credential sessions")
		return
	}
	payloads := make([]CredentialSessionPayload, 0, len(sessions))
	for index := range sessions {
		payload, err := credentialSessionPayload(r.Context(), repository, &sessions[index])
		if err != nil {
			h.logger.Error("failed to resolve credential session references", zap.String("session_id", sessions[index].ID), zap.Error(err))
			writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to list credential sessions")
			return
		}
		payloads = append(payloads, payload)
	}
	writeJSON(w, http.StatusOK, payloads)
}

func (h *Handler) GetCredentialSession(w http.ResponseWriter, r *http.Request) {
	repository := h.credentialStore()
	if repository == nil {
		writeError(w, http.StatusNotImplemented, ErrCodeInternal, "Credential sessions are unavailable in this build")
		return
	}
	session, err := repository.GetCredentialSession(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeCredentialSessionError(w, "get", r.PathValue("id"), err)
		return
	}
	payload, err := credentialSessionPayload(r.Context(), repository, session)
	if err != nil {
		h.writeCredentialSessionError(w, "get", session.ID, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (h *Handler) CreateCredentialSession(w http.ResponseWriter, r *http.Request) {
	repository := h.credentialStore()
	if repository == nil {
		writeError(w, http.StatusNotImplemented, ErrCodeInternal, "Credential sessions are unavailable in this build")
		return
	}
	limitRequestBody(w, r)
	var req CreateCredentialSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid request body")
		return
	}
	var session *credentialsession.Session
	var err error
	if loginID := strings.TrimSpace(req.CredentialLoginID); loginID != "" {
		builder, ok := h.auth.(chatGPTLoginSessionBuilder)
		if !ok {
			writeError(w, http.StatusNotImplemented, ErrCodeInternal, "GPT login is unavailable in this build")
			return
		}
		id := strings.TrimSpace(req.ID)
		if id == "" {
			id = uuid.NewString()
		}
		session, err = builder.BuildCredentialSessionFromChatGPTLogin(loginID, id)
	} else {
		session, err = buildCredentialSession(req)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, err.Error())
		return
	}
	created, err := repository.CreateCredentialSession(r.Context(), session)
	if err != nil {
		h.writeCredentialSessionError(w, "create", session.ID, err)
		return
	}
	if loginID := strings.TrimSpace(req.CredentialLoginID); loginID != "" {
		if builder, ok := h.auth.(chatGPTLoginSessionBuilder); ok {
			if err := builder.FinalizeChatGPTLogin(loginID); err != nil {
				h.logger.Warn("failed to finalize chatgpt login after credential session creation", zap.String("login_id", loginID), zap.String("session_id", created.ID), zap.Error(err))
			}
		}
	}
	payload, err := credentialSessionPayload(r.Context(), repository, created)
	if err != nil {
		h.writeCredentialSessionError(w, "create", created.ID, err)
		return
	}
	writeJSON(w, http.StatusCreated, payload)
}

func buildCredentialSession(req CreateCredentialSessionRequest) (*credentialsession.Session, error) {
	if req.Kind == credentialsession.KindChatGPT {
		return nil, errors.New("chatgpt credential sessions must be created from a completed credential login")
	}
	id := strings.TrimSpace(req.ID)
	if id == "" {
		id = uuid.NewString()
	}
	session := &credentialsession.Session{
		ID:         id,
		Kind:       req.Kind,
		SecretData: req.SecretData,
		Version:    1,
	}
	if req.AuthState != nil {
		session.AuthState = credentialsession.NormalizeAuthState(req.Kind, req.AuthState.Clone())
	} else {
		session.AuthState.Status = credentialsession.DefaultAuthStatus(req.Kind)
	}
	subject := credentialsession.PendingSubject()
	if err := session.SetSubject(subject); err != nil {
		return nil, err
	}
	if err := session.Validate(); err != nil {
		return nil, err
	}
	return session, nil
}

func (h *Handler) UpdateCredentialSession(w http.ResponseWriter, r *http.Request) {
	repository := h.credentialStore()
	if repository == nil {
		writeError(w, http.StatusNotImplemented, ErrCodeInternal, "Credential sessions are unavailable in this build")
		return
	}
	limitRequestBody(w, r)
	var req UpdateCredentialSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid request body")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" || req.ExpectedVersion < 1 || strings.TrimSpace(req.SecretData) == "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "id, expected_version, and secret_data are required")
		return
	}
	current, err := repository.GetCredentialSession(r.Context(), id)
	if err != nil {
		h.writeCredentialSessionError(w, "update", id, err)
		return
	}
	if current.Kind == credentialsession.KindChatGPT {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "chatgpt credential sessions must be changed through reauthentication or verified import")
		return
	}
	subject := current.Subject()
	authState := current.AuthState
	if req.AuthState != nil {
		authState = credentialsession.NormalizeAuthState(current.Kind, req.AuthState.Clone())
	}
	ownedCtx, release, err := repository.WithCredentialSessionMutations(r.Context(), []string{id})
	if err != nil {
		h.writeCredentialSessionError(w, "update", id, err)
		return
	}
	defer release()
	if _, err := repository.UpdateCredentialSessionCAS(ownedCtx, id, req.ExpectedVersion, req.SecretData, subject, authState); err != nil {
		h.writeCredentialSessionError(w, "update", id, err)
		return
	}
	updated, err := repository.GetCredentialSession(ownedCtx, id)
	if err != nil {
		h.writeCredentialSessionError(w, "update", id, err)
		return
	}
	payload, err := credentialSessionPayload(ownedCtx, repository, updated)
	if err != nil {
		h.writeCredentialSessionError(w, "update", id, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

// ReauthenticateCredentialSession rotates a verified ChatGPT login in place.
// A resolved subject cannot change because one session may be shared by several
// routes; choosing another account is a route-rebinding operation, not rotation.
func (h *Handler) ReauthenticateCredentialSession(w http.ResponseWriter, r *http.Request) {
	repository := h.credentialStore()
	if repository == nil {
		writeError(w, http.StatusNotImplemented, ErrCodeInternal, "Credential sessions are unavailable in this build")
		return
	}
	builder, ok := h.auth.(chatGPTLoginSessionBuilder)
	if !ok {
		writeError(w, http.StatusNotImplemented, ErrCodeInternal, "GPT login is unavailable in this build")
		return
	}

	limitRequestBody(w, r)
	var req ReauthenticateCredentialSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Invalid request body")
		return
	}
	sessionID := strings.TrimSpace(r.PathValue("id"))
	loginID := strings.TrimSpace(req.CredentialLoginID)
	if sessionID == "" || loginID == "" || req.ExpectedVersion < 1 {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "id, expected_version, and credential_login_id are required")
		return
	}

	h.logger.Info("credential session reauthentication started",
		zap.String("operation", credentialSessionReauthenticationOperation),
		zap.String("session_id", sessionID),
		zap.String("login_id", loginID),
		zap.Int64("expected_version", req.ExpectedVersion),
	)
	candidate, err := builder.BuildCredentialSessionFromChatGPTLogin(loginID, sessionID)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, err.Error())
		return
	}
	if candidate == nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "completed GPT login did not produce a credential session")
		return
	}
	if err := candidate.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, err.Error())
		return
	}
	candidateSubject := candidate.Subject()
	if candidate.Kind != credentialsession.KindChatGPT ||
		candidate.AuthState.Status != credentialsession.AuthStatusActive ||
		!candidateSubject.Resolved() {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "completed GPT login must resolve an active ChatGPT account")
		return
	}

	ownedCtx, release, err := repository.WithCredentialSessionMutations(r.Context(), []string{sessionID})
	if err != nil {
		h.writeCredentialSessionError(w, "reauthenticate", sessionID, err)
		return
	}
	defer release()

	current, err := repository.GetCredentialSession(ownedCtx, sessionID)
	if err != nil {
		h.writeCredentialSessionError(w, "reauthenticate", sessionID, err)
		return
	}
	if current.Kind != credentialsession.KindChatGPT {
		writeError(w, http.StatusConflict, ErrCodeConflict, "reauthentication is only supported for chatgpt credential sessions")
		return
	}
	currentSubject := current.Subject()
	if currentSubject.Kind != credentialsession.SubjectPending && !currentSubject.Equal(candidateSubject) {
		h.logger.Warn("credential session reauthentication rejected a different account",
			zap.String("operation", credentialSessionReauthenticationOperation),
			zap.String("session_id", sessionID),
			zap.String("current_account_id", string(currentSubject.Value)),
			zap.String("authenticated_account_id", string(candidateSubject.Value)),
		)
		writeErrorWithDetails(w, http.StatusConflict, ErrCodeConflict,
			"The authenticated GPT account differs from this credential session. Select another session for the route instead.",
			map[string]string{
				"kind":                     credentialSessionSubjectMismatchDetailKind,
				"session_id":               sessionID,
				"current_account_id":       string(currentSubject.Value),
				"authenticated_account_id": string(candidateSubject.Value),
			},
		)
		return
	}

	nextVersion, err := repository.UpdateCredentialSessionCAS(
		ownedCtx,
		sessionID,
		req.ExpectedVersion,
		candidate.SecretData,
		candidateSubject,
		candidate.AuthState,
	)
	if err != nil {
		h.writeCredentialSessionError(w, "reauthenticate", sessionID, err)
		return
	}
	if err := builder.FinalizeChatGPTLogin(loginID); err != nil {
		h.logger.Warn("failed to finalize chatgpt login after credential session reauthentication",
			zap.String("operation", credentialSessionReauthenticationOperation),
			zap.String("login_id", loginID),
			zap.String("session_id", sessionID),
			zap.Error(err),
		)
	}
	updated, err := repository.GetCredentialSession(ownedCtx, sessionID)
	if err != nil {
		h.writeCredentialSessionError(w, "reauthenticate", sessionID, err)
		return
	}
	payload, err := credentialSessionPayload(ownedCtx, repository, updated)
	if err != nil {
		h.writeCredentialSessionError(w, "reauthenticate", sessionID, err)
		return
	}
	h.logger.Info("credential session reauthentication completed",
		zap.String("operation", credentialSessionReauthenticationOperation),
		zap.String("session_id", sessionID),
		zap.Int64("version", nextVersion),
		zap.Int("referenced_route_target_count", len(payload.ReferencedRouteTargets)),
	)
	writeJSON(w, http.StatusOK, payload)
}

func (h *Handler) DeleteCredentialSession(w http.ResponseWriter, r *http.Request) {
	repository := h.credentialStore()
	if repository == nil {
		writeError(w, http.StatusNotImplemented, ErrCodeInternal, "Credential sessions are unavailable in this build")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, ErrCodeValidation, "Credential session ID is required")
		return
	}
	if err := repository.DeleteCredentialSession(r.Context(), id); err != nil {
		h.writeCredentialSessionError(w, "delete", id, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) RefreshCredentialSession(w http.ResponseWriter, r *http.Request) {
	h.runCredentialSessionAuthAction(w, r, "refresh credential", func(ctx context.Context, service credentialSessionAuthService, snapshot credentialsession.Snapshot) (bool, error) {
		return service.RefreshCredentialSession(ctx, snapshot)
	})
}

func (h *Handler) RefreshCredentialSessionUsage(w http.ResponseWriter, r *http.Request) {
	h.runCredentialSessionAuthAction(w, r, "refresh usage", func(ctx context.Context, service credentialSessionAuthService, snapshot credentialsession.Snapshot) (bool, error) {
		return service.RefreshCredentialSessionUsage(ctx, snapshot)
	})
}

func (h *Handler) runCredentialSessionAuthAction(
	w http.ResponseWriter,
	r *http.Request,
	action string,
	run func(context.Context, credentialSessionAuthService, credentialsession.Snapshot) (bool, error),
) {
	repository := h.credentialStore()
	service, ok := h.auth.(credentialSessionAuthService)
	if repository == nil || !ok {
		writeError(w, http.StatusNotImplemented, ErrCodeInternal, "Credential session authentication is unavailable in this build")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	session, err := repository.GetCredentialSession(r.Context(), id)
	if err != nil {
		h.writeCredentialSessionError(w, action, id, err)
		return
	}
	snapshot, err := session.Snapshot()
	if err != nil {
		h.writeCredentialSessionError(w, action, id, err)
		return
	}
	supported, err := run(r.Context(), service, snapshot)
	if !supported {
		writeError(w, http.StatusConflict, ErrCodeConflict, action+" is only supported for chatgpt credential sessions")
		return
	}
	if err != nil {
		var stateErr *providerauth.ProviderAuthStateError
		if errors.As(err, &stateErr) {
			writeErrorWithDetails(w, http.StatusConflict, ErrCodeProviderAuthRequired, stateErr.Error(), map[string]string{
				"kind": credentialSessionAuthStateDetailKind, "session_id": id,
				"auth_status": string(stateErr.Status), "auth_reason": stateErr.Reason,
			})
			return
		}
		h.writeCredentialSessionError(w, action, id, err)
		return
	}
	updated, err := repository.GetCredentialSession(r.Context(), id)
	if err != nil {
		h.writeCredentialSessionError(w, action, id, err)
		return
	}
	payload, err := credentialSessionPayload(r.Context(), repository, updated)
	if err != nil {
		h.writeCredentialSessionError(w, action, id, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func credentialSessionPayload(ctx context.Context, repository credentialSessionStore, session *credentialsession.Session) (CredentialSessionPayload, error) {
	references, err := repository.CredentialSessionRouteTargetIDs(ctx, session.ID)
	if err != nil {
		return CredentialSessionPayload{}, err
	}
	secretData := ""
	if session.Kind == credentialsession.KindAPIKey {
		// Static API keys are operator-managed values, so the admin resource must
		// remain readable as well as writable. ChatGPT's structured token bundle
		// has its own explicit export flow and is intentionally not projected here.
		secretData = session.SecretData
	}
	return CredentialSessionPayload{
		ID: session.ID, Kind: session.Kind, Version: session.Version,
		SecretData: secretData,
		Subject:    session.Subject(), AuthState: session.AuthState.Clone(),
		ReferencedRouteTargets: references, CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt,
	}, nil
}

func (h *Handler) writeCredentialSessionError(w http.ResponseWriter, action, id string, err error) {
	switch {
	case errors.Is(err, credentialsession.ErrNotFound), errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, ErrCodeNotFound, "Credential session not found: "+id)
	case errors.Is(err, credentialsession.ErrVersionConflict), errors.Is(err, credentialsession.ErrSessionReferenced):
		writeError(w, http.StatusConflict, ErrCodeConflict, err.Error())
	case errors.Is(err, credentialsession.ErrInvalidSession):
		writeError(w, http.StatusBadRequest, ErrCodeValidation, err.Error())
	default:
		h.logger.Error("credential session operation failed", zap.String("action", action), zap.String("session_id", id), zap.Error(err))
		writeError(w, http.StatusInternalServerError, ErrCodeInternal, "Failed to "+action+" credential session")
	}
}
