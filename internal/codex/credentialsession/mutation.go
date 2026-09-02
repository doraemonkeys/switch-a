package credentialsession

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

type mutationCoordinator struct {
	mu       sync.Mutex
	sessions map[string]*mutationLock
}

type mutationLock struct {
	permit     chan struct{}
	references int
}

type mutationLease struct {
	sessionID string
	lock      *mutationLock
}

type mutationContextKey struct{}

type mutationOwnership struct {
	coordinator *mutationCoordinator
	sessionIDs  []string
	active      atomic.Bool
}

// MutationCoordinator serializes refresh/import/delete work by SessionID. The
// context capability lets nested repository calls reuse an existing lease
// without storing context in a struct.
type MutationCoordinator struct {
	inner *mutationCoordinator
}

func NewMutationCoordinator() *MutationCoordinator {
	return &MutationCoordinator{inner: &mutationCoordinator{sessions: make(map[string]*mutationLock)}}
}

func (c *MutationCoordinator) With(ctx context.Context, sessionIDs []string) (context.Context, func(), error) {
	if c == nil || c.inner == nil {
		return nil, nil, fmt.Errorf("credential session mutation coordinator is not initialized")
	}
	return c.inner.with(ctx, sessionIDs)
}

func (c *MutationCoordinator) Owns(ctx context.Context, sessionID string) bool {
	if c == nil || c.inner == nil {
		return false
	}
	ownership, _ := ctx.Value(mutationContextKey{}).(*mutationOwnership)
	return ownership != nil && ownership.coordinator == c.inner && ownership.active.Load() && ownership.covers([]string{strings.TrimSpace(sessionID)})
}

func (c *mutationCoordinator) with(ctx context.Context, sessionIDs []string) (context.Context, func(), error) {
	if ctx == nil {
		return nil, nil, fmt.Errorf("acquire credential session mutations: context must not be nil")
	}
	normalized, err := normalizeSessionIDs(sessionIDs)
	if err != nil {
		return nil, nil, err
	}
	if ownership, _ := ctx.Value(mutationContextKey{}).(*mutationOwnership); ownership != nil && ownership.coordinator == c && ownership.active.Load() {
		if !ownership.covers(normalized) {
			return nil, nil, fmt.Errorf("acquire credential session mutations: active lease cannot be expanded")
		}
		return ctx, func() {}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	leases := make([]mutationLease, 0, len(normalized))
	for _, sessionID := range normalized {
		lock := c.reference(sessionID)
		select {
		case <-ctx.Done():
			c.unreference(sessionID, lock)
			c.release(leases)
			return nil, nil, ctx.Err()
		case <-lock.permit:
			if err := ctx.Err(); err != nil {
				lock.permit <- struct{}{}
				c.unreference(sessionID, lock)
				c.release(leases)
				return nil, nil, err
			}
			leases = append(leases, mutationLease{sessionID: sessionID, lock: lock})
		}
	}
	ownership := &mutationOwnership{coordinator: c, sessionIDs: normalized}
	ownership.active.Store(true)
	ownedCtx := context.WithValue(ctx, mutationContextKey{}, ownership)
	var once sync.Once
	return ownedCtx, func() {
		once.Do(func() {
			ownership.active.Store(false)
			c.release(leases)
		})
	}, nil
}

func (o *mutationOwnership) covers(sessionIDs []string) bool {
	for _, sessionID := range sessionIDs {
		if _, found := slices.BinarySearch(o.sessionIDs, sessionID); !found {
			return false
		}
	}
	return true
}

func normalizeSessionIDs(sessionIDs []string) ([]string, error) {
	unique := make(map[string]struct{}, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		sessionID = strings.TrimSpace(sessionID)
		if sessionID == "" {
			return nil, fmt.Errorf("acquire credential session mutations: session ID must not be blank")
		}
		unique[sessionID] = struct{}{}
	}
	result := make([]string, 0, len(unique))
	for sessionID := range unique {
		result = append(result, sessionID)
	}
	slices.Sort(result)
	return result, nil
}

func (c *mutationCoordinator) reference(sessionID string) *mutationLock {
	c.mu.Lock()
	defer c.mu.Unlock()
	lock := c.sessions[sessionID]
	if lock == nil {
		lock = &mutationLock{permit: make(chan struct{}, 1)}
		lock.permit <- struct{}{}
		c.sessions[sessionID] = lock
	}
	lock.references++
	return lock
}

func (c *mutationCoordinator) unreference(sessionID string, lock *mutationLock) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sessions[sessionID] != lock || lock.references < 1 {
		panic("credentialsession: invalid mutation lock lifecycle")
	}
	lock.references--
	if lock.references == 0 {
		delete(c.sessions, sessionID)
	}
}

func (c *mutationCoordinator) release(leases []mutationLease) {
	for _, lease := range slices.Backward(leases) {
		lease.lock.permit <- struct{}{}
		c.unreference(lease.sessionID, lease.lock)
	}
}
