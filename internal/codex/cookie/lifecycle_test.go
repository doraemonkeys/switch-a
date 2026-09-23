package providercookie

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/identity"
)

func TestTransientRequestsDoNotDependOnStorage(t *testing.T) {
	repository := newMemoryRepository()
	unavailable := errors.New("database unavailable")
	repository.useErr, repository.createAlways = unavailable, unavailable
	repository.loadErr, repository.touchErr, repository.mergeErr = unavailable, unavailable, unavailable
	clock := &serviceClock{now: time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)}
	service := newTestService(t, repository, clock, nil, nil)
	authority := mustCookieAuthority(t, "transient")
	target := mustURL(t, "https://example.com/")
	owner := testClientScope(t, "owner")
	scheme, _ := NewResolvedExternalScheme("https")
	// Exceed the historical global limit using a store that rejects every call.
	for index := range DefaultMaxHandleBindingsGlobal + 1 {
		request, err := service.BeginRequest(context.Background(), "stateless", "", []codexidentity.ClientScope{owner})
		if err != nil {
			t.Fatal(err)
		}
		lines := []string(nil)
		switch index % 4 {
		case 1:
			lines = []string{"sid=; Max-Age=0"}
		case 2:
			lines = []string{"sid=value", "sid=; Max-Age=0"}
		case 3:
			lines = []string{"sid=expired; Expires=Wed, 01 Jan 2020 00:00:00 GMT"}
		}
		if _, err := request.ApplyResponse(authority, target, lines); err != nil {
			t.Fatal(err)
		}
		if header, err := request.Select(context.Background(), authority, target); err != nil || header != "" {
			t.Fatalf("select = %q, %v", header, err)
		}
		if _, err := request.Commit(context.Background(), authority); err != nil {
			t.Fatal(err)
		}
		if header, err := request.GatewaySetCookie(scheme); err != nil || header != "" {
			t.Fatalf("empty response = %q, %v", header, err)
		}
		request.DiscardAll()
	}
	if len(repository.bindings) != 0 || len(repository.cookies) != 0 {
		t.Fatal("transient requests changed durable state")
	}
}

func TestUpgradeReservationSurvivesReplacementWithoutCreatingEmptyBindings(t *testing.T) {
	repository := newMemoryRepository()
	clock := &serviceClock{now: time.Now()}
	service := newTestService(t, repository, clock, nil, nil)
	owner := testClientScope(t, "ws")
	request := beginTestRequest(t, service, "", owner)
	scheme, _ := NewResolvedExternalScheme("https")
	header, err := request.ReserveUpgradeCookie(scheme)
	if err != nil || header == "" {
		t.Fatalf("reserve = %q, %v", header, err)
	}
	repeated, err := request.ReserveUpgradeCookie(scheme)
	if err != nil || header != repeated || len(repository.bindings) != 0 {
		t.Fatal("reservation was not stable and transient")
	}
	parsed, err := http.ParseSetCookie(header)
	if err != nil {
		t.Fatal(err)
	}
	first, selected := mustCookieAuthority(t, "first"), mustCookieAuthority(t, "selected")
	target := mustURL(t, "https://example.com/")
	if _, err := request.ApplyResponse(first, target, []string{"sid=abandoned"}); err != nil {
		t.Fatal(err)
	}
	if err := request.Discard(first); err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(time.Hour)
	if _, err := request.ApplyResponse(selected, target, []string{"sid=selected"}); err != nil {
		t.Fatal(err)
	}
	if _, err := request.Commit(context.Background(), selected); err != nil {
		t.Fatal(err)
	}
	published, err := request.GatewaySetCookie(scheme)
	if err != nil || published != header {
		t.Fatalf("committed token changed: %q / %q, %v", published, header, err)
	}
	reused := beginTestRequest(t, service, parsed.Value, owner)
	if value, err := reused.Select(context.Background(), first, target); err != nil || value != "" {
		t.Fatalf("abandoned authority = %q, %v", value, err)
	}
	if value, err := reused.Select(context.Background(), selected, target); err != nil || value != "sid=selected" {
		t.Fatalf("selected authority = %q, %v", value, err)
	}
	if value, err := reused.ReserveUpgradeCookie(scheme); err != nil || value != "" {
		t.Fatalf("unnecessary refresh = %q, %v", value, err)
	}
	for _, binding := range repository.bindings {
		if !binding.CreatedAt.Equal(clock.now) {
			t.Fatal("TTL started at reservation rather than commit")
		}
	}
	if _, err := request.ReserveUpgradeCookie(scheme); !errors.Is(err, ErrOverlayDiscarded) {
		t.Fatalf("late reservation = %v", err)
	}

	empty := beginTestRequest(t, service, "", owner)
	if _, err := empty.ReserveUpgradeCookie(scheme); err != nil {
		t.Fatal(err)
	}
	if _, err := empty.Commit(context.Background(), selected); err != nil {
		t.Fatal(err)
	}
	if len(repository.bindings) != 1 {
		t.Fatal("empty upgrade persisted its reservation")
	}
}

func TestCreationRollbackKeepsOverlayAfterIdentifierCollision(t *testing.T) {
	repository := newMemoryRepository()
	service := newTestService(t, repository, &serviceClock{now: time.Now()}, nil, nil)
	request := beginTestRequest(t, service, "", testClientScope(t, "retry"))
	authority := mustCookieAuthority(t, "retry")
	target := mustURL(t, "https://example.com/")
	if _, err := request.ApplyResponse(authority, target, []string{"sid=retained"}); err != nil {
		t.Fatal(err)
	}
	repository.createErr = ErrIdentifierClash
	repository.mergeErr = errors.New("transaction rolled back")
	if _, err := request.Commit(context.Background(), authority); !errors.Is(err, ErrStorage) {
		t.Fatalf("commit = %v", err)
	}
	repository.mergeErr = nil
	if header, err := request.Select(context.Background(), authority, target); err != nil || header != "sid=retained" {
		t.Fatalf("rollback lost overlay = %q, %v", header, err)
	}
	if result, err := request.Commit(context.Background(), authority); err != nil || result.Upserted != 1 {
		t.Fatalf("retry = %+v, %v", result, err)
	}
}

func TestUpgradeReservationFailuresDoNotPersist(t *testing.T) {
	repository := newMemoryRepository()
	service := newTestService(t, repository, &serviceClock{now: time.Now()}, nil, nil)
	scheme, _ := NewResolvedExternalScheme("https")
	request := beginTestRequest(t, service, "", testClientScope(t, "failure"))
	if _, err := request.ReserveUpgradeCookie(ResolvedExternalScheme{}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("scheme = %v", err)
	}
	repository.createAlways = ErrIdentifierClash
	if _, err := request.ReserveUpgradeCookie(scheme); err != nil {
		t.Fatal(err)
	}
	authority := mustCookieAuthority(t, "reserved")
	if _, err := request.ApplyResponse(authority, mustURL(t, "https://example.com"), []string{"sid=value"}); err != nil {
		t.Fatal(err)
	}
	if _, err := request.Commit(context.Background(), authority); !errors.Is(err, ErrIdentifierClash) {
		t.Fatalf("reserved collision = %v", err)
	}
	if len(repository.bindings) != 0 {
		t.Fatal("reserved collision persisted a jar")
	}

	entropy := beginTestRequest(t, service, "", testClientScope(t, "entropy"))
	service.random = bytes.NewReader(nil)
	if _, err := entropy.ReserveUpgradeCookie(scheme); !errors.Is(err, ErrCrypto) {
		t.Fatalf("entropy = %v", err)
	}
}
