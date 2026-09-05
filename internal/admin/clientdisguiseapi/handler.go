// Package clientdisguiseapi projects credential-owned disguises for administration.
package clientdisguiseapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/codex/clientidentity"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type Repository interface {
	ListLogins(context.Context) ([]clientdisguise.LoginIdentity, error)
	ListBindings(context.Context) ([]clientdisguise.ProfileBinding, error)
	ListProfiles(context.Context) ([]clientdisguise.ProfileRevision, error)
	ListReferences(context.Context) ([]clientdisguise.ReferenceSource, error)
	ListTransportSamples(context.Context) ([]clientdisguise.TransportSample, error)
	SetBinding(context.Context, clientdisguise.ProfileBinding) (clientdisguise.ProfileBinding, error)
	LearnSample(context.Context, clientdisguise.Sample) (clientdisguise.LearnResult, error)
	SaveReference(context.Context, clientdisguise.ReferenceSource) error
	SaveTransportSample(context.Context, clientdisguise.TransportSample) error
}
type Catalog interface {
	ListCredentialSessions(context.Context) ([]credentialsession.Session, error)
	ListProviders(context.Context) ([]model.Provider, error)
}
type Clients interface {
	ListClients(context.Context) ([]clientidentity.Client, error)
	BindKey(context.Context, []byte, string) (clientidentity.Resolution, error)
}
type Config struct {
	Repository Repository
	Catalog    Catalog
	Clients    Clients
	Logger     *zap.Logger
}
type Handler struct {
	repository Repository
	catalog    Catalog
	clients    Clients
	logger     *zap.Logger
}

func NewHandler(cfg Config) *Handler {
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}
	return &Handler{repository: cfg.Repository, catalog: cfg.Catalog, clients: cfg.Clients, logger: cfg.Logger}
}
func (h *Handler) fail(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, clientdisguise.ErrInvalid) {
		status = http.StatusBadRequest
	}
	if errors.Is(err, clientdisguise.ErrNotFound) || errors.Is(err, clientidentity.ErrNotFound) {
		status = http.StatusNotFound
	}
	if errors.Is(err, clientdisguise.ErrConflict) || errors.Is(err, clientidentity.ErrConflict) {
		status = http.StatusConflict
	}
	h.logger.Error("client disguise administration failed", zap.Error(err), zap.Int("status", status))
	respond(w, status, map[string]string{"message": err.Error()})
}
func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	if err := json.NewDecoder(r.Body).Decode(value); err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"message": "Invalid JSON: " + err.Error()})
		return false
	}
	return true
}
func (h *Handler) SaveBinding(w http.ResponseWriter, r *http.Request) {
	var value clientdisguise.ProfileBinding
	if !decode(w, r, &value) {
		return
	}
	value.CredentialSessionID = r.PathValue("id")
	// Administrative reads never create devices; only an outbound target commits one.
	sessions, err := h.catalog.ListCredentialSessions(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	found := false
	for _, session := range sessions {
		if session.ID == value.CredentialSessionID {
			found = true
			break
		}
	}
	if !found {
		h.fail(w, clientdisguise.ErrNotFound)
		return
	}
	result, err := h.repository.SetBinding(r.Context(), value)
	if err != nil {
		h.fail(w, err)
		return
	}
	h.logger.Info("client disguise binding updated", zap.String("credential_session_id", value.CredentialSessionID), zap.String("revision_id", result.RevisionID), zap.String("mode", result.Mode))
	respond(w, http.StatusOK, result)
}
func (h *Handler) ImportSample(w http.ResponseWriter, r *http.Request) {
	var value clientdisguise.Sample
	if !decode(w, r, &value) {
		return
	}
	if !value.Tuple.Valid() || value.SourceID == "" || value.CapturedAt.IsZero() || value.ClientVersion == "" {
		respond(w, 400, map[string]string{"message": "Sample requires tuple, source, capture time and client version"})
		return
	}
	if value.ID == "" {
		value.ID = uuid.NewString()
	}
	result, err := h.repository.LearnSample(r.Context(), value)
	if err != nil {
		h.fail(w, err)
		return
	}
	h.logger.Info("client disguise sample learned", zap.String("source_id", value.SourceID), zap.String("revision_id", result.Revision.ID), zap.Bool("created", result.Created))
	respond(w, http.StatusOK, result)
}
func (h *Handler) SaveReference(w http.ResponseWriter, r *http.Request) {
	var value clientdisguise.ReferenceSource
	if !decode(w, r, &value) {
		return
	}
	value.ID = r.PathValue("id")
	if value.ID == "" || value.Name == "" || value.ClientIdentityID == "" {
		respond(w, 400, map[string]string{"message": "Reference requires id, name and client identity"})
		return
	}
	if err := h.requireClient(r.Context(), value.ClientIdentityID); err != nil {
		h.fail(w, err)
		return
	}
	if err := h.repository.SaveReference(r.Context(), value); err != nil {
		h.fail(w, err)
		return
	}
	respond(w, http.StatusOK, value)
}
func (h *Handler) ImportTransport(w http.ResponseWriter, r *http.Request) {
	var value clientdisguise.TransportSample
	if !decode(w, r, &value) {
		return
	}
	if value.ID == "" || value.SourceID == "" || value.CapturedAt.IsZero() {
		respond(w, 400, map[string]string{"message": "Transport sample requires id, source and capture time"})
		return
	}
	if err := h.repository.SaveTransportSample(r.Context(), value); err != nil {
		h.fail(w, err)
		return
	}
	respond(w, http.StatusOK, value)
}
func (h *Handler) BindKey(w http.ResponseWriter, r *http.Request) {
	var value struct {
		APIKey   string `json:"api_key"`
		ClientID string `json:"client_id"`
	}
	if !decode(w, r, &value) {
		return
	}
	if value.APIKey == "" || value.ClientID == "" {
		respond(w, 400, map[string]string{"message": "API key and client identity are required"})
		return
	}
	result, err := h.clients.BindKey(r.Context(), []byte(value.APIKey), value.ClientID)
	if err != nil {
		h.fail(w, err)
		return
	}
	h.logger.Info("client key bound", zap.String("client_id", result.ID))
	respond(w, http.StatusOK, map[string]string{"client_id": result.ID})
}
