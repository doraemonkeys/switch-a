package clientdisguiseapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestOverviewRecognizesClientsBeforeReferenceSelection(t *testing.T) {
	handler, persistence, clients := realAdministration(t)
	ctx := context.Background()
	recent, err := clients.Resolve(ctx, []byte("recent-client"))
	if err != nil {
		t.Fatal(err)
	}
	unobserved, err := clients.Resolve(ctx, []byte("unobserved-client"))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	ua := "Codex Desktop/0.153.4 (Windows 10.0.19045; x86_64)"
	if err := persistence.ClientDisguiseRepository().ObserveClient(ctx, recent.ID, http.Header{"User-Agent": {ua}, "Originator": {"Codex Desktop"}}, at); err != nil {
		t.Fatal(err)
	}
	if _, err := clients.BindKey(ctx, []byte("replacement-key"), recent.ID); err != nil {
		t.Fatal(err)
	}
	response := invoke(handler.Get, "")
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	var overview Overview
	if err := json.Unmarshal(response.Body.Bytes(), &overview); err != nil {
		t.Fatal(err)
	}
	if len(overview.Clients) != 2 {
		t.Fatalf("clients=%+v", overview.Clients)
	}
	for _, client := range overview.Clients {
		switch client.ClientID {
		case recent.ID:
			if client.LastRequest == nil || !client.LastRequest.ObservedAt.Equal(at) || client.LastRequest.UserAgent != ua || client.LastRequest.Tuple.Platform != "windows" {
				t.Fatalf("request identity missing: %+v", client)
			}
		case unobserved.ID:
			if client.LastRequest != nil {
				t.Fatalf("creation misrepresented as a request: %+v", client)
			}
		default:
			t.Fatalf("unexpected client: %+v", client)
		}
	}
}
