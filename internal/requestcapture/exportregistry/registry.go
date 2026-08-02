package exportregistry

import (
	"math"
	"unsafe"
)

// Registry is a bounded key arena whose backing storage is explicitly
// materialized and severed by its owner. Callers provide synchronization.
type Registry[T comparable] struct {
	slots    []slot[T]
	capacity int
	count    int
}

type slot[T comparable] struct {
	key      string
	value    T
	occupied bool
}

// New validates the arena shape without allocating its slot backing.
func New[T comparable](capacity int) (Registry[T], bool) {
	if _, valid := BackingChargeBytes[T](capacity); !valid {
		return Registry[T]{}, false
	}
	return Registry[T]{capacity: capacity}, true
}

// BackingChargeBytes returns the exact logical charge for the slice backing
// allocated by Materialize. The Registry header lives in its owner's object.
func BackingChargeBytes[T comparable](capacity int) (int64, bool) {
	if capacity <= 0 {
		return 0, false
	}
	slotBytes := uint64(unsafe.Sizeof(slot[T]{}))
	if slotBytes == 0 || uint64(capacity) > uint64(math.MaxInt64)/slotBytes {
		return 0, false
	}
	return int64(uint64(capacity) * slotBytes), true
}

func (registry *Registry[T]) Capacity() int {
	if registry == nil {
		return 0
	}
	return registry.capacity
}

func (registry *Registry[T]) Count() int {
	if registry == nil {
		return 0
	}
	return registry.count
}

func (registry *Registry[T]) IsMaterialized() bool {
	return registry != nil && registry.slots != nil
}

// Materialize allocates the complete, pre-validated arena. Its owner must reserve
// BackingChargeBytes before calling this method.
func (registry *Registry[T]) Materialize() bool {
	if registry == nil || registry.capacity <= 0 {
		return false
	}
	if registry.slots != nil {
		return true
	}
	registry.slots = make([]slot[T], registry.capacity)
	return true
}

// Dematerialize severs an empty arena. A false result guarantees that no backing
// was removed, so callers must not refund its charge.
func (registry *Registry[T]) Dematerialize() bool {
	if registry == nil || registry.slots == nil || registry.count != 0 {
		return false
	}
	registry.slots = nil
	return true
}

func (registry *Registry[T]) Get(key string) (T, bool) {
	var zero T
	if registry == nil || key == "" {
		return zero, false
	}
	for index := range registry.slots {
		entry := &registry.slots[index]
		if entry.occupied && entry.key == key {
			return entry.value, true
		}
	}
	return zero, false
}

func (registry *Registry[T]) ContainsExact(key string, value T) bool {
	_, found := registry.IndexExact(key, value)
	return found
}

func (registry *Registry[T]) IndexExact(key string, value T) (int, bool) {
	if registry == nil || key == "" {
		return -1, false
	}
	for index := range registry.slots {
		entry := &registry.slots[index]
		if entry.occupied && entry.key == key && entry.value == value {
			return index, true
		}
	}
	return -1, false
}

func (registry *Registry[T]) Put(key string, value T) bool {
	if registry == nil || registry.slots == nil || key == "" {
		return false
	}
	freeIndex := -1
	for index := range registry.slots {
		entry := &registry.slots[index]
		if entry.occupied {
			if entry.key == key {
				return false
			}
			continue
		}
		if freeIndex < 0 {
			freeIndex = index
		}
	}
	if freeIndex < 0 {
		return false
	}
	registry.slots[freeIndex] = slot[T]{
		key:      key,
		value:    value,
		occupied: true,
	}
	registry.count++
	return true
}

func (registry *Registry[T]) Move(oldKey, newKey string, value T) bool {
	if registry == nil || registry.slots == nil || oldKey == "" || newKey == "" {
		return false
	}
	oldIndex := -1
	for index := range registry.slots {
		entry := &registry.slots[index]
		if !entry.occupied {
			continue
		}
		if entry.key == newKey && newKey != oldKey {
			return false
		}
		if entry.key == oldKey && entry.value == value {
			oldIndex = index
		}
	}
	if oldIndex < 0 {
		return false
	}
	registry.slots[oldIndex].key = newKey
	return true
}

func (registry *Registry[T]) RemoveExact(key string, value T) bool {
	if registry == nil || key == "" {
		return false
	}
	for index := range registry.slots {
		entry := &registry.slots[index]
		if entry.occupied && entry.key == key && entry.value == value {
			registry.clear(index)
			return true
		}
	}
	return false
}

func (registry *Registry[T]) Entry(index int) (string, T, bool) {
	var zero T
	if registry == nil || index < 0 || index >= len(registry.slots) {
		return "", zero, false
	}
	entry := registry.slots[index]
	if !entry.occupied {
		return "", zero, false
	}
	return entry.key, entry.value, true
}

func (registry *Registry[T]) RemoveAt(index int) bool {
	if registry == nil || index < 0 || index >= len(registry.slots) ||
		!registry.slots[index].occupied {
		return false
	}
	registry.clear(index)
	return true
}

func (registry *Registry[T]) clear(index int) {
	registry.slots[index] = slot[T]{}
	registry.count--
}
