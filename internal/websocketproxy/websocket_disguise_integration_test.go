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
	repository := &testDisguiseRepository{revision: "physical"}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "profile-physical" {
			t.Errorf("physical profile = %q", got)
		}
		if got := r.Header.Get("Accept-Encoding"); got != "identity" {
			t.Errorf("explicit encoding changed: %q", got)
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
			Turn  string `json:"turn_id"`
			Input string `json:"input"`
		}
		if err := json.Unmarshal(frame, &request); err != nil {
			t.Error(err)
			return
		}
		if request.Turn == "original-turn" || request.Turn == "" || request.Input != "original-turn" {
			t.Errorf("physical frame changed wrong fields: %s", frame)
		}
		response, _ := json.Marshal(map[string]any{"type": "response.created", "response": map[string]any{"id": "original-response", "turn_id": request.Turn, "output": request.Turn}})
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
	connection, _, err := websocket.Dial(ctx, wsURL(server)+"/responses?model=gpt-5", options)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	if err := connection.Write(ctx, websocket.MessageText, []byte(`{"type":"response.create","turn_id":"original-turn","input":"original-turn"}`)); err != nil {
		t.Fatal(err)
	}
	_, payload, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Response struct {
			ID     string `json:"id"`
			Turn   string `json:"turn_id"`
			Output string `json:"output"`
		} `json:"response"`
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	if response.Response.Turn != "original-turn" || response.Response.ID != "original-response" || response.Response.Output == "original-turn" {
		t.Fatalf("restored response changed wrong fields: %s", payload)
	}
}
