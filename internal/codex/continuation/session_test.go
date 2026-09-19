package continuation

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func scopeFor(t *testing.T, account string) codexidentity.ProtocolScope {
	t.Helper()
	origin, err := codexidentity.ParseOrigin("https://example.test")
	if err != nil {
		t.Fatal(err)
	}
	subject, err := codexidentity.NewAccountCredentialSubject(account)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := codexidentity.NewUpstreamAuthority("vendor", origin, subject)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := codexidentity.NewProtocolScope(authority, "codex")
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func TestPolicyDirectionsAndIdentity(t *testing.T) {
	for _, same := range []bool{false, true} {
		for _, outbound := range []Boundary{None, SameIdentity, Any} {
			for _, inbound := range []Boundary{None, SameIdentity, Any} {
				t.Run(string(outbound)+"/"+string(inbound)+"/"+map[bool]string{true: "same", false: "different"}[same], func(t *testing.T) {
					a := Route{ProviderID: "a", ProtocolScope: "account-a", Policy: Policy{Outbound: outbound}}
					b := Route{ProviderID: "b", ProtocolScope: "account-b", Policy: Policy{Inbound: inbound}}
					if same {
						b.ProtocolScope = a.ProtocolScope
					}
					want := outbound != None && inbound != None && (same || outbound == Any && inbound == Any)
					if decision := Evaluate(a, b); decision.Allowed != want {
						t.Fatalf("decision = %+v, want allowed %v", decision, want)
					}
				})
			}
		}
	}
	a := Route{ProviderID: "a", ProtocolScope: "account-a", Policy: Policy{Outbound: None, Inbound: None}}
	if !Evaluate(a, a).Allowed {
		t.Fatal("current route was blocked")
	}
	b := a
	b.ProtocolScope = "different-account"
	if Evaluate(a, b).Allowed {
		t.Fatal("credential rebinding bypassed boundary")
	}
	if (Policy{}).Effective() != (Policy{Outbound: Any, Inbound: None}) {
		t.Fatal("incorrect defaults")
	}
	for _, p := range []Policy{{}, {Outbound: None, Inbound: SameIdentity}} {
		if err := p.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if (Policy{Inbound: "invalid"}).Validate() == nil || (Policy{Outbound: "invalid"}).Validate() == nil {
		t.Fatal("invalid policy accepted")
	}
}

func TestServingRouteSurvivesRestartAndKeepsOriginalProvenance(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "routes.sqlite")
	open := func() *gorm.DB {
		db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.AutoMigrate(&Binding{}); err != nil {
			t.Fatal(err)
		}
		return db
	}
	db := open()
	policies := map[string]Policy{"a": {Outbound: Any, Inbound: None}, "b": {Outbound: None, Inbound: Any}}
	var traces []Trace
	service := &Service{Store: NewRepository(db), Policy: func(_ context.Context, id string) (Policy, bool, error) { p, ok := policies[id]; return p, ok, nil }, Observe: func(trace Trace) { traces = append(traces, trace) }}
	a, b := scopeFor(t, "a"), scopeFor(t, "b")
	evidence := codexcontinuity.Evidence{Kind: codexcontinuity.KindThreadID, DigestInput: []byte("thread-one")}
	origin := codexcontinuity.Resolution{Status: codexcontinuity.ResolutionOwned, Owner: &codexcontinuity.Owner{RouteTargetHint: "a", ProtocolScope: a}}
	session := service.Begin("client-one", "operation-one")
	if err := session.Observe(ctx, evidence, origin); err != nil {
		t.Fatal(err)
	}
	if session.PreferredProviderID() != "a" {
		t.Fatal("origin not preferred")
	}
	if !session.Evaluate("a", a, policies["a"]).Allowed {
		t.Fatal("original blocked")
	}
	if !session.Evaluate("b", b, policies["b"]).Allowed {
		t.Fatal("authorized migration blocked")
	}
	if err := session.CommitVisible(ctx, "b", b, policies["b"]); err != nil {
		t.Fatal(err)
	}
	if err := session.CommitVisible(ctx, "b", b, policies["b"]); err != nil {
		t.Fatal(err)
	}
	if origin.Owner.RouteTargetHint != "a" || !origin.Owner.ProtocolScope.Equal(a) {
		t.Fatal("provenance was rewritten")
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	db = open()
	defer func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() }()
	service.Store = NewRepository(db)
	policies["b"] = Policy{Outbound: None, Inbound: None}
	resumed := service.Begin("client-one", "operation-two")
	if err := resumed.Observe(ctx, evidence, origin); err != nil {
		t.Fatal(err)
	}
	if resumed.PreferredProviderID() != "b" {
		t.Fatal("restart reverted serving provider")
	}
	if !resumed.Evaluate("b", b, policies["b"]).Allowed {
		t.Fatal("current conversation mistaken for external")
	}
	if resumed.Evaluate("a", a, Policy{Inbound: Any}).Allowed {
		t.Fatal("B outbound policy ignored")
	}
	otherClient := service.Begin("client-two", "other-client")
	if err := otherClient.Observe(ctx, evidence, codexcontinuity.Resolution{Status: codexcontinuity.ResolutionUnknown}); err != nil {
		t.Fatal(err)
	}
	if otherClient.PreferredProviderID() != "" || !otherClient.Evaluate("b", b, policies["b"]).Allowed {
		t.Fatal("another client's thread inherited a binding")
	}
	if len(traces) == 0 {
		t.Fatal("missing diagnostics")
	}
}

func TestNewConversationAndExternalStateAdmission(t *testing.T) {
	ctx := context.Background()
	scope := scopeFor(t, "a")
	service := &Service{}
	fresh := service.Begin("client", "fresh")
	if err := fresh.Observe(ctx, codexcontinuity.Evidence{Kind: codexcontinuity.KindThreadID, DigestInput: []byte("new")}, codexcontinuity.Resolution{Status: codexcontinuity.ResolutionUnknown}); err != nil {
		t.Fatal(err)
	}
	if !fresh.Evaluate("a", scope, Policy{}).Allowed || !fresh.Evaluate("b", scopeFor(t, "b"), Policy{}).Allowed {
		t.Fatal("new conversation replacement blocked")
	}
	external := service.Begin("client", "external")
	if err := external.Observe(ctx, codexcontinuity.Evidence{Kind: codexcontinuity.KindResponseReference, DigestInput: []byte("old-response")}, codexcontinuity.Resolution{Status: codexcontinuity.ResolutionUnknown}); err != nil {
		t.Fatal(err)
	}
	if external.Evaluate("a", scope, Policy{}).Allowed {
		t.Fatal("unknown external state accepted by default")
	}
	if !external.Evaluate("a", scope, Policy{Inbound: Any}).Allowed {
		t.Fatal("explicit external admission ignored")
	}
	var absent *Session
	if !absent.Evaluate("a", scope, Policy{}).Allowed || absent.PreferredProviderID() != "" {
		t.Fatal("non-Codex session was constrained")
	}
	if err := absent.Observe(ctx, codexcontinuity.Evidence{}, codexcontinuity.Resolution{}); err != nil {
		t.Fatal(err)
	}
	if err := absent.CommitVisible(ctx, "a", scope, Policy{}); err != nil {
		t.Fatal(err)
	}
	if _, err := absent.Authorize(ctx, codexidentity.CandidateSnapshot{}); err != nil {
		t.Fatal(err)
	}
}

type failingStore struct{ lookup, save error }

func (s failingStore) Lookup(context.Context, Binding) (Binding, bool, error) {
	return Binding{}, false, s.lookup
}
func (s failingStore) Save(context.Context, []Binding) error { return s.save }

func TestRouteDependencyFailuresDoNotBecomeFreshConversations(t *testing.T) {
	ctx := context.Background()
	failure := errors.New("database unavailable")
	evidence := codexcontinuity.Evidence{Kind: codexcontinuity.KindThreadID, DigestInput: []byte("thread")}
	service := &Service{Store: failingStore{lookup: failure}}
	if err := service.Begin("client", "read").Observe(ctx, evidence, codexcontinuity.Resolution{}); !errors.Is(err, failure) {
		t.Fatalf("lookup = %v", err)
	}
	service = &Service{Store: failingStore{save: failure}}
	s := service.Begin("client", "write")
	if err := s.Observe(ctx, evidence, codexcontinuity.Resolution{}); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitVisible(ctx, "a", scopeFor(t, "a"), Policy{}); !errors.Is(err, failure) {
		t.Fatalf("save = %v", err)
	}
	service = &Service{Policy: func(context.Context, string) (Policy, bool, error) { return Policy{}, false, failure }}
	if err := service.Begin("client", "policy").Observe(ctx, evidence, codexcontinuity.Resolution{Owner: &codexcontinuity.Owner{RouteTargetHint: "a"}}); !errors.Is(err, failure) {
		t.Fatalf("policy = %v", err)
	}
}

func candidateFor(t *testing.T, id string) codexidentity.CandidateSnapshot {
	t.Helper()
	digest := sha256.Sum256([]byte(id))
	subject, err := credentialsession.KeyedDigestSubject("test", digest[:])
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := codexidentity.NewAuthorityResolver().Resolve(credentialsession.RouteSnapshot{
		RouteTargetID: id, APIType: "codex", VendorScope: "vendor",
		Credential: credentialsession.Snapshot{SessionID: id, Kind: credentialsession.KindAPIKey, SecretData: "secret", Version: 1, Subject: subject, AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive}},
	}, "codex", &url.URL{Scheme: "https", Host: "example.test"})
	if err != nil {
		t.Fatal(err)
	}
	return candidate
}

