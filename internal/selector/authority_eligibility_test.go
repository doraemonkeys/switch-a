package selector

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/model"
)

const authorityTestAPIType = "codex"

func authorityTestProvider(id, origin, subjectID string, priority int) model.Provider {
	subject, err := credentialsession.AccountSubject(subjectID)
	if err != nil {
		panic(err)
	}
	return model.Provider{
		ID:       id,
		Enabled:  true,
		Vendor:   "openai",
		Priority: priority,
		APITypes: []model.ProviderAPIType{{
			ProviderID: id,
			APIType:    authorityTestAPIType,
			BaseURL:    origin + "/v1/responses",
		}},
		CredentialSessions: []credentialsession.RouteSnapshot{{
			RouteTargetID: id,
			APIType:       authorityTestAPIType,
			Credential: credentialsession.Snapshot{
				SessionID:  "session-" + id,
				Vendor:     "openai",
				Kind:       credentialsession.KindAPIKey,
				SecretData: "secret-" + id,
				Version:    1,
				Subject:    subject,
				AuthState:  credentialsession.AuthState{Status: credentialsession.AuthStatusActive},
			},
		}},
	}
}

func authorityForProvider(t *testing.T, provider model.Provider) codexidentity.UpstreamAuthority {
	t.Helper()
	target, err := url.Parse(provider.BaseURLForAPIType(authorityTestAPIType))
	if err != nil {
		t.Fatalf("parse candidate URL: %v", err)
	}
	candidate, err := codexidentity.NewAuthorityResolver().Resolve(
		provider.CredentialSessions[0],
		authorityTestAPIType,
		target,
	)
	if err != nil {
		t.Fatalf("resolve candidate authority: %v", err)
	}
	return candidate.Authority()
}

func TestRequiredAuthorityOverridesPreferredStickyAndStrategy(t *testing.T) {
	allowed := authorityTestProvider("allowed", "https://api.example.test", "account-a", 100)
	crossAuthority := authorityTestProvider("cross", "https://other.example.test", "account-b", 1)
	store := newMockStore()
	store.providers = []model.Provider{crossAuthority, allowed}
	required := authorityForProvider(t, allowed)
	req := &model.SelectRequest{
		ClientIP:               "127.0.0.1",
		APIType:                authorityTestAPIType,
		StickyMode:             model.StickyModeAPIType,
		RequiredAuthority:      &required,
		PreferredRouteTargetID: crossAuthority.ID,
	}
	sticky := NewMemoryStickyCache(&mockClock{})
	sticky.Set(BuildContinuityKey(req), crossAuthority.ID, DefaultStickyTTL)
	selector := NewSelector(Config{Store: store, StickyCache: sticky})

	result, err := selector.SelectWithMetadata(context.Background(), req)
	if err != nil {
		t.Fatalf("SelectWithMetadata() error = %v", err)
	}
	defer result.Lease.Release()
	if result.Provider().ID != allowed.ID {
		t.Fatalf("selected provider = %q, want authority-matching %q", result.Provider().ID, allowed.ID)
	}
	if _, found := sticky.Get(BuildContinuityKey(req)); found {
		t.Fatal("cross-authority sticky hint was not evicted")
	}
	candidate, ok := result.CandidateSnapshot()
	if !ok || !candidate.Authority().Equal(required) {
		t.Fatal("lease did not retain the authority-constrained candidate snapshot")
	}
}

