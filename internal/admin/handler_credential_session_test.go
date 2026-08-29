package admin

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/keyring"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth"
	"github.com/doraemonkeys/switch-a/internal/store"
	"go.uber.org/zap"
)

type provenLoginAuth struct {
	session   *credentialsession.Session
	finalized string
}

func (*provenLoginAuth) StartChatGPTLogin() (*providerauth.ChatGPTLoginStartResponse, error) {
	return nil, nil
}

func (*provenLoginAuth) GetChatGPTLoginStatus(string) (*providerauth.ChatGPTLoginStatusResponse, error) {
	return nil, nil
}

func (*provenLoginAuth) ImportChatGPTLogin(context.Context, string) (*providerauth.ChatGPTLoginStatusResponse, error) {
	return nil, nil
}

func (a *provenLoginAuth) BuildCredentialSessionFromChatGPTLogin(_ string, sessionID string) (*credentialsession.Session, error) {
	session := a.session.Clone()
	session.ID = sessionID
	return session, nil
}

func (a *provenLoginAuth) FinalizeChatGPTLogin(loginID string) error {
	a.finalized = loginID
	return nil
}

type adminCredentialSigner struct{}

func (adminCredentialSigner) Sign(_ codexkeyring.HMACPurpose, input []byte) (codexkeyring.Digest, error) {
	return codexkeyring.Digest{Version: "h-admin", Sum: sha256.Sum256(input)}, nil
}

func newCredentialSessionHandler(t *testing.T) (*Handler, *store.SQLiteStore) {
	t.Helper()
	repository, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "admin.db"), internal.RealClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.FinalizeStaticCredentialSubjects(context.Background(), adminCredentialSigner{}); err != nil {
		_ = repository.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	return NewHandler(Config{Store: repository, Logger: zap.NewNop()}), repository
}

