package providerauth

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestBuildCredentialSessionAuthViewUsesOnlySessionSnapshot(t *testing.T) {
	if got := BuildCredentialSessionAuthView(nil); got != nil {
		t.Fatalf("nil snapshot view = %#v", got)
	}
	static := credentialsession.Snapshot{
		Kind: credentialsession.KindAPIKey, SecretData: "key",
		AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive},
	}
	if got := BuildCredentialSessionAuthView(&static); got.Type != credentialsession.KindAPIKey ||
		got.Status != credentialsession.AuthStatusActive || got.Reason != "" {
		t.Fatalf("static view = %#v", got)
	}
	static.SecretData = " "
	if got := BuildCredentialSessionAuthView(&static); got.Status != credentialsession.AuthStatusNotConnected ||
		got.Reason != ProviderAuthReasonMissingAPIKey {
		t.Fatalf("missing static view = %#v", got)
	}

	invalid := credentialsession.Snapshot{
		Kind: credentialsession.KindChatGPT, SecretData: "{",
		AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive},
	}
	if got := BuildCredentialSessionAuthView(&invalid); got.Status != credentialsession.AuthStatusNotConnected ||
		got.Reason != ProviderAuthReasonCredentialInvalid || got.LastError == "" {
		t.Fatalf("invalid ChatGPT view = %#v", got)
	}

	now := time.Now().UTC()
	credential := &model.ChatGPTProviderCredential{
		AccessToken: "access", RefreshToken: "refresh", AccountID: "acct",
		Email: " user@example.com ", PlanType: "pro", ExpiresAt: now.Add(time.Hour), LastRefresh: now,
	}
	chatGPT, err := chatGPTCredentialSessionSnapshot(credential, "session")
	if err != nil {
		t.Fatal(err)
	}
	view := BuildCredentialSessionAuthView(&chatGPT)
	if view.Status != credentialsession.AuthStatusActive || view.AccountID != "acct" ||
		view.Email != "user@example.com" || view.ExpiresAt == nil || view.LastRefreshAt == nil {
		t.Fatalf("ChatGPT view = %#v", view)
	}
	if serviceView := NewService(Config{}).BuildCredentialSessionAuthView(chatGPT); serviceView.AccountID != "acct" {
		t.Fatalf("service view = %#v", serviceView)
	}
}

func TestCredentialSessionAuthViewReasonAndErrors(t *testing.T) {
	if got := credentialSessionAuthReason(credentialsession.KindChatGPT, credentialsession.AuthState{}); got != ProviderAuthReasonLoginRequired {
		t.Fatalf("fallback reason = %q", got)
	}
	if got := credentialSessionAuthReason(credentialsession.KindChatGPT, credentialsession.AuthState{
		Status: credentialsession.AuthStatusReauthRequired, StatusReason: ProviderAuthReasonInvalidGrant,
	}); got != ProviderAuthReasonInvalidGrant {
		t.Fatalf("explicit reason = %q", got)
	}
	if got := credentialSessionAuthReason(credentialsession.KindAPIKey, credentialsession.AuthState{}); got != "" {
		t.Fatalf("static fallback reason = %q", got)
	}
	for _, stateErr := range []*ProviderAuthStateError{
		{SessionID: "session", Status: ProviderAuthStatusReauthRequired, Reason: ProviderAuthReasonInvalidGrant},
		{SessionID: "session", Status: ProviderAuthStatusReauthRequired},
		{Status: ProviderAuthStatusNotConnected},
	} {
		if strings.TrimSpace(stateErr.Error()) == "" {
			t.Fatal("auth-state error is blank")
		}
	}
	if got := chatGPTCredentialAuthView(nil); got.Status != credentialsession.AuthStatusNotConnected {
		t.Fatalf("nil credential view = %#v", got)
	}
}