func TestThreadServingRouteWinsOverOpaqueProvenance(t *testing.T) {
	ctx := context.Background()
	a, b := candidateFor(t, "a"), candidateFor(t, "b")
	service := &Service{}
	s := service.Begin("client", "mixed")
	thread := codexcontinuity.Evidence{Kind: codexcontinuity.KindThreadID, DigestInput: []byte("thread")}
	ownerA := codexcontinuity.Resolution{Owner: &codexcontinuity.Owner{RouteTargetHint: "a", ProtocolScope: a.ProtocolScope()}}
	ownerB := codexcontinuity.Resolution{Owner: &codexcontinuity.Owner{RouteTargetHint: "b", ProtocolScope: b.ProtocolScope()}}
	for _, kind := range []codexcontinuity.Kind{codexcontinuity.KindTurnState, codexcontinuity.KindTurnMetadata, codexcontinuity.KindResponseReference, codexcontinuity.KindWindowID, codexcontinuity.KindConversationID} {
		if err := s.Observe(ctx, codexcontinuity.Evidence{Kind: kind, DigestInput: []byte("prior")}, ownerA); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Observe(ctx, thread, ownerB); err != nil {
		t.Fatal(err)
	}
	if err := s.Observe(ctx, thread, ownerA); err != nil {
		t.Fatal(err)
	}
	if s.PreferredProviderID() != "b" {
		t.Fatal("replay or older state replaced serving source")
	}
	if _, err := s.Authorize(ctx, b); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize(ctx, a); !IsDenied(err) || err.Error() == "" {
		t.Fatalf("denial = %v", err)
	}
	if err := s.CommitVisible(ctx, "b", b.ProtocolScope(), Policy{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Observe(ctx, codexcontinuity.Evidence{Kind: codexcontinuity.KindThreadID, DigestInput: []byte("other-thread")}, ownerA); err != nil {
		t.Fatal(err)
	}
	if s.PreferredProviderID() != "" {
		t.Fatal("ambiguous thread evidence selected a preference")
	}
}

func TestClientSessionDoesNotBindNewConversations(t *testing.T) {
	ctx := context.Background()
	service := &Service{}
	s := service.Begin("client", "new-thread")
	if err := s.Observe(ctx, codexcontinuity.Evidence{Kind: codexcontinuity.KindSessionID, DigestInput: []byte("shared-session")}, codexcontinuity.Resolution{Owner: &codexcontinuity.Owner{RouteTargetHint: "old"}}); err != nil {
		t.Fatal(err)
	}
	target := candidateFor(t, "new")
	if _, err := s.Authorize(ctx, target); err != nil {
		t.Fatalf("new conversation inherited client session: %v", err)
	}
	if err := s.CommitVisible(ctx, "new", target.ProtocolScope(), Policy{}); err != nil {
		t.Fatal(err)
	}
	if s.PreferredProviderID() != "new" {
		t.Fatal("connection-local serving route missing")
	}
	if ProtocolKey(codexidentity.ProtocolScope{}) != "" {
		t.Fatal("invalid identity encoded")
	}
	failure := errors.New("policy unavailable")
	service.Policy = func(context.Context, string) (Policy, bool, error) { return Policy{}, false, failure }
	if _, err := service.Begin("client", "failed").Authorize(ctx, target); !errors.Is(err, failure) {
		t.Fatalf("policy error = %v", err)
	}
}
