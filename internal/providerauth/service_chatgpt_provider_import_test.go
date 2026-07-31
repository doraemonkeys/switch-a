package providerauth

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
)

type importTestIDGenerator struct {
	ids   []string
	index int
}

func (g *importTestIDGenerator) NewID() string {
	if g.index >= len(g.ids) {
		return ""
	}
	id := g.ids[g.index]
	g.index++
	return id
}

type mutableImportTestClock struct {
	now time.Time
}

func (c *mutableImportTestClock) Now() time.Time {
	return c.now
}

func (*mutableImportTestClock) NewTicker(interval time.Duration) *time.Ticker {
	return time.NewTicker(interval)
}

func newSub2APIImportTestService(
	t *testing.T,
	now time.Time,
	ids ...string,
) (*Service, *mutableImportTestClock) {
	t.Helper()
	clock := &mutableImportTestClock{now: now}
	service := NewService(Config{
		Clock:       clock,
		IDGenerator: &importTestIDGenerator{ids: ids},
		HTTPClient: stubHTTPDoer{do: func(req *http.Request) (*http.Response, error) {
			t.Fatalf("preview made an unexpected network request to %s", req.URL.String())
			return nil, errors.New("unexpected preview network request")
		}},
	})
	t.Cleanup(func() {
		if err := service.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown returned error: %v", err)
		}
	})
	return service, clock
}

func marshalSub2APIImportDocument(t *testing.T, accounts []any, proxies any) []byte {
	t.Helper()
	document := map[string]any{"accounts": accounts}
	if proxies != nil {
		document["proxies"] = proxies
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("json.Marshal import document returned error: %v", err)
	}
	return raw
}

func sub2APIImportAccount(
	name string,
	accessToken string,
	refreshToken string,
	idToken string,
	accountID string,
	concurrency int,
	priority int,
) map[string]any {
	credentials := map[string]any{
		"access_token":       accessToken,
		"refresh_token":      refreshToken,
		"chatgpt_account_id": accountID,
	}
	if idToken != "" {
		credentials["id_token"] = idToken
	}
	return map[string]any{
		"name":        name,
		"platform":    sub2APIPlatformOpenAI,
		"type":        sub2APIAccountTypeOAuth,
		"credentials": credentials,
		"concurrency": concurrency,
		"priority":    priority,
	}
}

func hasChatGPTProviderImportWarning(warnings []ChatGPTProviderImportWarning, code string) bool {
	for _, warning := range warnings {
		if warning.Code == code {
			return true
		}
	}
	return false
}

func sealSub2APIImportWithSourceDispositions(
	t *testing.T,
	service *Service,
	preview *ChatGPTProviderImportPreview,
) {
	t.Helper()
	dispositions := make([]ChatGPTProviderImportCandidateDisposition, len(preview.Items))
	for index, item := range preview.Items {
		dispositions[index] = ChatGPTProviderImportCandidateDisposition{
			CandidateID: item.CandidateID,
			State:       item.State,
		}
	}
	if err := service.SealChatGPTProviderImportPreview(preview.ImportID, dispositions); err != nil {
		t.Fatalf("SealChatGPTProviderImportPreview returned error: %v", err)
	}
}