func TestCredentialRefreshCacheAndStaticAuthHelpers(t *testing.T) {
	now := time.Now().UTC()
	service := NewService(Config{Clock: fixedClock{now: now}})
	current := &model.ChatGPTProviderCredential{
		AccessToken: "old", RefreshToken: "refresh", AccountID: "acct",
		LastRefresh: now.Add(-time.Hour), ExpiresAt: now.Add(time.Minute),
	}
	newer := cloneChatGPTCredential(current)
	newer.AccessToken = "new"
	newer.LastRefresh = now
	newer.ExpiresAt = now.Add(time.Hour)
	if !shouldPreferChatGPTCredential(newer, current) ||
		shouldPreferChatGPTCredential(nil, current) ||
		shouldPreferChatGPTCredential(&model.ChatGPTProviderCredential{AccountID: "other"}, current) {
		t.Fatal("credential preference did not enforce freshness and subject")
	}
	service.storeRecentChatGPTRefresh("", newer)
	service.storeRecentChatGPTRefresh("session", newer)
	if got := service.reuseRecentChatGPTRefresh("session", current); got.AccessToken != "new" {
		t.Fatalf("reused credential = %#v", got)
	}
	service.InvalidateCredentialSessions([]string{"", " session ", "missing"})
	if got := service.reuseRecentChatGPTRefresh("session", current); got.AccessToken != "old" {
		t.Fatalf("credential remained cached after invalidation: %#v", got)
	}

	headers := make(http.Header)
	applyStaticAuthHeader(headers, "key", authModeXAPIKey, authModeBearer, nil)
	if headers.Get("X-Api-Key") != "key" {
		t.Fatalf("x-api-key = %q", headers.Get("X-Api-Key"))
	}
	headers = make(http.Header)
	applyStaticAuthHeader(headers, "key", authModeBearer, authModeXAPIKey, nil)
	if headers.Get("Authorization") != "Bearer key" {
		t.Fatalf("authorization = %q", headers.Get("Authorization"))
	}
	if detectAuthMode(nil) != authModeBearer ||
		detectAuthMode(&http.Request{Header: http.Header{"Authorization": {"Bearer client"}}}) != authModeBearer ||
		detectAuthMode(&http.Request{Header: http.Header{"X-Api-Key": {"client"}}}) != authModeXAPIKey {
		t.Fatal("auth-mode detection matrix failed")
	}
	headers = make(http.Header)
	applyStaticAuthHeader(headers, "key", "", authModeXAPIKey, nil)
	if headers.Get("X-Api-Key") != "key" {
		t.Fatalf("global x-api-key fallback = %#v", headers)
	}
	headers = make(http.Header)
	applyStaticAuthHeader(headers, "key", authModeAuto, authModeBearer, &http.Request{Header: http.Header{"X-Api-Key": {"client"}}})
	if headers.Get("X-Api-Key") != "key" {
		t.Fatalf("auto x-api-key detection = %#v", headers)
	}
}

func TestCredentialSessionPureContractBranches(t *testing.T) {
	if LoopbackCallbackPort() <= 0 || ChatGPTCodexBaseURL() == "" {
		t.Fatal("public OAuth endpoint metadata is incomplete")
	}
	if encoded, err := encodeChatGPTCredentialSecret(nil); err != nil || encoded != "" {
		t.Fatalf("encode nil credential = (%q, %v)", encoded, err)
	}
	encoded, err := encodeChatGPTCredentialSecret(&model.ChatGPTProviderCredential{
		AccessToken: "access", RefreshToken: "refresh", IDToken: "id",
		OAuthIssuer: " issuer ", OAuthClientID: " client ",
	})
	if err != nil {
		t.Fatal(err)
	}
	secret, err := decodeChatGPTCredentialSecret(encoded)
	if err != nil || secret.OAuthIssuer != "issuer" || secret.OAuthClientID != "client" {
		t.Fatalf("encoded secret = %#v, err=%v", secret, err)
	}

	audienceCases := []struct {
		name   string
		claims map[string]any
		want   bool
	}{
		{name: "string", claims: map[string]any{"aud": " client "}, want: true},
		{name: "wrong string", claims: map[string]any{"aud": "other"}},
		{name: "empty array", claims: map[string]any{"aud": []any{}}},
		{name: "matching array", claims: map[string]any{"aud": []any{" client ", "client"}}, want: true},
		{name: "blank then matching", claims: map[string]any{"aud": []any{" ", "client"}}, want: true},
		{name: "only blank", claims: map[string]any{"aud": []any{" "}}},
		{name: "non string", claims: map[string]any{"aud": []any{42}}},
		{name: "mixed", claims: map[string]any{"aud": []any{"client", "other"}}},
		{name: "missing", claims: map[string]any{}},
	}
	for _, test := range audienceCases {
		t.Run(test.name, func(t *testing.T) {
			if got := hasOnlyImportedChatGPTAudience(test.claims, "client"); got != test.want {
				t.Fatalf("hasOnlyImportedChatGPTAudience() = %t, want %t", got, test.want)
			}
		})
	}

	refreshFailureCases := []struct {
		err      error
		reason   string
		terminal bool
	}{
		{err: nil},
		{err: errors.New("refresh_token_reused"), reason: ProviderAuthReasonRefreshTokenReused, terminal: true},
		{err: errors.New("INVALID_GRANT"), reason: ProviderAuthReasonInvalidGrant, terminal: true},
		{err: errors.New("login_required"), reason: ProviderAuthReasonInteractionRequired, terminal: true},
		{err: errors.New("consent_required"), reason: ProviderAuthReasonInteractionRequired, terminal: true},
		{err: errors.New("please re-auth"), reason: ProviderAuthReasonInteractionRequired, terminal: true},
		{err: errors.New("temporary outage")},
	}
	for _, test := range refreshFailureCases {
		if reason, terminal := classifyChatGPTRefreshFailure(test.err); reason != test.reason || terminal != test.terminal {
			t.Fatalf("classifyChatGPTRefreshFailure(%v) = (%q, %t)", test.err, reason, terminal)
		}
	}
	for _, value := range []float64{math.NaN(), math.Inf(1), 1.5, float64(minimumJSONTime.Unix() - 1)} {
		if _, err := jsonRepresentableUnixFloat(value); err == nil {
			t.Fatalf("jsonRepresentableUnixFloat(%v) error = nil", value)
		}
	}
	if value, err := jsonRepresentableUnixFloat(0); err != nil || !value.Equal(time.Unix(0, 0)) {
		t.Fatalf("jsonRepresentableUnixFloat(0) = (%v, %v)", value, err)
	}
	identity := snapshotChatGPTCredentialIdentity(nil, " issuer ", " client ")
	if identity.OAuthIssuer != "issuer" || identity.OAuthClientID != "client" {
		t.Fatalf("nil credential identity = %#v", identity)
	}
	if firstNonEmpty("", "") != "" {
		t.Fatal("firstNonEmpty returned a value for empty inputs")
	}
}

