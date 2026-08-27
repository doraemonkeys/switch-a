package codexheaders

import (
	"bytes"
	"testing"
)

func TestScanServerSSEPreservesCompleteEventPrefixes(t *testing.T) {
	created := []byte("event: response.created\r\ndata: {\"type\":\"response.created\",\r\ndata: \"response\":{\"id\":\"created-ref\"}}\r\n\r\n")
	completed := []byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"completed-ref\"}}\n\n")
	partial := []byte("event: response.created\ndata: {\"type\":")
	raw := append(append(append([]byte(nil), created...), completed...), partial...)
	before := append([]byte(nil), raw...)

	scan := ScanServerSSE(raw, false)
	if scan.ConsumedBytes() != len(created)+len(completed) {
		t.Fatalf("consumed = %d, want %d", scan.ConsumedBytes(), len(created)+len(completed))
	}
	messages := scan.Messages()
	if len(messages) != 2 || messages[0].EventType() != eventResponseCreated || messages[1].EventType() != eventResponseCompleted {
		t.Fatalf("messages = %#v", messages)
	}
	for index, want := range []string{"created-ref", "completed-ref"} {
		result := DecideServerMessage(messages[index], fixedLookup(OwnerCurrent))
		decision := requireOnlyDecision(t, result)
		if got := string(decision.Candidate().Value().Bytes()); got != want {
			t.Fatalf("message %d response ID = %q, want %q", index, got, want)
		}
	}
	if !bytes.Equal(raw, before) {
		t.Fatal("SSE scan mutated caller bytes")
	}
	if replay := messages[0].ReplayBytes(); &replay[0] != &raw[0] || !bytes.Equal(replay, created) {
		t.Fatal("first event did not retain its exact caller-owned slice")
	}
	if replay := messages[1].ReplayBytes(); &replay[0] != &raw[len(created)] || !bytes.Equal(replay, completed) {
		t.Fatal("second event did not retain its exact caller-owned slice")
	}
	copyOfMessages := scan.Messages()
	copyOfMessages[0] = MessageView{}
	if scan.Messages()[0].EventType() != eventResponseCreated {
		t.Fatal("Messages exposed mutable scan state")
	}
}

func TestScanServerSSELeavesIncompleteInputForCaller(t *testing.T) {
	partial := []byte("event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"ref\"}}")
	scan := ScanServerSSE(partial, false)
	if scan.ConsumedBytes() != 0 || len(scan.Messages()) != 0 {
		t.Fatalf("incomplete scan = consumed %d messages %#v", scan.ConsumedBytes(), scan.Messages())
	}

	final := ScanServerSSE(partial, true)
	if final.ConsumedBytes() != len(partial) || len(final.Messages()) != 1 {
		t.Fatalf("final scan = consumed %d messages %#v", final.ConsumedBytes(), final.Messages())
	}
	decision := requireOnlyDecision(t, DecideServerMessage(final.Messages()[0], fixedLookup(OwnerCurrent)))
	if got := string(decision.Candidate().Value().Bytes()); got != "ref" {
		t.Fatalf("final response ID = %q", got)
	}

	empty := ScanServerSSE(nil, true)
	if empty.ConsumedBytes() != 0 || empty.Messages() != nil {
		t.Fatalf("empty final scan = %#v", empty)
	}
}

func TestScanServerSSEKeepsNonDataEventsOpaque(t *testing.T) {
	raw := []byte(": heartbeat\nevent: ping\nretry: 1000\n\n")
	scan := ScanServerSSE(raw, false)
	if scan.ConsumedBytes() != len(raw) || len(scan.Messages()) != 1 {
		t.Fatalf("scan = consumed %d messages %#v", scan.ConsumedBytes(), scan.Messages())
	}
	result := DecideServerMessage(scan.Messages()[0], fixedLookup(OwnerConflict))
	if result.Outcome() != ActionForward || len(result.Decisions()) != 0 || !bytes.Equal(result.ReplayBytes(), raw) {
		t.Fatalf("opaque SSE event = %#v", result)
	}
}

func TestScanServerSSEAcceptsTheStandardLeadingUTF8BOM(t *testing.T) {
	raw := append([]byte{0xef, 0xbb, 0xbf}, []byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"bom-ref\"}}\n\n")...)
	message := ScanServerSSE(raw, false).Messages()[0]
	result := DecideServerMessage(message, fixedLookup(OwnerCurrent))
	decision := requireOnlyDecision(t, result)
	if got := string(decision.Candidate().Value().Bytes()); got != "bom-ref" {
		t.Fatalf("response ID = %q", got)
	}
	if replay := result.ReplayBytes(); !bytes.Equal(replay, raw) || &replay[0] != &raw[0] {
		t.Fatal("BOM event replay changed caller bytes")
	}
}

