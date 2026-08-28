package admin

import (
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestBuildProviderFromExport_InvalidMetadataRejected(t *testing.T) {
	t.Parallel()

	if provider, ok := buildProviderFromExport(&ExportedProvider{
		ID:   "provider-1",
		Name: "Provider",
		APITypes: []ExportedAPIType{{
			APIType: "codex", BaseURL: "https://example.com", CredentialSessionID: "session-1",
		}},
		AuthMode: "invalid",
		Enabled:  true,
	}, map[string]bool{}); ok || provider != nil {
		t.Fatalf("buildProviderFromExport(invalid credential type) = (%#v, %v), want (nil, false)", provider, ok)
	}

	if provider, ok := buildProviderFromExport(&ExportedProvider{
		ID:               "provider-1",
		Name:             "Provider",
		UsageLimitPolicy: "invalid",
		Enabled:          true,
	}, map[string]bool{}); ok || provider != nil {
		t.Fatalf("buildProviderFromExport(invalid usage policy) = (%#v, %v), want (nil, false)", provider, ok)
	}
}

func TestNormalizeProviderScopeFromExport_InvalidScopeFallsBackToAny(t *testing.T) {
	t.Parallel()

	if got := normalizeProviderScopeFromExport("invalid"); got != model.ScopeAny {
		t.Fatalf("normalizeProviderScopeFromExport(invalid) = %q, want %q", got, model.ScopeAny)
	}
	if got := normalizeProviderScopeFromExport(string(model.ScopeVendor)); got != model.ScopeVendor {
		t.Fatalf("normalizeProviderScopeFromExport(valid) = %q, want %q", got, model.ScopeVendor)
	}
}

func TestValidateImportedProvider_RequiresSessionForEveryAPIType(t *testing.T) {
	t.Parallel()

	provider := &model.Provider{
		ID: "provider-1",
		APITypes: []model.ProviderAPIType{
			{ProviderID: "provider-1", APIType: "codex", BaseURL: "https://example.com"},
		},
	}
	if validateImportedProvider(provider) {
		t.Fatal("validateImportedProvider() = true, want false when route has no credential session")
	}
}

func TestBuildCredentialSessionsFromExport_PreservesPendingSubject(t *testing.T) {
	t.Parallel()

	sessions, warnings := buildCredentialSessionsFromExport([]ExportedCredentialSession{{
		ID: "session-1", Kind: credentialsession.KindAPIKey,
		TransferMode: CredentialSessionTransferStaticSecret,
		SecretData:   "secret", Version: 1, Subject: credentialsession.PendingSubject(),
	}})
	if len(sessions) != 1 || len(warnings) != 0 || sessions[0].SubjectKind != credentialsession.SubjectPending {
		t.Fatalf("buildCredentialSessionsFromExport() = (%#v, %#v), want one pending session", sessions, warnings)
	}
}

func TestCanonicalProviderImportExportJSON_InvalidProviderReturnsFalse(t *testing.T) {
	t.Parallel()

	payload, ok := canonicalProviderImportExportJSON(&ExportedProvider{
		ID:   "provider-1",
		Name: "Provider",
		APITypes: []ExportedAPIType{{
			APIType: "codex", BaseURL: "https://example.com", CredentialSessionID: "session-1",
		}},
		AuthMode: "invalid",
		Enabled:  true,
	}, map[string]bool{})
	if ok || payload != nil {
		t.Fatalf("canonicalProviderImportExportJSON(invalid) = (%v, %v), want (nil, false)", payload, ok)
	}
}

func TestBuildCredentialSessionsFromExport_BlankSecretRejected(t *testing.T) {
	t.Parallel()
	subject, err := credentialsession.AccountSubject("account-1")
	if err != nil {
		t.Fatal(err)
	}

	sessions, warnings := buildCredentialSessionsFromExport([]ExportedCredentialSession{{
		ID: "session-1", Kind: credentialsession.KindChatGPT,
		TransferMode: CredentialSessionTransferStaticSecret,
		SecretData:   "   ", Version: 1, Subject: subject,
	}})
	if len(sessions) != 0 || len(warnings) != 1 {
		t.Fatalf("buildCredentialSessionsFromExport(blank secret) = (%#v, %#v), want rejection", sessions, warnings)
	}
}

func TestCredentialSessionTransferValidationMatrix(t *testing.T) {
	validStatic := importedTestSession("static", "secret")
	validChatGPT := buildExportedCredentialSession(verifiedChatGPTSession(t, "chat", "token", "acct"))

	t.Run("static boundary", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*ExportedCredentialSession)
			want   string
		}{
			{name: "transfer mode", mutate: func(item *ExportedCredentialSession) { item.TransferMode = CredentialSessionTransferReauthenticate }, want: "transfer_mode"},
			{name: "kind", mutate: func(item *ExportedCredentialSession) { item.Kind = credentialsession.KindChatGPT }, want: "only api_key"},
			{name: "subject", mutate: func(item *ExportedCredentialSession) { item.Subject = credentialsession.Subject{} }, want: "unknown subject"},
			{name: "session", mutate: func(item *ExportedCredentialSession) { item.SecretData = "" }, want: "secret data is blank"},
		}
		for _, testCase := range tests {
			t.Run(testCase.name, func(t *testing.T) {
				item := validStatic
				testCase.mutate(&item)
				if _, err := buildStaticCredentialSessionFromExport(item); err == nil || !strings.Contains(err.Error(), testCase.want) {
					t.Fatalf("buildStaticCredentialSessionFromExport() error = %v", err)
				}
			})
		}
		if session, err := buildStaticCredentialSessionFromExport(validStatic); err != nil || session.ID != validStatic.ID {
			t.Fatalf("valid static session = (%#v, %v)", session, err)
		}
	})

	t.Run("ChatGPT descriptor", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*ExportedCredentialSession)
			want   string
		}{
			{name: "transfer mode", mutate: func(item *ExportedCredentialSession) { item.TransferMode = CredentialSessionTransferStaticSecret }, want: "transfer_mode"},
			{name: "metadata", mutate: func(item *ExportedCredentialSession) { item.ID = "" }, want: "requires id"},
			{name: "secret", mutate: func(item *ExportedCredentialSession) { item.SecretData = "attacker" }, want: "cannot carry secret"},
			{name: "authority", mutate: func(item *ExportedCredentialSession) { item.AuthState.AccountID = "acct" }, want: "canonical recovery"},
		}
		for _, testCase := range tests {
			t.Run(testCase.name, func(t *testing.T) {
				item := validChatGPT
				testCase.mutate(&item)
				if err := validateChatGPTReauthenticationDescriptor(item); err == nil || !strings.Contains(err.Error(), testCase.want) {
					t.Fatalf("validateChatGPTReauthenticationDescriptor() error = %v", err)
				}
			})
		}
		if err := validateChatGPTReauthenticationDescriptor(validChatGPT); err != nil {
			t.Fatalf("valid descriptor error = %v", err)
		}
	})

	t.Run("batch", func(t *testing.T) {
		invalidStatic := validStatic
		invalidStatic.ID = "invalid"
		invalidStatic.SecretData = ""
		sessions, warnings := buildCredentialSessionsFromExport([]ExportedCredentialSession{
			validStatic,
			validStatic,
			validChatGPT,
			invalidStatic,
		})
		if len(sessions) != 1 || len(warnings) != 3 {
			t.Fatalf("batch result = (%#v, %#v)", sessions, warnings)
		}
	})
}

