package providerimport

import (
	"fmt"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/model"
)

func TestProviderImportIDAllocatorAssignsLargeCollisionSetLinearly(t *testing.T) {
	const collisionCount = 500
	allocator := newProviderImportIDAllocator([]model.Provider{
		{ID: "shared-name"},
		{ID: "shared-name-2"},
	}, collisionCount)

	for index := 0; index < collisionCount; index++ {
		want := fmt.Sprintf("shared-name-%d", index+3)
		if got := allocator.allocate("Shared Name", nil); got != want {
			t.Fatalf("allocation %d = %q, want %q", index, got, want)
		}
	}
	if got := allocator.nextSuffix["shared-name"]; got != collisionCount+3 {
		t.Fatalf("next suffix = %d, want %d", got, collisionCount+3)
	}
}

func TestProviderImportIDAllocatorLeavesRoomForCollisionSuffix(t *testing.T) {
	allocator := newProviderImportIDAllocator(nil, 2)
	name := strings.Repeat("a", maxProviderImportNameCharacters)
	first := allocator.allocate(name, nil)
	second := allocator.allocate(name, nil)
	if len(first) != maxProviderImportGeneratedIDBaseLength || len(second) > maxProviderImportIdentifierCharacters {
		t.Fatalf("generated IDs have lengths (%d, %d), want commit-safe IDs no longer than %d", len(first), len(second), maxProviderImportIdentifierCharacters)
	}
}

func BenchmarkProviderImportIDAllocatorCollisions(b *testing.B) {
	for iteration := 0; iteration < b.N; iteration++ {
		allocator := newProviderImportIDAllocator(nil, 500)
		for index := 0; index < 500; index++ {
			allocator.allocate("Shared Name", nil)
		}
	}
}
