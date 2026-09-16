package codexws

import (
	"context"
	"testing"
)

func TestPermitAbandonPendingLifecycle(t *testing.T) {
	ctx := context.Background()
	var absent *Permit
	if err := absent.AbandonPending(ctx); err != nil {
		t.Fatal(err)
	}
	permit := &Permit{operation: &Operation{}}
	if err := permit.AbandonPending(ctx); err != nil {
		t.Fatal(err)
	}
	if err := permit.AbandonPending(ctx); err != nil {
		t.Fatal(err)
	}
	if err := permit.Commit(ctx); err == nil {
		t.Fatal("abandoned permit became physically committed")
	}
	committed := &Permit{operation: &Operation{}, committed: true}
	if err := committed.AbandonPending(ctx); err != nil || committed.abandoned {
		t.Fatalf("committed ownership was abandoned: %v", err)
	}
}
