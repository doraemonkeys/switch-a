package clientdisguise

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestObservedReferenceFeaturesAndPartialUpdates(t *testing.T) {
	r := testRepository(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if err := r.SaveReference(ctx, ReferenceSource{ID: "source", Name: "Desktop", ClientIdentityID: "client"}); err != nil {
		t.Fatal(err)
	}
	headers := http.Header{"User-Agent": {"Codex Desktop/1.0.0 (Windows 10.0.26200; x86_64) unknown (Codex Desktop; 26.820.60940)"}, "Originator": {"codex_desktop"}, "Installation_id": {"do-not-learn"}}
	if err := r.ObserveClient(ctx, "other", headers, at); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := r.Export(ctx)
	if len(snapshot.Samples) != 0 {
		t.Fatal("unconfigured reference learned")
	}
	if err := r.ObserveClient(ctx, "client", headers, at); err != nil {
		t.Fatal(err)
	}
	snapshot, _ = r.Export(ctx)
	if len(snapshot.Samples) != 1 {
		t.Fatal(snapshot.Samples)
	}
	sample := snapshot.Samples[0]
	if sample.Features.DesktopBuild != "26.820.60940" || sample.Features.OSVersion != "10.0.26200" {
		t.Fatal(sample.Features)
	}
	encoded, _ := json.Marshal(sample)
	if string(encoded) == "" {
		t.Fatal("empty")
	}
	update, err := r.LearnSample(ctx, Sample{ID: "partial", SourceID: "source", Tuple: windowsDesktop, ClientVersion: "1.0.0", CapturedAt: at.Add(time.Hour), Features: Features{UserAgent: "UA-only-update"}})
	if err != nil {
		t.Fatal(err)
	}
	if update.Revision.Features.UserAgent != "UA-only-update" || update.Revision.Features.DesktopBuild != sample.Features.DesktopBuild || update.Revision.Features.Originator != "codex_desktop" {
		t.Fatal("partial sample cleared fields", update.Revision)
	}
	for _, input := range []http.Header{{}, {"User-Agent": {"Codex Desktop/no-version (Windows; x86_64)"}}, {"User-Agent": {"Codex Desktop/1.0.0 (Windows; x86_64)"}, "X-Stainless-Os": {"Linux"}}} {
		if err := r.ObserveClient(ctx, "client", input, at); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.ObserveClient(ctx, "", headers, at); err != nil {
		t.Fatal(err)
	}
	if _, err := r.LearnSample(ctx, Sample{ID: "bad", SourceID: "absent", Tuple: windowsDesktop, ClientVersion: "1.0", CapturedAt: at}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := r.ListLogins(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ListProfiles(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ListReferences(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestTransportSnapshotCloneAndBackupMappingHistory(t *testing.T) {
	r := testRepository(t)
	ctx := context.Background()
	now := time.Now().UTC()
	login, err := r.SyncLoginAccount(ctx, "login", AccountBasis{Kind: "keyed_digest", KeyVersion: "h1", Value: make([]byte, 32)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.MapIdentity(ctx, MappingKey{GenerationID: login.GenerationID, ClientIdentityID: "client", Namespace: "turn", Original: "turn"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.SyncLoginAccount(ctx, "login", AccountBasis{Kind: "keyed_digest", KeyVersion: "h2", Value: make([]byte, 32)}); err != nil {
		t.Fatal(err)
	}
	versions, err := r.RequiredHMACVersions(ctx)
	if err != nil || len(versions) != 2 || versions[0] != "h1" {
		t.Fatal(versions, err)
	}
	sample := TransportSample{ID: "transport", SourceID: "source", CapturedAt: now, TLSProfile: "go-standard", HTTPProfile: "http1", Config: json.RawMessage(`{"http_protocol":"http1"}`)}
	if err := r.SaveTransportSample(ctx, sample); err != nil {
		t.Fatal(err)
	}
	if err := r.SaveTransportSample(ctx, sample); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ListTransportSamples(ctx); err != nil {
		t.Fatal(err)
	}
	basis := AccountBasis{Kind: "keyed_digest", KeyVersion: "h2", Value: make([]byte, 32)}
	candidate, err := r.EvaluateCandidate(ctx, "login", basis, Policy{Enabled: true}, PlatformFacts{Tuple: windowsDesktop})
	if err != nil {
		t.Fatal(err)
	}
	target, err := r.CommitTarget(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	binding := target.Binding
	binding.TransportSampleID = sample.ID
	binding.TelemetryPathMappings = map[string]string{"telemetry/a": "telemetry/b"}
	binding.RemapCacheKeys = true
	if _, err := r.SetBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	candidate, err = r.EvaluateCandidate(ctx, "login", basis, Policy{Enabled: true}, PlatformFacts{Tuple: windowsDesktop})
	if err != nil || candidate.Transport == nil {
		t.Fatal(candidate, err)
	}
	target, err = r.CommitTarget(ctx, candidate)
	if err != nil || target.Transport == nil {
		t.Fatal(target, err)
	}
	cloned := target.Clone()
	cloned.Transport.Config[0] = 'X'
	cloned.Binding.TelemetryPathMappings["telemetry/a"] = "mutated"
	cloned.Login.AccountBasis.Value[0] = 1
	if target.Transport.Config[0] == 'X' || target.Login.AccountBasis.Value[0] != 0 || target.Binding.TelemetryPathMappings["telemetry/a"] == "mutated" {
		t.Fatal("target clone leaked")
	}
	match := true
	candidate.Policy.MatchPlatform = &match
	candidate.Profile.Features.Headers = map[string]string{"Originator": "reference"}
	copy := candidate.Clone()
	*copy.Policy.MatchPlatform = false
	copy.Binding.TelemetryPathMappings["telemetry/a"] = "mutated"
	copy.Profile.Features.Headers["Originator"] = "mutated"
	copy.Transport.Config[0] = 'X'
	if !*candidate.Policy.MatchPlatform || candidate.Profile.Features.Headers["Originator"] == "mutated" || candidate.Transport.Config[0] == 'X' {
		t.Fatal("candidate clone leaked")
	}
	snapshot, err := r.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	restored := testRepository(t)
	if err := restored.Import(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := restored.Import(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.Mappings[0].Mapped = "conflicting"
	if err := restored.Import(ctx, snapshot); !errors.Is(err, ErrConflict) {
		t.Fatal("mapping conflict accepted", err)
	}
}

func TestDomainValidationAndTransactionalFailure(t *testing.T) {
	r := testRepository(t)
	ctx := context.Background()
	if _, err := r.SyncLoginAccount(ctx, "", AccountBasis{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := r.EvaluateCandidate(ctx, "s", account("a"), Policy{UnknownPlatform: "invalid"}, PlatformFacts{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := r.LearnSample(ctx, Sample{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := r.SaveReference(ctx, ReferenceSource{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	for _, sample := range []TransportSample{
		{},
		{ID: "s", SourceID: "ref", CapturedAt: time.Now(), TLSProfile: "chrome"},
		{ID: "s", SourceID: "ref", CapturedAt: time.Now(), HTTPProfile: "unknown"},
		{ID: "s", SourceID: "ref", CapturedAt: time.Now(), Config: json.RawMessage(`{"unsupported":true}`)},
		{ID: "s", SourceID: "ref", CapturedAt: time.Now(), HTTPProfile: "http1", Config: json.RawMessage(`{"http_protocol":"http2"}`)},
	} {
		if err := r.SaveTransportSample(ctx, sample); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	if mapped, err := r.MapIdentity(ctx, MappingKey{}); err != nil || mapped != "" {
		t.Fatal(mapped, err)
	}
	if _, err := r.MapIdentity(ctx, MappingKey{Original: "x"}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if original, ok, err := r.RestoreIdentity(ctx, "missing", "c", "thread", "x"); err != nil || ok || original != "x" {
		t.Fatal(original, ok, err)
	}
	disabled, err := r.EvaluateCandidate(ctx, "s", account("a"), Policy{}, PlatformFacts{})
	if err != nil || !disabled.Decision.Allowed {
		t.Fatal(disabled, err)
	}
	if _, err := r.CommitTarget(ctx, disabled); err != nil {
		t.Fatal(err)
	}
	rejected := candidateFor(t, r, "new", Tuple{})
	if _, err := r.CommitTarget(ctx, rejected); !errors.Is(err, ErrCandidateExcluded) {
		t.Fatal(err)
	}
	initial := candidateFor(t, r, "atomic", windowsDesktop)
	if err := r.db.Exec("CREATE TRIGGER fail_profile BEFORE INSERT ON client_disguise_profile_bindings BEGIN SELECT RAISE(ABORT, 'injected failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := r.CommitTarget(ctx, initial); err == nil {
		t.Fatal("injected failure ignored")
	}
	if _, err := r.GetLogin(ctx, "atomic"); !errors.Is(err, ErrNotFound) {
		t.Fatal("partial login persisted", err)
	}
	if err := r.db.Exec("DROP TRIGGER fail_profile").Error; err != nil {
		t.Fatal(err)
	}
	_, err = r.CommitTarget(ctx, initial)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetBinding(ctx, ProfileBinding{CredentialSessionID: "atomic", Mode: "invalid"}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := r.SetBinding(ctx, ProfileBinding{CredentialSessionID: "missing", Mode: ModeAuto}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := r.SelectProfile(ctx, "atomic", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := validateRevision(ProfileRevision{ID: "p", Tuple: windowsDesktop, SourceID: "ref", ClientVersion: "1", Features: Features{ClientVersion: "2"}}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := validateRevision(ProfileRevision{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
