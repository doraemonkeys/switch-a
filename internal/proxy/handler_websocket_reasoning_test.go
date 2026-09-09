package proxy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/doraemonkeys/switch-a/internal/model"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestWebSocketRequestedReasoningAcrossTurns(t *testing.T) {
	for _, probe := range []bool{false, true} {
		for _, finalAbsent := range []bool{false, true} {
			t.Run(fmt.Sprintf("probe-%t/final-absent-%t", probe, finalAbsent), func(t *testing.T) {
				received := make(chan []byte, 8)
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					conn, err := websocket.Accept(w, r, nil)
					if err != nil {
						return
					}
					defer conn.CloseNow()
					for index := 1; ; index++ {
						_, data, err := conn.Read(r.Context())
						if err != nil {
							return
						}
						received <- data
						// Response-side settings must never overwrite the requested effort.
						response := fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_%d","model":"gpt-test","status":"completed","reasoning":{"effort":"upstream"}}}`, index)
						if err := conn.Write(r.Context(), websocket.MessageText, []byte(response)); err != nil {
							return
						}
					}
				}))
				defer upstream.Close()
				store := newMockStore()
				store.providers = []model.Provider{withTestStaticCredential(model.Provider{
					ID: "reasoning-p1", Name: "Reasoning", AuthMode: "bearer", Enabled: true,
					APITypes: []model.ProviderAPIType{{ProviderID: "reasoning-p1", APIType: APITypeCodex, BaseURL: upstream.URL}},
				}, "", "key")}
				store.configs[ConfigKeyWebSocketProbeClientModel] = strconv.FormatBool(probe)
				// Make bootstrap require the first client message when probing is enabled.
				store.routingPolicies = []model.RoutingPolicy{{Enabled: true, APIType: APITypeCodex,
					ModelMatchType: model.RoutingPolicyModelMatchTypePrefix, ModelMatchValue: "gpt-"}}
				registry := NewActiveRequestRegistry()
				core, traces := observer.New(zap.DebugLevel)
				handler := newProxyCodexTestHandler(t, Config{Store: store, Logger: zap.New(core), ActiveRegistry: registry})
				proxyServer := httptest.NewServer(handler)
				defer proxyServer.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				conn, _, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses", proxyCodexDialOptions())
				if err != nil {
					t.Fatal(err)
				}
				defer conn.CloseNow()
				if !probe {
					waitFor(t, func() bool { return len(registry.List()) == 1 }, testPollTimeout)
					if got := registry.List()[0]; got.State == nil || *got.State != model.ReasoningObservationPending {
						t.Fatalf("initial reasoning = %+v", got.RequestedReasoningObservation)
					}
				}
				turns := []struct {
					body, effort string
					state        model.ReasoningObservationState
					messageType  websocket.MessageType
				}{
					{`{"type":"response.create","model":"gpt-test","input":[],"reasoning":{"effort":"high"}}`, "high", model.ReasoningObservationCaptured, websocket.MessageText},
					{`{"type":"response.append","input":[],"reasoning":{"effort":"ignored"}}`, "high", model.ReasoningObservationCaptured, websocket.MessageText},
					{`{"type":"response.create","model":"gpt-test","reasoning":{"effort":"binary"}}`, "high", model.ReasoningObservationCaptured, websocket.MessageBinary},
					{`{"type":"response.create","model":"gpt-test","input":[],"reasoning":{"effort":"low"}}`, "low", model.ReasoningObservationCaptured, websocket.MessageText},
				}
				if finalAbsent {
					turns = append(turns, struct {
						body, effort string
						state        model.ReasoningObservationState
						messageType  websocket.MessageType
					}{
						`{"type":"response.create","model":"gpt-test","input":[]}`, "", model.ReasoningObservationAbsent, websocket.MessageText})
				}
				for _, turn := range turns {
					if err := conn.Write(ctx, turn.messageType, []byte(turn.body)); err != nil {
						t.Fatal(err)
					}
					if _, _, err := conn.Read(ctx); err != nil {
						t.Fatal(err)
					}
					select {
					case raw := <-received:
						if string(raw) != turn.body {
							t.Fatal("reasoning observation changed the forwarded message")
						}
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
					waitFor(t, func() bool {
						active := registry.List()
						if len(active) != 1 || active[0].State == nil || *active[0].State != turn.state {
							return false
						}
						return turn.effort == "" && active[0].Effort == nil || active[0].Effort != nil && *active[0].Effort == turn.effort
					}, testPollTimeout)
				}
				if err := conn.Close(websocket.StatusNormalClosure, "done"); err != nil {
					t.Fatal(err)
				}
				waitFor(t, func() bool { return store.LastLog() != nil }, testPollTimeout)
				log := store.LastLog()
				last := turns[len(turns)-1]
				if log.State == nil || *log.State != last.state || last.effort == "" && log.Effort != nil || last.effort != "" && (log.Effort == nil || *log.Effort != last.effort) {
					t.Fatalf("final reasoning = %+v", log.RequestedReasoningObservation)
				}
				wantTurns := 2
				if finalAbsent {
					wantTurns++
				}
				events := traces.FilterMessage("websocket.request_reasoning_observed").All()
				if len(events) != wantTurns {
					t.Fatalf("logical requests=%d, want %d", len(events), wantTurns)
				}
				for i, event := range events {
					fields := event.ContextMap()
					if fields["operation_id"] != log.RequestID || fields["client_request_index"] != uint64(i+1) {
						t.Fatalf("trace correlation = %+v", fields)
					}
				}
			})
		}
	}
}
