package codexidentity

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
)

func TestCredentialSubjectConversionAndImmutability(t *testing.T) {
	account, err := NewAccountCredentialSubject(" account-1 ")
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := account.AccountID(); !ok || value != "account-1" || account.Kind() != CredentialSubjectAccount {
		t.Fatalf("account subject = %#v, (%q, %t)", account, value, ok)
	}
	if _, _, ok := account.KeyedDigest(); ok {
		t.Fatal("account subject exposed a keyed digest")
	}

	raw := bytes.Repeat([]byte{7}, DigestSize)
	sessionSubject, err := credentialsession.KeyedDigestSubject("h1", raw)
	if err != nil {
		t.Fatal(err)
	}
	keyed, err := CredentialSubjectFromSession(sessionSubject)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] = 9
	sessionSubject.Value[1] = 9
	version, digest, ok := keyed.KeyedDigest()
	if !ok || version != "h1" || digest[0] != 7 || digest[1] != 7 || keyed.Kind() != CredentialSubjectKeyedDigest {
		t.Fatalf("keyed subject aliased input: %#v", keyed)
	}
	if value, ok := keyed.AccountID(); ok || value != "" {
		t.Fatal("keyed subject exposed an account")
	}
	encoded, err := keyed.MarshalBinary()
	if err != nil || !bytes.Contains(encoded, []byte(credentialSubjectCodec)) {
		t.Fatalf("MarshalBinary() = %x, %v", encoded, err)
	}
	if !keyed.Equal(keyed) || keyed.Equal(account) {
		t.Fatal("CredentialSubject equality is incorrect")
	}

	if _, err := NewAccountCredentialSubject(" "); !IsError(err, ErrorInvalidInput) {
		t.Fatalf("blank account error = %v", err)
	}
	if _, err := CredentialSubjectFromSession(credentialsession.PendingSubject()); !IsError(err, ErrorSubjectPending) || !errors.Is(err, credentialsession.ErrSubjectPending) {
		t.Fatalf("pending subject error = %v", err)
	}
	if _, err := CredentialSubjectFromSession(credentialsession.Subject{Kind: "future"}); !IsError(err, ErrorInvalidInput) {
		t.Fatalf("unknown subject error = %v", err)
	}
	if _, err := CredentialSubjectFromSession(credentialsession.Subject{Kind: credentialsession.SubjectAccount, Value: []byte(" account-1 ")}); !IsError(err, ErrorInvalidInput) {
		t.Fatalf("non-canonical account error = %v", err)
	}
	if _, err := (CredentialSubject{}).MarshalBinary(); !IsError(err, ErrorInvalidInput) {
		t.Fatalf("zero subject MarshalBinary error = %v", err)
	}
}

func TestAuthorityAndScopeIsolationMatrix(t *testing.T) {
	origin := mustRequestOrigin(t, "https://api.example.com:443/v1")
	otherOrigin := mustRequestOrigin(t, "https://api.example.com:8443/v1")
	subject, _ := NewAccountCredentialSubject("account-1")
	otherSubject, _ := NewAccountCredentialSubject("account-2")
	base := mustAuthority(t, "openai", origin, subject)
	if base.Vendor() != "openai" || !base.Origin().Equal(origin) || !base.Subject().Equal(subject) {
		t.Fatalf("authority accessors = %#v", base)
	}

	tests := []struct {
		name  string
		got   UpstreamAuthority
		equal bool
	}{
		{name: "same", got: mustAuthority(t, "openai", origin, subject), equal: true},
		{name: "vendor", got: mustAuthority(t, "azure", origin, subject)},
		{name: "origin", got: mustAuthority(t, "openai", otherOrigin, subject)},
		{name: "subject", got: mustAuthority(t, "openai", origin, otherSubject)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := base.Equal(test.got); got != test.equal {
				t.Fatalf("Equal() = %t, want %t", got, test.equal)
			}
		})
	}
	codex, err := NewProtocolScope(base, "codex")
	if err != nil {
		t.Fatal(err)
	}
	responses, err := NewProtocolScope(base, "responses")
	if err != nil {
		t.Fatal(err)
	}
	if codex.Equal(responses) || codex.APIType() != "codex" || !codex.Authority().Equal(base) {
		t.Fatal("APIType did not isolate ProtocolScope")
	}
	if !base.CookieAuthority().Equal(responses.Authority().CookieAuthority()) {
		t.Fatal("Cookie authority incorrectly included APIType")
	}
	if !base.CookieAuthority().Authority().Equal(base) {
		t.Fatal("CookieAuthority lost its upstream authority")
	}
	for _, value := range []interface{ MarshalBinary() ([]byte, error) }{base, codex, base.CookieAuthority()} {
		encoded, encodeErr := value.MarshalBinary()
		if encodeErr != nil || len(encoded) == 0 {
			t.Fatalf("MarshalBinary() = %x, %v", encoded, encodeErr)
		}
	}
	blankVendor, err := NewUpstreamAuthority(" ", origin, subject)
	if err != nil || blankVendor.Vendor() != "" {
		t.Fatalf("blank vendor authority = (%#v, %v)", blankVendor, err)
	}
	if _, err := NewUpstreamAuthority("openai", NormalizedOrigin{}, subject); !IsError(err, ErrorInvalidOrigin) {
		t.Fatalf("zero origin error = %v", err)
	}
	if _, err := NewProtocolScope(base, " "); !IsError(err, ErrorInvalidInput) {
		t.Fatalf("blank API type error = %v", err)
	}
	if _, err := (UpstreamAuthority{}).MarshalBinary(); err == nil {
		t.Fatal("zero authority MarshalBinary succeeded")
	}
	if _, err := (ProtocolScope{}).MarshalBinary(); err == nil {
		t.Fatal("zero protocol scope MarshalBinary succeeded")
	}
	if _, err := (CookieAuthority{}).MarshalBinary(); err == nil {
		t.Fatal("zero cookie authority MarshalBinary succeeded")
	}
}

