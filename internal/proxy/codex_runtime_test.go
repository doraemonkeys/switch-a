package proxy

import (
	"context"
	"crypto/rand"
	"net/http"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/clientidentity"
	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/http"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/websocket"
	"github.com/doraemonkeys/switch-a/internal/providerauth"
)

type proxyCodexFixture struct {
	runtime          *codexhttp.Runtime
	webSocketRuntime *codexws.Runtime
	providerCookies  *providercookie.Service
	externalScheme   codexhttp.ExternalSchemeResolver
}

type proxyClientIdentityResolver struct {
	digester interface {
		ClientScope([]byte) (codexidentity.ClientScope, error)
		ClientScopeCandidates([]byte) ([]codexidentity.ClientScope, error)
	}
}

func (r proxyClientIdentityResolver) Resolve(_ context.Context, key []byte) (clientidentity.Resolution, error) {
	scope, err := r.digester.ClientScope(key)
	if err != nil {
		return clientidentity.Resolution{}, err
	}
	aliases, err := r.digester.ClientScopeCandidates(key)
	return clientidentity.Resolution{ID: scope.KeyVersion() + string(key), Primary: scope, Aliases: aliases}, err
}

const proxyCodexTestAuthorization = "Bearer proxy-test-client"

func proxyCodexTestClientScope(t *testing.T) codexidentity.ClientScope {
	t.Helper()
	digester, err := codexidentity.NewDigester(proxyCodexTestHMAC{})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := digester.ClientScope([]byte("proxy-test-client"))
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func newProxyCodexFixture(t *testing.T) proxyCodexFixture {
	t.Helper()
	hmac := proxyCodexTestHMAC{}
	digesterValue, err := codexidentity.NewDigester(hmac)
	if err != nil {
		t.Fatal(err)
	}
	limits := codexcontinuity.Limits{
		PendingTTL: 24 * time.Hour, CommittedIdleTTL: 30 * 24 * time.Hour,
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
		ClientIdentities: proxyClientIdentityResolver{&digesterValue}, Continuity: continuity, ProviderCookies: cookies, ExternalScheme: scheme,
	})
	if err != nil {
		t.Fatal(err)
	}
	webSocketRuntime, err := codexws.New(codexws.Config{
		ClientIdentities: proxyClientIdentityResolver{&digesterValue}, Continuity: continuity, ProviderCookies: cookies, ExternalScheme: scheme,
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
		config.Auth = newProxyTestAuthenticator()
	}
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

func newProxyTestAuthenticator() ProviderAuthenticator {
	return providerauth.NewService(providerauth.Config{})
}

func authorizeProxyCodexTestRequest(request *http.Request) {
	request.Header.Set("Authorization", proxyCodexTestAuthorization)
}
