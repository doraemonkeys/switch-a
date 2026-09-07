package server

import (
	"net/http"

	"github.com/doraemonkeys/switch-a/internal/admin"
	"github.com/doraemonkeys/switch-a/internal/admin/clientapikeyapi"
)

func registerClientAPIKeyRoutes(mux *http.ServeMux, handler *clientapikeyapi.Handler, auth *admin.AuthMiddleware) {
	if handler == nil {
		return
	}
	mux.Handle("GET /admin/api/client-api-keys", auth.WrapFunc(handler.List))
	mux.Handle("PUT /admin/api/client-api-keys/policy", auth.WrapFunc(handler.SetPolicy))
	mux.Handle("POST /admin/api/client-api-keys", auth.WrapFunc(handler.Create))
	mux.Handle("POST /admin/api/client-api-keys/generate", auth.WrapFunc(handler.Generate))
	// Imported opaque IDs must never collide with collection actions.
	mux.Handle("PUT /admin/api/client-api-keys/keys/{id}", auth.WrapFunc(handler.Rename))
	mux.Handle("DELETE /admin/api/client-api-keys/keys/{id}", auth.WrapFunc(handler.Delete))
}