func TestPreferredRouteTargetAppliesOnlyInsideRequiredAuthority(t *testing.T) {
	primary := authorityTestProvider("primary", "https://api.example.test", "account-a", 1)
	preferred := authorityTestProvider("preferred", "https://api.example.test", "account-a", 100)
	store := newMockStore()
	store.providers = []model.Provider{primary, preferred}
	required := authorityForProvider(t, primary)
	selector := NewSelector(Config{Store: store})

	result, err := selector.SelectWithMetadata(context.Background(), &model.SelectRequest{
		APIType:                authorityTestAPIType,
		RequiredAuthority:      &required,
		PreferredRouteTargetID: preferred.ID,
	})
	if err != nil {
		t.Fatalf("SelectWithMetadata() error = %v", err)
	}
	defer result.Lease.Release()
	if result.Provider().ID != preferred.ID || result.Metadata.Source != SelectionSourcePreferredRoute {
		t.Fatalf("preferred selection = (%q, %q)", result.Provider().ID, result.Metadata.Source)
	}

	stateless, err := selector.SelectWithMetadata(context.Background(), &model.SelectRequest{
		APIType:                authorityTestAPIType,
		PreferredRouteTargetID: preferred.ID,
	})
	if err != nil {
		t.Fatalf("stateless SelectWithMetadata() error = %v", err)
	}
	defer stateless.Lease.Release()
	if stateless.Provider().ID != primary.ID {
		t.Fatalf("unbounded route hint selected %q, want strategy provider %q", stateless.Provider().ID, primary.ID)
	}
}

func TestAuthorityConstraintAllowsReplacementButRejectsCrossAuthority(t *testing.T) {
	current := authorityTestProvider("current", "https://api.example.test", "account-a", 1)
	replacement := authorityTestProvider("replacement", "https://api.example.test", "account-a", 2)
	crossAuthority := authorityTestProvider("cross", "https://other.example.test", "account-b", 0)
	store := newMockStore()
	store.providers = []model.Provider{current, replacement, crossAuthority}
	required := authorityForProvider(t, current)
	selector := NewSelector(Config{Store: store})
	req := &model.SelectRequest{
		APIType:                authorityTestAPIType,
		SwitchMode:             model.SwitchModeReplacement,
		RequiredAuthority:      &required,
		PreferredRouteTargetID: current.ID,
	}

	reservation, err := selector.ReserveAlternate(context.Background(), AlternateReservationRequest{
		Request:            req,
		ExcludeProviderIDs: map[string]bool{current.ID: true},
	})
	if err != nil {
		t.Fatalf("ReserveAlternate() error = %v", err)
	}
	defer reservation.Release()
	if reservation.Provider().ID != replacement.ID {
		t.Fatalf("replacement = %q, want same-authority %q", reservation.Provider().ID, replacement.ID)
	}

	store.providers = []model.Provider{crossAuthority}
	if _, err := selector.SelectWithMetadata(context.Background(), req); !errors.Is(err, internal.ErrNoProvider) {
		t.Fatalf("cross-authority-only selection error = %v, want ErrNoProvider", err)
	}
}

func TestAuthorityResolutionAndSnapshotStorageFailuresFailClosed(t *testing.T) {
	resolved := authorityTestProvider("resolved", "https://api.example.test", "account-a", 1)
	required := authorityForProvider(t, resolved)
	pending := authorityTestProvider("pending", "https://api.example.test", "account-a", 1)
	pending.CredentialSessions[0].Credential.Subject = credentialsession.PendingSubject()
	store := newMockStore()
	store.providers = []model.Provider{pending}
	selector := NewSelector(Config{Store: store})
	req := &model.SelectRequest{APIType: authorityTestAPIType, RequiredAuthority: &required}

	if _, err := selector.SelectWithMetadata(context.Background(), req); !errors.Is(err, internal.ErrNoProvider) {
		t.Fatalf("pending subject selection error = %v, want ErrNoProvider", err)
	}

	stateless, err := selector.SelectWithMetadata(context.Background(), &model.SelectRequest{APIType: authorityTestAPIType})
	if err != nil {
		t.Fatalf("stateless pending selection error = %v", err)
	}
	defer stateless.Lease.Release()
	if _, resolved := stateless.CandidateSnapshot(); resolved {
		t.Fatal("pending subject produced a resolved lease identity")
	}

	storageErr := errors.New("credential snapshot storage unavailable")
	store.err = storageErr
	if _, err := selector.SelectWithMetadata(context.Background(), req); !errors.Is(err, storageErr) {
		t.Fatalf("storage failure = %v, want %v", err, storageErr)
	}
}