func TestPreviewSub2APIChatGPTImport_StagesSafeRichPreview(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	idToken := chatgptAuthJWT(t, "acct-ready", "ready@example.com", "plus", now.Add(-time.Hour))
	accessToken := chatgptAccessJWT(t, "acct-ready", "ready@example.com", "plus", now.Add(-time.Hour))
	account := sub2APIImportAccount(
		"  Imported Plus  ",
		accessToken,
		"refresh-secret",
		idToken,
		"acct-ready",
		10,
		3,
	)
	account["rate_multiplier"] = 1.25
	account["auto_pause_on_expired"] = true
	account["extra"] = map[string]any{
		"codex_5h_used_percent":   17.5,
		"codex_5h_window_minutes": 300,
		"codex_5h_reset_at":       now.Add(90 * time.Minute).Format(time.RFC3339),
		"codex_7d_used_percent":   43.25,
		"codex_7d_window_minutes": 10080,
		"codex_7d_reset_at":       now.Add(6 * 24 * time.Hour).Unix(),
		"codex_usage_updated_at":  now.Add(-5 * time.Minute).Format(time.RFC3339),
	}
	raw := marshalSub2APIImportDocument(t, []any{account}, []any{map[string]any{"name": "ignored"}})
	service, _ := newSub2APIImportTestService(t, now, "candidate-ready", "import-ready")

	preview, err := service.PreviewSub2APIChatGPTImport(raw)
	if err != nil {
		t.Fatalf("PreviewSub2APIChatGPTImport returned error: %v", err)
	}
	if preview.ImportID != "import-ready" {
		t.Fatalf("ImportID = %q, want %q", preview.ImportID, "import-ready")
	}
	if !preview.ExpiresAt.Equal(now.Add(chatGPTProviderImportSessionTTL)) {
		t.Fatalf("ExpiresAt = %s, want %s", preview.ExpiresAt, now.Add(chatGPTProviderImportSessionTTL))
	}
	if preview.Summary != (ChatGPTProviderImportSummary{Total: 1, Ready: 1}) {
		t.Fatalf("Summary = %#v, want one ready account", preview.Summary)
	}
	for _, code := range []string{
		ChatGPTProviderImportWarningProxiesIgnored,
		ChatGPTProviderImportWarningRateMultiplierIgnored,
		ChatGPTProviderImportWarningAutoPauseIgnored,
	} {
		if !hasChatGPTProviderImportWarning(preview.Warnings, code) {
			t.Fatalf("Warnings = %#v, want code %q", preview.Warnings, code)
		}
	}

	item := preview.Items[0]
	if item.CandidateID != "candidate-ready" || item.SourceIndex != 0 {
		t.Fatalf("preview identity = %#v, want deterministic candidate at source 0", item)
	}
	if item.State != ChatGPTProviderImportCandidateStateReady {
		t.Fatalf("State = %q, want %q", item.State, ChatGPTProviderImportCandidateStateReady)
	}
	if item.Name != "Imported Plus" || item.Concurrency != 10 || item.Priority != 3 {
		t.Fatalf("routing preview = %#v, want trimmed name and direct source routing values", item)
	}
	if item.Auth == nil || item.Auth.AccountID != "acct-ready" || item.Auth.Email != "ready@example.com" {
		t.Fatalf("Auth = %#v, want account identity from token claims", item.Auth)
	}
	if item.Auth.Usage == nil || item.Auth.Usage.FiveHour == nil || item.Auth.Usage.OneWeek == nil {
		t.Fatalf("Usage = %#v, want 5h and 7d snapshots", item.Auth.Usage)
	}
	if item.Auth.Usage.FiveHour.UsedPercent != 17.5 || item.Auth.Usage.FiveHour.WindowSeconds != 5*60*60 {
		t.Fatalf("FiveHour = %#v, want imported 5h usage", item.Auth.Usage.FiveHour)
	}
	if item.Auth.Usage.OneWeek.UsedPercent != 43.25 || item.Auth.Usage.OneWeek.WindowSeconds != 7*24*60*60 {
		t.Fatalf("OneWeek = %#v, want imported 7d usage", item.Auth.Usage.OneWeek)
	}
	if !hasChatGPTProviderImportWarning(item.Warnings, ChatGPTProviderImportWarningTokenExpired) {
		t.Fatalf("item warnings = %#v, want expired-token disclosure", item.Warnings)
	}

	previewJSON, err := json.Marshal(preview)
	if err != nil {
		t.Fatalf("json.Marshal preview returned error: %v", err)
	}
	for _, secret := range []string{accessToken, "refresh-secret", idToken, "secret_data"} {
		if strings.Contains(string(previewJSON), secret) {
			t.Fatalf("preview JSON disclosed secret marker %q", secret)
		}
	}
	if _, err := service.ClaimChatGPTProviderImport(preview.ImportID); !errors.Is(err, ErrChatGPTProviderImportPreviewNotSealed) {
		t.Fatalf("claim before seal error = %v, want ErrChatGPTProviderImportPreviewNotSealed", err)
	}
	sealSub2APIImportWithSourceDispositions(t, service, preview)

	candidates, err := service.ClaimChatGPTProviderImport(preview.ImportID)
	if err != nil {
		t.Fatalf("ClaimChatGPTProviderImport returned error: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1", len(candidates))
	}
	candidate := candidates[0]
	secret, err := decodeChatGPTCredentialSecret(candidate.Credential.SecretData)
	if err != nil {
		t.Fatalf("decodeChatGPTCredentialSecret returned error: %v", err)
	}
	if secret.AccessToken != accessToken || secret.RefreshToken != "refresh-secret" || secret.IDToken != idToken {
		t.Fatalf("staged secret = %#v, want original credential tokens", secret)
	}
	if candidate.Credential.BindingAccountID == nil || *candidate.Credential.BindingAccountID != "acct-ready" {
		t.Fatalf("BindingAccountID = %#v, want acct-ready", candidate.Credential.BindingAccountID)
	}
	candidateJSON, err := json.Marshal(candidate)
	if err != nil {
		t.Fatalf("json.Marshal candidate returned error: %v", err)
	}
	for _, secretValue := range []string{accessToken, "refresh-secret", idToken, "secret_data"} {
		if strings.Contains(string(candidateJSON), secretValue) {
			t.Fatalf("candidate JSON disclosed secret marker %q", secretValue)
		}
	}

	// Claims return deep clones so request-specific enrichment cannot mutate a retryable draft.
	candidates[0].Credential.SecretData = "mutated"
	candidates[0].AuthState.UsageSnapshot.FiveHour.UsedPercent = 99
	candidates[0].Disposition.State = ChatGPTProviderImportCandidateStateExisting
	if err := service.ReleaseChatGPTProviderImportClaim(preview.ImportID); err != nil {
		t.Fatalf("ReleaseChatGPTProviderImportClaim returned error: %v", err)
	}
	retrievedAgain, err := service.ClaimChatGPTProviderImport(preview.ImportID)
	if err != nil {
		t.Fatalf("second ClaimChatGPTProviderImport returned error: %v", err)
	}
	if retrievedAgain[0].Credential.SecretData == "mutated" {
		t.Fatal("staged credential shared mutable state with a retrieved candidate")
	}
	if retrievedAgain[0].AuthState.UsageSnapshot.FiveHour.UsedPercent != 17.5 {
		t.Fatal("staged usage snapshot shared mutable state with a retrieved candidate")
	}
	if retrievedAgain[0].Disposition == nil || retrievedAgain[0].Disposition.State != ChatGPTProviderImportCandidateStateReady {
		t.Fatal("staged disposition shared mutable state with a retrieved candidate")
	}

	provider := &model.Provider{
		ID:          "provider-final",
		Name:        "Edited in preview",
		Priority:    8,
		Concurrency: 4,
	}
	if err := ApplyChatGPTProviderImportCandidate(provider, retrievedAgain[0]); err != nil {
		t.Fatalf("ApplyChatGPTProviderImportCandidate returned error: %v", err)
	}
	if provider.Name != "Edited in preview" || provider.Priority != 8 || provider.Concurrency != 4 {
		t.Fatalf("ApplyChatGPTProviderImportCandidate overwrote caller-owned routing fields: %#v", provider)
	}
	if provider.CredentialType != model.ProviderCredentialTypeChatGPT || provider.AuthMode != authModeBearer {
		t.Fatalf("provider auth normalization = %#v, want ChatGPT bearer", provider)
	}
	if provider.Credential.ProviderID != provider.ID || provider.AuthState.ProviderID != provider.ID {
		t.Fatalf("split records not rebound to provider %q", provider.ID)
	}
	decoded, err := DecodeProviderChatGPTCredential(provider)
	if err != nil {
		t.Fatalf("DecodeProviderChatGPTCredential returned error: %v", err)
	}
	if decoded.AccountID != "acct-ready" || decoded.RefreshToken != "refresh-secret" {
		t.Fatalf("decoded applied credential = %#v, want staged account and refresh token", decoded)
	}

	if err := service.FinalizeChatGPTProviderImport(preview.ImportID); err != nil {
		t.Fatalf("FinalizeChatGPTProviderImport returned error: %v", err)
	}
	if _, err := service.ClaimChatGPTProviderImport(preview.ImportID); !errors.Is(err, ErrChatGPTProviderImportNotFound) {
		t.Fatalf("claim after finalize error = %v, want ErrChatGPTProviderImportNotFound", err)
	}
}