func TestStageChatGPTReauthenticationDescriptorBoundaries(t *testing.T) {
	descriptor := buildExportedCredentialSession(verifiedChatGPTSession(t, "chat", "token", "acct"))
	existing := map[string]credentialsession.Snapshot{
		"chat": {SessionID: "chat", Kind: credentialsession.KindAPIKey},
	}
	staged := stagedConfigImport{}
	if err := stageChatGPTReauthenticationDescriptor(&staged, descriptor, existing); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched existing error = %v", err)
	}
	delete(existing, "chat")
	if err := stageChatGPTReauthenticationDescriptor(&staged, descriptor, existing); err == nil || !strings.Contains(err.Error(), "requires verified") {
		t.Fatalf("missing existing error = %v", err)
	}
	descriptor.SecretData = "attacker"
	if err := stageChatGPTReauthenticationDescriptor(&staged, descriptor, existing); err == nil || !strings.Contains(err.Error(), "cannot import ChatGPT") {
		t.Fatalf("invalid descriptor error = %v", err)
	}
}

func TestStageImportedCredentialSessionsRejectsDuplicateAndInvalidStatic(t *testing.T) {
	valid := importedTestSession("static", "secret")
	invalid := importedTestSession("invalid", "")
	staged := stagedConfigImport{}
	declared := stageImportedCredentialSessions(
		&staged,
		[]ExportedCredentialSession{valid, valid, invalid},
		nil,
	)
	if len(declared) != 1 || len(staged.bundle.CredentialSessions) != 1 || len(staged.warnings) != 2 {
		t.Fatalf("staged static sessions = declared %#v bundle %#v warnings %#v", declared, staged.bundle.CredentialSessions, staged.warnings)
	}
}

func TestGroupAndRoutingPolicyImportDiffers_NilAndInvalidInputs(t *testing.T) {
	t.Parallel()

	if groupImportDiffers(&ExportedGroup{}, &model.Group{ID: "group-1"}) {
		t.Fatal("groupImportDiffers(invalid imported group) = true, want false")
	}

	policy := &model.RoutingPolicy{APIType: "codex"}
	if !routingPolicyImportDiffers(nil, policy) {
		t.Fatal("routingPolicyImportDiffers(nil, policy) = false, want true")
	}
	if routingPolicyImportDiffers(nil, nil) {
		t.Fatal("routingPolicyImportDiffers(nil, nil) = true, want false")
	}
}