func TestCredentialPreferenceAndRefreshBookkeepingBranches(t *testing.T) {
	now := time.Now().UTC()
	ready := func(account, access, refresh string, refreshedAt, expiresAt time.Time) *model.ChatGPTProviderCredential {
		return &model.ChatGPTProviderCredential{
			AccountID: account, AccessToken: access, RefreshToken: refresh,
			LastRefresh: refreshedAt, ExpiresAt: expiresAt,
		}
	}
	candidate := ready("acct", "candidate", "refresh", now, now.Add(2*time.Hour))
	if !shouldPreferChatGPTCredential(candidate, nil) {
		t.Fatal("ready candidate was not preferred over nil")
	}
	if !shouldPreferChatGPTCredential(candidate, ready("acct", "", "", now, now)) {
		t.Fatal("ready candidate was not preferred over incomplete current credential")
	}
	if shouldPreferChatGPTCredential(candidate, ready("other", "old", "refresh", now, now)) {
		t.Fatal("candidate from another account was preferred")
	}
	if !shouldPreferChatGPTCredential(candidate, ready("acct", "old", "other", now.Add(-time.Minute), now.Add(3*time.Hour))) {
		t.Fatal("more recently refreshed candidate was not preferred")
	}
	if !shouldPreferChatGPTCredential(candidate, ready("acct", "old", "other", now, now.Add(time.Hour))) {
		t.Fatal("equal refresh time with later expiry was not preferred")
	}
	if !shouldPreferChatGPTCredential(candidate, ready("acct", "old", "refresh", now.Add(time.Minute), now.Add(time.Hour))) {
		t.Fatal("same refresh token with later expiry was not preferred")
	}
	if shouldPreferChatGPTCredential(candidate, ready("acct", "old", "other", now.Add(time.Minute), now.Add(3*time.Hour))) {
		t.Fatal("older candidate with different refresh token was preferred")
	}

	service := NewService(Config{Clock: fixedClock{now: now}})
	service.refreshMu.Lock()
	service.recentChatGPTRefreshes["expired"] = recentChatGPTRefresh{
		credential: candidate, expiresAt: now,
	}
	service.pruneRecentChatGPTRefreshesLocked(now)
	_, retained := service.recentChatGPTRefreshes["expired"]
	service.refreshMu.Unlock()
	if retained {
		t.Fatal("expired refresh generation was retained")
	}

	usageAt := now.Add(time.Minute)
	if _, owner := service.beginProviderUsageObservation("session", &model.ProviderUsageSnapshot{FetchedAt: &usageAt}); !owner {
		t.Fatal("first usage observation did not become owner")
	}
	service.finishProviderUsageObservation("session")
	if _, exists := service.inFlightUsageObservations["session"]; exists {
		t.Fatal("finished usage observation remained in flight")
	}
}