func TestPreviewSub2APIChatGPTImport_ContainsUnserializableExternalValues(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)

	t.Run("extreme jwt expiration invalidates only its account", func(t *testing.T) {
		accessToken := makeTestJWT(t, map[string]any{
			"iss":   defaultOAuthIssuer,
			"aud":   chatGPTAPIAudience,
			"exp":   int64(math.MaxInt64),
			"email": "extreme@example.com",
			"https://api.openai.com/auth": map[string]any{
				"chatgpt_account_id": "acct-extreme",
				"chatgpt_plan_type":  "plus",
			},
		})
		account := sub2APIImportAccount(
			"Extreme expiration",
			accessToken,
			"refresh-extreme",
			"",
			"acct-extreme",
			1,
			0,
		)
		service, _ := newSub2APIImportTestService(t, now, "candidate-extreme", "import-extreme")

		preview, err := service.PreviewSub2APIChatGPTImport(marshalSub2APIImportDocument(t, []any{account}, nil))
		if err != nil {
			t.Fatalf("PreviewSub2APIChatGPTImport returned error: %v", err)
		}
		if preview.Items[0].State != ChatGPTProviderImportCandidateStateInvalid ||
			preview.Summary != (ChatGPTProviderImportSummary{Total: 1, Invalid: 1}) {
			t.Fatalf("preview = %#v, want one independently invalid account", preview)
		}
		if _, err := json.Marshal(preview); err != nil {
			t.Fatalf("json.Marshal preview returned error: %v", err)
		}
	})

	t.Run("extreme usage metadata is ignored without blocking import", func(t *testing.T) {
		account := sub2APIImportAccount(
			"Extreme usage",
			chatgptAccessJWT(t, "acct-usage", "usage@example.com", "plus", now.Add(time.Hour)),
			"refresh-usage",
			"",
			"acct-usage",
			1,
			0,
		)
		account["extra"] = json.RawMessage(`{
			"codex_5h_used_percent":1e999,
			"codex_5h_window_minutes":150119987579017,
			"codex_5h_reset_at":9223372036854775807,
			"codex_usage_updated_at":9223372036854775807
		}`)
		service, _ := newSub2APIImportTestService(t, now, "candidate-usage", "import-usage")

		preview, err := service.PreviewSub2APIChatGPTImport(marshalSub2APIImportDocument(t, []any{account}, nil))
		if err != nil {
			t.Fatalf("PreviewSub2APIChatGPTImport returned error: %v", err)
		}
		item := preview.Items[0]
		if item.State != ChatGPTProviderImportCandidateStateReady || item.Auth == nil {
			t.Fatalf("item = %#v, want ready account", item)
		}
		if item.Auth.Usage != nil {
			t.Fatalf("usage = %#v, want corrupt window omitted instead of fabricated zero values", item.Auth.Usage)
		}
		if len(item.Warnings) != 4 {
			t.Fatalf("warnings = %#v, want one warning for each ignored usage field", item.Warnings)
		}
		if _, err := json.Marshal(preview); err != nil {
			t.Fatalf("json.Marshal preview returned error: %v", err)
		}
	})
}

