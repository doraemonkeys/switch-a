package clientdisguiseapi

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/codex/clientidentity"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeService struct {
	failAt  string
	calls   []string
	binding clientdisguise.ProfileBinding
	key     string
}

func (f *fakeService) err(name string) error {
	f.calls = append(f.calls, name)
	if f.failAt == name {
		return errors.New("repository failure")
	}
	return nil
}
func (f *fakeService) ListLogins(context.Context) ([]clientdisguise.LoginIdentity, error) {
	return []clientdisguise.LoginIdentity{{CredentialSessionID: "login", DeviceID: "device"}}, f.err("logins")
}
func (f *fakeService) ListBindings(context.Context) ([]clientdisguise.ProfileBinding, error) {
	return []clientdisguise.ProfileBinding{{CredentialSessionID: "login", RevisionID: "revision"}}, f.err("bindings")
}
func (f *fakeService) ListProfiles(context.Context) ([]clientdisguise.ProfileRevision, error) {
	return []clientdisguise.ProfileRevision{}, f.err("profiles")
}
func (f *fakeService) ListReferences(context.Context) ([]clientdisguise.ReferenceSource, error) {
	return []clientdisguise.ReferenceSource{}, f.err("references")
}
func (f *fakeService) ListTransportSamples(context.Context) ([]clientdisguise.TransportSample, error) {
	return []clientdisguise.TransportSample{}, f.err("transports")
}
func (f *fakeService) ListCredentialSessions(context.Context) ([]credentialsession.Session, error) {
	return []credentialsession.Session{{ID: "login", Name: "Login"}}, f.err("sessions")
}
func (f *fakeService) ListProviders(context.Context) ([]model.Provider, error) {
	return []model.Provider{{ID: "a", Name: "A", ClientDisguise: clientdisguise.Policy{Enabled: true}, CredentialSessions: []credentialsession.RouteSnapshot{{Credential: credentialsession.Snapshot{SessionID: "login"}}}}, {ID: "b", Name: "B", CredentialSessions: []credentialsession.RouteSnapshot{{Credential: credentialsession.Snapshot{SessionID: "login"}}}}}, f.err("providers")
}
func (f *fakeService) ListClients(context.Context) ([]clientidentity.Client, error) {
	return []clientidentity.Client{{ID: "client"}}, f.err("clients")
}
func (f *fakeService) BindKey(_ context.Context, key []byte, id string) (clientidentity.Resolution, error) {
	f.key = string(key)
	return clientidentity.Resolution{ID: id}, f.err("key")
}
func (f *fakeService) SetBinding(_ context.Context, value clientdisguise.ProfileBinding) (clientdisguise.ProfileBinding, error) {
	f.binding = value
	return value, f.err("save")
}
func (f *fakeService) LearnSample(_ context.Context, value clientdisguise.Sample) (clientdisguise.LearnResult, error) {
	return clientdisguise.LearnResult{Revision: clientdisguise.ProfileRevision{ID: value.ID}, Created: true, AdvancedSessions: []string{"login"}}, f.err("learn")
}
func (f *fakeService) SaveReference(context.Context, clientdisguise.ReferenceSource) error {
	return f.err("reference")
}
func (f *fakeService) SaveTransportSample(context.Context, clientdisguise.TransportSample) error {
	return f.err("transport")
}
func setup() (*Handler, *fakeService) {
	service := &fakeService{}
	return NewHandler(Config{Repository: service, Catalog: service, Clients: service}), service
}
func invoke(handler http.HandlerFunc, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	request.SetPathValue("id", "login")
	response := httptest.NewRecorder()
	handler(response, request)
	return response
}
func TestOverviewSharesIdentityWithoutCreatingIt(t *testing.T) {
	handler, service := setup()
	response := invoke(handler.Get, "")
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	var value Overview
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	if len(value.Logins) != 1 || len(value.Logins[0].Providers) != 2 || !value.Logins[0].Providers[0].ClientDisguise.Enabled || value.Logins[0].Providers[1].ClientDisguise.Enabled {
		t.Fatalf("overview=%+v", value)
	}
	if value.Logins[0].Identity.DeviceID != "device" || value.Logins[0].Binding.RevisionID != "revision" || value.Clients[0].ClientID != "client" {
		t.Fatal(value)
	}
	for _, call := range service.calls {
		if call == "save" {
			t.Fatal("GET mutated binding")
		}
	}
}
func TestOverviewRepositoryFailures(t *testing.T) {
	for _, stage := range []string{"sessions", "providers", "logins", "bindings", "profiles", "references", "transports", "clients"} {
		t.Run(stage, func(t *testing.T) {
			handler, service := setup()
			service.failAt = stage
			if got := invoke(handler.Get, "").Code; got != 500 {
				t.Fatal(got)
			}
		})
	}
}
func TestMutationsAndValidation(t *testing.T) {
	handler, service := setup()
	binding := clientdisguise.ProfileBinding{CredentialSessionID: "forged", Mode: "pinned", RevisionID: "old"}
	data, _ := json.Marshal(binding)
	if response := invoke(handler.SaveBinding, string(data)); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if service.binding.CredentialSessionID != "login" || service.binding.Mode != "pinned" {
		t.Fatal(service.binding)
	}
	sample := clientdisguise.Sample{ID: "sample", SourceID: "reference", CapturedAt: time.Now(), Tuple: clientdisguise.Tuple{ClientType: "desktop", Platform: "windows", Arch: "amd64"}, ClientVersion: "1.0"}
	data, _ = json.Marshal(sample)
	cases := []struct {
		name    string
		handler http.HandlerFunc
		body    string
		status  int
	}{
		{"sample", handler.ImportSample, string(data), 200}, {"sample missing", handler.ImportSample, "{}", 400},
		{"reference", handler.SaveReference, `{"name":"reference","client_identity_id":"client"}`, 200},
		{"reference missing", handler.SaveReference, "{}", 400},
		{"transport", handler.ImportTransport, `{"id":"sample","source_id":"reference","captured_at":"2026-09-05T00:00:00Z"}`, 200},
		{"transport missing", handler.ImportTransport, "{}", 400},
		{"key", handler.BindKey, `{"api_key":"replacement","client_id":"client"}`, 200}, {"key missing", handler.BindKey, "{}", 400},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if response := invoke(item.handler, item.body); response.Code != item.status {
				t.Fatal(response.Code, response.Body.String())
			}
		})
	}
	if service.key != "replacement" {
		t.Fatal("key binding missing")
	}
	for _, fn := range []http.HandlerFunc{handler.SaveBinding, handler.ImportSample, handler.SaveReference, handler.ImportTransport, handler.BindKey} {
		if invoke(fn, "{").Code != 400 {
			t.Fatal("malformed JSON accepted")
		}
	}
}
func TestMutationErrors(t *testing.T) {
	handler, service := setup()
	cases := []struct {
		stage string
		fn    http.HandlerFunc
		body  string
	}{
		{"sessions", handler.SaveBinding, "{}"}, {"save", handler.SaveBinding, "{}"},
		{"reference", handler.SaveReference, `{"name":"ref","client_identity_id":"client"}`},
		{"clients", handler.SaveReference, `{"name":"ref","client_identity_id":"client"}`},
		{"transport", handler.ImportTransport, `{"id":"id","source_id":"ref","captured_at":"2026-09-05T00:00:00Z"}`},
		{"key", handler.BindKey, `{"api_key":"key","client_id":"client"}`},
		{"learn", handler.ImportSample, `{"source_id":"ref","captured_at":"2026-09-05T00:00:00Z","client_version":"1","tuple":{"client_type":"cli","platform":"linux","arch":"amd64"}}`},
	}
	for _, item := range cases {
		service.failAt = item.stage
		if got := invoke(item.fn, item.body).Code; got != 500 {
			t.Fatalf("%s: %d", item.stage, got)
		}
	}
	for _, item := range []struct {
		err  error
		code int
	}{{clientdisguise.ErrNotFound, 404}, {clientdisguise.ErrInvalid, 400}, {clientidentity.ErrNotFound, 404}, {clientdisguise.ErrConflict, 409}, {clientidentity.ErrConflict, 409}} {
		response := httptest.NewRecorder()
		handler.fail(response, item.err)
		if response.Code != item.code {
			t.Fatal(response.Code)
		}
	}
	request := httptest.NewRequest(http.MethodPut, "/", strings.NewReader("{}"))
	request.SetPathValue("id", "absent")
	response := httptest.NewRecorder()
	service.failAt = ""
	handler.SaveBinding(response, request)
	if response.Code != 404 {
		t.Fatal(response.Code)
	}
}
