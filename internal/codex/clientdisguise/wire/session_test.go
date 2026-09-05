package wire

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	disguise "github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
)

type memoryMapper struct {
	mu     sync.Mutex
	values map[disguise.MappingKey]string
	fail   error
}

func (m *memoryMapper) MapIdentity(_ context.Context, key disguise.MappingKey) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return "", m.fail
	}
	if m.values == nil {
		m.values = make(map[disguise.MappingKey]string)
	}
	if value, ok := m.values[key]; ok {
		return value, nil
	}
	value := fmt.Sprintf("mapped-%s-%s-%s-%s", key.GenerationID, key.ClientIdentityID, key.Namespace, key.Original)
	m.values[key] = value
	return value, nil
}
func (m *memoryMapper) RestoreIdentity(_ context.Context, generation, client, namespace, value string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return "", false, m.fail
	}
	for key, mapped := range m.values {
		if key.GenerationID == generation && key.ClientIdentityID == client && key.Namespace == namespace && mapped == value {
			return key.Original, true, nil
		}
	}
	return "", false, nil
}
func testSession() *Session {
	return NewSession(&memoryMapper{}, disguise.TargetSnapshot{
		Policy:  disguise.Policy{Enabled: true},
		Login:   disguise.LoginIdentity{GenerationID: "login", DeviceID: "device"},
		Binding: disguise.ProfileBinding{TelemetryPathMappings: map[string]string{"/original": "/telemetry"}},
		Profile: disguise.ProfileRevision{Features: disguise.Features{UserAgent: "sample-agent", Originator: "sample-origin", Headers: map[string]string{"Cookie": "forbidden", "Accept-Encoding": "br", "Version": "1.2.3"}}},
	}, "client", "operation")
}
func TestIdentityAcrossCarriersAndProtocolOnlyInverse(t *testing.T) {
	s := testSession()
	ctx := context.Background()
	original := http.Header{"Thread-Id": {"thread"}, "Session-Id": {"session"}, "X-Codex-Window-Id": {"thread:17"}, "X-Client-Request-Id": {"req"}, "Installation_id": {"install"}, "Accept-Encoding": {"gzip"}, "Cookie": {"client=1"}, "Chatgpt-Account-Id": {"account"}, "X-Codex-Turn-State": {"opaque"}}
	saved := original.Clone()
	headers, err := s.Headers(ctx, original)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, saved) {
		t.Fatal("headers mutated")
	}
	thread := headers.Get("Thread-Id")
	if thread == "thread" || headers.Get("X-Codex-Window-Id") != thread+":17" || headers.Get("User-Agent") != "sample-agent" || headers.Get("Originator") != "sample-origin" || headers.Get("Accept-Encoding") != "gzip" || headers.Get("Cookie") != "client=1" {
		t.Fatal(headers)
	}
	metadata := `{"installation_id":"install","thread_id":"thread","turn_id":"","cwd":"/original","nested":{"thread_id":"prompt"}}`
	input := []byte(`{ "type":"response.create","client_metadata":{"thread_id":"thread","session_id":"session","x-codex-window-id":"thread:17","x-codex-turn-metadata":` + quote(metadata) + `},"turn_id":null,"input":[{"thread_id":"prompt","text":"thread"}],"prompt_cache_key":"cache","unknown":1.2300e+2 }`)
	derived, err := s.ClientFrame(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(derived, []byte(`"thread_id":`+quote(thread))) || !bytes.Contains(derived, []byte(`"turn_id":null`)) || !bytes.Contains(derived, []byte(`"prompt_cache_key":"cache"`)) || !bytes.Contains(derived, []byte(`"unknown":1.2300e+2 }`)) {
		t.Fatalf("%s", derived)
	}
	if !bytes.Contains(derived, []byte(`"input":[{"thread_id":"prompt","text":"thread"}]`)) {
		t.Fatal(string(derived))
	}
	response := bytes.Replace(derived, []byte("response.create"), []byte("response.created"), 1)
	restored, err := s.ServerFrame(ctx, response)
	if err != nil {
		t.Fatal(err)
	}
	expected := bytes.Replace(input, []byte("response.create"), []byte("response.created"), 1)
	if !bytes.Equal(restored, expected) {
		t.Fatalf("restoration mismatch\ngot %s\nwant %s", restored, expected)
	}
	restoredHeaders, err := s.RestoreHeaders(ctx, headers)
	if err != nil {
		t.Fatal(err)
	}
	if restoredHeaders.Get("Thread-Id") != "thread" || restoredHeaders["Installation_id"][0] != "install" || restoredHeaders.Get("Chatgpt-Account-Id") != "account" {
		t.Fatal(restoredHeaders)
	}
	if len(s.Differences()) == 0 {
		t.Fatal("missing evidence")
	}
	before := len(s.Differences())
	_, _ = s.Headers(ctx, original)
	if len(s.Differences()) != before {
		t.Fatal("replay duplicated evidence")
	}
}
func quote(value string) string { raw, _ := json.Marshal(value); return string(raw) }
func TestUnknownAndEmptyProtocolPreservation(t *testing.T) {
	inputs := []string{
		`{"type":"future.event","client_metadata":{"thread_id":7}}`,
		`{"type":"future.event","broken":`,
		`opaque payload`, `[]`, `null`, ` `,
		`{"type":"response.create","turn_id":"","client_metadata":{"thread_id":null,"session_id":"","Thread_Id":"unknown-case"},"metadata":{"thread_id":"business"},"response.client_metadata":{"thread_id":"business"}}`,
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			s := testSession()
			got, err := s.ClientFrame(context.Background(), []byte(input))
			if err != nil || string(got) != input {
				t.Fatalf("got %q err %v", got, err)
			}
		})
	}
	for _, input := range []string{`{}`, `[1, true, false, null, -0.002E-10]`, `{"a\u0062":"\u1234\n\/\\\"","unknown":1e99}`, `{"` + strings.Repeat("x", maxCapturedKey+1) + `":"unchanged"}`} {
		got, err := testSession().RequestJSON(context.Background(), []byte(input))
		if err != nil || string(got) != input {
			t.Fatalf("got %q err %v", got, err)
		}
	}
}
func TestKnownInvalidJSONFailsWithStableDiagnostic(t *testing.T) {
	malformed := []string{`{"thread_id":1}`, `{"thread_id":[]}`, `{"thread_id":tru}`, `{"a":"\q"}`, `{"a":"\u0X00"}`, `{"a":"` + "\n" + `"}`, `{"a" 1}`, `{"a":1 "b":2}`, `[1 2]`, `{"a":01}`, `{"a":1.}`, `{"a":1e}`, `{"a":--1}`, `{"a":+1}`, `{"a":nulL}`, `{} trailing`, `{"a":`, `{"type":"response.create","a":`}
	for _, input := range malformed {
		t.Run(input, func(t *testing.T) {
			s := testSession()
			_, err := s.RequestJSON(context.Background(), []byte(input))
			var failure *Failure
			if !errors.As(err, &failure) || failure.OperationID != "operation" || failure.DiagnosticID == "" || s.Failure() != failure || failure.Unwrap() == nil {
				t.Fatalf("%T %v", err, err)
			}
			if !strings.Contains(err.Error(), failure.DiagnosticID) {
				t.Fatal(err)
			}
			_, next := s.RequestJSON(context.Background(), []byte(`{"thread_id":false}`))
			if next != err {
				t.Fatal("diagnostic changed")
			}
		})
	}
}
func TestMappingFailureAndFrozenSnapshots(t *testing.T) {
	s := testSession()
	cause := errors.New("database mapping failure")
	s.mapper.(*memoryMapper).fail = cause
	_, err := s.Headers(context.Background(), http.Header{"Thread-Id": {"thread"}})
	var failure *Failure
	if !errors.As(err, &failure) || !errors.Is(err, cause) || failure.Stage != "mapping" || failure.OriginalSnippet != "thread" {
		t.Fatal(err)
	}
	s = testSession()
	s.mapper = nil
	_, err = s.RequestJSON(context.Background(), []byte(`{"thread_id":"a"}`))
	if err == nil {
		t.Fatal("missing mapper")
	}
	s = testSession()
	s.target.Login.DeviceID = ""
	_, err = s.Headers(context.Background(), http.Header{"Installation-Id": {"a"}})
	if err == nil {
		t.Fatal("missing device")
	}
	target := testSession().target
	session := NewSession(&memoryMapper{}, target, "client", "op")
	target.Profile.Features.Headers["Version"] = "changed"
	target.Binding.TelemetryPathMappings["/original"] = "/changed"
	headers, err := session.Headers(context.Background(), nil)
	if err != nil || headers.Get("Version") != "1.2.3" {
		t.Fatal(headers, err)
	}
	s = testSession()
	s.target.Binding.RemapCacheKeys = true
	got, err := s.RequestJSON(context.Background(), []byte(`{"prompt_cache_key":"a","window_id":"opaque","turn_id":"turn"}`))
	if err != nil || bytes.Contains(got, []byte(`"prompt_cache_key":"a"`)) {
		t.Fatal(string(got), err)
	}
	restored, err := s.ResponseJSON(context.Background(), got)
	if err != nil || string(restored) != `{"prompt_cache_key":"a","window_id":"opaque","turn_id":"turn"}` {
		t.Fatal(string(restored), err)
	}
	unknown, err := s.ResponseJSON(context.Background(), []byte(`{"thread_id":"unknown"}`))
	if err != nil || string(unknown) != `{"thread_id":"unknown"}` {
		t.Fatal(string(unknown), err)
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }
func TestStreamingFailureAndCancellation(t *testing.T) {
	s := testSession()
	cause := errors.New("sink failed")
	err := s.RestoreStream(context.Background(), strings.NewReader(`{"a":1}`), failingWriter{cause}, "application/json")
	if !errors.Is(err, cause) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s = testSession()
	err = s.RestoreStream(ctx, strings.NewReader(`{"a":1}`), io.Discard, "application/json")
	if !errors.Is(err, context.Canceled) || s.Failure() != nil {
		t.Fatal(err, s.Failure())
	}
	s = testSession()
	s.target.Policy.Enabled = false
	original := []byte("not JSON")
	got, err := s.ClientFrame(ctx, original)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err = s.RestoreStream(ctx, bytes.NewReader(original), &output, "application/json"); err != nil || output.String() != "not JSON" {
		t.Fatal(err)
	}
}