func TestPreviewSub2APIChatGPTImport_ClassifiesAccountsIndependently(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	accessOnlyJWT := chatgptAccessJWT(t, "acct-shared", "first@example.com", "team", now.Add(time.Hour))
	readyIDToken := chatgptAuthJWT(t, "acct-shared", "first@example.com", "team", now.Add(time.Hour))
	duplicateIDToken := chatgptAuthJWT(t, "acct-shared", "second@example.com", "plus", now.Add(time.Hour))
	duplicateAccessToken := chatgptAccessJWT(t, "acct-shared", "second@example.com", "plus", now.Add(time.Hour))
	invalidIDToken := chatgptAuthJWT(t, "acct-invalid", "invalid@example.com", "free", now.Add(time.Hour))
	ready := sub2APIImportAccount("First", accessOnlyJWT, "refresh-first", readyIDToken, "acct-shared", 7, 2)
	duplicate := sub2APIImportAccount("Second", duplicateAccessToken, "refresh-second", duplicateIDToken, "acct-shared", 8, 4)
	invalid := sub2APIImportAccount("Broken", "access-invalid", "", invalidIDToken, "acct-invalid", 1, 0)
	unsupported := map[string]any{
		"name":        "Anthropic",
		"platform":    "anthropic",
		"type":        "oauth",
		"concurrency": 1,
		"priority":    0,
	}
	raw := marshalSub2APIImportDocument(t, []any{ready, duplicate, invalid, unsupported}, []any{})
	service, _ := newSub2APIImportTestService(
		t,
		now,
		"candidate-first",
		"candidate-second",
		"candidate-invalid",
		"candidate-unsupported",
		"import-mixed",
	)

	preview, err := service.PreviewSub2APIChatGPTImport(raw)
	if err != nil {
		t.Fatalf("PreviewSub2APIChatGPTImport returned error: %v", err)
	}
	wantSummary := ChatGPTProviderImportSummary{
		Total:       4,
		Ready:       1,
		Duplicate:   1,
		Invalid:     1,
		Unsupported: 1,
	}
	if preview.Summary != wantSummary {
		t.Fatalf("Summary = %#v, want %#v", preview.Summary, wantSummary)
	}
	wantStates := []ChatGPTProviderImportCandidateState{
		ChatGPTProviderImportCandidateStateReady,
		ChatGPTProviderImportCandidateStateDuplicate,
		ChatGPTProviderImportCandidateStateInvalid,
		ChatGPTProviderImportCandidateStateUnsupported,
	}
	for index, wantState := range wantStates {
		if preview.Items[index].State != wantState {
			t.Fatalf("item %d state = %q, want %q", index, preview.Items[index].State, wantState)
		}
	}
	if !hasChatGPTProviderImportWarning(preview.Items[1].Warnings, ChatGPTProviderImportWarningDuplicateAccount) {
		t.Fatalf("duplicate warnings = %#v, want duplicate marker", preview.Items[1].Warnings)
	}
	if len(preview.Warnings) != 0 {
		t.Fatalf("global warnings = %#v, want none for empty proxies and absent ignored fields", preview.Warnings)
	}
	dispositions := make([]ChatGPTProviderImportCandidateDisposition, len(preview.Items))
	for index, item := range preview.Items {
		dispositions[index] = ChatGPTProviderImportCandidateDisposition{
			CandidateID: item.CandidateID,
			State:       item.State,
		}
	}
	dispositions[0].State = ChatGPTProviderImportCandidateStateExisting
	dispositions[0].ExpectedProviderID = "bound-provider"
	dispositions[0].ExpectedCredentialVersion = 7
	dispositions[0].ExpectedCredentialCreatedAt = now.Add(-24 * time.Hour)
	if err := service.SealChatGPTProviderImportPreview(preview.ImportID, dispositions); err != nil {
		t.Fatalf("SealChatGPTProviderImportPreview returned error: %v", err)
	}

	allCandidates, err := service.ClaimChatGPTProviderImport(preview.ImportID)
	if err != nil {
		t.Fatalf("Claim all candidates returned error: %v", err)
	}
	if len(allCandidates) != 4 {
		t.Fatalf("Get all candidate count = %d, want 4 including blocked states", len(allCandidates))
	}
	if disposition := allCandidates[0].Disposition; disposition == nil ||
		disposition.State != ChatGPTProviderImportCandidateStateExisting ||
		disposition.ExpectedProviderID != "bound-provider" ||
		disposition.ExpectedCredentialVersion != 7 ||
		!disposition.ExpectedCredentialCreatedAt.Equal(now.Add(-24*time.Hour)) {
		t.Fatalf("sealed existing disposition = %#v, want bound-provider version 7", disposition)
	}
	if err := ApplyChatGPTProviderImportCandidate(&model.Provider{ID: "blocked"}, allCandidates[1]); !errors.Is(err, ErrChatGPTProviderImportInvalidCandidate) {
		t.Fatalf("apply duplicate error = %v, want ErrChatGPTProviderImportInvalidCandidate", err)
	}
	if err := service.ReleaseChatGPTProviderImportClaim(preview.ImportID); err != nil {
		t.Fatalf("ReleaseChatGPTProviderImportClaim returned error: %v", err)
	}
}

func TestPreviewSub2APIChatGPTImport_RejectsUntrustedOrMismatchedIdentity(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	validToken := chatgptAuthJWT(t, "acct-signed", "signed@example.com", "plus", now.Add(time.Hour))
	validAccessToken := chatgptAccessJWT(t, "acct-signed", "signed@example.com", "plus", now.Add(time.Hour))
	maliciousIssuerToken := makeTestJWT(t, map[string]any{
		"iss":                         "https://attacker.invalid",
		"aud":                         defaultOAuthClientID,
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct-issuer"},
	})
	maliciousAudienceToken := makeTestJWT(t, map[string]any{
		"iss":                         defaultOAuthIssuer,
		"aud":                         "attacker-client",
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct-audience"},
	})
	accounts := []any{
		sub2APIImportAccount("Mismatch", validAccessToken, "refresh-a", validToken, "acct-exported", 1, 0),
		sub2APIImportAccount("Issuer", chatgptAccessJWT(t, "acct-issuer", "issuer@example.com", "plus", now.Add(time.Hour)), "refresh-b", maliciousIssuerToken, "acct-issuer", 1, 0),
		sub2APIImportAccount("Audience", chatgptAccessJWT(t, "acct-audience", "audience@example.com", "plus", now.Add(time.Hour)), "refresh-c", maliciousAudienceToken, "acct-audience", 1, 0),
	}
	service, _ := newSub2APIImportTestService(
		t,
		now,
		"candidate-mismatch",
		"candidate-issuer",
		"candidate-audience",
		"import-untrusted",
	)

	preview, err := service.PreviewSub2APIChatGPTImport(marshalSub2APIImportDocument(t, accounts, nil))
	if err != nil {
		t.Fatalf("PreviewSub2APIChatGPTImport returned error: %v", err)
	}
	if preview.Summary != (ChatGPTProviderImportSummary{Total: 3, Invalid: 3}) {
		t.Fatalf("Summary = %#v, want three invalid accounts", preview.Summary)
	}
	if !hasChatGPTProviderImportWarning(preview.Items[0].Warnings, ChatGPTProviderImportWarningAccountIDMismatch) {
		t.Fatalf("mismatch warnings = %#v, want account-ID mismatch", preview.Items[0].Warnings)
	}
	for _, item := range preview.Items {
		if item.Auth != nil {
			t.Fatalf("invalid item %q exposed an auth preview: %#v", item.Name, item.Auth)
		}
	}
	encoded, err := json.Marshal(preview)
	if err != nil {
		t.Fatalf("json.Marshal preview returned error: %v", err)
	}
	for _, attackerControlledValue := range []string{"https://attacker.invalid", "attacker-client"} {
		if strings.Contains(string(encoded), attackerControlledValue) {
			t.Fatalf("preview echoed rejected JWT routing claim %q", attackerControlledValue)
		}
	}
}

