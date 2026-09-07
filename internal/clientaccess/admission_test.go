package clientaccess

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestAdmissionCredentialLocationsAndForwarding(t *testing.T) {
	cases := []struct {
		name, apiType, query string
		headers              http.Header
		allowed              bool
		state, reason        string
	}{
		{name: "missing", state: "absent", reason: "absent"},
		{name: "bearer", headers: http.Header{"Authorization": {"Bearer existing-client-key"}}, allowed: true},
		{name: "mixed case bearer", headers: http.Header{"authorization": {"bEaReR\texisting-client-key "}}, allowed: true},
		{name: "api key", headers: http.Header{"X-Api-Key": {"existing-client-key"}}, allowed: true},
		{name: "matching locations", headers: http.Header{"Authorization": {"Bearer existing-client-key"}, "X-Api-Key": {"existing-client-key"}}, allowed: true},
		{name: "different locations", headers: http.Header{"Authorization": {"Bearer other"}, "X-Api-Key": {"existing-client-key"}}, state: "ambiguous", reason: "ambiguous"},
		{name: "unlisted", headers: http.Header{"Authorization": {"Bearer upstream-key"}}, state: "single", reason: "unlisted"},
		{name: "invalid scheme", headers: http.Header{"Authorization": {"Basic existing-client-key"}}, state: "invalid", reason: "invalid"},
		{name: "duplicate authorization", headers: http.Header{"Authorization": {"Bearer existing-client-key", "Bearer existing-client-key"}}, state: "invalid", reason: "invalid"},
		{name: "gemini header", apiType: "gemini", headers: http.Header{"x-goog-api-key": {"existing-client-key"}}, allowed: true},
		{name: "gemini query", apiType: "gemini", query: "key=existing%2Dclient%2Dkey&model=ignored", allowed: true},
		{name: "gemini matching all", apiType: "gemini", query: "key=existing-client-key", headers: http.Header{"X-Goog-Api-Key": {"existing-client-key"}, "Authorization": {"Bearer existing-client-key"}}, allowed: true},
		{name: "gemini query ignored elsewhere", apiType: "codex", query: "key=existing-client-key", state: "absent", reason: "absent"},
		{name: "gemini header ignored elsewhere", headers: http.Header{"X-Goog-Api-Key": {"existing-client-key"}}, state: "absent", reason: "absent"},
		{name: "gemini conflict", apiType: "gemini", query: "key=other", headers: http.Header{"X-Goog-Api-Key": {"existing-client-key"}}, state: "ambiguous", reason: "ambiguous"},
		{name: "gemini invalid bearer cannot bypass", apiType: "gemini", query: "key=existing-client-key", headers: http.Header{"Authorization": {"Basic value"}}, state: "invalid", reason: "invalid"},
		{name: "gemini ambiguous bearer cannot bypass", apiType: "gemini", query: "key=existing-client-key", headers: http.Header{"Authorization": {"Bearer value"}, "X-Api-Key": {"other"}}, state: "ambiguous", reason: "ambiguous"},
		{name: "duplicate google header", apiType: "gemini", headers: http.Header{"X-Goog-Api-Key": {"existing-client-key"}, "x-goog-api-key": {"existing-client-key"}}, state: "invalid", reason: "invalid"},
		{name: "duplicate google query", apiType: "gemini", query: "key=existing-client-key&key=existing-client-key", state: "invalid", reason: "invalid"},
		{name: "empty google key", apiType: "gemini", query: "key=", state: "invalid", reason: "invalid"},
		{name: "google controls", apiType: "gemini", query: "key=existing-client-key%0A", state: "invalid", reason: "invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "http://gateway/custom//path?"+tc.query, nil)
			r.Header = tc.headers.Clone()
			headerBefore, queryBefore, pathBefore := r.Header.Clone(), r.URL.RawQuery, r.URL.EscapedPath()
			store := &memoryStore{snapshot: Snapshot{Mode: ModeRestricted, Keys: []Key{validKey()}}}
			service := NewService(ServiceConfig{Store: store})
			decision, err := service.Admit(context.Background(), r, tc.apiType)
			if err != nil || decision.Allowed != tc.allowed {
				t.Fatalf("admission %#v %v", decision, err)
			}
			if tc.allowed {
				if decision.KeyID != "client-1" || decision.Reason != ReasonMatched || decision.CredentialState != "single" {
					t.Fatalf("matched %#v", decision)
				}
			} else if decision.Reason != tc.reason || decision.CredentialState != tc.state {
				t.Fatalf("denied %#v", decision)
			}
			if !reflect.DeepEqual(r.Header, headerBefore) || r.URL.RawQuery != queryBefore || r.URL.EscapedPath() != pathBefore {
				t.Fatal("admission changed original forwarding input")
			}
			store.snapshot.Mode = ModePermissive
			decision, err = service.Admit(context.Background(), r, tc.apiType)
			if err != nil || !decision.Allowed || decision.CredentialState != CredentialUnchecked || decision.Reason != ReasonPermissive {
				t.Fatalf("permissive %#v %v", decision, err)
			}
		})
	}
}

func TestAdmissionPolicyChangesAndErrors(t *testing.T) {
	ctx := context.Background()
	m := &memoryStore{snapshot: Snapshot{Mode: ModePermissive}}
	s := NewService(ServiceConfig{Store: m})
	if decision, err := s.Admit(ctx, nil, "codex"); err != nil || !decision.Allowed {
		t.Fatalf("permissive %#v %v", decision, err)
	}
	m.snapshot.Mode = ModeRestricted
	if decision, err := s.Admit(ctx, nil, "codex"); err != nil || decision.Allowed || decision.Reason != "absent" {
		t.Fatalf("restricted %#v %v", decision, err)
	}
	if decision, err := s.Admit(ctx, &http.Request{}, "gemini"); err != nil || decision.Allowed || decision.Reason != "absent" {
		t.Fatalf("nil URL %#v %v", decision, err)
	}
	m.snapshot.Keys = []Key{validKey()}
	r := httptest.NewRequest(http.MethodGet, "http://gateway/responses", nil)
	r.Header.Set("Authorization", "Bearer "+validKey().Key)
	if decision, err := s.Admit(ctx, r, "codex"); err != nil || !decision.Allowed {
		t.Fatalf("allow %#v %v", decision, err)
	}
	m.snapshot.Keys = nil
	if decision, err := s.Admit(ctx, r, "codex"); err != nil || decision.Allowed || decision.Reason != ReasonUnlisted {
		t.Fatalf("revoked %#v %v", decision, err)
	}
	m.err = errStorage
	if decision, err := s.Admit(ctx, r, "codex"); !errors.Is(err, errStorage) || decision.Allowed {
		t.Fatalf("failed policy %#v %v", decision, err)
	}
}