func TestAuthorityEncodingUsesLengthPrefixes(t *testing.T) {
	origin := mustRequestOrigin(t, "https://example.com")
	subject, _ := NewAccountCredentialSubject("c")
	first := mustAuthority(t, "a|b", origin, subject)
	second := mustAuthority(t, "a", origin, subject)
	firstBytes, _ := first.MarshalBinary()
	secondBytes, _ := second.MarshalBinary()
	if bytes.Equal(firstBytes, secondBytes) || !bytes.Contains(firstBytes, []byte(upstreamAuthorityCodec)) {
		t.Fatal("authority encoding is ambiguous or unversioned")
	}
}

func TestAuthorityResolverUsesOneFrozenCredentialSnapshot(t *testing.T) {
	digest := bytes.Repeat([]byte{4}, DigestSize)
	sessionSubject, err := credentialsession.KeyedDigestSubject("h1", digest)
	if err != nil {
		t.Fatal(err)
	}
	route := credentialsession.RouteSnapshot{
		RouteTargetID: "route-a",
		APIType:       "codex",
		VendorScope:   "openai",
		Credential: credentialsession.Snapshot{
			SessionID:  "session-a",
			Kind:       credentialsession.KindAPIKey,
			SecretData: "credential-secret-never-log",
			Version:    7,
			Subject:    sessionSubject,
			AuthState:  credentialsession.AuthState{AccountID: "diagnostic-only"},
		},
	}
	resolver := NewAuthorityResolver()
	target, _ := url.Parse("wss://API.EXAMPLE.COM:443/v1?attempt=1")
	candidate, err := resolver.Resolve(route, "codex", target)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.RouteTargetID() != "route-a" || candidate.CredentialSessionID() != "session-a" || candidate.CredentialVersion() != 7 || candidate.APIType() != "codex" {
		t.Fatalf("candidate accessors = %#v", candidate)
	}
	if candidate.Authority().Origin().String() != "https://api.example.com" || candidate.ProtocolScope().APIType() != "codex" {
		t.Fatalf("candidate scope = %#v", candidate.ProtocolScope())
	}

	// Mutating either the source or one accessor result cannot change the
	// credential snapshot atomically bound into the candidate.
	route.Credential.Subject.Value[0] = 8
	route.Credential.AuthState.AccountID = "mutated"
	first := candidate.Credential()
	first.Subject.Value[1] = 8
	first.AuthState.AccountID = "mutated-again"
	second := candidate.Credential()
	if second.Subject.Value[0] != 4 || second.Subject.Value[1] != 4 || second.AuthState.AccountID != "diagnostic-only" || second.SecretData != "credential-secret-never-log" {
		t.Fatalf("candidate credential was mutable: %#v", second.Subject)
	}

	otherRoute := route
	otherRoute.RouteTargetID = "route-b"
	otherRoute.Credential.Subject = second.Subject.Clone()
	other, err := (AuthorityResolver{}).Resolve(otherRoute, "codex", target)
	if err != nil {
		t.Fatal(err)
	}
	if !candidate.Authority().Equal(other.Authority()) || !candidate.ProtocolScope().Equal(other.ProtocolScope()) {
		t.Fatal("RouteTarget changed security authority")
	}
	if err := candidate.ValidateApplied(mustApplied(t, "openai", candidate.Authority().Origin(), candidate.Authority().Subject())); err != nil {
		t.Fatalf("ValidateApplied(equal) error = %v", err)
	}
	mismatched := mustApplied(t, "other", candidate.Authority().Origin(), candidate.Authority().Subject())
	if err := candidate.ValidateApplied(mismatched); err == nil {
		t.Fatal("ValidateApplied(mismatch) succeeded")
	}
	formatted := []string{fmt.Sprint(candidate), fmt.Sprintf("%#v", candidate)}
	encodedCandidate, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	formatted = append(formatted, string(encodedCandidate))
	for _, output := range formatted {
		if strings.Contains(output, "credential-secret-never-log") || strings.Contains(output, "diagnostic-only") {
			t.Fatalf("candidate formatting leaked credential data: %q", output)
		}
	}
}

