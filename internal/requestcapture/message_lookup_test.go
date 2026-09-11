package requestcapture

import (
	"net/http"
	"testing"
)

func TestWebSocketMessageResultsPreserveIdentityAndArrivalOrder(t *testing.T) {
	manager := newTestManager(t, nil)
	session := startTestSession(t, manager, 1, 1<<20, "selected")
	gateway := manager.BeginGateway(GatewayStart{GatewayRequestID: "message-identities"})
	begin := func() Recorder {
		recorder := gateway.BeginWebSocket(RawWebSocketStart{
			TargetURL: "wss://example.test/socket",
			Attempt:   AttemptMetadata{Provider: ProviderIdentity{ID: "selected"}},
			Request:   RawRequest{Method: http.MethodGet},
		})
		recorder.ObserveWebSocketHandshake(WebSocketHandshake{StatusCode: http.StatusSwitchingProtocols})
		return recorder
	}
	first, second := begin(), begin()
	earlier, later := gateway.NewMessageID(), gateway.NewMessageID()
	read := func(recorder Recorder, lineage MessageLineage) MessageRef {
		return recorder.MessageRead(MessageRead{
			Lineage: lineage, Direction: MessageDirectionUpstreamToClient,
			Type: MessageTypeText, Payload: []byte("message"),
		})
	}
	// Lineage issuance, arrival, and result completion have independent orders.
	lastIssued := read(first, later)
	otherRecord := read(second, MessageLineage{})
	firstIssued := read(first, earlier)
	duplicateLineage := read(first, later)
	if duplicateLineage.Lineage() == later || !duplicateLineage.Valid() {
		t.Fatal("duplicate lineage did not receive a distinct identity")
	}
	confirmed := MessageResult{Disposition: MessageDispositionForwarded, WriteConfirmed: true}
	first.MessageResult(otherRecord, confirmed)
	mismatched := lastIssued
	mismatched.sequence = firstIssued.sequence
	first.MessageResult(mismatched, confirmed)
	missing := lastIssued
	missing.lineage = gateway.NewMessageID().lineage
	first.MessageResult(missing, confirmed)
	if record := testRecordState(t, first); record.writtenBytes != 0 {
		t.Fatal("a foreign or mismatched reference changed the record")
	}
	for _, ref := range []MessageRef{duplicateLineage, firstIssued, lastIssued} {
		first.MessageResult(ref, confirmed)
		first.MessageResult(ref, MessageResult{Disposition: MessageDispositionSuppressed})
	}
	detail, err := readRecordDetailForTest(t, manager, session.SessionID, first.ID(), 64)
	if err != nil {
		t.Fatal(err)
	}
	messages := detail.WebSocket.Messages
	if len(messages) != 3 || messages[0].MessageID != lastIssued.ID() ||
		messages[1].MessageID != firstIssued.ID() || messages[2].MessageID != duplicateLineage.ID() {
		t.Fatalf("arrival order changed: %#v", messages)
	}
	for _, message := range messages {
		if !message.ClientVisible || message.Disposition != MessageDispositionForwarded {
			t.Fatalf("out-of-order or duplicate result corrupted message: %#v", message)
		}
	}
	if detail.Summary.ApplicationWriteConfirmedBytes != int64(3*len("message")) {
		t.Fatalf("confirmed bytes = %d", detail.Summary.ApplicationWriteConfirmedBytes)
	}
	oldState := testRecordState(t, first)
	first.Finish(Outcome{TerminationReason: TerminationReasonWebSocketClose})
	second.Finish(Outcome{TerminationReason: TerminationReasonWebSocketClose})
	first.MessageResult(lastIssued, confirmed)
	if oldState.messageByLineage != nil || first.Valid() {
		t.Fatal("eviction retained message lookup ownership")
	}
	if err := manager.Stop(session.SessionID); err != nil {
		t.Fatal(err)
	}
	second.MessageResult(otherRecord, confirmed)
	if manager.Status().ProcessMemory.ChargedBytes != 0 {
		t.Fatal("stale references retained capture memory")
	}
}
