package clientdisguiseapi

import (
	"context"
	"net/http"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
)

type ProviderView struct {
	ProviderID     string                `json:"provider_id"`
	ProviderName   string                `json:"provider_name"`
	ClientDisguise clientdisguise.Policy `json:"client_disguise"`
}
type LoginView struct {
	CredentialSessionID string                         `json:"credential_session_id"`
	Name                string                         `json:"name"`
	Identity            *clientdisguise.LoginIdentity  `json:"identity,omitempty"`
	Binding             *clientdisguise.ProfileBinding `json:"binding,omitempty"`
	Providers           []ProviderView                 `json:"providers"`
}
type ClientView struct {
	ClientID string `json:"client_id"`
}
type Overview struct {
	Logins           []LoginView                      `json:"logins"`
	Profiles         []clientdisguise.ProfileRevision `json:"profiles"`
	References       []clientdisguise.ReferenceSource `json:"references"`
	TransportSamples []clientdisguise.TransportSample `json:"transport_samples"`
	Clients          []ClientView                     `json:"clients"`
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	value, err := h.overview(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	respond(w, http.StatusOK, value)
}
func (h *Handler) overview(ctx context.Context) (Overview, error) {
	result := Overview{Logins: []LoginView{}, Clients: []ClientView{}}
	sessions, err := h.catalog.ListCredentialSessions(ctx)
	if err != nil {
		return result, err
	}
	providers, err := h.catalog.ListProviders(ctx)
	if err != nil {
		return result, err
	}
	identities, err := h.repository.ListLogins(ctx)
	if err != nil {
		return result, err
	}
	bindings, err := h.repository.ListBindings(ctx)
	if err != nil {
		return result, err
	}
	result.Profiles, err = h.repository.ListProfiles(ctx)
	if err != nil {
		return result, err
	}
	result.References, err = h.repository.ListReferences(ctx)
	if err != nil {
		return result, err
	}
	result.TransportSamples, err = h.repository.ListTransportSamples(ctx)
	if err != nil {
		return result, err
	}
	clients, err := h.clients.ListClients(ctx)
	if err != nil {
		return result, err
	}
	for _, client := range clients {
		result.Clients = append(result.Clients, ClientView{ClientID: client.ID})
	}
	for _, session := range sessions {
		view := LoginView{CredentialSessionID: session.ID, Name: session.Name, Providers: []ProviderView{}}
		for _, identity := range identities {
			if identity.CredentialSessionID == session.ID {
				view.Identity = &identity
				break
			}
		}
		for _, binding := range bindings {
			if binding.CredentialSessionID == session.ID {
				view.Binding = &binding
				break
			}
		}
		for _, provider := range providers {
			for _, route := range provider.CredentialSessions {
				if route.Credential.SessionID == session.ID {
					view.Providers = append(view.Providers, ProviderView{ProviderID: provider.ID, ProviderName: provider.Name, ClientDisguise: provider.ClientDisguise})
					break
				}
			}
		}
		result.Logins = append(result.Logins, view)
	}
	return result, nil
}
