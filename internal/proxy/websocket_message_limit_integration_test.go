package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/doraemonkeys/switch-a/internal/defaults"
	"github.com/doraemonkeys/switch-a/internal/model"
	"go.uber.org/zap"
)

type messageLimitStore struct {
	*mockStore
	sizeMiB atomic.Int64
}

func (s *messageLimitStore) GetConfig(ctx context.Context, key string) (string, error) {
	if key == defaults.ConfigKeyWebSocketMaxMessageSizeMiB {
		if size := s.sizeMiB.Load(); size > 0 {
			return strconv.FormatInt(size, 10), nil
		}
		return "", nil
	}
	return s.mockStore.GetConfig(ctx, key)
}

func TestWebSocketMessageLimitRuntime(t *testing.T) {
	const oldLimitMiB = 16
	const responseCommand = "send-large-response"
	largeResponse := bytes.Repeat([]byte("r"), int(2*defaults.BytesPerMiB))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		conn.SetReadLimit(-1)
		for {
			kind, payload, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			if string(payload) == responseCommand {
				payload = largeResponse
			}
			if err := conn.Write(r.Context(), kind, payload); err != nil {
				return
			}
		}
	}))
	t.Cleanup(upstream.Close)

	st := &messageLimitStore{mockStore: newMockStore()}
	st.providers = []model.Provider{withTestStaticCredential(model.Provider{
		ID: "message-limit", Name: "Message Limit", Enabled: true, AuthMode: "bearer",
		APITypes: []model.ProviderAPIType{{ProviderID: "message-limit", APIType: "codex", BaseURL: upstream.URL}},
	}, "", "ws-key")}
	handler := newProxyCodexTestHandler(t, Config{Store: st, Logger: zap.NewNop()})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	connect := func(path string) *websocket.Conn {
		t.Helper()
		conn, _, err := websocket.Dial(ctx, wsURL(server)+path, proxyCodexDialOptions())
		if err != nil {
			t.Fatal(err)
		}
		conn.SetReadLimit(-1)
		t.Cleanup(func() { _ = conn.CloseNow() })
		return conn
	}
	roundTrip := func(conn *websocket.Conn, payload, want []byte) {
		t.Helper()
		if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
			t.Fatal(err)
		}
		_, got, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("received %d bytes, want %d", len(got), len(want))
		}
	}

	// A large first response.create also exercises model probing before routing.
	first := connect("/responses")
	largeRequest, err := json.Marshal(map[string]any{
		"type":  "response.create",
		"model": "message-limit-model",
		"input": strings.Repeat("x", int((oldLimitMiB+1)*defaults.BytesPerMiB)),
	})
	if err != nil {
		t.Fatal(err)
	}
	roundTrip(first, largeRequest, largeRequest)

	// The setting changes while the first connection remains open.
	st.sizeMiB.Store(1)
	roundTrip(first, largeResponse, largeResponse)
	roundTrip(first, []byte(responseCommand), largeResponse)

	second := connect("/responses?model=message-limit-model")
	atLimit := bytes.Repeat([]byte("x"), int(defaults.BytesPerMiB))
	roundTrip(second, atLimit, atLimit)
	// The peer can close while Write is still sending the oversized message.
	if err := second.Write(ctx, websocket.MessageText, largeResponse); err == nil {
		if _, _, err := second.Read(ctx); err == nil {
			t.Fatal("oversized client message passed the configured limit")
		}
	}

	third := connect("/responses?model=message-limit-model")
	if err := third.Write(ctx, websocket.MessageText, []byte(responseCommand)); err != nil {
		t.Fatal(err)
	}
	for {
		_, payload, err := third.Read(ctx)
		if err != nil {
			break
		}
		// The gateway may send a short failure event before closing the socket.
		if bytes.Equal(payload, largeResponse) {
			t.Fatal("oversized upstream message passed the configured limit")
		}
	}
	waitFor(t, func() bool { return st.LogsLen() >= 2 }, testPollTimeout)
	st.mu.Lock()
	defer st.mu.Unlock()
	var clientLimit, upstreamLimit bool
	for _, log := range st.logs {
		if log.SessionEvidenceJSON == nil {
			continue
		}
		var evidence struct {
			Transport struct {
				Source   string `json:"source"`
				RawError string `json:"raw_error_snippet"`
			} `json:"transport"`
		}
		if err := json.Unmarshal([]byte(*log.SessionEvidenceJSON), &evidence); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(evidence.Transport.RawError, "message too big") {
			continue
		}
		clientLimit = clientLimit || evidence.Transport.Source == "client"
		upstreamLimit = upstreamLimit || evidence.Transport.Source == "upstream"
	}
	if !clientLimit || !upstreamLimit {
		t.Fatalf("limit evidence: client=%v, upstream=%v", clientLimit, upstreamLimit)
	}
}
