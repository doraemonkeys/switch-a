package wire

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	disguise "github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
)

const (
	rootThreadID    = "01990c82-10f0-7b21-8ac0-1ba32684b015"
	childThreadID   = "01990c82-21e0-7b21-8ac0-1ba32684b016"
	clientDeviceID  = "b6e8f4e7-cc02-4b6e-85f2-12f599a7c0b1"
	virtualDeviceID = "ea3e1205-3673-40c9-a2cd-bf75163cf734"
)

// Equal values across field names are intentional: root sessions, request
// headers and cache keys can all refer to the same original thread.
func TestDeviceDisguisePreservesConversationGraphAcrossCarriers(t *testing.T) {
	for _, thread := range []string{rootThreadID, childThreadID} {
		t.Run(thread, func(t *testing.T) {
			ctx := context.Background()
			s := NewSession(disguise.TargetSnapshot{
				Policy: disguise.Policy{Enabled: true},
				Login:  disguise.LoginIdentity{DeviceID: virtualDeviceID},
			}, "operation")
			metadata := map[string]string{
				"installation_id": clientDeviceID,
				"thread_id":       thread, "session_id": rootThreadID,
				"turn_id":          "01990c82-31e0-7b21-8ac0-1ba32684b017",
				"parent_thread_id": rootThreadID, "forked_from_thread_id": rootThreadID,
				"parent_turn_id":    "01990c82-20e0-7b21-8ac0-1ba32684b018",
				"root_turn_id":      "01990c82-20e0-7b21-8ac0-1ba32684b018",
				"window_id":         thread + ":003",
				"context_window_id": "01990c82-31e0-7b21-8ac0-1ba32684b019",
			}
			raw, err := json.Marshal(metadata)
			if err != nil {
				t.Fatal(err)
			}
			original := http.Header{
				"Installation-Id": {clientDeviceID},
				"Thread-Id":       {thread}, "Session-Id": {rootThreadID},
				"X-Client-Request-Id":      {thread},
				"X-Codex-Window-Id":        {thread + ":003"},
				"X-Codex-Parent-Thread-Id": {rootThreadID},
				"X-Codex-Turn-Metadata":    {string(raw)},
			}
			saved := original.Clone()
			headers, err := s.Headers(ctx, original)
			if err != nil {
				t.Fatal(err)
			}
			wantHeaders := original.Clone()
			wantHeaders.Set("Installation-Id", virtualDeviceID)
			wantMetadata := strings.Replace(string(raw), clientDeviceID, virtualDeviceID, 1)
			wantHeaders.Set("X-Codex-Turn-Metadata", wantMetadata)
			if !reflect.DeepEqual(headers, wantHeaders) || !reflect.DeepEqual(original, saved) {
				t.Fatalf("headers = %v, want %v; original = %v", headers, wantHeaders, original)
			}
			for _, cache := range []string{rootThreadID, "review:" + rootThreadID, "custom/team:key/v1"} {
				request := []byte(`{ "type":"response.create","thread_id":` + quote(thread) +
					`,"prompt_cache_key":` + quote(cache) +
					`,"client_metadata":{"x-codex-turn-metadata":` + quote(string(raw)) +
					`},"input":[{"installation_id":` + quote(clientDeviceID) + `}] }`)
				want := bytes.Replace(request, []byte(quote(string(raw))), []byte(quote(wantMetadata)), 1)
				for name, transform := range map[string]func(context.Context, []byte) ([]byte, error){
					"http": s.RequestJSON, "websocket": s.ClientFrame,
				} {
					got, err := transform(ctx, request)
					if err != nil || !bytes.Equal(got, want) {
						t.Fatalf("%s: got %s want %s err %v", name, got, want, err)
					}
					response := bytes.Replace(got, []byte("response.create"), []byte("response.completed"), 1)
					wantResponse := bytes.Replace(request, []byte("response.create"), []byte("response.completed"), 1)
					restored, err := s.ServerFrame(ctx, response)
					if err != nil || !bytes.Equal(restored, wantResponse) {
						t.Fatalf("response = %s, err %v", restored, err)
					}
				}
			}
			restored, err := s.RestoreHeaders(ctx, headers)
			if err != nil || !reflect.DeepEqual(restored, original) {
				t.Fatal(restored, err)
			}
			for _, difference := range s.Differences() {
				if !strings.Contains(difference.FieldPath, "installation") &&
					!strings.Contains(strings.ToLower(difference.FieldPath), "installation-id") &&
					!strings.Contains(strings.ToLower(difference.FieldPath), "turn-metadata") {
					t.Fatalf("context identity appeared in disguise differences: %+v", difference)
				}
			}
		})
	}
}

