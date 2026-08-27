package proxy

import (
	"context"
	"crypto/rand"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/http"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/websocket"
)

type proxyCodexFixture struct {
	runtime          *codexhttp.Runtime
	webSocketRuntime *codexws.Runtime
	providerCookies  *providercookie.Service
	externalScheme   codexhttp.ExternalSchemeResolver
}

const proxyCodexTestAuthorization = "Bearer proxy-test-client"

func newProxyCodexFixture(t *testing.T) proxyCodexFixture {
	t.Helper()
	hmac := proxyCodexTestHMAC{}
	digesterValue, err := codexidentity.NewDigester(hmac)
	if err != nil {
		t.Fatal(err)
	}
	limits := codexcontinuity.Limits{
		PendingTTL: 24 * time.Hour, CommittedTTL: 30 * 24 * time.Hour,
		TombstoneTTL: 7 * 24 * time.Hour, MaxBindings: 100,
	}
	policy, err := codexcontinuity.NewPolicy(map[codexcontinuity.Kind]codexcontinuity.Limits{
		codexcontinuity.KindThreadID: limits, codexcontinuity.KindSessionID: limits,
		codexcontinuity.KindConversationID: limits, codexcontinuity.KindWindowID: limits,
		codexcontinuity.KindTurnState: limits, codexcontinuity.KindTurnMetadata: limits,
		codexcontinuity.KindResponseReference: limits,
	})
	if err != nil {
		t.Fatal(err)
	}
	continuity, err := codexcontinuity.NewService(codexcontinuity.Config{
		Store: newProxyCodexTestContinuityStore(), Digester: &digesterValue, Policy: policy, Clock: internal.RealClock{},
	})
	if err != nil {
		t.Fatal(err)
	}
	cookies, err := providercookie.NewService(providercookie.ServiceConfig{
		Repository: newProxyCodexTestCookieRepository(), HandleDigester: hmac, Random: rand.Reader,
		Clock: internal.RealClock{}, HostCanonicalizer: providercookie.HostCanonicalizerFunc(codexidentity.CanonicalizeCookieHost),
		PublicSuffixList: codexidentity.PublicSuffixList{}, Policy: providercookie.DefaultPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	scheme := codexhttp.NewTrustedProxySchemeResolver(nil)
	httpRuntime, err := codexhttp.New(codexhttp.Config{
		ClientScopes: &digesterValue, Continuity: continuity, ProviderCookies: cookies, ExternalScheme: scheme,
	})
	if err != nil {
		t.Fatal(err)
	}
	webSocketRuntime, err := codexws.New(codexws.Config{
		ClientScopes: &digesterValue, Continuity: continuity, ProviderCookies: cookies, ExternalScheme: scheme,
	})
	if err != nil {
		t.Fatal(err)
	}
	return proxyCodexFixture{
		runtime: httpRuntime, webSocketRuntime: webSocketRuntime,
		providerCookies: cookies, externalScheme: scheme,
	}
}

// Proxy-level Codex tests must enter through the same complete capability graph as the application.
func newProxyCodexTestHandler(t *testing.T, config Config) *Handler {
	t.Helper()
	if config.Auth == nil {
		config.Auth = proxyCodexTestAuthenticator{}
	}
	return newProxyCodexTestHandlerPreservingAuth(t, config)
}

func newProxyCodexTestHandlerPreservingAuth(t *testing.T, config Config) *Handler {
	t.Helper()
	if config.CodexHTTP == nil || config.CodexWebSocket == nil {
		fixture := newProxyCodexFixture(t)
		if config.CodexHTTP == nil {
			config.CodexHTTP = fixture.runtime
		}
		if config.CodexWebSocket == nil {
			config.CodexWebSocket = fixture.webSocketRuntime
		}
	}
	return NewHandler(config)
}

type proxyCodexTestAuthenticator struct{}

var _ ProviderAuthenticator = proxyCodexTestAuthenticator{}

func (proxyCodexTestAuthenticator) ApplyProviderCredentials(
	_ context.Context,
	headers http.Header,
	candidate codexidentity.CandidateSnapshot,
	providerAuthMode string,
	globalAuthMode string,
	originalRequest *http.Request,
	finalURL *url.URL,
) (codexidentity.AppliedIdentity, error) {
	credential := candidate.Credential()
	SetAuthHeader(headers, credential.SecretData, providerAuthMode, globalAuthMode, originalRequest)
	subject, err := codexidentity.CredentialSubjectFromSession(credential.Subject)
	if err != nil {
		return codexidentity.AppliedIdentity{}, err
	}
	return codexidentity.AppliedIdentityFromRequest(candidate.Authority().Vendor(), finalURL, subject)
}

func (proxyCodexTestAuthenticator) RefreshCredentialSession(
	context.Context,
	credentialsession.Snapshot,
) (bool, error) {
	return false, nil
}

func authorizeProxyCodexTestRequest(request *http.Request) {
	request.Header.Set("Authorization", proxyCodexTestAuthorization)
}
