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
		original := []byte(`{"type":"response.create","response":{"installation_id":"business","client_metadata":{"installation_id":"business-extension","turn_id":42}}}`)
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
	header, err := s.Headers(context.Background(), http.Header{"Installation-Id": {"install"}})
	if err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"response":{"installation_id":` + quote(header.Get("Installation-Id")) + `,"client_metadata":{"installation_id":` + quote(header.Get("Installation-Id")) + `}}}`)
	derived, err := s.ResponseJSON(context.Background(), original)
	if err != nil || string(derived) != `{"response":{"installation_id":"install","client_metadata":{"installation_id":"install"}}}` {
		t.Fatal(string(derived), err)
	}
}

func TestBodylessResponsesRetainRepresentationMetadata(t *testing.T) {
	for _, scenario := range []struct {
		method string
		status int
	}{{http.MethodGet, http.StatusNotModified}, {http.MethodHead, http.StatusOK}, {http.MethodGet, http.StatusNoContent}, {http.MethodGet, http.StatusResetContent}, {http.MethodGet, http.StatusEarlyHints}, {http.MethodConnect, http.StatusOK}} {
		s := testSession()
		mapped, err := s.Headers(context.Background(), http.Header{"Installation-Id": {"install"}})
		if err != nil {
			t.Fatal(err)
		}
		head := upstreamtransport.ResponseHead{RequestMethod: scenario.method, StatusCode: scenario.status, ContentLength: 128, Header: http.Header{
			"Content-Type": {"application/json"}, "Content-Encoding": {"gzip"}, "Content-Length": {"128"}, "Etag": {"original-representation"}, "Installation-Id": {mapped.Get("Installation-Id")},
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
		if derived.ContentLength != 128 || derived.Header.Get("Content-Encoding") != "gzip" || derived.Header.Get("Content-Length") != "128" || derived.Header.Get("Etag") != "original-representation" || derived.Header.Get("Installation-Id") != "install" {
			t.Fatalf("%+v: %+v", scenario, derived)
		}
		if !reflect.DeepEqual(original, head.Header) {
			t.Fatal("original metadata mutated")
		}
	}
}