func TestReloadChatGPTCredentialSessionRejectsInvalidLifecycleStates(t *testing.T) {
	now := time.Now().UTC()
	if _, _, err := NewService(Config{}).reloadChatGPTCredentialSession(context.Background(), "provider", "session"); err == nil {
		t.Fatal("reload without credential store succeeded")
	}

	staticSubject, err := credentialsession.KeyedDigestSubject("v1", make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	static := credentialsession.Snapshot{
		SessionID: "static", Kind: credentialsession.KindAPIKey,
		SecretData: "key", Version: 1, Subject: staticSubject,
		AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive},
	}
	store := &profileCredentialStore{session: sessionFromAppliedSnapshot(t, static)}
	service := NewService(Config{CredentialStore: store})
	if _, _, err := service.reloadChatGPTCredentialSession(context.Background(), "provider", "static"); err == nil {
		t.Fatal("static session was accepted as ChatGPT")
	}

	accountSubject, err := credentialsession.AccountSubject("acct")
	if err != nil {
		t.Fatal(err)
	}
	invalid := credentialsession.Snapshot{
		SessionID: "invalid", Kind: credentialsession.KindChatGPT,
		SecretData: "{", Version: 1, Subject: accountSubject,
		AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive, AccountID: "acct"},
	}
	store.session = sessionFromAppliedSnapshot(t, invalid)
	if _, _, err := service.reloadChatGPTCredentialSession(context.Background(), "provider", "invalid"); err == nil {
		t.Fatal("invalid ChatGPT secret was accepted")
	}

	valid := profileSnapshot(t, now)
	valid.AuthState.Status = credentialsession.AuthStatusReauthRequired
	valid.AuthState.StatusReason = ProviderAuthReasonInvalidGrant
	store.session = sessionFromAppliedSnapshot(t, valid)
	if _, _, err := service.reloadChatGPTCredentialSession(context.Background(), "provider", "session"); err == nil {
		t.Fatal("inactive ChatGPT session was accepted")
	}
	valid.AuthState.Status = credentialsession.AuthStatusActive
	store.session = sessionFromAppliedSnapshot(t, valid)
	reloaded, credential, err := service.reloadChatGPTCredentialSession(context.Background(), "provider", "session")
	if err != nil || reloaded.SessionID != "session" || credential.AccountID != "acct" {
		t.Fatalf("valid reload = (%#v, %#v, %v)", reloaded, credential, err)
	}
}

func TestEnsureFreshChatGPTCredentialRejectsInvalidSessionStates(t *testing.T) {
	service := NewService(Config{})
	if _, err := service.ensureFreshChatGPTSessionCredential(context.Background(), "provider", nil, false); err == nil {
		t.Fatal("nil session snapshot was accepted")
	}

	invalid := credentialsession.Snapshot{
		SessionID: "invalid", Kind: credentialsession.KindChatGPT, SecretData: "{",
		AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive},
	}
	if _, err := service.ensureFreshChatGPTSessionCredential(context.Background(), "provider", &invalid, false); err == nil {
		t.Fatal("malformed ChatGPT secret was accepted")
	}

	static := credentialsession.Snapshot{
		SessionID: "static", Kind: credentialsession.KindAPIKey, SecretData: "key",
		AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive},
	}
	if _, err := service.ensureFreshChatGPTSessionCredential(context.Background(), "provider", &static, false); err == nil {
		t.Fatal("static session was accepted as ChatGPT")
	}
	if applicable, err := service.RefreshCredentialSession(context.Background(), static); err != nil || applicable {
		t.Fatalf("RefreshCredentialSession(static) = (%t, %v)", applicable, err)
	}

	now := time.Now().UTC()
	inactive := profileSnapshot(t, now)
	inactive.AuthState.Status = credentialsession.AuthStatusReauthRequired
	inactive.AuthState.StatusReason = ProviderAuthReasonInvalidGrant
	if _, err := service.ensureFreshChatGPTSessionCredential(context.Background(), "provider", &inactive, false); err == nil {
		t.Fatal("inactive ChatGPT session was accepted")
	}

	incompleteSecret, err := model.EncodeChatGPTProviderSecret(&model.ChatGPTProviderSecret{AccessToken: "access"})
	if err != nil {
		t.Fatal(err)
	}
	incomplete := profileSnapshot(t, now)
	incomplete.SecretData = incompleteSecret
	if _, err := service.ensureFreshChatGPTSessionCredential(context.Background(), "provider", &incomplete, false); err == nil {
		t.Fatal("incomplete ChatGPT session was accepted")
	}
}
