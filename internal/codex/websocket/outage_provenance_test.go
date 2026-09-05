package codexws

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestLiveConnectionAndUpstreamResponsesSurviveContinuityStoreOutages(t *testing.T) {
	service, store := newTestContinuityFixture(t)
	runtime := testRuntime(t, service)
	request := testRequest("degraded-client")
	op, err := runtime.Begin(context.Background(), request, codexAPIType, "degraded-operation", "")
	if err != nil {
		t.Fatal(err)
	}
	candidate, applied := testCandidate(t, "route-a", "https://api.example.test/v1")
	if _, err := op.PrepareDial(context.Background(), make(http.Header), candidate, applied, mustURL(t, "wss://api.example.test/v1")); err != nil {
		t.Fatal(err)
	}
	if err := op.OpenConnection(); err != nil {
		t.Fatal(err)
	}
	defer op.CloseConnection()

	store.lookupErr = errors.New("continuity lookup unavailable")
	if permit, err := op.PrepareClientFrame(
		context.Background(), true, []byte(`{"type":"response.create","previous_response_id":"imported-response","client_metadata":{"thread_id":"thread-during-outage","x-codex-turn-metadata":"metadata-during-outage"}}`),
	); permit != nil || Classify(err) != FailureStorage {
		t.Fatalf("unproven state permit=%#v class=%q err=%v", permit, Classify(err), err)
	}
	clientPermit, err := op.PrepareClientFrame(
		context.Background(), true, []byte(`{"type":"response.create","client_metadata":{"thread_id":"thread-during-outage","x-codex-turn-metadata":"metadata-during-outage"}}`),
	)
	if err != nil || clientPermit == nil || len(clientPermit.leases) != 0 {
		t.Fatalf("auxiliary degraded client permit=%#v err=%v", clientPermit, err)
	}
	if required, _ := op.RequiredAuthority(); required == nil || !required.Equal(candidate.Authority()) {
		t.Fatalf("degraded client authority = %v", required)
	}

	store.lookupErr = nil
	store.failClaimAt = store.claimCount + 1
	clientClaimPermit, err := op.PrepareClientFrame(
		context.Background(), true, []byte(`{"type":"response.create","client_metadata":{"session_id":"session-claim-outage"}}`),
	)
	if err != nil || clientClaimPermit == nil || len(clientClaimPermit.leases) != 0 {
		t.Fatalf("claim-outage client permit=%#v err=%v", clientClaimPermit, err)
	}
	if err := clientClaimPermit.Commit(context.Background()); err != nil {
		t.Fatal("commit claim-outage client permit:", err)
	}

	store.failClaimAt = 0
	clientCommitPermit, err := op.PrepareClientFrame(
		context.Background(), true, []byte(`{"type":"response.create","client_metadata":{"session_id":"session-commit-outage"}}`),
	)
	if err != nil || clientCommitPermit == nil || len(clientCommitPermit.leases) != 1 {
		t.Fatalf("commit-outage client permit=%#v err=%v", clientCommitPermit, err)
	}
	store.commitErr = errors.New("continuity commit unavailable")
	if err := clientCommitPermit.Commit(context.Background()); err != nil {
		t.Fatal("live client frame was closed by continuity commit outage:", err)
	}
	store.commitErr = nil

	store.lookupErr = errors.New("continuity lookup unavailable")
	serverPermit, err := op.PrepareServerFrame(
		context.Background(), true, []byte(`{"type":"response.created","response":{"id":"response-during-outage"}}`),
	)
	if err != nil || serverPermit == nil || len(serverPermit.leases) != 1 {
		t.Fatalf("degraded server permit=%#v err=%v", serverPermit, err)
	}
	if err := serverPermit.Commit(context.Background()); err != nil {
		t.Fatal("commit degraded server permit:", err)
	}

	store.lookupErr = nil
	store.failClaimAt = store.claimCount + 1
	claimPermit, err := op.PrepareServerFrame(
		context.Background(), true, []byte(`{"type":"response.created","response":{"id":"response-claim-outage"}}`),
	)
	if err != nil || claimPermit == nil || len(claimPermit.leases) != 1 {
		t.Fatalf("claim-outage server permit=%#v err=%v", claimPermit, err)
	}
	if err := claimPermit.Commit(context.Background()); err != nil {
		t.Fatal("commit claim-outage server permit:", err)
	}
	store.lookupErr = errors.New("continuity lookup unavailable")
	recoveredPermit, err := op.PrepareClientFrame(
		context.Background(), true, []byte(`{"type":"response.create","previous_response_id":"response-claim-outage"}`),
	)
	if err != nil || recoveredPermit == nil || len(recoveredPermit.leases) != 1 {
		t.Fatalf("next-turn provenance recovery permit=%#v err=%v", recoveredPermit, err)
	}
	if err := recoveredPermit.Commit(context.Background()); err != nil {
		t.Fatal("commit recovered client permit:", err)
	}

	store.lookupErr = nil
	store.failClaimAt = 0
	commitPermit, err := op.PrepareServerFrame(
		context.Background(), true, []byte(`{"type":"response.created","response":{"id":"response-commit-outage"}}`),
	)
	if err != nil || commitPermit == nil || len(commitPermit.leases) != 1 {
		t.Fatalf("commit-outage server permit=%#v err=%v", commitPermit, err)
	}
	store.commitErr = errors.New("continuity commit unavailable")
	if err := commitPermit.Commit(context.Background()); err != nil {
		t.Fatal("visible response was closed by continuity commit outage:", err)
	}
}

func TestStoreOutageCannotAssignUnknownStateAcrossConcurrentAuthorities(t *testing.T) {
	service, store := newTestContinuityFixture(t)
	runtime := testRuntime(t, service)
	payload := []byte(`{"type":"response.create","previous_response_id":"shared-unknown-state"}`)

	operations := make([]*Operation, 0, 2)
	for index, origin := range []string{"https://authority-a.example/v1", "https://authority-b.example/v1"} {
		op, err := runtime.Begin(context.Background(), testRequest("shared-client"), codexAPIType, fmt.Sprintf("concurrent-%d", index), "")
		if err != nil {
			t.Fatal(err)
		}
		candidate, applied := testCandidate(t, fmt.Sprintf("route-%d", index), origin)
		if _, err := op.PrepareDial(context.Background(), make(http.Header), candidate, applied, mustURL(t, strings.Replace(origin, "https://", "wss://", 1))); err != nil {
			t.Fatal(err)
		}
		if err := op.OpenConnection(); err != nil {
			t.Fatal(err)
		}
		defer op.CloseConnection()
		operations = append(operations, op)
	}
	store.lookupErr = errors.New("continuity lookup unavailable")

	type result struct {
		permit *Permit
		err    error
	}
	start := make(chan struct{})
	results := make(chan result, len(operations))
	for _, operation := range operations {
		go func(op *Operation) {
			<-start
			permit, err := op.PrepareClientFrame(context.Background(), true, payload)
			results <- result{permit: permit, err: err}
		}(operation)
	}
	close(start)
	for range operations {
		result := <-results
		if result.permit != nil || Classify(result.err) != FailureStorage {
			t.Fatalf("unknown state crossed outage boundary: permit=%#v class=%q err=%v", result.permit, Classify(result.err), result.err)
		}
	}
}
