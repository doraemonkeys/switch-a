package clientaccess

import (
	"context"
	"reflect"
	"testing"
)

func TestRepositoryImportPreservesZeroTimestamps(t *testing.T) {
	repo, _ := testRepository(t)
	expected := Snapshot{Mode: ModeRestricted, Keys: []Key{validKey()}}
	if err := repo.Replace(context.Background(), expected); err != nil {
		t.Fatal(err)
	}
	actual, err := repo.Snapshot(context.Background())
	if err != nil || !reflect.DeepEqual(actual, expected) {
		t.Fatalf("import changed timestamps: %#v %v", actual, err)
	}
}
