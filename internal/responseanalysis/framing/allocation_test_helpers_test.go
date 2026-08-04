package framing

import (
	"sync"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
)

type reservationRequest struct {
	class    allocation.Class
	capacity int
}

type trackingReserver struct {
	mu sync.Mutex

	active   int
	peak     int
	requests []reservationRequest
	denyAt   int
	nilAt    int
}

func (r *trackingReserver) Reserve(class allocation.Class, capacity int) (allocation.Grant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, reservationRequest{class: class, capacity: capacity})
	requestNumber := len(r.requests)
	if r.denyAt == requestNumber {
		return nil, &allocation.Denial{
			Reason:            allocation.DenialRequestMemoryExhausted,
			Class:             class,
			RequestedCapacity: capacity,
		}
	}
	if r.nilAt == requestNumber {
		return nil, nil
	}
	r.active += capacity
	if r.active > r.peak {
		r.peak = r.active
	}
	return &trackingGrant{reserver: r, capacity: capacity}, nil
}

func (r *trackingReserver) snapshot() (active, peak int, requests []reservationRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active, r.peak, append([]reservationRequest(nil), r.requests...)
}

type trackingGrant struct {
	once     sync.Once
	reserver *trackingReserver
	capacity int
}

func (g *trackingGrant) Release() {
	if g == nil {
		return
	}
	g.once.Do(func() {
		g.reserver.mu.Lock()
		g.reserver.active -= g.capacity
		g.reserver.mu.Unlock()
	})
}

type boundedSnapshot struct {
	active   int
	peak     int
	denials  int
	byClass  map[allocation.Class]int
	requests []reservationRequest
}

// boundedReserver models the request account used by the runtime: every grant
// contributes its complete capacity and a request is rejected before the
// configured ceiling could be exceeded.
type boundedReserver struct {
	mu sync.Mutex

	limit    int
	active   int
	peak     int
	denials  int
	byClass  map[allocation.Class]int
	requests []reservationRequest
}

func newBoundedReserver(limit int) *boundedReserver {
	return &boundedReserver{limit: limit, byClass: make(map[allocation.Class]int)}
}

func (r *boundedReserver) Reserve(class allocation.Class, capacity int) (allocation.Grant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, reservationRequest{class: class, capacity: capacity})
	if capacity < 0 || capacity > r.limit-r.active {
		r.denials++
		return nil, &allocation.Denial{
			Reason:            allocation.DenialRequestMemoryExhausted,
			Class:             class,
			RequestedCapacity: capacity,
		}
	}
	r.active += capacity
	r.byClass[class] += capacity
	if r.active > r.peak {
		r.peak = r.active
	}
	return &boundedGrant{reserver: r, class: class, capacity: capacity}, nil
}

func (r *boundedReserver) snapshot() boundedSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	byClass := make(map[allocation.Class]int, len(r.byClass))
	for class, capacity := range r.byClass {
		byClass[class] = capacity
	}
	return boundedSnapshot{
		active:   r.active,
		peak:     r.peak,
		denials:  r.denials,
		byClass:  byClass,
		requests: append([]reservationRequest(nil), r.requests...),
	}
}

type boundedGrant struct {
	once     sync.Once
	reserver *boundedReserver
	class    allocation.Class
	capacity int
}

func (g *boundedGrant) Release() {
	if g == nil || g.reserver == nil {
		return
	}
	g.once.Do(func() {
		g.reserver.mu.Lock()
		g.reserver.active -= g.capacity
		g.reserver.byClass[g.class] -= g.capacity
		g.reserver.mu.Unlock()
	})
}
