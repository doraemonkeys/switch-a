package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/clientaccess"
)

func TestClientAPIKeyPolicySurvivesStoreRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "switch-a.db")
	ctx := context.Background()
	var created clientaccess.Key
	func() {
		st, err := NewSQLiteStore(path, internal.RealClock{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := st.Close(); err != nil {
				t.Error(err)
			}
		}()
		service := clientaccess.NewService(clientaccess.ServiceConfig{Store: st.ClientAPIKeyRepository()})
		created, err = service.Create(ctx, "Laptop", "arbitrary-client-key")
		if err != nil {
			t.Fatal(err)
		}
		if err := service.SetMode(ctx, clientaccess.ModeRestricted); err != nil {
			t.Fatal(err)
		}
	}()
	st, err := NewSQLiteStore(path, internal.RealClock{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	}()
	snapshot, err := st.ClientAPIKeySnapshot(ctx)
	if err != nil || snapshot.Mode != clientaccess.ModeRestricted || len(snapshot.Keys) != 1 || snapshot.Keys[0] != created {
		t.Fatalf("restarted policy %#v %v", snapshot, err)
	}
	config, err := st.GetAllConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range config {
		if value == created.Key {
			t.Fatal("client key leaked into generic runtime settings")
		}
	}
}