func TestPreviewSub2APIChatGPTImport_RejectsInvalidDocuments(t *testing.T) {
	tooManyAccounts := make([]any, maxSub2APIImportAccounts+1)
	for index := range tooManyAccounts {
		tooManyAccounts[index] = map[string]any{}
	}
	tooManyRaw := marshalSub2APIImportDocument(t, tooManyAccounts, nil)
	tests := []struct {
		name string
		raw  []byte
	}{
		{name: "empty", raw: []byte("  ")},
		{name: "invalid json", raw: []byte("{")},
		{name: "missing accounts", raw: []byte(`{}`)},
		{name: "null accounts", raw: []byte(`{"accounts":null}`)},
		{name: "accounts not array", raw: []byte(`{"accounts":{}}`)},
		{name: "empty accounts", raw: []byte(`{"accounts":[]}`)},
		{name: "too many accounts", raw: tooManyRaw},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, _ := newSub2APIImportTestService(t, time.Now().UTC())
			if _, err := service.PreviewSub2APIChatGPTImport(test.raw); !errors.Is(err, ErrChatGPTProviderImportInvalidDocument) {
				t.Fatalf("error = %v, want ErrChatGPTProviderImportInvalidDocument", err)
			}
		})
	}
}

func TestChatGPTProviderImportLifecycle_ExpiresFinalizesAndCancels(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	account := sub2APIImportAccount(
		"Expiring",
		chatgptAccessJWT(t, "acct-expiring", "expires@example.com", "plus", now.Add(time.Hour)),
		"refresh",
		chatgptAuthJWT(t, "acct-expiring", "expires@example.com", "plus", now.Add(time.Hour)),
		"acct-expiring",
		1,
		0,
	)
	raw := marshalSub2APIImportDocument(t, []any{account}, nil)
	service, clock := newSub2APIImportTestService(
		t,
		now,
		"candidate-expiring",
		"import-expiring",
		"candidate-cancelled",
		"import-cancelled",
	)

	expiring, err := service.PreviewSub2APIChatGPTImport(raw)
	if err != nil {
		t.Fatalf("first preview returned error: %v", err)
	}
	clock.now = now.Add(chatGPTProviderImportSessionTTL)
	if _, err := service.ClaimChatGPTProviderImport(expiring.ImportID); !errors.Is(err, ErrChatGPTProviderImportExpired) {
		t.Fatalf("expired claim error = %v, want ErrChatGPTProviderImportExpired", err)
	}
	if _, err := service.ClaimChatGPTProviderImport(expiring.ImportID); !errors.Is(err, ErrChatGPTProviderImportNotFound) {
		t.Fatalf("second expired claim error = %v, want consumed ErrChatGPTProviderImportNotFound", err)
	}

	cancelled, err := service.PreviewSub2APIChatGPTImport(raw)
	if err != nil {
		t.Fatalf("second preview returned error: %v", err)
	}
	if err := service.CancelChatGPTProviderImport(cancelled.ImportID); err != nil {
		t.Fatalf("CancelChatGPTProviderImport returned error: %v", err)
	}
	if err := service.CancelChatGPTProviderImport(cancelled.ImportID); err != nil {
		t.Fatalf("idempotent cancel returned error: %v", err)
	}
	if _, err := service.ClaimChatGPTProviderImport(cancelled.ImportID); !errors.Is(err, ErrChatGPTProviderImportNotFound) {
		t.Fatalf("claim after cancel error = %v, want ErrChatGPTProviderImportNotFound", err)
	}
	if err := service.FinalizeChatGPTProviderImport("missing"); !errors.Is(err, ErrChatGPTProviderImportNotFound) {
		t.Fatalf("finalize missing error = %v, want ErrChatGPTProviderImportNotFound", err)
	}
	if err := service.FinalizeChatGPTProviderImport(" "); !errors.Is(err, ErrChatGPTProviderImportNotFound) {
		t.Fatalf("finalize blank error = %v, want ErrChatGPTProviderImportNotFound", err)
	}
	if err := service.CancelChatGPTProviderImport(" "); err != nil {
		t.Fatalf("cancel blank returned error: %v", err)
	}

	expiryService, expiryClock := newSub2APIImportTestService(
		t,
		now,
		"candidate-finalize-expired",
		"import-finalize-expired",
	)
	expiredFinalize, err := expiryService.PreviewSub2APIChatGPTImport(raw)
	if err != nil {
		t.Fatalf("expiry preview returned error: %v", err)
	}
	expiryClock.now = now.Add(chatGPTProviderImportSessionTTL)
	if err := expiryService.FinalizeChatGPTProviderImport(expiredFinalize.ImportID); !errors.Is(err, ErrChatGPTProviderImportExpired) {
		t.Fatalf("expired finalize error = %v, want ErrChatGPTProviderImportExpired", err)
	}
}

func TestChatGPTProviderImportLifecycle_ScheduledExpiryDestroysSecrets(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	clock := &mutableClock{now: now}
	scheduler := &manualScheduler{}
	service := newCallbackLifecycleTestService(Config{
		Clock:       clock,
		IDGenerator: &importTestIDGenerator{ids: []string{"candidate-scheduled", "import-scheduled"}},
	}, &recordingCallbackEndpoint{}, scheduler)
	account := sub2APIImportAccount(
		"Scheduled",
		chatgptAccessJWT(t, "acct-scheduled", "scheduled@example.com", "plus", now.Add(time.Hour)),
		"refresh-scheduled",
		chatgptAuthJWT(t, "acct-scheduled", "scheduled@example.com", "plus", now.Add(time.Hour)),
		"acct-scheduled",
		1,
		0,
	)

	preview, err := service.PreviewSub2APIChatGPTImport(marshalSub2APIImportDocument(t, []any{account}, nil))
	if err != nil {
		t.Fatalf("PreviewSub2APIChatGPTImport returned error: %v", err)
	}
	expiry := scheduler.latest(t)
	if expiry.delay != chatGPTProviderImportSessionTTL {
		t.Fatalf("scheduled delay = %s, want %s", expiry.delay, chatGPTProviderImportSessionTTL)
	}

	clock.Advance(chatGPTProviderImportSessionTTL)
	expiry.Run()
	service.mu.Lock()
	remainingDrafts := len(service.providerImports)
	service.mu.Unlock()
	if remainingDrafts != 0 {
		t.Fatalf("provider import drafts after scheduled expiry = %d, want 0", remainingDrafts)
	}
	if _, err := service.ClaimChatGPTProviderImport(preview.ImportID); !errors.Is(err, ErrChatGPTProviderImportNotFound) {
		t.Fatalf("claim after scheduled destruction error = %v, want ErrChatGPTProviderImportNotFound", err)
	}
}

