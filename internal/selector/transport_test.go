package selector

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func transportProvider(id, transport string, priority int) model.Provider {
	p := authorityTestProvider(id, "https://upstream.example", id, priority)
	p.APITypes[0].Transport = transport
	p.CredentialSessions[0].Transport = transport
	return p
}

func TestTransportFiltersStickyInitialAlternateAndEmptyCandidates(t *testing.T) {
	ctx := context.Background()
	httpOnly := transportProvider("http-only", "http", 1)
	wsOnly := transportProvider("ws-only", "websocket", 2)
	backup := transportProvider("ws-backup", "websocket", 3)
	store := newMockStore()
	store.providers = []model.Provider{httpOnly, wsOnly, backup}
	sticky := NewMemoryStickyCache(internal.RealClock{})
	selector := NewSelector(Config{Store: store, StickyCache: sticky})
	req := &model.SelectRequest{APIType: "codex", Transport: "websocket", StickyMode: model.StickyModeAPIType}
	sticky.Set(buildStickyKey(req), httpOnly.ID, time.Minute)
	result, err := selector.SelectWithMetadata(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Provider().ID != wsOnly.ID {
		t.Fatalf("WS selected %s", result.Provider().ID)
	}
	if _, found := sticky.Get(buildStickyKey(req)); found {
		t.Fatal("ineligible sticky retained")
	}
	result.Lease.Release()
	alternate, err := selector.ReserveAlternate(ctx, AlternateReservationRequest{Request: req, ExcludeProviderIDs: map[string]bool{wsOnly.ID: true}})
	if err != nil {
		t.Fatal(err)
	}
	if alternate.Provider().ID != backup.ID {
		t.Fatal("alternate ignored transport")
	}
	alternate.Release()
	store.providers = []model.Provider{httpOnly}
	if _, err := selector.SelectWithMetadata(ctx, req); !errors.Is(err, internal.ErrNoProvider) {
		t.Fatalf("no WS provider: %v", err)
	}
	eligibility, err := NewProviderSelectionEligibility(ctx, store, nil, req, httpOnly)
	if err != nil {
		t.Fatal(err)
	}
	eligible, reason, err := eligibility.allowsExistingRoute(ctx, &httpOnly, true)
	if err != nil || eligible || reason != errorrule.ReasonTransportUnsupported {
		t.Fatalf("dispatch rejection: %v %s %v", eligible, reason, err)
	}
}

func TestTransportCredentialsAndConversationAuthority(t *testing.T) {
	p := transportProvider("dual", "http", 1)
	ws := transportProvider("dual-ws", "websocket", 1)
	ws.APITypes[0].ProviderID = p.ID
	ws.CredentialSessions[0].RouteTargetID = p.ID
	p.APITypes = append(p.APITypes, ws.APITypes...)
	p.CredentialSessions = append(p.CredentialSessions, ws.CredentialSessions...)
	store := newMockStore()
	store.providers = []model.Provider{p}
	s := NewSelector(Config{Store: store})
	ctx := context.Background()
	httpReq := &model.SelectRequest{APIType: "codex", Transport: "http"}
	wsReq := &model.SelectRequest{APIType: "codex", Transport: "websocket"}
	httpResult, err := s.SelectWithMetadata(ctx, httpReq)
	if err != nil {
		t.Fatal(err)
	}
	defer httpResult.Lease.Release()
	wsResult, err := s.SelectWithMetadata(ctx, wsReq)
	if err != nil {
		t.Fatal(err)
	}
	defer wsResult.Lease.Release()
	httpCandidate, _ := httpResult.CandidateSnapshot()
	wsCandidate, _ := wsResult.CandidateSnapshot()
	if httpCandidate.Credential().SecretData != "secret-dual" || wsCandidate.Credential().SecretData != "secret-dual-ws" {
		t.Fatal("wrong transport credential")
	}
	if httpCandidate.SameDispatchIdentity(wsCandidate) || httpCandidate.Authority().Equal(wsCandidate.Authority()) {
		t.Fatal("different credentials collapsed")
	}
	if BuildContinuityKey(httpReq) != BuildContinuityKey(wsReq) || buildStickyKey(httpReq) == buildStickyKey(wsReq) {
		t.Fatal("affinity and continuity dimensions conflated")
	}
	authority := httpCandidate.Authority()
	wsReq.RequiredAuthority = &authority
	if _, err := s.SelectWithMetadata(ctx, wsReq); !errors.Is(err, internal.ErrNoProvider) {
		t.Fatal("cross-credential continuation accepted", err)
	}
	// Sharing a credential preserves conversation authority across transports.
	store.providers[0].CredentialSessions[1].Credential = store.providers[0].CredentialSessions[0].Credential
	shared, err := s.SelectWithMetadata(ctx, wsReq)
	if err != nil {
		t.Fatal(err)
	}
	defer shared.Lease.Release()
	candidate, _ := shared.CandidateSnapshot()
	if !candidate.Authority().Equal(authority) || candidate.SameDispatchIdentity(httpCandidate) {
		t.Fatal("shared authority lost transport boundary")
	}
}
