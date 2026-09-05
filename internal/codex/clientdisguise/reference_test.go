package clientdisguise

import (
	"context"
	"errors"
	"testing"
)

func TestReferenceRequiresPersistedClient(t *testing.T) {
	repo := testRepository(t)
	ctx := context.Background()
	source := ReferenceSource{ID: "source", Name: "Reference", ClientIdentityID: "missing"}
	if err := repo.SaveReference(ctx, source); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	references, err := repo.ListReferences(ctx)
	if err != nil || len(references) != 0 {
		t.Fatal(references, err)
	}
	source.ClientIdentityID = "client"
	if err := repo.SaveReference(ctx, source); err != nil {
		t.Fatal(err)
	}
	source.ClientIdentityID = "missing"
	if err := repo.SaveReference(ctx, source); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	references, err = repo.ListReferences(ctx)
	if err != nil || len(references) != 1 || references[0].ClientIdentityID != "client" {
		t.Fatal(references, err)
	}
	source.ClientIdentityID = ""
	if err := repo.SaveReference(ctx, source); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
