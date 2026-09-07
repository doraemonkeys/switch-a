package clientaccess

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

var errStorage = errors.New("storage unavailable")

type memoryStore struct {
	snapshot Snapshot
	err      error
	created  Key
	renamed  Key
	deleted  string
	mode     Mode
}

func (m *memoryStore) Snapshot(context.Context) (Snapshot, error) { return m.snapshot, m.err }
func (m *memoryStore) CreateKey(_ context.Context, key Key) error { m.created = key; return m.err }
func (m *memoryStore) RenameKey(_ context.Context, id, name string, at time.Time) (Key, error) {
	m.renamed = Key{ID: id, Name: name, UpdatedAt: at}
	return m.renamed, m.err
}
func (m *memoryStore) DeleteKey(_ context.Context, id string) error { m.deleted = id; return m.err }
func (m *memoryStore) SetMode(_ context.Context, mode Mode) error   { m.mode = mode; return m.err }

func validKey() Key { return Key{ID: "client-1", Name: "Laptop", Key: "existing-client-key"} }

func TestValidateSnapshot(t *testing.T) {
	key := validKey()
	cases := []struct {
		name     string
		snapshot Snapshot
		valid    bool
	}{
		{"default", Snapshot{Mode: ModePermissive}, true},
		{"empty restricted", Snapshot{Mode: ModeRestricted}, true},
		{"configured", Snapshot{Mode: ModeRestricted, Keys: []Key{key}}, true},
		{"unknown mode", Snapshot{Mode: "enabled"}, false},
		{"duplicate ID", Snapshot{Mode: ModeRestricted, Keys: []Key{key, {ID: key.ID, Name: "Desktop", Key: "different"}}}, false},
		{"duplicate secret", Snapshot{Mode: ModeRestricted, Keys: []Key{key, {ID: "other", Name: "Desktop", Key: key.Key}}}, false},
		{"no ID", Snapshot{Mode: ModeRestricted, Keys: []Key{{Name: "Name", Key: "value"}}}, false},
		{"no name", Snapshot{Mode: ModeRestricted, Keys: []Key{{ID: "id", Key: "value"}}}, false},
		{"dot ID", Snapshot{Mode: ModeRestricted, Keys: []Key{{ID: ".", Name: "Device", Key: "value"}}}, false},
		{"parent ID", Snapshot{Mode: ModeRestricted, Keys: []Key{{ID: "..", Name: "Device", Key: "value"}}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSnapshot(tc.snapshot)
			if tc.valid && err != nil || !tc.valid && !errors.Is(err, ErrValidation) {
				t.Fatalf("validation: %v", err)
			}
		})
	}
	for _, value := range []string{"", " leading", "trailing ", "line\rbreak", "line\nbreak", "tab\tinside", string([]byte{127}), strings.Repeat("a", MaxKeyBytes+1)} {
		if err := validateKey(value); !errors.Is(err, ErrValidation) {
			t.Errorf("invalid key accepted: %q", value)
		}
	}
	if err := validateKey(strings.Repeat("a", MaxKeyBytes)); err != nil {
		t.Fatal(err)
	}
}

func TestServiceManagement(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.FixedZone("test", 3600))
	m := &memoryStore{snapshot: Snapshot{Mode: ModePermissive}}
	s := NewService(ServiceConfig{Store: m, Now: func() time.Time { return now }, NewID: func() string { return "stable-id" }, Random: bytes.NewReader(make([]byte, GeneratedKeyRandomBytes))})
	snapshot, err := s.Snapshot(ctx)
	if err != nil || snapshot.Keys == nil {
		t.Fatalf("snapshot %#v %v", snapshot, err)
	}
	key, err := s.Create(ctx, " Laptop ", "existing-key")
	if err != nil || key.ID != "stable-id" || key.Name != "Laptop" || key.Key != "existing-key" || key.CreatedAt.Location() != time.UTC || !key.CreatedAt.Equal(now) || key != m.created {
		t.Fatalf("create %#v %v", key, err)
	}
	generated, err := s.Generate(ctx, "Generated")
	if err != nil || generated.Key != GeneratedKeyPrefix+strings.Repeat("A", 43) {
		t.Fatalf("generated %#v %v", generated, err)
	}
	renamed, err := s.Rename(ctx, "stable-id", " Desktop ")
	if err != nil || renamed.Name != "Desktop" || renamed.ID != "stable-id" || !renamed.UpdatedAt.Equal(now) {
		t.Fatalf("rename %#v %v", renamed, err)
	}
	if err := s.Delete(ctx, "stable-id"); err != nil || m.deleted != "stable-id" {
		t.Fatalf("delete %v", err)
	}
	if err := s.SetMode(ctx, ModeRestricted); err != nil || m.mode != ModeRestricted {
		t.Fatalf("mode %v", err)
	}
}

func TestServiceValidationAndFailures(t *testing.T) {
	ctx := context.Background()
	m := &memoryStore{snapshot: Snapshot{Mode: ModePermissive}}
	s := NewService(ServiceConfig{Store: m, Random: bytes.NewReader(nil)})
	for _, call := range []func() error{
		func() error { _, err := s.Create(ctx, " ", "key"); return err },
		func() error { _, err := s.Create(ctx, "name", " "); return err },
		func() error { _, err := s.Generate(ctx, " "); return err },
		func() error { _, err := s.Rename(ctx, "id", " "); return err },
		func() error { return s.SetMode(ctx, "wrong") },
	} {
		if err := call(); !errors.Is(err, ErrValidation) {
			t.Fatalf("validation %v", err)
		}
	}
	if _, err := s.Generate(ctx, "name"); !errors.Is(err, io.EOF) {
		t.Fatalf("random failure %v", err)
	}
	badID := NewService(ServiceConfig{Store: m, NewID: func() string { return "" }})
	if _, err := badID.Create(ctx, "name", "key"); !errors.Is(err, ErrValidation) {
		t.Fatalf("ID validation %v", err)
	}
	m.snapshot.Mode = "wrong"
	if _, err := s.Snapshot(ctx); !errors.Is(err, ErrValidation) {
		t.Fatalf("corrupt policy %v", err)
	}
	m.err = errStorage
	for _, call := range []func() error{
		func() error { _, err := s.Snapshot(ctx); return err },
		func() error { _, err := s.Create(ctx, "name", "key"); return err },
		func() error { _, err := s.Rename(ctx, "id", "name"); return err },
		func() error { return s.Delete(ctx, "id") },
		func() error { return s.SetMode(ctx, ModeRestricted) },
	} {
		if err := call(); !errors.Is(err, errStorage) {
			t.Fatalf("storage failure %v", err)
		}
	}
}

func TestServiceRequiresStore(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected programmer error for absent store")
		}
	}()
	NewService(ServiceConfig{})
}