func TestAuthorityResolverFailsClosed(t *testing.T) {
	account, _ := credentialsession.AccountSubject("account-1")
	valid := credentialsession.RouteSnapshot{
		RouteTargetID: "route",
		APIType:       "codex",
		VendorScope:   "openai",
		Credential: credentialsession.Snapshot{
			SessionID: "session",
			Kind:      credentialsession.KindChatGPT,
			Version:   1,
			Subject:   account,
			// A conflicting diagnostic AccountID is deliberately ignored: the
			// frozen CredentialSubject is the trusted candidate identity.
			AuthState: credentialsession.AuthState{AccountID: "diagnostic-conflict"},
		},
	}
	target, _ := url.Parse("https://example.com/v1")
	if _, err := (AuthorityResolver{}).Resolve(valid, "codex", target); err != nil {
		t.Fatalf("diagnostic account conflict changed authority: %v", err)
	}

	tests := []struct {
		name    string
		edit    func(*credentialsession.RouteSnapshot)
		apiType string
		want    ErrorKind
	}{
		{name: "route blank", edit: func(r *credentialsession.RouteSnapshot) { r.RouteTargetID = " " }, apiType: "codex", want: ErrorInvalidInput},
		{name: "request API blank", edit: func(*credentialsession.RouteSnapshot) {}, apiType: " ", want: ErrorInvalidInput},
		{name: "snapshot API blank", edit: func(r *credentialsession.RouteSnapshot) { r.APIType = " " }, apiType: "codex", want: ErrorInvalidInput},
		{name: "API conflict", edit: func(*credentialsession.RouteSnapshot) {}, apiType: "responses", want: ErrorSnapshotConflict},
		{name: "session blank", edit: func(r *credentialsession.RouteSnapshot) { r.Credential.SessionID = "" }, apiType: "codex", want: ErrorInvalidInput},
		{name: "version invalid", edit: func(r *credentialsession.RouteSnapshot) { r.Credential.Version = 0 }, apiType: "codex", want: ErrorInvalidInput},
		{name: "kind invalid", edit: func(r *credentialsession.RouteSnapshot) { r.Credential.Kind = "future" }, apiType: "codex", want: ErrorInvalidInput},
		{name: "pending subject", edit: func(r *credentialsession.RouteSnapshot) { r.Credential.Subject = credentialsession.PendingSubject() }, apiType: "codex", want: ErrorSubjectPending},
		{name: "invalid subject", edit: func(r *credentialsession.RouteSnapshot) {
			r.Credential.Subject = credentialsession.Subject{Kind: credentialsession.SubjectAccount}
		}, apiType: "codex", want: ErrorInvalidInput},
		{name: "ChatGPT keyed subject conflict", edit: func(r *credentialsession.RouteSnapshot) {
			digest := bytes.Repeat([]byte{1}, DigestSize)
			r.Credential.Subject, _ = credentialsession.KeyedDigestSubject("h1", digest)
		}, apiType: "codex", want: ErrorSnapshotConflict},
	}
	withoutVendorScope := valid
	withoutVendorScope.VendorScope = ""
	resolvedWithoutVendor, err := (AuthorityResolver{}).Resolve(withoutVendorScope, "codex", target)
	if err != nil || resolvedWithoutVendor.Authority().Vendor() != "" {
		t.Fatalf("Resolve(blank vendor scope) = (%#v, %v)", resolvedWithoutVendor, err)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			route := valid
			route.Credential.Subject = valid.Credential.Subject.Clone()
			test.edit(&route)
			_, err := (AuthorityResolver{}).Resolve(route, test.apiType, target)
			if !IsError(err, test.want) {
				t.Fatalf("Resolve() error = %v, want %q", err, test.want)
			}
		})
	}
	badURL := &url.URL{Scheme: "https", Host: "user@example.com"}
	if _, err := (AuthorityResolver{}).Resolve(valid, "codex", badURL); !IsError(err, ErrorInvalidOrigin) {
		t.Fatalf("Resolve(bad URL) error = %v", err)
	}
}