func TestScanServerSSEResponseMetadataReferenceIsTransportSpecific(t *testing.T) {
	raw := []byte("event: response.metadata\ndata: {\"type\":\"response.metadata\",\"response_id\":\"metadata-ref\"}\n\n")
	scan := ScanServerSSE(raw, false)
	decision := requireOnlyDecision(t, DecideServerMessage(scan.Messages()[0], fixedLookup(OwnerCurrent)))
	if decision.Field() != FieldResponseReference || string(decision.Candidate().Value().Bytes()) != "metadata-ref" {
		t.Fatalf("SSE metadata decision = %#v", decision)
	}

	frame := []byte(`{"type":"response.metadata","response_id":"must-stay-opaque-on-websocket"}`)
	frameResult := DecideServerMessage(InspectServerFrame(frame), fixedLookup(OwnerConflict))
	if frameResult.Outcome() != ActionForward || len(frameResult.Decisions()) != 0 {
		t.Fatalf("unconfirmed websocket metadata path was interpreted: %#v", frameResult.Decisions())
	}

	for _, test := range []struct {
		name   string
		value  string
		reason Reason
	}{
		{name: "duplicate", value: `"one","response_id":"two"`, reason: ReasonDuplicateSecurityKey},
		{name: "null", value: `null`, reason: ReasonInvalidProjection},
		{name: "empty", value: `""`, reason: ReasonInvalidProjection},
		{name: "non-string", value: `7`, reason: ReasonInvalidProjection},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := []byte("data: {\"type\":\"response.metadata\",\"response_id\":" + test.value + "}\n\n")
			message := ScanServerSSE(body, false).Messages()[0]
			decision := requireOnlyDecision(t, DecideServerMessage(message, fixedLookup(OwnerCurrent)))
			if decision.Action() != ActionReject || decision.Reason() != test.reason {
				t.Fatalf("decision = %#v", decision)
			}
		})
	}
}

func TestScanServerSSERejectsInvalidRecognizedSecurityShape(t *testing.T) {
	for _, test := range []struct {
		name   string
		raw    string
		reason Reason
	}{
		{name: "duplicate response", raw: "data: {\"type\":\"response.completed\",\"response\":{},\"response\":{}}\n\n", reason: ReasonDuplicateSecurityKey},
		{name: "invalid id", raw: "data: {\"type\":\"response.completed\",\"response\":{\"id\":null}}\n\n", reason: ReasonInvalidProjection},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := []byte(test.raw)
			scan := ScanServerSSE(raw, false)
			if scan.ConsumedBytes() != len(raw) || len(scan.Messages()) != 1 {
				t.Fatalf("scan = consumed %d messages %#v", scan.ConsumedBytes(), scan.Messages())
			}
			result := DecideServerMessage(scan.Messages()[0], fixedLookup(OwnerCurrent))
			decision := requireOnlyDecision(t, result)
			if decision.Action() != ActionReject || decision.Reason() != test.reason {
				t.Fatalf("decision = %#v", decision)
			}
			if !bytes.Equal(result.ReplayBytes(), raw) {
				t.Fatal("rejected SSE event bytes changed")
			}
		})
	}
}

func TestScanServerSSEUnknownDataStaysOpaque(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte("data: not-json\n\n"),
		[]byte("data: []\n\n"),
		[]byte("event: future\ndata: {\"type\":\"future.event\",\"response\":{\"id\":null}}\r\n\r\n"),
	} {
		scan := ScanServerSSE(raw, false)
		if scan.ConsumedBytes() != len(raw) || len(scan.Messages()) != 1 {
			t.Fatalf("scan = consumed %d messages %#v", scan.ConsumedBytes(), scan.Messages())
		}
		message := scan.Messages()[0]
		result := DecideServerMessage(message, func(BindingCandidate) OwnerStatus {
			t.Fatal("opaque SSE data performed an owner lookup")
			return OwnerConflict
		})
		if message.Recognized() || result.Outcome() != ActionForward || len(result.Decisions()) != 0 || !bytes.Equal(result.ReplayBytes(), raw) {
			t.Fatalf("opaque SSE result = %#v", result)
		}
	}
}
