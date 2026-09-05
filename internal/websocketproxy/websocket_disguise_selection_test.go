package websocketproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/coder/websocket"
	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestDisguiseFinalSelectionCommitsOnlyActivatedLease(t *testing.T) {
	repository := &testDisguiseRepository{revision: "one"}
	providers := []model.Provider{testDisguiseProvider("first"), testDisguiseProvider("active")}
	o := newDisguiseTestOrchestrator(t, repository, providers)
	o.selectReq.StickyMode = model.StickyModeModel
	first := o.handler.newFallbackProviderLease(&providers[0], APITypeCodex)
	active := o.handler.newFallbackProviderLease(&providers[1], APITypeCodex)
	source := o.handler.newFallbackProviderLease(&providers[1], APITypeCodex).(*fallbackProviderLease)
	source.generation = active.Generation()
	o.handler.selector = &routingTestSelector{initial: ProviderSelection{Lease: first}, active: ProviderSelection{Lease: active}}
	o.handler.activeSessions = &routingTestActiveSessions{lease: source, found: true}
	selected, err := o.selectPhysicalTarget(context.Background(), 0)
	if err != nil || selected.Provider().ID != "active" {
		t.Fatalf("final target: %#v %v", selected, err)
	}
	if first.Held() || !source.Held() {
		t.Fatal("superseded or active source lease ownership wrong")
	}
	if len(repository.commits) != 1 || repository.commits[0] != "active-codex-credential" {
		t.Fatalf("speculative target persisted: %v", repository.commits)
	}
}
func TestDisguiseConcurrentBindingExclusionReselectsWithoutAttempt(t *testing.T) {
	repository := &testDisguiseRepository{revision: "one", excludedSession: "first-codex-credential"}
	providers := []model.Provider{testDisguiseProvider("first"), testDisguiseProvider("second")}
	o := newDisguiseTestOrchestrator(t, repository, providers)
	first := o.handler.newFallbackProviderLease(&providers[0], APITypeCodex)
	second := o.handler.newFallbackProviderLease(&providers[1], APITypeCodex)
	o.handler.selector = &routingTestSelector{initial: ProviderSelection{Lease: first}, alternate: ProviderSelection{Lease: second}}
	selection, _, failure := o.selectProvider(context.Background(), 0)
	if failure != nil || selection.Provider().ID != "second" {
		t.Fatalf("commit race failed reselect: %#v %#v", selection, failure)
	}
	if first.Held() || !second.Held() {
		t.Fatal("race lost lease retained or winner released")
	}
	if len(o.attempts) != 0 || len(o.excludedProviders) != 0 {
		t.Fatal("platform race consumed physical attempt accounting")
	}
	if len(o.disguise.Operation().Exclusions()) != 1 {
		t.Fatal("race exclusion evidence lost")
	}
}
func TestDisguiseServerRestorationPreservesProtocolAndBusinessData(t *testing.T) {
	repository := &testDisguiseRepository{revision: "one"}
	providers := []model.Provider{testDisguiseProvider("first")}
	o := newDisguiseTestOrchestrator(t, repository, providers)
	selectDisguiseTestTarget(t, o, &providers[0])
	mapped, err := o.disguise.Current().ClientFrame(context.Background(), []byte(`{"type":"response.create","turn_id":"original-turn"}`))
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	if err = json.Unmarshal(mapped, &request); err != nil {
		t.Fatal(err)
	}
	source := []byte(fmt.Sprintf(`{"type":"response.created","response":{"id":"resp-original","turn_id":%q,"output":[{"text":%q}]}}`, request["turn_id"], request["turn_id"]))
	decision := o.composeUpstreamPreWrite(context.Background(), nil)(webSocketPreWriteContext{MessageType: websocket.MessageText, Data: source})
	if decision.Action != webSocketPreWriteActionForward {
		t.Fatal(decision.Err)
	}
	if !bytes.Contains(decision.PreparedPayload, []byte(`"turn_id":"original-turn"`)) || !bytes.Contains(decision.PreparedPayload, []byte(`"id":"resp-original"`)) {
		t.Fatalf("protocol restoration wrong: %s", decision.PreparedPayload)
	}
	if !bytes.Contains(decision.PreparedPayload, []byte(fmt.Sprintf(`"text":%q`, request["turn_id"]))) {
		t.Fatal("business text altered")
	}
	repository.mappingError = errors.New("inverse unavailable")
	failed := o.composeUpstreamPreWrite(context.Background(), nil)(webSocketPreWriteContext{MessageType: websocket.MessageText, Data: source})
	if failed.Action != webSocketPreWriteActionReject || disguiseFailure(failed.Err) == nil {
		t.Fatal("restore failure allowed original passthrough")
	}
}
