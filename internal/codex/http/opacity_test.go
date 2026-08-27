package codexhttp

import (
	"bytes"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/headers"
)

func TestOpaqueHTTPBodiesPreserveWireBytesAndCreateNoOwnerEvidence(t *testing.T) {
	tests := []struct {
		name     string
		wire     []byte
		semantic []byte
	}{
		{name: "non JSON", wire: []byte("future wire format"), semantic: []byte("future wire format")},
		{name: "JSON non-object", wire: []byte("[1,2,3]"), semantic: []byte("[1,2,3]")},
		{
			name:     "unknown event with malformed known-looking fields",
			wire:     []byte(`{"type":"future.event","client_metadata":{"thread_id":null},"previous_response_id":7}`),
			semantic: []byte(`{"type":"future.event","client_metadata":{"thread_id":null},"previous_response_id":7}`),
		},
		{name: "semantic decode failure", wire: []byte{0x1f, 0x8b, 0x08, 0x00}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := append([]byte(nil), test.wire...)
			view, result := discoverClientEvidence(nil, test.wire, test.semantic)
			if view.Recognized() || view.EventType() != "" {
				t.Fatalf("opaque body recognized as %q", view.EventType())
			}
			if result.Outcome() != codexheaders.ActionForward || len(result.Decisions()) != 0 {
				t.Fatalf("opaque body decisions = %#v", result.Decisions())
			}
			if !bytes.Equal(test.wire, before) || !bytes.Equal(result.ReplayBytes(), test.wire) {
				t.Fatal("opaque HTTP body bytes changed")
			}
			if len(test.wire) > 0 && &result.ReplayBytes()[0] != &test.wire[0] {
				t.Fatal("opaque HTTP body did not retain the caller-owned wire buffer")
			}
		})
	}
}

func TestRecognizedInvalidHTTPControlFieldStillRejects(t *testing.T) {
	body := []byte(`{"type":"response.create","previous_response_id":null}`)
	view, result := discoverClientEvidence(nil, body, body)
	if !view.Recognized() || view.EventType() != "response.create" || !result.Rejected() {
		t.Fatalf("recognized invalid body view=%#v decisions=%#v", view, result.Decisions())
	}
	if !bytes.Equal(result.ReplayBytes(), body) {
		t.Fatal("rejected recognized body bytes changed")
	}
}
