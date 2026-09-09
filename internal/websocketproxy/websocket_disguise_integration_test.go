package websocketproxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/doraemonkeys/switch-a/internal/model"
	"go.uber.org/zap"
)

func TestDisguiseGatewayPhysicalHandshakeAndResponseRestoration(t *testing.T) {
	const threadID = "01990c82-10f0-7b21-8ac0-1ba32684b015"
	const deviceID = "device-physical-codex-credential"
	repository := &testDisguiseRepository{revision: "physical"}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "profile-physical" {
			t.Errorf("physical profile = %q", got)
		}
		if got := r.Header.Get("Accept-Encoding"); got != "identity" {
			t.Errorf("explicit encoding changed: %q", got)
		}
		for _, field := range []string{"Thread-Id", "Session-Id", "X-Client-Request-Id"} {
			if r.Header.Get(field) != threadID {
				t.Errorf("%s changed: %q", field, r.Header.Get(field))
			}
		}
		if r.Header.Get("Installation-Id") != deviceID || r.Header.Get("X-Codex-Window-Id") != threadID+":3" {
			t.Errorf("device/window headers = %v", r.Header)
		}
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.CloseNow()
		_, frame, err := connection.Read(r.Context())
		if err != nil {
			t.Error(err)
			return
		}
		var request struct {
			Installation string `json:"installation_id"`
			Thread       string `json:"thread_id"`
			Session      string `json:"session_id"`
			Cache        string `json:"prompt_cache_key"`
			Turn         string `json:"turn_id"`
			Input        string `json:"input"`
		}
		if err := json.Unmarshal(frame, &request); err != nil {
			t.Error(err)
			return
		}
		if request.Installation != deviceID || request.Thread != threadID || request.Session != threadID || request.Cache != threadID || request.Turn != "original-turn" || request.Input != "original-turn" {
			t.Errorf("physical frame changed wrong fields: %s", frame)
		}
		response, _ := json.Marshal(map[string]any{"type": "response.created", "response": map[string]any{"id": "original-response", "installation_id": request.Installation, "turn_id": request.Turn, "output": request.Installation}})
		if err := connection.Write(r.Context(), websocket.MessageText, response); err != nil {
			t.Error(err)
			return
		}
		_ = connection.Close(websocket.StatusNormalClosure, "complete")
	}))
	defer upstream.Close()
	provider := testDisguiseProvider("physical")
	provider.APITypes[0].BaseURL = upstream.URL
	store := newMockStore()
	store.providers = []model.Provider{provider}
	gateway := newTestGateway(t, Config{Store: store, Disguise: repository, Logger: zap.NewNop()})
	server := newGatewayIntegrationServer(gateway, RequestConfig{GlobalAuthMode: "bearer", GlobalMaxAttempts: 1}, "physical-disguise-request")
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	options := codexDialOptions()
	options.HTTPHeader.Set("User-Agent", "codex/1.0.0 (Windows; amd64)")
	options.HTTPHeader.Set("Accept-Encoding", "identity")
	options.HTTPHeader.Set("Installation-Id", "original-device")
	options.HTTPHeader.Set("X-Codex-Window-Id", threadID+":3")
	for _, field := range []string{"Thread-Id", "Session-Id", "X-Client-Request-Id"} {
		options.HTTPHeader.Set(field, threadID)
	}
	connection, _, err := websocket.Dial(ctx, wsURL(server)+"/responses?model=gpt-5", options)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	request := `{"type":"response.create","installation_id":"original-device","thread_id":"` + threadID + `","session_id":"` + threadID + `","prompt_cache_key":"` + threadID + `","turn_id":"original-turn","input":"original-turn"}`
	if err := connection.Write(ctx, websocket.MessageText, []byte(request)); err != nil {
		t.Fatal(err)
	}
	_, payload, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Response struct {
			Installation string `json:"installation_id"`
			ID           string `json:"id"`
			Turn         string `json:"turn_id"`
			Output       string `json:"output"`
		} `json:"response"`
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	if response.Response.Installation != "original-device" || response.Response.Turn != "original-turn" || response.Response.ID != "original-response" || response.Response.Output != deviceID {
		t.Fatalf("restored response changed wrong fields: %s", payload)
	}
}
