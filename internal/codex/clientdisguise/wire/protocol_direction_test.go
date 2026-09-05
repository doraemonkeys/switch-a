package wire

import (
	"context"
	"io"
	"net/http"
	"reflect"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

func TestRequestResponseExtensionRemainsOpaque(t *testing.T) {
	for _, websocket := range []bool{false, true} {
		s := testSession()
		original := []byte(`{"type":"response.create","response":{"thread_id":"business","client_metadata":{"thread_id":"business-extension","turn_id":42}}}`)
		var derived []byte
		var err error
		if websocket {
			derived, err = s.ClientFrame(context.Background(), original)
		} else {
			derived, err = s.RequestJSON(context.Background(), original)
		}
		if err != nil || string(derived) != string(original) {
			t.Fatalf("websocket=%v got %s err %v", websocket, derived, err)
		}
		if len(s.Differences()) != 0 {
			t.Fatal("unknown request extension produced mapping evidence")
		}
	}
	s := testSession()
	header, err := s.Headers(context.Background(), http.Header{"Thread-Id": {"thread"}})
	if err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"response":{"thread_id":` + quote(header.Get("Thread-Id")) + `,"client_metadata":{"thread_id":` + quote(header.Get("Thread-Id")) + `}}}`)
	derived, err := s.ResponseJSON(context.Background(), original)
	if err != nil || string(derived) != `{"response":{"thread_id":"thread","client_metadata":{"thread_id":"thread"}}}` {
		t.Fatal(string(derived), err)
	}
}

func TestBodylessResponsesRetainRepresentationMetadata(t *testing.T) {
	for _, scenario := range []struct {
		method string
		status int
	}{{http.MethodGet, http.StatusNotModified}, {http.MethodHead, http.StatusOK}, {http.MethodGet, http.StatusNoContent}, {http.MethodGet, http.StatusResetContent}, {http.MethodGet, http.StatusEarlyHints}, {http.MethodConnect, http.StatusOK}} {
		s := testSession()
		mapped, err := s.Headers(context.Background(), http.Header{"Thread-Id": {"thread"}})
		if err != nil {
			t.Fatal(err)
		}
		head := upstreamtransport.ResponseHead{RequestMethod: scenario.method, StatusCode: scenario.status, ContentLength: 128, Header: http.Header{
			"Content-Type": {"application/json"}, "Content-Encoding": {"gzip"}, "Content-Length": {"128"}, "Etag": {"original-representation"}, "Thread-Id": {mapped.Get("Thread-Id")},
		}}
		original := head.Header.Clone()
		derived, body, err := s.RestoreResponse(context.Background(), head, http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := io.ReadAll(body)
		_ = body.Close()
		if err != nil || len(payload) != 0 || body != http.NoBody || s.Failure() != nil {
			t.Fatalf("%+v body %q err %v failure %v", scenario, payload, err, s.Failure())
		}
		if derived.ContentLength != 128 || derived.Header.Get("Content-Encoding") != "gzip" || derived.Header.Get("Content-Length") != "128" || derived.Header.Get("Etag") != "original-representation" || derived.Header.Get("Thread-Id") != "thread" {
			t.Fatalf("%+v: %+v", scenario, derived)
		}
		if !reflect.DeepEqual(original, head.Header) {
			t.Fatal("original metadata mutated")
		}
	}
}