func TestCredentialSessionCRUDAndReferenceDeletionContract(t *testing.T) {
	handler, repository := newCredentialSessionHandler(t)
	createRequest := httptest.NewRequest(http.MethodPost, "/admin/api/credential-sessions", strings.NewReader(`{
		"id":"static-session","vendor":"openai","kind":"api_key","secret_data":"secret-1"
	}`))
	createResponse := httptest.NewRecorder()
	handler.CreateCredentialSession(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated || strings.Contains(createResponse.Body.String(), "secret-1") {
		t.Fatalf("create response = %d %s", createResponse.Code, createResponse.Body.String())
	}
	var created CredentialSessionPayload
	if err := json.NewDecoder(createResponse.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Subject.Kind != credentialsession.SubjectKeyedDigest || created.Subject.KeyVersion != "h-admin" || created.Version != 1 {
		t.Fatalf("created payload = %#v", created)
	}

	provider := &model.Provider{
		ID: "route-1", Name: "Route", Vendor: "openai", Enabled: true,
		APITypes: []model.ProviderAPIType{{ProviderID: "route-1", APIType: "codex", BaseURL: "https://example.com"}},
		CredentialSessions: []credentialsession.RouteSnapshot{{
			RouteTargetID: "route-1", APIType: "codex", VendorScope: "openai", Credential: credentialsession.Snapshot{SessionID: created.ID},
		}},
	}
	if err := repository.CreateProvider(context.Background(), provider); err != nil {
		t.Fatal(err)
	}
	deleteReferenced := httptest.NewRecorder()
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/admin/api/credential-sessions/static-session", nil)
	deleteRequest.SetPathValue("id", created.ID)
	handler.DeleteCredentialSession(deleteReferenced, deleteRequest)
	if deleteReferenced.Code != http.StatusConflict {
		t.Fatalf("delete referenced status = %d body=%s", deleteReferenced.Code, deleteReferenced.Body.String())
	}

	updateRequest := httptest.NewRequest(http.MethodPut, "/admin/api/credential-sessions/static-session", strings.NewReader(`{
		"expected_version":1,"secret_data":"secret-2"
	}`))
	updateRequest.SetPathValue("id", created.ID)
	updateResponse := httptest.NewRecorder()
	handler.UpdateCredentialSession(updateResponse, updateRequest)
	if updateResponse.Code != http.StatusOK || strings.Contains(updateResponse.Body.String(), "secret-2") {
		t.Fatalf("update response = %d %s", updateResponse.Code, updateResponse.Body.String())
	}
	var updated CredentialSessionPayload
	if err := json.NewDecoder(updateResponse.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.Version != 2 || string(updated.Subject.Value) == string(created.Subject.Value) {
		t.Fatalf("updated payload = %#v", updated)
	}

	listResponse := httptest.NewRecorder()
	handler.ListCredentialSessions(listResponse, httptest.NewRequest(http.MethodGet, "/admin/api/credential-sessions", nil))
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), `"route-1"`) || strings.Contains(listResponse.Body.String(), "secret-2") {
		t.Fatalf("list response = %d %s", listResponse.Code, listResponse.Body.String())
	}
	if err := repository.DeleteProvider(context.Background(), provider.ID); err != nil {
		t.Fatal(err)
	}
	deleteResponse := httptest.NewRecorder()
	handler.DeleteCredentialSession(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete response = %d %s", deleteResponse.Code, deleteResponse.Body.String())
	}
}

func TestCredentialSessionCodexAuthExportUsesSessionLifecycle(t *testing.T) {
	handler, repository := newCredentialSessionHandler(t)
	secret, err := model.EncodeChatGPTProviderSecret(&model.ChatGPTProviderSecret{
		AccessToken: "access", RefreshToken: "refresh", IDToken: "id",
	})
	if err != nil {
		t.Fatal(err)
	}
	subject, err := credentialsession.AccountSubject("account-1")
	if err != nil {
		t.Fatal(err)
	}
	session := &credentialsession.Session{
		ID: "login-session", Kind: credentialsession.KindChatGPT,
		SecretData: secret, Version: 1,
		AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive, AccountID: "account-1"},
	}
	if err := session.SetSubject(subject); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateCredentialSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	exportRequest := httptest.NewRequest(http.MethodGet, "/admin/api/credential-sessions/login-session/codex-auth", nil)
	exportRequest.SetPathValue("id", session.ID)
	exportResponse := httptest.NewRecorder()
	handler.ExportCredentialSessionCodexAuth(exportResponse, exportRequest)
	if exportResponse.Code != http.StatusOK || exportResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("export response = %d %s", exportResponse.Code, exportResponse.Body.String())
	}
	var document providerauth.CodexAuthDocument
	if err := json.NewDecoder(exportResponse.Body).Decode(&document); err != nil {
		t.Fatal(err)
	}
	if document.Tokens.AccountID != "account-1" || document.Tokens.RefreshToken != "refresh" {
		t.Fatalf("auth document = %#v", document)
	}

	credentialSnapshot, err := session.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	providers := []*model.Provider{
		{
			ID: "route-paused", Name: "Paused Route", Vendor: "openai", Enabled: true,
			APITypes: []model.ProviderAPIType{{ProviderID: "route-paused", APIType: "codex", BaseURL: "https://paused.example.com"}},
			CredentialSessions: []credentialsession.RouteSnapshot{{
				RouteTargetID: "route-paused", APIType: "codex", VendorScope: "openai", Credential: credentialSnapshot,
			}},
		},
		{
			ID: "route-live", Name: "Live Route", Vendor: "openai", Enabled: true,
			APITypes: []model.ProviderAPIType{{ProviderID: "route-live", APIType: "codex", BaseURL: "https://live.example.com"}},
			CredentialSessions: []credentialsession.RouteSnapshot{{
				RouteTargetID: "route-live", APIType: "codex", VendorScope: "openai", Credential: credentialSnapshot,
			}},
		},
	}
	for _, provider := range providers {
		if err := repository.CreateProvider(context.Background(), provider); err != nil {
			t.Fatal(err)
		}
	}
	providers[0].Enabled = false
	if err := repository.UpdateProvider(context.Background(), providers[0]); err != nil {
		t.Fatal(err)
	}

	blockedResponse := httptest.NewRecorder()
	handler.ExportCredentialSessionCodexAuth(blockedResponse, exportRequest)
	if blockedResponse.Code != http.StatusConflict {
		t.Fatalf("blocked export response = %d %s", blockedResponse.Code, blockedResponse.Body.String())
	}
	var blocked struct {
		Code    string                        `json:"code"`
		Message string                        `json:"message"`
		Details codexAuthExportBlockedDetails `json:"details"`
	}
	if err := json.NewDecoder(blockedResponse.Body).Decode(&blocked); err != nil {
		t.Fatal(err)
	}
	if blocked.Code != ErrCodeConflict || blocked.Details.Kind != codexAuthExportBlockedDetailKind ||
		blocked.Details.CredentialSessionID != session.ID ||
		!reflect.DeepEqual(blocked.Details.BlockingRouteTargetIDs, []string{"route-live"}) {
		t.Fatalf("blocked export payload = %#v", blocked)
	}

	providers[1].Enabled = false
	if err := repository.UpdateProvider(context.Background(), providers[1]); err != nil {
		t.Fatal(err)
	}
	allPausedResponse := httptest.NewRecorder()
	handler.ExportCredentialSessionCodexAuth(allPausedResponse, exportRequest)
	if allPausedResponse.Code != http.StatusOK {
		t.Fatalf("all-paused export response = %d %s", allPausedResponse.Code, allPausedResponse.Body.String())
	}
}

