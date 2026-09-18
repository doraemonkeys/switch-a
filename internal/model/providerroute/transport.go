// Package providerroute identifies independently configured upstream transports.
package providerroute

const (
	HTTP      = "http"
	WebSocket = "websocket"
)

// Normalize gives ordinary HTTP the zero-value semantics used by Go callers.
// Persisted routes and management responses always carry the explicit value.
func Normalize(transport string) string {
	if transport == "" {
		return HTTP
	}
	return transport
}

func Valid(apiType, transport string) bool {
	transport = Normalize(transport)
	return transport == HTTP || apiType == "codex" && transport == WebSocket
}

// Key keeps transport selection separate from account and conversation identity.
type Key struct {
	APIType   string
	Transport string
}

func NewKey(apiType, transport string) Key {
	return Key{APIType: apiType, Transport: Normalize(transport)}
}
