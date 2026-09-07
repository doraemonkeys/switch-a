// Package clientapikeyapi manages downstream credentials independently of admin and provider authentication.
package clientapikeyapi

import (
	"context"
	"net/http"

	"github.com/doraemonkeys/switch-a/internal/clientaccess"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type Manager interface {
	Snapshot(context.Context) (clientaccess.Snapshot, error)
	Create(context.Context, string, string) (clientaccess.Key, error)
	Generate(context.Context, string) (clientaccess.Key, error)
	Rename(context.Context, string, string) (clientaccess.Key, error)
	Delete(context.Context, string) error
	SetMode(context.Context, clientaccess.Mode) error
}

type Handler struct {
	manager Manager
	logger  *zap.Logger
}

func NewHandler(manager Manager, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{manager: manager, logger: logger}
}

func (h *Handler) operation(r *http.Request) *zap.Logger {
	return h.logger.With(zap.String("operation_id", uuid.NewString()), zap.String("method", r.Method), zap.String("path", r.URL.Path))
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	log := h.operation(r)
	snapshot, err := h.manager.Snapshot(r.Context())
	if err != nil {
		fail(w, log, err)
		return
	}
	// Empty collections have the same JSON shape before and after the first key.
	if snapshot.Keys == nil {
		snapshot.Keys = []clientaccess.Key{}
	}
	respond(w, http.StatusOK, snapshot)
}

func (h *Handler) SetPolicy(w http.ResponseWriter, r *http.Request) {
	log := h.operation(r)
	var input struct {
		Mode clientaccess.Mode `json:"mode"`
	}
	if !decode(w, r, &input) {
		return
	}
	previous, err := h.manager.Snapshot(r.Context())
	if err != nil {
		fail(w, log, err)
		return
	}
	if err := h.manager.SetMode(r.Context(), input.Mode); err != nil {
		fail(w, log, err)
		return
	}
	log.Info("client API key policy updated", zap.String("previous_observed_mode", string(previous.Mode)), zap.String("mode", string(input.Mode)))
	respond(w, http.StatusOK, input)
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	h.create(w, r, false)
}

func (h *Handler) Generate(w http.ResponseWriter, r *http.Request) {
	h.create(w, r, true)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request, generate bool) {
	log := h.operation(r)
	var input struct {
		Name string `json:"name"`
		Key  string `json:"key"`
	}
	if !decode(w, r, &input) {
		return
	}
	var key clientaccess.Key
	var err error
	if generate {
		key, err = h.manager.Generate(r.Context(), input.Name)
	} else {
		key, err = h.manager.Create(r.Context(), input.Name, input.Key)
	}
	if err != nil {
		fail(w, log, err)
		return
	}
	log.Info("client API key created", zap.String("key_id", key.ID), zap.String("name", key.Name), zap.Bool("generated", generate))
	respond(w, http.StatusCreated, key)
}

func (h *Handler) Rename(w http.ResponseWriter, r *http.Request) {
	log := h.operation(r)
	var input struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &input) {
		return
	}
	key, err := h.manager.Rename(r.Context(), r.PathValue("id"), input.Name)
	if err != nil {
		fail(w, log, err)
		return
	}
	log.Info("client API key renamed", zap.String("key_id", key.ID), zap.String("name", key.Name))
	respond(w, http.StatusOK, key)
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	log := h.operation(r)
	id := r.PathValue("id")
	if err := h.manager.Delete(r.Context(), id); err != nil {
		fail(w, log, err)
		return
	}
	log.Info("client API key deleted", zap.String("key_id", id))
	w.WriteHeader(http.StatusNoContent)
}