func TestCredentialSessionHTTPRejectsSelfAssertedChatGPTAuthority(t *testing.T) {
	handler, repository := newCredentialSessionHandler(t)
	subject, err := credentialsession.AccountSubject("self-asserted")
	if err != nil {
		t.Fatal(err)
	}
	createBody, err := json.Marshal(map[string]any{
		"id": "untrusted-login", "kind": credentialsession.KindChatGPT,
		"secret_data": "unverified-secret", "subject": subject,
		"auth_state": credentialsession.AuthState{Status: credentialsession.AuthStatusActive, AccountID: "self-asserted"},
	})
	if err != nil {
		t.Fatal(err)
	}
	createResponse := httptest.NewRecorder()
	handler.CreateCredentialSession(createResponse, httptest.NewRequest(http.MethodPost, "/admin/api/credential-sessions", strings.NewReader(string(createBody))))
	if createResponse.Code != http.StatusBadRequest {
		t.Fatalf("self-asserted create response = %d %s", createResponse.Code, createResponse.Body.String())
	}
	if _, err := repository.GetCredentialSession(context.Background(), "untrusted-login"); !errors.Is(err, credentialsession.ErrNotFound) {
		t.Fatalf("self-asserted session persisted: %v", err)
	}

	verified := &credentialsession.Session{
		ID: "verified-login", Kind: credentialsession.KindChatGPT,
		SecretData: "verified-secret", Version: 1,
		AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive, AccountID: "verified-account"},
	}
	verifiedSubject, err := credentialsession.AccountSubject("verified-account")
	if err != nil {
		t.Fatal(err)
	}
	if err := verified.SetSubject(verifiedSubject); err != nil {
		t.Fatal(err)
	}
	loginAuth := &provenLoginAuth{session: verified}
	verifiedHandler := NewHandler(Config{Store: repository, Auth: loginAuth, Logger: zap.NewNop()})
	loginResponse := httptest.NewRecorder()
	verifiedHandler.CreateCredentialSession(loginResponse, httptest.NewRequest(
		http.MethodPost,
		"/admin/api/credential-sessions",
		strings.NewReader(`{"id":"verified-login","credential_login_id":"login-proof"}`),
	))
	if loginResponse.Code != http.StatusCreated || loginAuth.finalized != "login-proof" {
		t.Fatalf("verified login create response = %d %s finalized=%q", loginResponse.Code, loginResponse.Body.String(), loginAuth.finalized)
	}

	updateResponse := httptest.NewRecorder()
	updateRequest := httptest.NewRequest(http.MethodPut, "/admin/api/credential-sessions/verified-login", strings.NewReader(`{
		"expected_version":1,"secret_data":"forged-secret","subject":{"kind":"account","value":"Zm9yZ2Vk"},"auth_state":{"status":"active","account_id":"forged"}
	}`))
	updateRequest.SetPathValue("id", "verified-login")
	verifiedHandler.UpdateCredentialSession(updateResponse, updateRequest)
	if updateResponse.Code != http.StatusBadRequest {
		t.Fatalf("self-asserted update response = %d %s", updateResponse.Code, updateResponse.Body.String())
	}
	stored, err := repository.GetCredentialSession(context.Background(), "verified-login")
	if err != nil || stored.SecretData != "verified-secret" || string(stored.SubjectValue) != "verified-account" || stored.Version != 1 {
		t.Fatalf("rejected update mutated verified authority: session=%#v err=%v", stored, err)
	}
}
