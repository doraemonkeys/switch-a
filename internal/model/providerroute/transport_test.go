package providerroute

import "testing"

func TestTransportSemantics(t *testing.T) {
	for _, tc := range []struct {
		api, transport string
		valid          bool
	}{
		{"codex", HTTP, true}, {"codex", WebSocket, true}, {"codex", "", true},
		{"openai", HTTP, true}, {"claude", HTTP, true}, {"custom", WebSocket, false}, {"codex", "sse", false}, {"codex", "HTTP", false},
	} {
		if got := Valid(tc.api, tc.transport); got != tc.valid {
			t.Errorf("Valid(%q,%q) = %v", tc.api, tc.transport, got)
		}
	}
	if Normalize("") != HTTP || Normalize(WebSocket) != WebSocket {
		t.Fatal("transport normalization")
	}
	if NewKey("codex", "") != NewKey("codex", HTTP) || NewKey("codex", HTTP) == NewKey("codex", WebSocket) {
		t.Fatal("route keys collide")
	}
}