func TestContextFieldsRemainOpaqueToDisguise(t *testing.T) {
	// Forwarding must not coerce opaque IDs or add a disguise-specific type
	// contract to fields owned by another protocol or extension.
	fields := []string{
		"thread_id", "thread-id", "session_id", "session-id",
		"turn_id", "turn-id", "x-codex-turn-id",
		"request_id", "request-id", "x-client-request-id",
		"window_id", "window-id", "x-codex-window-id", "context_window_id",
		"parent_thread_id", "x-codex-parent-thread-id", "forked_from_thread_id",
		"parent_turn_id", "root_turn_id", "prompt_cache_key",
	}
	ctx := context.Background()
	s := NewSession(disguise.TargetSnapshot{Policy: disguise.Policy{Enabled: true}}, "operation")
	headers := make(http.Header)
	for _, field := range fields {
		headers.Set(field, "opaque:not-a-uuid")
	}
	for _, transform := range []func(context.Context, http.Header) (http.Header, error){s.Headers, s.RestoreHeaders} {
		got, err := transform(ctx, headers)
		if err != nil || !reflect.DeepEqual(got, headers) {
			t.Fatal(got, err)
		}
	}
	for _, value := range []string{`"01990c82-10f0-7b21-8ac0-1ba32684b015"`, `"opaque:007"`, `"escaped\/key\u0031"`, `""`, "null", "42", "[]", "{}"} {
		var members []string
		for _, field := range fields {
			members = append(members, quote(field)+":"+value)
		}
		object := "{" + strings.Join(members, ",") + "}"
		for _, document := range []string{
			object,
			`{"client_metadata":` + object + "}",
			`{"x-codex-turn-metadata":` + quote(object) + "}",
			`{"response":{"client_metadata":` + object + "}}",
			`{"type":"response.create","client_metadata":` + object + "}",
			`{"type":"response.completed","response":` + object + "}",
		} {
			for _, transform := range []func(context.Context, []byte) ([]byte, error){s.RequestJSON, s.ResponseJSON, s.ClientFrame, s.ServerFrame} {
				got, err := transform(ctx, []byte(document))
				if err != nil || string(got) != document {
					t.Fatalf("got %s want %s err %v", got, document, err)
				}
			}
		}
	}
	if len(s.Differences()) != 0 {
		t.Fatal(s.Differences())
	}
}

func TestInstallationAliasesAndAbsentIdentity(t *testing.T) {
	ctx := context.Background()
	s := NewSession(disguise.TargetSnapshot{
		Policy: disguise.Policy{Enabled: true},
		Login:  disguise.LoginIdentity{DeviceID: virtualDeviceID},
	}, "operation")
	for _, alias := range []string{"installation_id", "installation-id", "x-codex-installation-id"} {
		header := http.Header{}
		header.Set(alias, clientDeviceID)
		got, err := s.Headers(ctx, header)
		if err != nil || got.Get(alias) != virtualDeviceID {
			t.Fatal(got, err)
		}
		body := []byte("{" + quote(alias) + ":" + quote(clientDeviceID) + "}")
		derived, err := s.RequestJSON(ctx, body)
		if err != nil || string(derived) != strings.Replace(string(body), clientDeviceID, virtualDeviceID, 1) {
			t.Fatal(string(derived), err)
		}
		restored, err := s.ResponseJSON(ctx, derived)
		if err != nil || !bytes.Equal(restored, body) {
			t.Fatal(string(restored), err)
		}
	}
	for _, body := range []string{"{}", `{"installation_id":null}`, `{"installation_id":""}`} {
		got, err := s.RequestJSON(ctx, []byte(body))
		if err != nil || string(got) != body {
			t.Fatal(string(got), err)
		}
	}
	got, err := s.Headers(ctx, http.Header{})
	if err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
	unknown, err := s.ResponseJSON(ctx, []byte(`{"installation_id":"unobserved-device"}`))
	if err != nil || string(unknown) != `{"installation_id":"unobserved-device"}` {
		t.Fatal(string(unknown), err)
	}
}
