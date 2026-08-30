package codexintegration_test

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	continuitysqlite "github.com/doraemonkeys/switch-a/internal/codex/continuity/sqlite"
	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	cookiesqlite "github.com/doraemonkeys/switch-a/internal/codex/cookie/sqlite"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/http"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/codex/keyring"
	"github.com/doraemonkeys/switch-a/internal/codex/websocket"
	glebarezsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const testSSEEventLimit = 256 * 1024

const (
	testAPIType         = "codex"
	testGatewayURL      = "https://gateway.example.test/codex/v1/responses"
	testContinuityLimit = int64(100)
)

type fixtureOptions struct {
	continuityMax      int64
	cookiePolicy       providercookie.Policy
	hmacCurrent        string
	aeadCurrent        string
	databasePath       string
	existingDB         *gorm.DB
	existingClock      *fixtureClock
	existingTrace      *fixtureTrace
	existingGeneration *fixtureGenerationIDs
}

type runtimeFixture struct {
	databasePath string
	db           *gorm.DB
	clock        *fixtureClock
	trace        *fixtureTrace
	generations  *fixtureGenerationIDs
	keyring      *codexkeyring.Keyring
	digester     *codexidentity.Digester
	continuity   *codexcontinuity.Service
	cookies      *providercookie.Service
	http         *codexhttp.Runtime
	ws           *codexws.Runtime
}

