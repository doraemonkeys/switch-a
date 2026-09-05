package codexws

import (
	"context"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
)

type routeHintContinuity struct {
	Continuity
	owner codexcontinuity.Owner
	hints map[codexcontinuity.Kind]string
}

func (c *routeHintContinuity) ResolveOwner(
	_ context.Context,
	request codexcontinuity.ResolveRequest,
) (codexcontinuity.Binding, error) {
	owner := c.owner
	owner.RouteTargetHint = c.hints[request.Evidence.Kind]
	return codexcontinuity.Binding{Kind: request.Evidence.Kind, Owner: owner}, nil
}

func TestWebSocketRouteTargetPreferenceMatchesAbsorbingHTTPFold(t *testing.T) {
	clientScope := testClientScope(t, "route-preference-client")
	candidate, _ := testCandidate(t, "physical-route", "https://provider.test/v1")
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
				continuity := &routeHintContinuity{
					Continuity: newTestContinuity(t),
					owner: codexcontinuity.Owner{
						ClientScope:   clientScope,
						ProtocolScope: candidate.ProtocolScope(),
					},
					hints: test.hints,
				}
				runtime := testRuntime(t, continuity)
				request := testRequest("route-preference-client")
				for kind := range test.hints {
					request.Header.Set(webSocketOwnerHeader(kind), string(kind)+"-value")
				}
				operation, err := runtime.Begin(context.Background(), request, codexAPIType, "route-preference", "")
				if err != nil {
					t.Fatal(err)
				}
				authority, route := operation.RequiredAuthority()
				if authority == nil || !authority.Equal(candidate.Authority()) || route != test.want {
					t.Fatalf("authority/route = (%v, %q), want (%v, %q)", authority, route, candidate.Authority(), test.want)
				}
				operation.DiscardCookies()
			}
		})
	}
}

func TestWebSocketRouteConflictRemainsAbsorbingAcrossFrameObservations(t *testing.T) {
	clientScope := testClientScope(t, "route-sequence-client")
	candidate, _ := testCandidate(t, "physical-route", "https://provider.test/v1")
	continuity := &routeHintContinuity{
		Continuity: newTestContinuity(t),
		owner: codexcontinuity.Owner{
			ClientScope:   clientScope,
			ProtocolScope: candidate.ProtocolScope(),
		},
		hints: map[codexcontinuity.Kind]string{
			codexcontinuity.KindThreadID:  "route-a",
			codexcontinuity.KindSessionID: "route-b",
			codexcontinuity.KindWindowID:  "route-a",
		},
	}
	runtime := testRuntime(t, continuity)
	request := testRequest("route-sequence-client")
	request.Header.Set("Thread-Id", "thread-a")
	op, err := runtime.Begin(context.Background(), request, codexAPIType, "route-sequence", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, route := op.RequiredAuthority(); route != "route-a" {
		t.Fatalf("initial route = %q", route)
	}
	if err := op.InspectBootstrapFrame(context.Background(), true, []byte(
		`{"type":"response.create","client_metadata":{"session_id":"session-b"}}`,
	)); err != nil {
		t.Fatal(err)
	}
	if _, route := op.RequiredAuthority(); route != "" {
		t.Fatalf("conflicted route = %q", route)
	}
	if err := op.InspectBootstrapFrame(context.Background(), true, []byte(
		`{"type":"response.create","client_metadata":{"x-codex-window-id":"window-a"}}`,
	)); err != nil {
		t.Fatal(err)
	}
	if _, route := op.RequiredAuthority(); route != "" {
		t.Fatalf("later route evidence restored conflicted preference: %q", route)
	}
	op.DiscardCookies()
}

func webSocketOwnerHeader(kind codexcontinuity.Kind) string {
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
