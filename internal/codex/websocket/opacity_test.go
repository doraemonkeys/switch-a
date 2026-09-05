package codexws

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
)

type resolveCountingContinuity struct {
	Continuity
	resolveCalls int
}

func (c *resolveCountingContinuity) ResolveOwner(
	ctx context.Context,
	request codexcontinuity.ResolveRequest,
) (codexcontinuity.Binding, error) {
	c.resolveCalls++
	return c.Continuity.ResolveOwner(ctx, request)
}

func TestOpaqueWebSocketFramesCreateNoOwnerOrRouteEffect(t *testing.T) {
	continuity := &resolveCountingContinuity{Continuity: newTestContinuity(t)}
	runtime := testRuntime(t, continuity)
	op, err := runtime.Begin(context.Background(), testRequest("opaque-client"), codexAPIType, "opaque-frames", "")
	if err != nil {
		t.Fatal(err)
	}
	if continuity.resolveCalls != 0 {
		t.Fatalf("owner lookups after begin = %d", continuity.resolveCalls)
	}

	for _, test := range []struct {
		name    string
		payload []byte
	}{
		{name: "non JSON", payload: []byte("future wire format")},
		{name: "JSON non-object", payload: []byte("[1,2,3]")},
		{name: "unknown event", payload: []byte(`{"type":"future.event","client_metadata":{"thread_id":null}}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := append([]byte(nil), test.payload...)
			permit, prepareErr := op.PrepareClientFrame(context.Background(), true, test.payload)
			if prepareErr != nil || permit == nil || len(permit.leases) != 0 {
				t.Fatalf("client permit=%#v err=%v", permit, prepareErr)
			}
			serverPermit, prepareErr := op.PrepareServerFrame(context.Background(), true, test.payload)
			if prepareErr != nil || serverPermit == nil || len(serverPermit.leases) != 0 {
				t.Fatalf("server permit=%#v err=%v", serverPermit, prepareErr)
			}
			if err := op.InspectBootstrapFrame(context.Background(), true, test.payload); err != nil {
				t.Fatalf("bootstrap opaque frame failed: %v", err)
			}
			if continuity.resolveCalls != 0 {
				t.Fatalf("opaque frame owner lookups = %d", continuity.resolveCalls)
			}
			if authority, route := op.RequiredAuthority(); authority != nil || route != "" {
				t.Fatalf("opaque frame changed route = (%v, %q)", authority, route)
			}
			if !bytes.Equal(test.payload, before) {
				t.Fatal("opaque frame bytes changed")
			}
		})
	}

	binary := []byte{0x00, 0xff, 0x10, 0x80}
	before := append([]byte(nil), binary...)
	if permit, prepareErr := op.PrepareClientFrame(context.Background(), false, binary); prepareErr != nil || permit != nil {
		t.Fatalf("binary client permit=%#v err=%v", permit, prepareErr)
	}
	if permit, prepareErr := op.PrepareServerFrame(context.Background(), false, binary); prepareErr != nil || permit != nil {
		t.Fatalf("binary server permit=%#v err=%v", permit, prepareErr)
	}
	if err := op.InspectBootstrapFrame(context.Background(), false, binary); err != nil {
		t.Fatalf("binary bootstrap frame failed: %v", err)
	}
	if continuity.resolveCalls != 0 || !bytes.Equal(binary, before) {
		t.Fatalf("binary frame mutated state or bytes: lookups=%d", continuity.resolveCalls)
	}
}

func TestAppendAndInjectRecognitionDoesNotGuessOwnerFields(t *testing.T) {
	continuity := &resolveCountingContinuity{Continuity: newTestContinuity(t)}
	runtime := testRuntime(t, continuity)
	op, err := runtime.Begin(context.Background(), testRequest("controls-client"), codexAPIType, "connection-controls", "")
	if err != nil {
		t.Fatal(err)
	}
	missing := op.ClassifyClientFrame(context.Background(), true, []byte(`{"type":"response.append"}`))
	if missing.Disposition() != ClientFrameReject || Classify(missing.Rejection()) != FailureIdentity ||
		missing.ReplayEligible() || missing.ReplacementEligible() || !missing.CurrentConnectionRequired() {
		t.Fatalf("connection-free append decision = %#v err=%v", missing.Trace(), missing.Rejection())
	}
	candidate, applied := testCandidate(t, "route-a", "https://api.example.test/v1")
	if _, err := op.PrepareDial(context.Background(), make(http.Header), candidate, applied, mustURL(t, "wss://api.example.test/v1")); err != nil {
		t.Fatal(err)
	}
	if err := op.OpenConnection(); err != nil {
		t.Fatal(err)
	}
	defer op.CloseConnection()
	for _, event := range []string{"response.append", "response.inject"} {
		payload := []byte(`{"type":"` + event + `","response":{"id":"must-not-be-read"},"response_id":"also-opaque"}`)
		frame := op.ClassifyClientFrame(context.Background(), true, payload)
		if frame.Disposition() != ClientFrameForward || frame.ReplayEligible() || frame.ReplacementEligible() ||
			!frame.CurrentConnectionRequired() {
			t.Fatalf("%s decision=%#v", event, frame)
		}
		permit, prepareErr := frame.PrepareDelivery(context.Background())
		if prepareErr != nil || permit == nil || len(permit.leases) != 0 {
			t.Fatalf("%s permit=%#v err=%v", event, permit, prepareErr)
		}
		if err := permit.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if op.ReplacementAllowed() {
		t.Fatal("confirmed connection-bound control left replacement open")
	}
	if continuity.resolveCalls != 0 {
		t.Fatalf("connection controls performed %d owner lookups", continuity.resolveCalls)
	}
}