func newRuntimeFixture(t *testing.T, options fixtureOptions) *runtimeFixture {
	t.Helper()
	if options.continuityMax == 0 {
		options.continuityMax = testContinuityLimit
	}
	if options.cookiePolicy == (providercookie.Policy{}) {
		options.cookiePolicy = providercookie.DefaultPolicy()
	}
	if options.hmacCurrent == "" {
		options.hmacCurrent = "h1"
	}
	if options.aeadCurrent == "" {
		options.aeadCurrent = "a1"
	}
	if options.databasePath == "" {
		options.databasePath = filepath.Join(t.TempDir(), "cross-protocol.db")
	}

	db := options.existingDB
	if db == nil {
		var err error
		db, err = gorm.Open(glebarezsqlite.Open(options.databasePath), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		if err != nil {
			t.Fatal(err)
		}
		sqlDB, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		sqlDB.SetMaxOpenConns(1)
		if err := db.Exec("PRAGMA busy_timeout=5000").Error; err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = sqlDB.Close() })
		if err := continuitysqlite.Migrate(context.Background(), db); err != nil {
			t.Fatal(err)
		}
		if err := cookiesqlite.Migrate(context.Background(), db); err != nil {
			t.Fatal(err)
		}
	}

	clock := options.existingClock
	if clock == nil {
		clock = &fixtureClock{now: time.Date(2026, 8, 27, 5, 0, 0, 0, time.UTC)}
	}
	trace := options.existingTrace
	if trace == nil {
		trace = &fixtureTrace{}
	}
	generations := options.existingGeneration
	if generations == nil {
		generations = &fixtureGenerationIDs{}
	}
	keyring := parseFixtureKeyring(t, options.hmacCurrent, options.aeadCurrent)
	digesterValue, err := codexidentity.NewDigester(keyring)
	if err != nil {
		t.Fatal(err)
	}
	digester := &digesterValue

	continuityRepository, err := continuitysqlite.Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	continuity, err := codexcontinuity.NewService(codexcontinuity.Config{
		Store: continuityRepository, Digester: digester,
		Policy: fixtureContinuityPolicy(t, options.continuityMax), Clock: clock,
		Observer: trace, GenerationIDs: generations,
	})
	if err != nil {
		t.Fatal(err)
	}
	cookieRepository, err := cookiesqlite.Open(context.Background(), cookiesqlite.Config{
		DB: db, Cipher: keyring, BusyTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	cookies, err := providercookie.NewService(providercookie.ServiceConfig{
		Repository: cookieRepository, HandleDigester: keyring, Random: cryptorand.Reader,
		Clock:             clock,
		HostCanonicalizer: providercookie.HostCanonicalizerFunc(codexidentity.CanonicalizeCookieHost),
		PublicSuffixList:  codexidentity.PublicSuffixList{}, Policy: options.cookiePolicy, Trace: trace,
	})
	if err != nil {
		t.Fatal(err)
	}

	scheme := fixtureSchemeResolver("https")
	httpRuntime, err := codexhttp.New(codexhttp.Config{
		ClientScopes: digester, Continuity: continuity,
		ProviderCookies: cookies, ExternalScheme: scheme,
	})
	if err != nil {
		t.Fatal(err)
	}
	webSocketRuntime, err := codexws.New(codexws.Config{
		ClientScopes: digester, Continuity: continuity,
		ProviderCookies: cookies, ExternalScheme: scheme,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &runtimeFixture{
		databasePath: options.databasePath, db: db, clock: clock,
		trace: trace, generations: generations, keyring: keyring, digester: digester,
		continuity: continuity, cookies: cookies,
		http: httpRuntime, ws: webSocketRuntime,
	}
}

func (f *runtimeFixture) restart(t *testing.T, hmacCurrent, aeadCurrent string) *runtimeFixture {
	t.Helper()
	return newRuntimeFixture(t, fixtureOptions{
		continuityMax: testContinuityLimit, cookiePolicy: providercookie.DefaultPolicy(),
		hmacCurrent: hmacCurrent, aeadCurrent: aeadCurrent,
		databasePath: f.databasePath, existingDB: f.db, existingClock: f.clock,
		existingTrace: f.trace, existingGeneration: f.generations,
	})
}

func fixtureContinuityPolicy(t *testing.T, max int64) codexcontinuity.Policy {
	t.Helper()
	limits := codexcontinuity.Limits{
		PendingTTL: time.Hour, CommittedTTL: 30 * 24 * time.Hour,
		TombstoneTTL: 24 * time.Hour, MaxBindings: max,
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
	return policy
}

func parseFixtureKeyring(t *testing.T, hmacCurrent, aeadCurrent string) *codexkeyring.Keyring {
	t.Helper()
	material := func(value byte) string {
		return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{value}, 32))
	}
	document := map[string]any{
		"schema_version": 1,
		"hmac": map[string]any{
			"current": hmacCurrent,
			"keys":    map[string]string{"h1": material(1), "h2": material(2)},
		},
		"aead": map[string]any{
			"current": aeadCurrent,
			"keys":    map[string]string{"a1": material(11), "a2": material(12)},
		},
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := codexkeyring.Parse(encoded, cryptorand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return keyring
}

type candidateSpec struct {
	routeTarget string
	vendor      string
	subject     string
	apiType     string
	requestURL  string
}

func fixtureCandidate(t *testing.T, spec candidateSpec) (codexidentity.CandidateSnapshot, codexidentity.AppliedIdentity, *url.URL) {
	t.Helper()
	if spec.routeTarget == "" {
		spec.routeTarget = "route-a"
	}
	if spec.vendor == "" {
		spec.vendor = "openai"
	}
	if spec.subject == "" {
		spec.subject = "subject-a"
	}
	if spec.apiType == "" {
		spec.apiType = testAPIType
	}
	if spec.requestURL == "" {
		spec.requestURL = "https://api.example.test/v1/responses"
	}
	finalURL := mustParseURL(t, spec.requestURL)
	sum := sha256.Sum256([]byte("fixture-subject:" + spec.subject))
	subject, err := credentialsession.KeyedDigestSubject("h1", sum[:])
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := codexidentity.NewAuthorityResolver().Resolve(credentialsession.RouteSnapshot{
		RouteTargetID: spec.routeTarget, APIType: spec.apiType, VendorScope: spec.vendor,
		Credential: credentialsession.Snapshot{
			SessionID: "session-" + spec.routeTarget,
			Kind:      credentialsession.KindAPIKey, SecretData: "fixture-provider-secret",
			Version: 1, Subject: subject,
		},
	}, spec.apiType, finalURL)
	if err != nil {
		t.Fatal(err)
	}
	authority := candidate.Authority()
	applied, err := codexidentity.NewAppliedIdentity(authority.Vendor(), authority.Origin(), authority.Subject())
	if err != nil {
		t.Fatal(err)
	}
	return candidate, applied, finalURL
}

func fixtureRequest(method, secret string, headers http.Header) *http.Request {
	request, _ := http.NewRequest(method, testGatewayURL, nil)
	if secret != "" {
		request.Header.Set("Authorization", "Bearer "+secret)
	}
	for name, values := range headers {
		request.Header[name] = append([]string(nil), values...)
	}
	return request
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func websocketURL(t *testing.T, source *url.URL) *url.URL {
	t.Helper()
	copyURL := *source
	switch copyURL.Scheme {
	case "https":
		copyURL.Scheme = "wss"
	case "http":
		copyURL.Scheme = "ws"
	default:
		t.Fatalf("unsupported HTTP scheme %q", copyURL.Scheme)
	}
	return &copyURL
}

func gatewayHandle(t *testing.T, setCookie string) string {
	t.Helper()
	response := &http.Response{Header: http.Header{"Set-Cookie": {setCookie}}}
	for _, cookie := range response.Cookies() {
		if cookie.Name == providercookie.GatewayHandleName {
			return cookie.Value
		}
	}
	t.Fatalf("gateway handle missing from %q", setCookie)
	return ""
}

type fixtureClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fixtureClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fixtureClock) Advance(delta time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(delta)
	c.mu.Unlock()
}

type fixtureGenerationIDs struct {
	mu   sync.Mutex
	next int
}

func (s *fixtureGenerationIDs) NewGenerationID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	return "generation-" + strconv.Itoa(s.next)
}

type fixtureTrace struct {
	mu         sync.Mutex
	continuity []codexcontinuity.Event
	cookies    []providercookie.TraceEvent
}

func (r *fixtureTrace) ObserveContinuity(event codexcontinuity.Event) {
	r.mu.Lock()
	r.continuity = append(r.continuity, event)
	r.mu.Unlock()
}

func (r *fixtureTrace) RecordProviderCookieTrace(event providercookie.TraceEvent) {
	r.mu.Lock()
	r.cookies = append(r.cookies, event)
	r.mu.Unlock()
}

func (r *fixtureTrace) Snapshot() ([]codexcontinuity.Event, []providercookie.TraceEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]codexcontinuity.Event(nil), r.continuity...), append([]providercookie.TraceEvent(nil), r.cookies...)
}

type fixtureSchemeResolver string

func (s fixtureSchemeResolver) ResolveExternalScheme(*http.Request) (providercookie.ResolvedExternalScheme, error) {
	return providercookie.NewResolvedExternalScheme(string(s))
}

func requireHTTPError(t *testing.T, err error, kind codexhttp.ErrorKind) {
	t.Helper()
	if !codexhttp.IsKind(err, kind) {
		t.Fatalf("HTTP error = %v, want kind %q", err, kind)
	}
}

func requireWSFailure(t *testing.T, err error, class codexws.FailureClass) {
	t.Helper()
	if codexws.Classify(err) != class {
		t.Fatalf("WebSocket error = %v, want class %q", err, class)
	}
}

func operationID(protocol string, sequence int) string {
	return fmt.Sprintf("wave4-%s-%02d", protocol, sequence)
}
