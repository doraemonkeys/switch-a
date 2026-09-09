package clientdisguise

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestImmutableRecordsComparePersistedValues(t *testing.T) {
	now := time.Now()
	t.Run("profile", func(t *testing.T) {
		original := ProfileRevision{ID: "observed", Tuple: windowsDesktop, ClientVersion: "0.153.4",
			SourceID: "reference", CapturedAt: now, CreatedAt: now, Features: Features{UserAgent: "original"}}
		equivalent := original
		equivalent.CapturedAt, equivalent.CreatedAt = now.UTC(), now.UTC()
		changed := equivalent
		changed.Features.UserAgent = "changed"
		assertImmutableRoundTrip(t, original, equivalent, changed, "id", original.ID, ProfileRevision.equalImmutable)
	})
	t.Run("sample", func(t *testing.T) {
		original := Sample{ID: "observation", SourceID: "reference", CapturedAt: now,
			Tuple: windowsDesktop, ClientVersion: "0.153.4"}
		equivalent := original
		equivalent.CapturedAt = now.UTC()
		changed := equivalent
		changed.CapturedAt = now.Add(time.Nanosecond)
		assertImmutableRoundTrip(t, original, equivalent, changed, "id", original.ID, Sample.equalImmutable)
	})
	t.Run("transport", func(t *testing.T) {
		original := TransportSample{ID: "transport", SourceID: "reference", CapturedAt: now,
			Name: "Desktop", Config: json.RawMessage(`{"version":1}`)}
		equivalent := original
		equivalent.CapturedAt = now.UTC()
		changed := equivalent
		changed.Config = json.RawMessage(`{"version":2}`)
		assertImmutableRoundTrip(t, original, equivalent, changed, "id", original.ID, TransportSample.equalImmutable)
	})
	t.Run("login", func(t *testing.T) {
		original := LoginIdentity{CredentialSessionID: "login", GenerationID: "generation",
			DeviceID: "device", AccountBasis: account("account"), CreatedAt: now}
		equivalent := original
		equivalent.CreatedAt = now.UTC()
		changed := equivalent
		changed.DeviceID = "another-device"
		assertImmutableRoundTrip(t, original, equivalent, changed, "credential_session_id", original.CredentialSessionID, LoginIdentity.equalImmutable)
	})
	t.Run("history", func(t *testing.T) {
		original := LoginHistory{GenerationID: "generation", Identity: LoginIdentity{
			CredentialSessionID: "login", GenerationID: "generation", DeviceID: "device",
			AccountBasis: account("account"), CreatedAt: now}}
		equivalent := original
		equivalent.Identity.CreatedAt = now.UTC()
		changed := equivalent
		changed.Identity.AccountBasis = account("another-account")
		assertImmutableRoundTrip(t, original, equivalent, changed, "generation_id", original.GenerationID, LoginHistory.equalImmutable)
	})
}

func assertImmutableRoundTrip[T any](t *testing.T, original, equivalent, changed T, key, value string, equal func(T, T) bool) {
	t.Helper()
	repo := testRepository(t)
	if err := mergeImmutable(repo.db, &original, key, value, equal); err != nil {
		t.Fatalf("new immutable record rejected after persistence: %v", err)
	}
	if err := mergeImmutable(repo.db, &equivalent, key, value, equal); err != nil {
		t.Fatalf("same record expressed in UTC rejected: %v", err)
	}
	if err := mergeImmutable(repo.db, &changed, key, value, equal); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed immutable content must conflict: %v", err)
	}
	if err := mergeImmutable(repo.db, &original, key, value, equal); err != nil {
		t.Fatalf("conflicting write changed the stored record: %v", err)
	}
}
