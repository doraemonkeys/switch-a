package codexws

import (
	"context"
	"testing"
)

func TestPermitAbandonBeforeDisclosureLifecycle(t *testing.T) {
	ctx := context.Background()
	var absent *Permit
	if err := absent.AbandonBeforeDisclosure(ctx); err != nil {
		t.Fatal(err)
	}
	permit := &Permit{operation: &Operation{}}
	if err := permit.AbandonBeforeDisclosure(ctx); err != nil {
		t.Fatal(err)
	}
	if err := permit.AbandonBeforeDisclosure(ctx); err != nil {
		t.Fatal(err)
	}
	if err := permit.Commit(ctx); err == nil {
		t.Fatal("abandoned permit became physically committed")
	}
	committed := &Permit{operation: &Operation{}, committed: true}
	if err := committed.AbandonBeforeDisclosure(ctx); err != nil || committed.abandoned {
		t.Fatalf("committed ownership was abandoned: %v", err)
	}
}
