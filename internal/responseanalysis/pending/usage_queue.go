package pending

import "sync"

// usageQueue serializes compaction with dequeue so a saturated producer cannot
// move an older partial observation behind a newer observation already taken by
// the coordinator. The signal is only a readiness hint; ownership stays in
// items until take transfers it to the coordinator.
type usageQueue[T any] struct {
	mu       sync.Mutex
	items    []T
	ready    chan struct{}
	capacity int
	overlay  func(*T, T)
	release  func(*T)
}

func newUsageQueue[T any](capacity int, overlay func(*T, T), release func(*T)) *usageQueue[T] {
	return &usageQueue[T]{
		items:    make([]T, 0, capacity),
		ready:    make(chan struct{}, 1),
		capacity: capacity,
		overlay:  overlay,
		release:  release,
	}
}

func (q *usageQueue[T]) offer(observation T) {
	q.mu.Lock()
	if len(q.items) < q.capacity {
		q.items = append(q.items, observation)
	} else {
		q.compact(observation)
	}
	q.mu.Unlock()
	q.signal()
}

func (q *usageQueue[T]) compact(observation T) {
	if q.overlay == nil {
		for index := range q.items {
			q.release(&q.items[index])
		}
		q.items = append(q.items[:0], observation)
		return
	}

	aggregate := q.items[0]
	for index := 1; index < len(q.items); index++ {
		q.overlay(&aggregate, q.items[index])
		q.release(&q.items[index])
	}
	q.overlay(&aggregate, observation)
	q.release(&observation)
	q.items = append(q.items[:0], aggregate)
}

func (q *usageQueue[T]) take() (T, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		var zero T
		return zero, false
	}

	observation := q.items[0]
	var zero T
	q.items[0] = zero
	q.items = q.items[1:]
	if len(q.items) > 0 {
		q.signal()
	}
	return observation, true
}

func (q *usageQueue[T]) signal() {
	select {
	case q.ready <- struct{}{}:
	default:
	}
}
