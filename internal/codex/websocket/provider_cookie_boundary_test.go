package codexws

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
)

func TestCookieReservationFailureRejectsAdmission(t *testing.T) {
	repository := &testCookieRepository{}
	service, err := providercookie.NewService(providercookie.ServiceConfig{
		Repository: repository, HandleDigester: testHandleDigester{},
		// Only the request-local JarID can be generated; reserving its handle fails.
		Random:            bytes.NewReader(bytes.Repeat([]byte{1}, providercookie.JarIDEntropyBytes)),
		HostCanonicalizer: providercookie.HostCanonicalizerFunc(codexidentity.CanonicalizeCookieHost),
		PublicSuffixList:  codexidentity.PublicSuffixList{}, Policy: providercookie.DefaultPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newTestRuntime(t, Config{ProviderCookies: service})
	operation, err := runtime.Begin(context.Background(), testRequest("client-a"), codexAPIType, "reserve-failure", "")
	var persistence *providercookie.PersistenceError
	if operation != nil || Classify(err) != FailureStorage || !errors.As(err, &persistence) || persistence.Kind != providercookie.PersistenceCrypto {
		t.Fatalf("admission = %v, %v", operation, err)
	}
	if repository.merges != 0 || repository.binding.JarID != (providercookie.JarID{}) {
		t.Fatal("failed reservation changed durable state")
	}
}

type reservationLeaseRepository struct {
	testCookieRepository
	releases int
}

func (r *reservationLeaseRepository) UseBinding(ctx context.Context, lookup providercookie.BindingLookup) (providercookie.BindingUse, error) {
	use, err := r.testCookieRepository.UseBinding(ctx, lookup)
	if err == nil && use.Disposition == providercookie.BindingValid {
		use.Refresh = true
		use.Release = func() { r.releases++ }
	}
	return use, err
}

type invalidCookieScheme struct{}

func (invalidCookieScheme) ResolveExternalScheme(*http.Request) (providercookie.ResolvedExternalScheme, error) {
	return providercookie.ResolvedExternalScheme{}, nil
}

func TestCookieReservationFailureReleasesExistingJarLease(t *testing.T) {
	repository := &reservationLeaseRepository{}
	service := newTestCookieService(t, repository)
	seed, err := service.BeginRequest(context.Background(), "seed-reservation", "", []codexidentity.ClientScope{testClientScope(t, "client-a")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(seed.DiscardAll)
	candidate, _ := testCandidate(t, "route-a", "https://api.example.test/v1")
	authority := candidate.Authority().CookieAuthority()
	if _, err := seed.ApplyResponse(authority, mustURL(t, "https://api.example.test/"), []string{"sid=value; Path=/"}); err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Commit(context.Background(), authority); err != nil {
		t.Fatal(err)
	}
	scheme, _ := providercookie.NewResolvedExternalScheme("https")
	header, err := seed.GatewaySetCookie(scheme)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := http.ParseSetCookie(header)
	if err != nil {
		t.Fatal(err)
	}
	request := testRequest("client-a")
	request.AddCookie(&http.Cookie{Name: handle.Name, Value: handle.Value})
	runtime := newTestRuntime(t, Config{ProviderCookies: service, ExternalScheme: invalidCookieScheme{}})
	if operation, err := runtime.Begin(context.Background(), request, codexAPIType, "reserve-invalid-scheme", ""); operation != nil || Classify(err) != FailureStorage {
		t.Fatalf("admission = %v, %v", operation, err)
	}
	if repository.releases != 1 {
		t.Fatalf("released leases = %d, want 1", repository.releases)
	}
}