func TestResolveChatGPTRefreshContext_RestrictsIssuerAndClient(t *testing.T) {
	validIDToken := makeTestJWT(t, map[string]any{
		"iss": defaultOAuthIssuer,
		"aud": defaultOAuthClientID,
	})
	badIssuerToken := makeTestJWT(t, map[string]any{
		"iss": "https://attacker.invalid",
		"aud": defaultOAuthClientID,
	})
	badAudienceToken := makeTestJWT(t, map[string]any{
		"iss": defaultOAuthIssuer,
		"aud": "attacker-client",
	})
	tests := []struct {
		name       string
		credential *model.ChatGPTProviderCredential
		wantIssuer string
		wantClient string
		wantErr    string
	}{
		{
			name: "explicit default context",
			credential: &model.ChatGPTProviderCredential{
				OAuthIssuer:   defaultOAuthIssuer,
				OAuthClientID: defaultOAuthClientID,
			},
			wantIssuer: defaultOAuthIssuer,
			wantClient: defaultOAuthClientID,
		},
		{
			name: "empty explicit client normalizes to fixed default",
			credential: &model.ChatGPTProviderCredential{
				OAuthIssuer: defaultOAuthIssuer,
			},
			wantIssuer: defaultOAuthIssuer,
			wantClient: defaultOAuthClientID,
		},
		{
			name: "arbitrary explicit issuer",
			credential: &model.ChatGPTProviderCredential{
				OAuthIssuer:   "https://attacker.invalid",
				OAuthClientID: defaultOAuthClientID,
			},
			wantErr: "unsupported oauth issuer",
		},
		{
			name: "arbitrary explicit client",
			credential: &model.ChatGPTProviderCredential{
				OAuthIssuer:   defaultOAuthIssuer,
				OAuthClientID: "attacker-client",
			},
			wantErr: "unsupported oauth client id",
		},
		{
			name:       "trusted token fallback",
			credential: &model.ChatGPTProviderCredential{IDToken: validIDToken},
			wantIssuer: defaultOAuthIssuer,
			wantClient: defaultOAuthClientID,
		},
		{
			name:       "arbitrary token issuer",
			credential: &model.ChatGPTProviderCredential{IDToken: badIssuerToken},
			wantErr:    "unsupported oauth issuer",
		},
		{
			name:       "arbitrary token audience",
			credential: &model.ChatGPTProviderCredential{IDToken: badAudienceToken},
			wantErr:    "unsupported oauth audience",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			issuer, clientID, err := resolveChatGPTRefreshContext(test.credential)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveChatGPTRefreshContext returned error: %v", err)
			}
			if issuer != test.wantIssuer || clientID != test.wantClient {
				t.Fatalf("context = (%q, %q), want (%q, %q)", issuer, clientID, test.wantIssuer, test.wantClient)
			}
		})
	}
}

func TestBuildRefreshedChatGPTCredential_PreservesAccountBinding(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	current := &model.ChatGPTProviderCredential{
		OAuthIssuer:   defaultOAuthIssuer,
		OAuthClientID: defaultOAuthClientID,
		AccountID:     "acct-current",
		Email:         "current@example.com",
		PlanType:      "plus",
	}

	t.Run("rejects mismatched refreshed id token", func(t *testing.T) {
		idToken := chatgptAuthJWT(t, "acct-other", "other@example.com", "plus", now.Add(time.Hour))
		_, err := buildRefreshedChatGPTCredential(
			current,
			"opaque-access-token",
			"refresh-token",
			idToken,
			defaultOAuthIssuer,
			defaultOAuthClientID,
			now,
		)
		if err == nil || !strings.Contains(err.Error(), "id_token identifies a different account") {
			t.Fatalf("error = %v, want refreshed id-token account mismatch", err)
		}
	})

	t.Run("rejects mismatched jwt access token", func(t *testing.T) {
		accessToken := chatgptAccessJWT(t, "acct-other", "other@example.com", "plus", now.Add(time.Hour))
		_, err := buildRefreshedChatGPTCredential(
			current,
			accessToken,
			"refresh-token",
			"",
			defaultOAuthIssuer,
			defaultOAuthClientID,
			now,
		)
		if err == nil || !strings.Contains(err.Error(), "access_token identifies a different account") {
			t.Fatalf("error = %v, want refreshed access-token account mismatch", err)
		}
	})

	t.Run("accepts matching purpose-specific jwt audiences", func(t *testing.T) {
		accessExpiry := now.Add(45 * time.Minute)
		accessToken := chatgptAccessJWT(t, "acct-current", "current@example.com", "plus", accessExpiry)
		idToken := chatgptAuthJWT(t, "acct-current", "current@example.com", "team", now.Add(time.Hour))
		credential, err := buildRefreshedChatGPTCredential(
			current,
			accessToken,
			"refresh-token",
			idToken,
			defaultOAuthIssuer,
			defaultOAuthClientID,
			now,
		)
		if err != nil {
			t.Fatalf("buildRefreshedChatGPTCredential returned error: %v", err)
		}
		if credential.AccountID != "acct-current" || credential.PlanType != "team" {
			t.Fatalf("credential identity = %#v, want same account with refreshed plan", credential)
		}
		if !credential.ExpiresAt.Equal(accessExpiry) {
			t.Fatalf("ExpiresAt = %s, want access-token expiry %s", credential.ExpiresAt, accessExpiry)
		}
	})
}

