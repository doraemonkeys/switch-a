package store

import (
	"context"
	"errors"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestGroupCRUD(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Create
	group := &model.Group{
		ID:       "g1",
		Name:     "Test Group",
		Strategy: "priority",
		Priority: 1,
		Weight:   10,
		Enabled:  true,
	}

	if err := store.CreateGroup(ctx, group); err != nil {
		t.Fatalf("CreateGroup failed: %v", err)
	}

	// Read
	got, err := store.GetGroup(ctx, "g1")
	if err != nil {
		t.Fatalf("GetGroup failed: %v", err)
	}
	if got == nil {
		t.Fatal("expected group, got nil")
	}
	if got.Name != "Test Group" {
		t.Errorf("Name = %q, want %q", got.Name, "Test Group")
	}

	// List
	groups, err := store.ListGroups(ctx)
	if err != nil {
		t.Fatalf("ListGroups failed: %v", err)
	}
	if len(groups) != 1 {
		t.Errorf("ListGroups len = %d, want 1", len(groups))
	}

	// Update
	group.Name = "Updated Group"
	if err := store.UpdateGroup(ctx, group); err != nil {
		t.Fatalf("UpdateGroup failed: %v", err)
	}

	got, err = store.GetGroup(ctx, "g1")
	if err != nil {
		t.Fatalf("GetGroup after update failed: %v", err)
	}
	if got.Name != "Updated Group" {
		t.Errorf("Name after update = %q, want %q", got.Name, "Updated Group")
	}

	// Delete
	if err := store.DeleteGroup(ctx, "g1"); err != nil {
		t.Fatalf("DeleteGroup failed: %v", err)
	}

	got, err = store.GetGroup(ctx, "g1")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetGroup after delete: expected ErrNotFound, got %v", err)
	}
	if got != nil {
		t.Error("expected nil after delete")
	}
}

func TestGetGroupNotFound(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	got, err := store.GetGroup(ctx, "nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetGroup: expected ErrNotFound, got %v", err)
	}
	if got != nil {
		t.Error("expected nil for non-existent group")
	}
}

func TestListGroups(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Empty list initially
	groups, err := store.ListGroups(ctx)
	if err != nil {
		t.Fatalf("ListGroups failed: %v", err)
	}
	if len(groups) != 0 {
		t.Errorf("expected 0 groups, got %d", len(groups))
	}

	// Add a group
	g := &model.Group{ID: "g1", Name: "Test", Enabled: true}
	if err := store.CreateGroup(ctx, g); err != nil {
		t.Fatalf("CreateGroup failed: %v", err)
	}

	groups, err = store.ListGroups(ctx)
	if err != nil {
		t.Fatalf("ListGroups failed: %v", err)
	}
	if len(groups) != 1 {
		t.Errorf("expected 1 group, got %d", len(groups))
	}
}

func TestDeleteGroupClearsProviderReference(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	// Create group
	group := &model.Group{
		ID:      "g1",
		Name:    "Test Group",
		Enabled: true,
	}
	if err := store.CreateGroup(ctx, group); err != nil {
		t.Fatalf("CreateGroup failed: %v", err)
	}

	// Create provider in group
	groupID := "g1"
	provider := &model.Provider{
		ID:      "p1",
		Name:    "Test Provider",
		APIKey:  "key",
		GroupID: &groupID,
		Enabled: true,
	}
	if err := store.CreateProvider(ctx, provider); err != nil {
		t.Fatalf("CreateProvider failed: %v", err)
	}

	// Delete group
	if err := store.DeleteGroup(ctx, "g1"); err != nil {
		t.Fatalf("DeleteGroup failed: %v", err)
	}

	// Verify provider still exists but has no group
	got, err := store.GetProvider(ctx, "p1")
	if err != nil {
		t.Fatalf("GetProvider failed: %v", err)
	}
	if got == nil {
		t.Fatal("expected provider to still exist")
	}
	if got.GroupID != nil {
		t.Error("expected GroupID to be nil after group deletion")
	}
}

func TestDeleteGroupRejectedWhenRoutingPolicyReferencesGroup(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	group := &model.Group{ID: "g1", Name: "Referenced", Enabled: true}
	if err := store.CreateGroup(ctx, group); err != nil {
		t.Fatalf("CreateGroup failed: %v", err)
	}
	policy := &model.RoutingPolicy{
		APIType: "codex",
		Enabled: true,
		Groups:  []model.RoutingPolicyGroup{{GroupID: "g1"}},
	}
	if err := store.CreateRoutingPolicy(ctx, policy); err != nil {
		t.Fatalf("CreateRoutingPolicy failed: %v", err)
	}

	err := store.DeleteGroup(ctx, "g1")
	if !errors.Is(err, ErrRoutingPolicyReferenceConflict) {
		t.Fatalf("DeleteGroup error = %v, want ErrRoutingPolicyReferenceConflict", err)
	}
	var conflict *RoutingPolicyGroupReferenceConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("DeleteGroup error = %v, want RoutingPolicyGroupReferenceConflictError", err)
	}
	if conflict.GroupID != "g1" {
		t.Fatalf("conflict.GroupID = %q, want g1", conflict.GroupID)
	}
}

func TestDeleteGroup_RejectsRoutingPolicyReference(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	if err := store.CreateGroup(ctx, &model.Group{ID: "g1", Name: "Referenced Group", Enabled: true}); err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	if err := store.CreateRoutingPolicy(ctx, &model.RoutingPolicy{
		APIType: "claude",
		Enabled: true,
		Groups:  []model.RoutingPolicyGroup{{GroupID: "g1"}},
	}); err != nil {
		t.Fatalf("CreateRoutingPolicy() error = %v", err)
	}

	err := store.DeleteGroup(ctx, "g1")
	if !errors.Is(err, ErrRoutingPolicyReferenceConflict) {
		t.Fatalf("DeleteGroup() error = %v, want ErrRoutingPolicyReferenceConflict", err)
	}
}
