package codexhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
)

func TestHTTPRouteTargetPreferenceUsesAbsorbingSharedFold(t *testing.T) {
	clientScope := testClientScope(t, "route-preference-client")
	candidate, _ := testCandidate(t, "physical-route", "provider.test", "route-preference-subject")
	tests := []struct {
		name  string
		hints map[codexcontinuity.Kind]string
		want  string
	}{
		{
			name: "consistent",
			hints: map[codexcontinuity.Kind]string{
				codexcontinuity.KindThreadID:  "route-a",
				codexcontinuity.KindSessionID: "route-a",
			},
			want: "route-a",
		},
		{
			name: "empty is no evidence",
			hints: map[codexcontinuity.Kind]string{
				codexcontinuity.KindThreadID:  "",
				codexcontinuity.KindSessionID: "route-a",
			},
			want: "route-a",
		},
		{
			name: "A B A remains conflicted",
			hints: map[codexcontinuity.Kind]string{
				codexcontinuity.KindThreadID:       "route-a",
				codexcontinuity.KindSessionID:      "route-b",
				codexcontinuity.KindConversationID: "route-a",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for range 100 {
				continuity := &continuityRecorder{resolve: func(request codexcontinuity.ResolveRequest) (codexcontinuity.Binding, error) {
					return codexcontinuity.Binding{Kind: request.Evidence.Kind, Owner: codexcontinuity.Owner{
						ClientScope:     clientScope,
						ProtocolScope:   candidate.ProtocolScope(),
						RouteTargetHint: test.hints[request.Evidence.Kind],
					}}, nil
				}}
				runtime := newAlwaysOnTestRuntime(t, Config{
					ClientScopes: testScopeDigester{current: clientScope, candidates: []codexidentity.ClientScope{clientScope}},
					Continuity:   continuity,
				})
				request := httptest.NewRequest(http.MethodPost, "http://gateway.test/codex/v1/responses", nil)
				request.Header.Set("Authorization", "Bearer client-secret")
				for kind := range test.hints {
					request.Header.Set(ownerHeader(kind), string(kind)+"-value")
				}
				operation, err := runtime.Begin(context.Background(), request, codexAPIType, "route-preference", "preserve_conversation", testClientEvidence(nil, nil))
				if err != nil {
					t.Fatal(err)
				}
				authority, route := operation.RequiredAuthority()
				if authority == nil || !authority.Equal(candidate.Authority()) || route != test.want {
					t.Fatalf("authority/route = (%v, %q), want (%v, %q)", authority, route, candidate.Authority(), test.want)
				}
				operation.Discard()
			}
		})
	}
}

func TestHTTPBeginPreservesContinuityRootCause(t *testing.T) {
	clientScope := testClientScope(t, "root-cause-client")
	for _, test := range []struct {
		kind         codexcontinuity.ErrorKind
		wantHTTPKind ErrorKind
	}{
		{kind: codexcontinuity.ErrorUnknown, wantHTTPKind: ErrorClientInput},
		{kind: codexcontinuity.ErrorExpired, wantHTTPKind: ErrorClientInput},
		{kind: codexcontinuity.ErrorConflict, wantHTTPKind: ErrorClientInput},
		{kind: codexcontinuity.ErrorUnavailable, wantHTTPKind: ErrorDependencyUnavailable},
	} {
		t.Run(string(test.kind), func(t *testing.T) {
			continuity := &continuityRecorder{resolveErr: &codexcontinuity.Error{Kind: test.kind}}
			runtime := newAlwaysOnTestRuntime(t, Config{
				ClientScopes: testScopeDigester{current: clientScope, candidates: []codexidentity.ClientScope{clientScope}},
				Continuity:   continuity,
			})
			request := httptest.NewRequest(http.MethodPost, "http://gateway.test/codex/v1/responses", nil)
			request.Header.Set("Authorization", "Bearer client-secret")
			request.Header.Set("X-Codex-Turn-State", "state")
			_, err := runtime.Begin(context.Background(), request, codexAPIType, "root-cause", "preserve_conversation", testClientEvidence(nil, nil))
			if !IsKind(err, test.wantHTTPKind) || !codexcontinuity.IsError(err, test.kind) {
				t.Fatalf("error = %v, want HTTP kind %q with continuity kind %q", err, test.wantHTTPKind, test.kind)
			}
		})
	}
}

func ownerHeader(kind codexcontinuity.Kind) string {
	switch kind {
	case codexcontinuity.KindThreadID:
		return "Thread-Id"
	case codexcontinuity.KindSessionID:
		return "Session-Id"
	case codexcontinuity.KindConversationID:
		return "Conversation_id"
	default:
		return "X-Codex-Window-Id"
	}
}
