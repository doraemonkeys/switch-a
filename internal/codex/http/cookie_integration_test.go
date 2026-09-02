package codexhttp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/keyring"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

type cookieTestClock struct{ now time.Time }

func (c cookieTestClock) Now() time.Time { return c.now }

type cookieTestDigester struct{}

func (cookieTestDigester) Sign(purpose codexkeyring.HMACPurpose, input []byte) (codexkeyring.Digest, error) {
	sum := sha256.Sum256(append([]byte(string(purpose)+":"), input...))
	return codexkeyring.Digest{Version: "test-v1", Sum: sum}, nil
}

func (d cookieTestDigester) LookupDigests(purpose codexkeyring.HMACPurpose, input []byte) ([]codexkeyring.Digest, error) {
	digest, err := d.Sign(purpose, input)
	return []codexkeyring.Digest{digest}, err
}

type cookieTestRepository struct {
	bindings map[codexkeyring.Digest]providercookie.BindingRecord
	cookies  map[providercookie.CookieScope]map[providercookie.CookieKey]providercookie.StoredCookie
	mergeErr error
}

func newCookieTestRepository() *cookieTestRepository {
	return &cookieTestRepository{
		bindings: make(map[codexkeyring.Digest]providercookie.BindingRecord),
		cookies:  make(map[providercookie.CookieScope]map[providercookie.CookieKey]providercookie.StoredCookie),
	}
}

func (r *cookieTestRepository) UseBinding(_ context.Context, lookup providercookie.BindingLookup) (providercookie.BindingUse, error) {
	for _, digest := range lookup.HandleDigests {
		record, exists := r.bindings[digest]
		if !exists {
			continue
		}
		for _, owner := range lookup.ClientScopes {
			if owner.Equal(record.ClientScope) {
				return providercookie.BindingUse{Disposition: providercookie.BindingValid, Record: record}, nil
			}
		}
		return providercookie.BindingUse{Disposition: providercookie.BindingOwnerMismatch, Record: record}, nil
	}
	return providercookie.BindingUse{Disposition: providercookie.BindingUnknown}, nil
}

func (r *cookieTestRepository) CreateBinding(_ context.Context, record providercookie.BindingRecord, _ providercookie.Policy) error {
	if _, exists := r.bindings[record.HandleDigest]; exists {
		return providercookie.ErrIdentifierClash
	}
	r.bindings[record.HandleDigest] = record
	return nil
}

func (r *cookieTestRepository) Load(_ context.Context, scope providercookie.CookieScope, _ time.Time) (providercookie.Snapshot, error) {
	values := r.cookies[scope]
	cookies := make([]providercookie.StoredCookie, 0, len(values))
	for _, cookie := range values {
		cookies = append(cookies, cookie)
	}
	return providercookie.NewSnapshot(scope, cookies)
}

func (*cookieTestRepository) Touch(context.Context, providercookie.CookieScope, []providercookie.CookieKey, time.Time) error {
	return nil
}

func (r *cookieTestRepository) Merge(_ context.Context, scope providercookie.CookieScope, mutations []providercookie.Mutation, _ time.Time, _ providercookie.Policy) (providercookie.MergeResult, error) {
	if r.mergeErr != nil {
		return providercookie.MergeResult{}, r.mergeErr
	}
	if r.cookies[scope] == nil {
		r.cookies[scope] = make(map[providercookie.CookieKey]providercookie.StoredCookie)
	}
	result := providercookie.MergeResult{}
	for _, mutation := range mutations {
		if cookie, exists := mutation.Cookie(); exists {
			r.cookies[scope][mutation.Key()] = cookie
			result.Upserted++
		} else {
			delete(r.cookies[scope], mutation.Key())
			result.Deleted++
		}
	}
	return result, nil
}

func (*cookieTestRepository) Cleanup(context.Context, providercookie.CleanupRequest) (providercookie.CleanupResult, error) {
	return providercookie.CleanupResult{}, nil
}