func TestAppliedIdentityEqualityAndMismatchDimensions(t *testing.T) {
	origin := mustRequestOrigin(t, "https://api.example.com")
	subject, _ := NewAccountCredentialSubject("actual-account-never-log")
	expected := mustAuthority(t, "openai", origin, subject)
	actual, err := NewAppliedIdentity("openai", origin, subject)
	if err != nil || !actual.Matches(expected) || !actual.Equal(actual) || !actual.Authority().Equal(expected) {
		t.Fatalf("NewAppliedIdentity() = %#v, %v", actual, err)
	}
	requestURL, _ := url.Parse("wss://API.EXAMPLE.COM/v1")
	fromRequest, err := AppliedIdentityFromRequest("openai", requestURL, subject)
	if err != nil || !fromRequest.Equal(actual) {
		t.Fatalf("AppliedIdentityFromRequest() = %#v, %v", fromRequest, err)
	}
	if err := ValidateAppliedIdentity(expected, actual); err != nil {
		t.Fatal(err)
	}

	otherSubject, _ := NewAccountCredentialSubject("other-account-never-log")
	tests := []struct {
		name                    string
		identity                AppliedIdentity
		vendor, origin, subject bool
	}{
		{name: "vendor", identity: mustApplied(t, "azure", origin, subject), vendor: true},
		{name: "origin", identity: mustApplied(t, "openai", mustRequestOrigin(t, "https://other.example.com"), subject), origin: true},
		{name: "subject", identity: mustApplied(t, "openai", origin, otherSubject), subject: true},
		{name: "all", identity: mustApplied(t, "azure", mustRequestOrigin(t, "https://other.example.com"), otherSubject), vendor: true, origin: true, subject: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateAppliedIdentity(expected, test.identity)
			var mismatch *AppliedIdentityMismatch
			if !errors.As(err, &mismatch) || mismatch.Vendor != test.vendor || mismatch.Origin != test.origin || mismatch.Subject != test.subject {
				t.Fatalf("mismatch = %#v, %v", mismatch, err)
			}
			if !IsError(err, ErrorAppliedIdentityMismatch) {
				t.Fatalf("mismatch category = %v", err)
			}
			for _, secret := range []string{"actual-account-never-log", "other-account-never-log"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("mismatch error leaked subject: %v", err)
				}
			}
		})
	}
	if _, err := AppliedIdentityFromRequest("openai", &url.URL{Scheme: "ftp", Host: "example.com"}, subject); !IsError(err, ErrorInvalidOrigin) {
		t.Fatalf("AppliedIdentityFromRequest(bad URL) error = %v", err)
	}
}

func TestCompositeIdentityFormattingIsLogSafe(t *testing.T) {
	origin := mustRequestOrigin(t, "https://example.com")
	accountID := "account-id-never-log"
	subject, _ := NewAccountCredentialSubject(accountID)
	authority := mustAuthority(t, "openai", origin, subject)
	scope, _ := NewProtocolScope(authority, "codex")
	applied := mustApplied(t, "openai", origin, subject)
	values := []any{authority, scope, authority.CookieAuthority(), applied}
	for _, value := range values {
		outputs := []string{fmt.Sprint(value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value)}
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		outputs = append(outputs, string(encoded))
		for _, output := range outputs {
			if strings.Contains(output, accountID) {
				t.Fatalf("formatted value leaked subject: %q", output)
			}
		}
	}
}

func mustAuthority(t *testing.T, vendor string, origin NormalizedOrigin, subject CredentialSubject) UpstreamAuthority {
	t.Helper()
	authority, err := NewUpstreamAuthority(vendor, origin, subject)
	if err != nil {
		t.Fatal(err)
	}
	return authority
}

func mustApplied(t *testing.T, vendor string, origin NormalizedOrigin, subject CredentialSubject) AppliedIdentity {
	t.Helper()
	identity, err := NewAppliedIdentity(vendor, origin, subject)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}