func TestActiveRetryAndActivationRevalidationShareAuthorityEligibility(t *testing.T) {
	current := authorityTestProvider("current", "https://api.example.test", "account-a", 1)
	replacement := authorityTestProvider("replacement", "https://api.example.test", "account-a", 2)
	store := newMockStore()
	store.providers = []model.Provider{current, replacement}
	required := authorityForProvider(t, current)
	selector := NewSelector(Config{Store: store})
	req := &model.SelectRequest{
		APIType:                authorityTestAPIType,
		RequiredAuthority:      &required,
		PreferredRouteTargetID: current.ID,
	}
	selected, err := selector.SelectWithMetadata(context.Background(), req)
	if err != nil {
		t.Fatalf("initial selection error = %v", err)
	}
	defer selected.Lease.Release()
	reuseReq := cloneSelectRequest(req)
	reuseReq.ProviderSwitchHistory = &model.ProviderSwitchHistory{
		OriginProviderID:    current.ID,
		AttemptChain:        []string{current.ID},
		ProviderSwitchCount: 1,
	}
	reuseReq.MaxProviderSwitches = 1

	active, err := selector.SelectActive(context.Background(), reuseReq, selected.Lease)
	if err != nil {
		t.Fatalf("same-authority active selection error = %v", err)
	}
	active.Lease.Release()
	retryInput := retryPermitInput(t, selected.Lease)
	retryInput.Request = reuseReq
	retryPermit, err := selector.ReserveSameProviderRetry(context.Background(), retryInput)
	if err != nil {
		t.Fatalf("same-authority retry of existing route error = %v", err)
	}
	retryPermit.Release()

	reservation, err := selector.ReserveAlternate(context.Background(), AlternateReservationRequest{
		Request:            req,
		ExcludeProviderIDs: map[string]bool{current.ID: true},
	})
	if err != nil {
		t.Fatalf("same-authority reservation error = %v", err)
	}
	store.providers[1].APITypes[0].BaseURL = "https://other.example.test/v1/responses"
	if err := reservation.PrepareActivation(context.Background()); err == nil {
		t.Fatal("reservation activation accepted a cross-authority live snapshot")
	} else if reason, ok := ProviderRejectionReason(err); !ok || reason != errorrule.ReasonAuthUnavailable {
		t.Fatalf("reservation rejection = (%q, %v), error = %v", reason, ok, err)
	}

	store.providers[0].APITypes[0].BaseURL = "https://other.example.test/v1/responses"
	if _, err := selector.SelectActive(context.Background(), reuseReq, selected.Lease); !errors.Is(err, internal.ErrNoProvider) {
		t.Fatalf("cross-authority active selection error = %v, want ErrNoProvider", err)
	}
	_, err = selector.ReserveSameProviderDispatch(context.Background(), SameProviderDispatchRequest{
		Current: selected.Lease,
		Request: reuseReq,
	})
	if reason, ok := ProviderRejectionReason(err); !ok || reason != errorrule.ReasonAuthUnavailable {
		t.Fatalf("same-provider revalidation rejection = (%q, %v), error = %v", reason, ok, err)
	}
}

func TestCandidateSnapshotCredentialIsDefensivelyCloned(t *testing.T) {
	provider := authorityTestProvider("provider", "https://api.example.test", "account-a", 1)
	store := newMockStore()
	store.providers = []model.Provider{provider}
	selector := NewSelector(Config{Store: store})
	result, err := selector.SelectWithMetadata(context.Background(), &model.SelectRequest{APIType: authorityTestAPIType})
	if err != nil {
		t.Fatalf("SelectWithMetadata() error = %v", err)
	}
	defer result.Lease.Release()
	candidate, ok := result.CandidateSnapshot()
	if !ok {
		t.Fatal("resolved candidate snapshot missing from lease")
	}
	credential := candidate.Credential()
	credential.Subject.Value[0] ^= 0xff
	if candidate.Credential().Subject.Value[0] == credential.Subject.Value[0] {
		t.Fatal("candidate credential accessor leaked mutable subject storage")
	}
}
