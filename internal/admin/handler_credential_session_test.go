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
	session     *credentialsession.Session
	buildErr    error
	finalizeErr error
	finalized   string
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
	if a.buildErr != nil {
		return nil, a.buildErr
	}
	if a.session == nil {
		return nil, nil
	}
	session := a.session.Clone()
	session.ID = sessionID
	return session, nil
}

func (a *provenLoginAuth) FinalizeChatGPTLogin(loginID string) error {
	a.finalized = loginID
	return a.finalizeErr
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
	return NewHandler(Config{
		Store: repository, CredentialSessions: repository, ProviderCredentials: repository, Logger: zap.NewNop(),
	}), repository
}

func TestCredentialSessionCRUDAndReferenceDeletionContract(t *testing.T) {
	handler, repository := newCredentialSessionHandler(t)
	createRequest := httptest.NewRequest(http.MethodPost, "/admin/api/credential-sessions", strings.NewReader(`{
		"id":"static-session","name":"Route key","vendor":"openai","kind":"api_key","secret_data":"secret-1"
	}`))
	createResponse := httptest.NewRecorder()
	handler.CreateCredentialSession(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated || !strings.Contains(createResponse.Body.String(), `"secret_data":"secret-1"`) {
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
	if updateResponse.Code != http.StatusOK || !strings.Contains(updateResponse.Body.String(), `"secret_data":"secret-2"`) {
		t.Fatalf("update response = %d %s", updateResponse.Code, updateResponse.Body.String())
	}
	var updated CredentialSessionPayload
	if err := json.NewDecoder(updateResponse.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.Version != 2 || string(updated.Subject.Value) == string(created.Subject.Value) {
		t.Fatalf("updated payload = %#v", updated)
	}
	renameRequest := httptest.NewRequest(http.MethodPatch, "/admin/api/credential-sessions/static-session/name", strings.NewReader(`{
		"expected_version":2,"name":"Renamed route key"
	}`))
	renameRequest.SetPathValue("id", created.ID)
	renameResponse := httptest.NewRecorder()
	handler.RenameCredentialSession(renameResponse, renameRequest)
	if renameResponse.Code != http.StatusOK || !strings.Contains(renameResponse.Body.String(), `"name":"Renamed route key"`) {
		t.Fatalf("rename response = %d %s", renameResponse.Code, renameResponse.Body.String())
	}

	listResponse := httptest.NewRecorder()
	handler.ListCredentialSessions(listResponse, httptest.NewRequest(http.MethodGet, "/admin/api/credential-sessions", nil))
	if listResponse.Code != http.StatusOK ||
		!strings.Contains(listResponse.Body.String(), `"provider_name":"Route"`) ||
		!strings.Contains(listResponse.Body.String(), `"api_type":"codex"`) ||
		!strings.Contains(listResponse.Body.String(), `"secret_data":"secret-2"`) {
		t.Fatalf("list response = %d %s", listResponse.Code, listResponse.Body.String())
	}
	getRequest := httptest.NewRequest(http.MethodGet, "/admin/api/credential-sessions/static-session", nil)
	getRequest.SetPathValue("id", created.ID)
	getResponse := httptest.NewRecorder()
	handler.GetCredentialSession(getResponse, getRequest)
	if getResponse.Code != http.StatusOK || !strings.Contains(getResponse.Body.String(), `"secret_data":"secret-2"`) {
		t.Fatalf("get response = %d %s", getResponse.Code, getResponse.Body.String())
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

func TestCredentialSessionPayloadDoesNotProjectChatGPTTokenBundle(t *testing.T) {
	_, repository := newCredentialSessionHandler(t)
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
	created, err := repository.CreateCredentialSession(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}

	payload, err := credentialSessionPayload(context.Background(), repository, created)
	if err != nil {
		t.Fatal(err)
	}
	if payload.SecretData != "" {
		t.Fatalf("chatgpt secret data was projected: %q", payload.SecretData)
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
	verifiedHandler := NewHandler(Config{Store: repository, CredentialSessions: repository, Auth: loginAuth, Logger: zap.NewNop()})
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

func TestCredentialSessionReauthenticationRotatesSharedSessionWithoutRebindingRoutes(t *testing.T) {
	_, repository := newCredentialSessionHandler(t)
	subject, err := credentialsession.AccountSubject("account-1")
	if err != nil {
		t.Fatal(err)
	}
	current := &credentialsession.Session{
		ID: "shared-login", Kind: credentialsession.KindChatGPT,
		SecretData: "expired-secret", Version: 1,
		AuthState: credentialsession.AuthState{
			Status: credentialsession.AuthStatusReauthRequired, AccountID: "account-1",
		},
	}
	if err := current.SetSubject(subject); err != nil {
		t.Fatal(err)
	}
	created, err := repository.CreateCredentialSession(context.Background(), current)
	if err != nil {
		t.Fatal(err)
	}
	static := &credentialsession.Session{
		ID: "static-login", Kind: credentialsession.KindAPIKey,
		SecretData: "api-secret", Version: 1,
		AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive},
	}
	if err := static.SetSubject(credentialsession.PendingSubject()); err != nil {
		t.Fatal(err)
	}
	createdStatic, err := repository.CreateCredentialSession(context.Background(), static)
	if err != nil {
		t.Fatal(err)
	}
	chatGPTSnapshot, err := created.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	staticSnapshot, err := createdStatic.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	providers := []*model.Provider{
		{
			ID: "mixed-route", Name: "Mixed Route", Enabled: true,
			APITypes: []model.ProviderAPIType{
				{ProviderID: "mixed-route", APIType: "codex", BaseURL: "https://codex.example.com"},
				{ProviderID: "mixed-route", APIType: "claude", BaseURL: "https://claude.example.com"},
			},
			CredentialSessions: []credentialsession.RouteSnapshot{
				{RouteTargetID: "mixed-route", APIType: "codex", Credential: chatGPTSnapshot},
				{RouteTargetID: "mixed-route", APIType: "claude", Credential: staticSnapshot},
			},
		},
		{
			ID: "shared-route", Name: "Shared Route", Enabled: true,
			APITypes: []model.ProviderAPIType{
				{ProviderID: "shared-route", APIType: "codex", BaseURL: "https://shared.example.com"},
			},
			CredentialSessions: []credentialsession.RouteSnapshot{
				{RouteTargetID: "shared-route", APIType: "codex", Credential: chatGPTSnapshot},
			},
		},
	}
	for _, provider := range providers {
		if err := repository.CreateProvider(context.Background(), provider); err != nil {
			t.Fatal(err)
		}
	}

	reauthenticated := current.Clone()
	reauthenticated.SecretData = "rotated-secret"
	reauthenticated.AuthState = credentialsession.AuthState{
		Status: credentialsession.AuthStatusActive, AccountID: "account-1", Email: "user@example.com",
	}
	loginAuth := &provenLoginAuth{session: reauthenticated, finalizeErr: errors.New("completed login cleanup failed")}
	handler := NewHandler(Config{Store: repository, CredentialSessions: repository, Auth: loginAuth, Logger: zap.NewNop()})
	request := httptest.NewRequest(http.MethodPost, "/admin/api/credential-sessions/shared-login/reauthenticate", strings.NewReader(`{
		"expected_version":1,"credential_login_id":"login-proof"
	}`))
	request.SetPathValue("id", current.ID)
	response := httptest.NewRecorder()
	handler.ReauthenticateCredentialSession(response, request)
	if response.Code != http.StatusOK || loginAuth.finalized != "login-proof" {
		t.Fatalf("reauthenticate response = %d %s finalized=%q", response.Code, response.Body.String(), loginAuth.finalized)
	}
	var payload CredentialSessionPayload
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Version != 2 || payload.AuthState.Status != credentialsession.AuthStatusActive ||
		!reflect.DeepEqual(payload.ReferencedRouteTargets, []string{"mixed-route", "shared-route"}) {
		t.Fatalf("reauthenticated payload = %#v", payload)
	}
	stored, err := repository.GetCredentialSession(context.Background(), current.ID)
	if err != nil || stored.SecretData != "rotated-secret" || stored.Version != 2 {
		t.Fatalf("stored session = %#v err=%v", stored, err)
	}
	mixed, err := repository.GetProvider(context.Background(), "mixed-route")
	if err != nil {
		t.Fatal(err)
	}
	codexCredential, hasCodexCredential := mixed.CredentialSessionForAPIType("codex")
	claudeCredential, hasClaudeCredential := mixed.CredentialSessionForAPIType("claude")
	if len(mixed.APITypes) != 2 || !hasCodexCredential || !hasClaudeCredential ||
		codexCredential.SessionID != current.ID || claudeCredential.SessionID != static.ID {
		t.Fatalf("mixed provider bindings changed during reauthentication: %#v", mixed)
	}
}

func TestCredentialSessionReauthenticationRejectsDifferentResolvedSubject(t *testing.T) {
	_, repository := newCredentialSessionHandler(t)
	currentSubject, err := credentialsession.AccountSubject("account-1")
	if err != nil {
		t.Fatal(err)
	}
	current := &credentialsession.Session{
		ID: "login-session", Kind: credentialsession.KindChatGPT,
		SecretData: "expired-secret", Version: 1,
		AuthState: credentialsession.AuthState{
			Status: credentialsession.AuthStatusReauthRequired, AccountID: "account-1",
		},
	}
	if err := current.SetSubject(currentSubject); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateCredentialSession(context.Background(), current); err != nil {
		t.Fatal(err)
	}
	differentSubject, err := credentialsession.AccountSubject("account-2")
	if err != nil {
		t.Fatal(err)
	}
	candidate := current.Clone()
	candidate.SecretData = "different-secret"
	candidate.AuthState = credentialsession.AuthState{Status: credentialsession.AuthStatusActive, AccountID: "account-2"}
	if err := candidate.SetSubject(differentSubject); err != nil {
		t.Fatal(err)
	}
	loginAuth := &provenLoginAuth{session: candidate}
	handler := NewHandler(Config{Store: repository, CredentialSessions: repository, Auth: loginAuth, Logger: zap.NewNop()})
	request := httptest.NewRequest(http.MethodPost, "/admin/api/credential-sessions/login-session/reauthenticate", strings.NewReader(`{
		"expected_version":1,"credential_login_id":"login-other-account"
	}`))
	request.SetPathValue("id", current.ID)
	response := httptest.NewRecorder()
	handler.ReauthenticateCredentialSession(response, request)
	if response.Code != http.StatusConflict || loginAuth.finalized != "" ||
		!strings.Contains(response.Body.String(), credentialSessionSubjectMismatchDetailKind) {
		t.Fatalf("subject mismatch response = %d %s finalized=%q", response.Code, response.Body.String(), loginAuth.finalized)
	}
	stored, err := repository.GetCredentialSession(context.Background(), current.ID)
	if err != nil || stored.SecretData != "expired-secret" || stored.Version != 1 || !stored.Subject().Equal(currentSubject) {
		t.Fatalf("subject mismatch mutated session: %#v err=%v", stored, err)
	}
}

func TestCredentialSessionReauthenticationResolvesRecoveryPendingSubject(t *testing.T) {
	_, repository := newCredentialSessionHandler(t)
	current := &credentialsession.Session{
		ID: "recovery-session", Kind: credentialsession.KindChatGPT,
		Version:   1,
		AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusReauthRequired},
	}
	if err := current.SetSubject(credentialsession.PendingSubject()); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateCredentialSession(context.Background(), current); err != nil {
		t.Fatal(err)
	}
	resolved, err := credentialsession.AccountSubject("recovered-account")
	if err != nil {
		t.Fatal(err)
	}
	candidate := current.Clone()
	candidate.SecretData = "recovered-secret"
	candidate.AuthState = credentialsession.AuthState{
		Status: credentialsession.AuthStatusActive, AccountID: "recovered-account",
	}
	if err := candidate.SetSubject(resolved); err != nil {
		t.Fatal(err)
	}
	loginAuth := &provenLoginAuth{session: candidate}
	handler := NewHandler(Config{Store: repository, CredentialSessions: repository, Auth: loginAuth, Logger: zap.NewNop()})
	request := httptest.NewRequest(http.MethodPost, "/admin/api/credential-sessions/recovery-session/reauthenticate", strings.NewReader(`{
		"expected_version":1,"credential_login_id":"login-recovery"
	}`))
	request.SetPathValue("id", current.ID)
	response := httptest.NewRecorder()
	handler.ReauthenticateCredentialSession(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("pending-subject reauthentication response = %d %s", response.Code, response.Body.String())
	}
	stored, err := repository.GetCredentialSession(context.Background(), current.ID)
	if err != nil || !stored.Subject().Equal(resolved) || stored.AuthState.Status != credentialsession.AuthStatusActive {
		t.Fatalf("recovered session = %#v err=%v", stored, err)
	}
}

func TestCredentialSessionReauthenticationRejectsInvalidInputsAndUnverifiedCandidates(t *testing.T) {
	_, repository := newCredentialSessionHandler(t)
	validCandidate := &credentialsession.Session{
		ID: "candidate", Kind: credentialsession.KindChatGPT,
		SecretData: "verified-secret", Version: 1,
		AuthState: credentialsession.AuthState{
			Status: credentialsession.AuthStatusActive, AccountID: "account-1",
		},
	}
	subject, err := credentialsession.AccountSubject("account-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := validCandidate.SetSubject(subject); err != nil {
		t.Fatal(err)
	}
	validBody := `{"expected_version":1,"credential_login_id":"login-proof"}`

	tests := []struct {
		name string
		auth ProviderAuthService
		body string
		want int
	}{
		{name: "auth service lacks verified login builder", auth: nil, body: validBody, want: http.StatusNotImplemented},
		{name: "invalid JSON", auth: &provenLoginAuth{session: validCandidate}, body: `{`, want: http.StatusBadRequest},
		{name: "missing required fields", auth: &provenLoginAuth{session: validCandidate}, body: `{}`, want: http.StatusBadRequest},
		{name: "completed login lookup fails", auth: &provenLoginAuth{buildErr: errors.New("login expired")}, body: validBody, want: http.StatusBadRequest},
		{name: "completed login returns no session", auth: &provenLoginAuth{}, body: validBody, want: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := NewHandler(Config{Store: repository, CredentialSessions: repository, Auth: test.auth, Logger: zap.NewNop()})
			request := httptest.NewRequest(http.MethodPost, "/admin/api/credential-sessions/missing/reauthenticate", strings.NewReader(test.body))
			request.SetPathValue("id", "missing")
			response := httptest.NewRecorder()
			handler.ReauthenticateCredentialSession(response, request)
			if response.Code != test.want {
				t.Fatalf("response = %d %s, want %d", response.Code, response.Body.String(), test.want)
			}
		})
	}

	staticCandidate := &credentialsession.Session{
		ID: "static-candidate", Kind: credentialsession.KindAPIKey,
		SecretData: "static-secret", Version: 1,
		AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive},
	}
	if err := staticCandidate.SetSubject(credentialsession.PendingSubject()); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(Config{Store: repository, CredentialSessions: repository, Auth: &provenLoginAuth{session: staticCandidate}, Logger: zap.NewNop()})
	request := httptest.NewRequest(http.MethodPost, "/admin/api/credential-sessions/missing/reauthenticate", strings.NewReader(validBody))
	request.SetPathValue("id", "missing")
	response := httptest.NewRecorder()
	handler.ReauthenticateCredentialSession(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("non-ChatGPT candidate response = %d %s", response.Code, response.Body.String())
	}
}

func TestCredentialSessionReauthenticationRejectsStaticTargetsAndVersionConflicts(t *testing.T) {
	_, repository := newCredentialSessionHandler(t)
	static := &credentialsession.Session{
		ID: "static-session", Kind: credentialsession.KindAPIKey,
		SecretData: "static-secret", Version: 1,
		AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive},
	}
	if err := static.SetSubject(credentialsession.PendingSubject()); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateCredentialSession(context.Background(), static); err != nil {
		t.Fatal(err)
	}
	subject, err := credentialsession.AccountSubject("account-1")
	if err != nil {
		t.Fatal(err)
	}
	candidate := &credentialsession.Session{
		ID: "candidate", Kind: credentialsession.KindChatGPT,
		SecretData: "verified-secret", Version: 1,
		AuthState: credentialsession.AuthState{
			Status: credentialsession.AuthStatusActive, AccountID: "account-1",
		},
	}
	if err := candidate.SetSubject(subject); err != nil {
		t.Fatal(err)
	}
	loginAuth := &provenLoginAuth{session: candidate}
	handler := NewHandler(Config{Store: repository, CredentialSessions: repository, Auth: loginAuth, Logger: zap.NewNop()})
	request := httptest.NewRequest(http.MethodPost, "/admin/api/credential-sessions/static-session/reauthenticate", strings.NewReader(`{
		"expected_version":1,"credential_login_id":"login-proof"
	}`))
	request.SetPathValue("id", static.ID)
	response := httptest.NewRecorder()
	handler.ReauthenticateCredentialSession(response, request)
	if response.Code != http.StatusConflict || loginAuth.finalized != "" {
		t.Fatalf("static target response = %d %s finalized=%q", response.Code, response.Body.String(), loginAuth.finalized)
	}

	chatGPT := candidate.Clone()
	chatGPT.ID = "chatgpt-session"
	chatGPT.AuthState.Status = credentialsession.AuthStatusReauthRequired
	if _, err := repository.CreateCredentialSession(context.Background(), chatGPT); err != nil {
		t.Fatal(err)
	}
	versionRequest := httptest.NewRequest(http.MethodPost, "/admin/api/credential-sessions/chatgpt-session/reauthenticate", strings.NewReader(`{
		"expected_version":2,"credential_login_id":"login-proof"
	}`))
	versionRequest.SetPathValue("id", chatGPT.ID)
	versionResponse := httptest.NewRecorder()
	handler.ReauthenticateCredentialSession(versionResponse, versionRequest)
	if versionResponse.Code != http.StatusConflict || loginAuth.finalized != "" {
		t.Fatalf("version conflict response = %d %s finalized=%q", versionResponse.Code, versionResponse.Body.String(), loginAuth.finalized)
	}
}