func TestCookieOverlayRetriesCommitAndClientScopeIsolation(t *testing.T) {
	repository := newCookieTestRepository()
	runtime := newCookieTestRuntime(t, repository)
	clientScope := testClientScope(t, "client-a")
	runtime.clientScopes = testScopeDigester{current: clientScope, candidates: []codexidentity.ClientScope{clientScope}}
	candidate, applied := testCandidate(t, "route-a", "provider.test", "subject-a")

	clientRequest := httptest.NewRequest(http.MethodPost, "http://gateway.test/codex/v1/responses", nil)
	clientRequest.Header.Set("Authorization", "Bearer client-a")
	operation, err := runtime.Begin(context.Background(), clientRequest, codexAPIType, "cookie-operation-one", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if operation.gatewaySetCookie == "" {
		t.Fatal("new Jar did not issue a gateway handle")
	}
	firstRequest := httptest.NewRequest(http.MethodPost, "https://provider.test/v1/responses", nil)
	attempt, err := operation.PrepareAttempt(context.Background(), firstRequest, candidate, applied)
	if err != nil {
		t.Fatal(err)
	}
	head := upstreamtransport.ResponseHead{
		SourceHeader: http.Header{"Set-Cookie": []string{"provider_session=one; Path=/; Secure"}},
		Header:       http.Header{"Set-Cookie": []string{"provider_session=one; Path=/; Secure"}},
	}
	if err := attempt.ObserveResponse(&head); err != nil {
		t.Fatal(err)
	}
	if head.Header.Get("Set-Cookie") != "" || head.SourceHeader.Get("Set-Cookie") == "" {
		t.Fatalf("downstream/source Set-Cookie = %q/%q", head.Header.Get("Set-Cookie"), head.SourceHeader.Get("Set-Cookie"))
	}

	retryRequest := httptest.NewRequest(http.MethodPost, "https://provider.test/v1/responses", nil)
	retryAttempt, err := operation.PrepareAttempt(context.Background(), retryRequest, candidate, applied)
	if err != nil {
		t.Fatal(err)
	}
	if got := retryRequest.Header.Get("Cookie"); got != "provider_session=one" {
		t.Fatalf("same-scope retry Cookie = %q", got)
	}
	visibleHeaders := make(http.Header)
	visibility, err := retryAttempt.PrepareVisible(context.Background(), visibleHeaders)
	if err != nil {
		t.Fatal(err)
	}
	if err := visibility.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	setCookie := visibleHeaders.Get("Set-Cookie")
	if !strings.Contains(setCookie, providercookie.GatewayHandleName+"=") || strings.Contains(setCookie, "provider_session") {
		t.Fatalf("client Set-Cookie = %q", setCookie)
	}
	handle := gatewayCookieValue(t, setCookie)
	operation.Discard()

	secondClientRequest := httptest.NewRequest(http.MethodPost, "http://gateway.test/codex/v1/responses", nil)
	secondClientRequest.Header.Set("Authorization", "Bearer client-a")
	secondOperation, err := runtime.Begin(context.Background(), secondClientRequest, codexAPIType, "cookie-operation-two", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if secondOperation.gatewaySetCookie == "" {
		t.Fatal("missing gateway handle did not issue a replacement alias")
	}
	if replacement := gatewayCookieValue(t, secondOperation.gatewaySetCookie); replacement == handle {
		t.Fatal("missing gateway handle reused its previous alias")
	}
	persistedRequest := httptest.NewRequest(http.MethodPost, "https://provider.test/v1/responses", nil)
	if _, err := secondOperation.PrepareAttempt(context.Background(), persistedRequest, candidate, applied); err != nil {
		t.Fatal(err)
	}
	if got := persistedRequest.Header.Get("Cookie"); got != "" {
		t.Fatalf("missing handle inherited provider Cookie %q", got)
	}
	secondOperation.Discard()

	explicitClientRequest := httptest.NewRequest(http.MethodPost, "http://gateway.test/codex/v1/responses", nil)
	explicitClientRequest.Header.Set("Authorization", "Bearer client-a")
	explicitClientRequest.AddCookie(&http.Cookie{Name: providercookie.GatewayHandleName, Value: handle})
	explicitOperation, err := runtime.Begin(context.Background(), explicitClientRequest, codexAPIType, "cookie-operation-explicit", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	explicitUpstream := httptest.NewRequest(http.MethodPost, "https://provider.test/v1/responses", nil)
	if _, err := explicitOperation.PrepareAttempt(context.Background(), explicitUpstream, candidate, applied); err != nil {
		t.Fatal(err)
	}
	if got := explicitUpstream.Header.Get("Cookie"); got != "provider_session=one" {
		t.Fatalf("returned handle Cookie = %q", got)
	}
	explicitOperation.Discard()

	otherScope := testClientScope(t, "client-b")
	runtime.clientScopes = testScopeDigester{current: otherScope, candidates: []codexidentity.ClientScope{otherScope}}
	isolatedRequest := httptest.NewRequest(http.MethodPost, "http://gateway.test/codex/v1/responses", nil)
	isolatedRequest.Header.Set("Authorization", "Bearer client-b")
	isolatedRequest.AddCookie(&http.Cookie{Name: providercookie.GatewayHandleName, Value: handle})
	isolatedOperation, err := runtime.Begin(context.Background(), isolatedRequest, codexAPIType, "cookie-operation-three", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	isolatedUpstream := httptest.NewRequest(http.MethodPost, "https://provider.test/v1/responses", nil)
	if _, err := isolatedOperation.PrepareAttempt(context.Background(), isolatedUpstream, candidate, applied); err != nil {
		t.Fatal(err)
	}
	if got := isolatedUpstream.Header.Get("Cookie"); got != "" {
		t.Fatalf("other ClientScope received Cookie %q", got)
	}
	isolatedOperation.Discard()
}

func TestCookieAuthoritySwitchDiscardsOverlayAndMergeFailureFailsGate(t *testing.T) {
	repository := newCookieTestRepository()
	runtime := newCookieTestRuntime(t, repository)
	clientScope := testClientScope(t, "client")
	runtime.clientScopes = testScopeDigester{current: clientScope, candidates: []codexidentity.ClientScope{clientScope}}
	firstCandidate, firstApplied := testCandidate(t, "route-a", "provider-a.test", "subject-a")
	secondCandidate, secondApplied := testCandidate(t, "route-b", "provider-b.test", "subject-b")
	clientRequest := httptest.NewRequest(http.MethodPost, "http://gateway.test/codex/v1/responses", nil)
	clientRequest.Header.Set("Authorization", "Bearer client")
	operation, err := runtime.Begin(context.Background(), clientRequest, codexAPIType, "cookie-switch", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	firstRequest := httptest.NewRequest(http.MethodPost, "https://provider-a.test/v1/responses", nil)
	firstAttempt, err := operation.PrepareAttempt(context.Background(), firstRequest, firstCandidate, firstApplied)
	if err != nil {
		t.Fatal(err)
	}
	head := upstreamtransport.ResponseHead{SourceHeader: http.Header{"Set-Cookie": []string{"a=one; Path=/; Secure"}}, Header: make(http.Header)}
	if err := firstAttempt.ObserveResponse(&head); err != nil {
		t.Fatal(err)
	}
	secondRequest := httptest.NewRequest(http.MethodPost, "https://provider-b.test/v1/responses", nil)
	secondAttempt, err := operation.PrepareAttempt(context.Background(), secondRequest, secondCandidate, secondApplied)
	if err != nil {
		t.Fatal(err)
	}
	if got := secondRequest.Header.Get("Cookie"); got != "" {
		t.Fatalf("authority switch leaked Cookie %q", got)
	}
	repository.mergeErr = errors.New("database unavailable")
	if _, err := secondAttempt.PrepareVisible(context.Background(), make(http.Header)); !IsKind(err, ErrorDependencyUnavailable) {
		t.Fatalf("merge failure = %v", err)
	}
	operation.Discard()
}

func newCookieTestRuntime(t *testing.T, repository providercookie.Repository) *Runtime {
	t.Helper()
	return newAlwaysOnTestRuntime(t, Config{ProviderCookies: newCookieTestService(t, repository)})
}

func newCookieTestService(t *testing.T, repository providercookie.Repository) *providercookie.Service {
	t.Helper()
	random := make([]byte, 4096)
	for index := range random {
		random[index] = byte(index%251 + 1)
	}
	service, err := providercookie.NewService(providercookie.ServiceConfig{
		Repository:     repository,
		HandleDigester: cookieTestDigester{},
		Random:         bytes.NewReader(random),
		Clock:          cookieTestClock{now: time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)},
		HostCanonicalizer: providercookie.HostCanonicalizerFunc(func(host string) (string, error) {
			return strings.ToLower(strings.TrimSuffix(host, ".")), nil
		}),
		PublicSuffixList: providercookie.PublicSuffixFunc(func(domain string) string {
			parts := strings.Split(domain, ".")
			return parts[len(parts)-1]
		}),
		Policy: providercookie.DefaultPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func gatewayCookieValue(t *testing.T, setCookie string) string {
	t.Helper()
	response := &http.Response{Header: http.Header{"Set-Cookie": []string{setCookie}}}
	cookies := response.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("parsed gateway cookies = %#v", cookies)
	}
	return cookies[0].Value
}