func TestChatGPTProviderImportValidationHelpers(t *testing.T) {
	t.Run("usage metadata errors remain non-blocking", func(t *testing.T) {
		now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
		snapshot, warnings := parseSub2APIUsageSnapshot(json.RawMessage(`{
			"codex_5h_used_percent":"bad",
			"codex_5h_window_minutes":-1,
			"codex_5h_reset_at":"not-a-time",
			"codex_usage_updated_at":{}
		}`), "plus", now)
		if snapshot != nil {
			t.Fatalf("snapshot = %#v, want invalid scalar window omitted", snapshot)
		}
		if len(warnings) != 4 {
			t.Fatalf("warnings = %#v, want one warning per invalid field", warnings)
		}
	})

	t.Run("future usage timestamp cannot suppress live refresh", func(t *testing.T) {
		now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
		snapshot, warnings := parseSub2APIUsageSnapshot(json.RawMessage(`{
			"codex_5h_used_percent":25,
			"codex_5h_window_minutes":300,
			"codex_usage_updated_at":"9999-12-31T23:59:59Z"
		}`), "plus", now)
		if snapshot == nil || snapshot.FiveHour == nil || snapshot.FetchedAt != nil {
			t.Fatalf("snapshot = %#v, want valid window with future fetched-at omitted", snapshot)
		}
		if len(warnings) != 1 || warnings[0].Code != ChatGPTProviderImportWarningUsageMetadataInvalid {
			t.Fatalf("warnings = %#v, want future timestamp warning", warnings)
		}
	})

	t.Run("apply rejects missing records and nil provider", func(t *testing.T) {
		candidate := ChatGPTProviderImportCandidate{
			CandidateID: "candidate",
			State:       ChatGPTProviderImportCandidateStateReady,
		}
		if err := ApplyChatGPTProviderImportCandidate(nil, candidate); !errors.Is(err, ErrChatGPTProviderImportInvalidCandidate) {
			t.Fatalf("nil provider error = %v, want ErrChatGPTProviderImportInvalidCandidate", err)
		}
		if err := ApplyChatGPTProviderImportCandidate(&model.Provider{}, candidate); !errors.Is(err, ErrChatGPTProviderImportInvalidCandidate) {
			t.Fatalf("missing records error = %v, want ErrChatGPTProviderImportInvalidCandidate", err)
		}
	})

	t.Run("summary includes caller-enriched existing state", func(t *testing.T) {
		summary := summarizeChatGPTProviderImportItems([]ChatGPTProviderImportPreviewItem{
			{State: ChatGPTProviderImportCandidateStateExisting},
		})
		if summary.Total != 1 || summary.Existing != 1 {
			t.Fatalf("summary = %#v, want one existing item", summary)
		}
	})

	t.Run("opaque id generation rejects empty source", func(t *testing.T) {
		service := NewService(Config{IDGenerator: &importTestIDGenerator{}})
		if _, err := service.generateOpaqueImportID(nil); err == nil {
			t.Fatal("generateOpaqueImportID error = nil, want exhausted-generator error")
		}
	})

	t.Run("binding account normalization handles nil", func(t *testing.T) {
		if got := normalizedBindingAccountID(nil); got != "" {
			t.Fatalf("normalizedBindingAccountID(nil) = %q, want empty", got)
		}
	})
}

func TestPreviewSub2APIChatGPTImport_HandlesSessionIDCollisionsAndShutdown(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	account := sub2APIImportAccount(
		"Collision",
		chatgptAccessJWT(t, "acct-collision", "collision@example.com", "plus", now.Add(time.Hour)),
		"refresh-collision",
		chatgptAuthJWT(t, "acct-collision", "collision@example.com", "plus", now.Add(time.Hour)),
		"acct-collision",
		1,
		0,
	)
	raw := marshalSub2APIImportDocument(t, []any{account}, nil)
	service, _ := newSub2APIImportTestService(
		t,
		now,
		"candidate-first",
		"shared-import",
		"candidate-second",
		"shared-import",
		"unique-import",
	)
	first, err := service.PreviewSub2APIChatGPTImport(raw)
	if err != nil {
		t.Fatalf("first preview returned error: %v", err)
	}
	second, err := service.PreviewSub2APIChatGPTImport(raw)
	if err != nil {
		t.Fatalf("second preview returned error: %v", err)
	}
	if first.ImportID != "shared-import" || second.ImportID != "unique-import" {
		t.Fatalf("import IDs = (%q, %q), want collision retry", first.ImportID, second.ImportID)
	}

	shutdownService, _ := newSub2APIImportTestService(t, now, "candidate-shutdown", "import-shutdown")
	if err := shutdownService.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}
	if _, err := shutdownService.PreviewSub2APIChatGPTImport(raw); !errors.Is(err, errProviderAuthServiceShutdown) {
		t.Fatalf("preview after shutdown error = %v, want errProviderAuthServiceShutdown", err)
	}
}

