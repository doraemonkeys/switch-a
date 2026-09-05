package proxy

import (
	"bytes"
	"net/http/httptest"
	"testing"

	codexheaders "github.com/doraemonkeys/switch-a/internal/codex/headers"
	"github.com/doraemonkeys/switch-a/internal/requestingress"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

func testClientEvidence(wire, semantic []byte) codexheaders.ClientEvidence {
	if len(wire) == 0 {
		return codexheaders.ClientEvidence{}
	}
	return codexheaders.InspectClientPayload(wire, semantic).ClientEvidence()
}

func newTestIngress(t *testing.T, body []byte) *requestingress.Handle {
	t.Helper()
	request := httptest.NewRequest("POST", "http://localhost", bytes.NewReader(body))
	ingress, err := requestingress.Start(t.Context(), request, requestingress.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ingress.Close() })
	if err := ingress.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	return ingress
}

func newTestBodySource(t *testing.T, body []byte) upstreamtransport.BodySource {
	return &ingressUpload{ingress: newTestIngress(t, body)}
}