func TestSealChatGPTProviderImportPreview_ValidatesAndFreezesDisposition(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	account := sub2APIImportAccount(
		"Existing",
		chatgptAccessJWT(t, "acct-existing", "existing@example.com", "plus", now.Add(time.Hour)),
		"refresh-existing",
		chatgptAuthJWT(t, "acct-existing", "existing@example.com", "plus", now.Add(time.Hour)),
		"acct-existing",
		1,
		0,
	)
	service, _ := newSub2APIImportTestService(t, now, "candidate-existing", "import-existing")
	preview, err := service.PreviewSub2APIChatGPTImport(marshalSub2APIImportDocument(t, []any{account}, nil))
	if err != nil {
		t.Fatalf("PreviewSub2APIChatGPTImport returned error: %v", err)
	}

	invalidDispositions := [][]ChatGPTProviderImportCandidateDisposition{
		{},
		{{CandidateID: "unknown", State: ChatGPTProviderImportCandidateStateReady}},
		{{CandidateID: "candidate-existing", State: ChatGPTProviderImportCandidateStateDuplicate}},
		{{
			CandidateID:        "candidate-existing",
			State:              ChatGPTProviderImportCandidateStateReady,
			ExpectedProviderID: "not-allowed-for-create",
		}},
		{{CandidateID: "candidate-existing", State: ChatGPTProviderImportCandidateStateExisting}},
		{{
			CandidateID:               "candidate-existing",
			State:                     ChatGPTProviderImportCandidateStateExisting,
			ExpectedProviderID:        "bound-provider",
			ExpectedCredentialVersion: 9,
		}},
		{{
			CandidateID:                 "candidate-existing",
			State:                       ChatGPTProviderImportCandidateStateExisting,
			ExpectedProviderID:          "bound-provider",
			ExpectedCredentialVersion:   -1,
			ExpectedCredentialCreatedAt: now.Add(-time.Hour),
		}},
	}
	for _, dispositions := range invalidDispositions {
		if err := service.SealChatGPTProviderImportPreview(preview.ImportID, dispositions); err == nil {
			t.Fatalf("SealChatGPTProviderImportPreview(%#v) error = nil, want validation failure", dispositions)
		}
	}

	disposition := ChatGPTProviderImportCandidateDisposition{
		CandidateID:                 "candidate-existing",
		State:                       ChatGPTProviderImportCandidateStateExisting,
		ExpectedProviderID:          " bound-provider ",
		ExpectedCredentialVersion:   9,
		ExpectedCredentialCreatedAt: now.Add(-time.Hour),
	}
	if err := service.SealChatGPTProviderImportPreview(preview.ImportID, []ChatGPTProviderImportCandidateDisposition{disposition}); err != nil {
		t.Fatalf("SealChatGPTProviderImportPreview returned error: %v", err)
	}
	if err := service.SealChatGPTProviderImportPreview(preview.ImportID, []ChatGPTProviderImportCandidateDisposition{disposition}); !errors.Is(err, ErrChatGPTProviderImportPreviewSealed) {
		t.Fatalf("second seal error = %v, want ErrChatGPTProviderImportPreviewSealed", err)
	}
	candidates, err := service.ClaimChatGPTProviderImport(preview.ImportID)
	if err != nil {
		t.Fatalf("ClaimChatGPTProviderImport returned error: %v", err)
	}
	got := candidates[0].Disposition
	if got == nil || got.ExpectedProviderID != "bound-provider" || got.ExpectedCredentialVersion != 9 ||
		!got.ExpectedCredentialCreatedAt.Equal(now.Add(-time.Hour)) {
		t.Fatalf("Disposition = %#v, want normalized immutable expectation", got)
	}

	if err := service.SealChatGPTProviderImportPreview(" ", nil); !errors.Is(err, ErrChatGPTProviderImportNotFound) {
		t.Fatalf("blank import seal error = %v, want ErrChatGPTProviderImportNotFound", err)
	}
}

func TestParseSub2APIAccount_ValidationMatrix(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	validAccess := chatgptAccessJWT(t, "acct", "user@example.com", "plus", now.Add(time.Hour))
	validID := chatgptAuthJWT(t, "acct", "user@example.com", "plus", now.Add(time.Hour))
	validCredentials := map[string]any{
		"access_token":       validAccess,
		"refresh_token":      "refresh",
		"id_token":           validID,
		"chatgpt_account_id": "acct",
	}
	tests := []struct {
		name string
		raw  json.RawMessage
	}{
		{name: "malformed account", raw: json.RawMessage(`{`)},
		{name: "non-object account", raw: json.RawMessage(`"account"`)},
		{name: "blank name", raw: marshalRawJSON(t, map[string]any{
			"name": " ", "platform": "openai", "type": "oauth", "credentials": validCredentials,
		})},
		{name: "negative concurrency", raw: marshalRawJSON(t, map[string]any{
			"name": "name", "platform": "openai", "type": "oauth", "credentials": validCredentials, "concurrency": -1,
		})},
		{name: "non-integer concurrency", raw: marshalRawJSON(t, map[string]any{
			"name": "name", "platform": "openai", "type": "oauth", "credentials": validCredentials, "concurrency": "many",
		})},
		{name: "unsafe concurrency integer", raw: marshalRawJSON(t, map[string]any{
			"name": "name", "platform": "openai", "type": "oauth", "credentials": validCredentials, "concurrency": maximumJSONSafeInteger + 1,
		})},
		{name: "negative priority", raw: marshalRawJSON(t, map[string]any{
			"name": "name", "platform": "openai", "type": "oauth", "credentials": validCredentials, "priority": -1,
		})},
		{name: "non-integer priority", raw: marshalRawJSON(t, map[string]any{
			"name": "name", "platform": "openai", "type": "oauth", "credentials": validCredentials, "priority": 1.5,
		})},
		{name: "unsafe priority integer", raw: marshalRawJSON(t, map[string]any{
			"name": "name", "platform": "openai", "type": "oauth", "credentials": validCredentials, "priority": maximumJSONSafeInteger + 1,
		})},
		{name: "missing credentials", raw: marshalRawJSON(t, map[string]any{
			"name": "name", "platform": "openai", "type": "oauth",
		})},
		{name: "invalid credentials shape", raw: marshalRawJSON(t, map[string]any{
			"name": "name", "platform": "openai", "type": "oauth", "credentials": "bad",
		})},
		{name: "missing access token", raw: marshalRawJSON(t, map[string]any{
			"name": "name", "platform": "openai", "type": "oauth", "credentials": map[string]any{"refresh_token": "refresh"},
		})},
		{name: "missing refresh token", raw: marshalRawJSON(t, map[string]any{
			"name": "name", "platform": "openai", "type": "oauth", "credentials": map[string]any{"access_token": validAccess},
		})},
		{name: "invalid access token", raw: marshalRawJSON(t, map[string]any{
			"name": "name", "platform": "openai", "type": "oauth", "credentials": map[string]any{"access_token": "bad", "refresh_token": "refresh"},
		})},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			item, candidate := parseSub2APIAccount(test.raw, "candidate", index, now)
			if item.State != ChatGPTProviderImportCandidateStateInvalid || candidate.State != ChatGPTProviderImportCandidateStateInvalid {
				t.Fatalf("states = (%q, %q), want invalid", item.State, candidate.State)
			}
			if !hasChatGPTProviderImportWarning(item.Warnings, ChatGPTProviderImportWarningInvalidAccount) {
				t.Fatalf("warnings = %#v, want invalid-account warning", item.Warnings)
			}
		})
	}
}

func marshalRawJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	return raw
}
