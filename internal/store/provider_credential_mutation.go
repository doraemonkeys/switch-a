package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

type providerCredentialMutationCoordinator struct {
	mu        sync.Mutex
	providers map[string]*providerCredentialMutationLock
}

type providerCredentialMutationLock struct {
	permit     chan struct{}
	references int
}

type providerCredentialMutationLease struct {
	providerID string
	lock       *providerCredentialMutationLock
}

type providerCredentialMutationContextKey struct{}

type providerCredentialMutationOwnership struct {
	coordinator *providerCredentialMutationCoordinator
	providerIDs []string
	active      atomic.Bool
}

type providerCredentialMutationLeaseExpansionError struct {
	providerID string
}

func (e *providerCredentialMutationLeaseExpansionError) Error() string {
	return fmt.Sprintf("provider credential mutation lease must include provider %q", e.providerID)
}

func newProviderCredentialMutationCoordinator() *providerCredentialMutationCoordinator {
	return &providerCredentialMutationCoordinator{
		providers: make(map[string]*providerCredentialMutationLock),
	}
}

// WithProviderCredentialMutations serializes credential changes per provider and
// returns a context proving ownership to nested store writes. Sorting the whole
// lock set before acquisition prevents cycles when workflows supply different order.
func (s *SQLiteStore) WithProviderCredentialMutations(
	ctx context.Context,
	providerIDs []string,
) (ownedCtx context.Context, release func(), err error) {
	if s.credentialMutations == nil {
		return nil, nil, fmt.Errorf("provider credential mutation coordinator is not initialized")
	}
	return s.credentialMutations.with(ctx, providerIDs)
}

func (c *providerCredentialMutationCoordinator) with(
	ctx context.Context,
	providerIDs []string,
) (context.Context, func(), error) {
	if ctx == nil {
		return nil, nil, fmt.Errorf("acquire provider credential mutations: context must not be nil")
	}

	normalizedIDs, err := normalizeProviderCredentialMutationIDs(providerIDs)
	if err != nil {
		return nil, nil, err
	}
	if ownership := providerCredentialMutationOwnershipFromContext(ctx); ownership != nil &&
		ownership.coordinator == c && ownership.active.Load() {
		if !ownership.covers(normalizedIDs) {
			return nil, nil, fmt.Errorf(
				"acquire provider credential mutations: active lease cannot be expanded",
			)
		}
		return ctx, func() {}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	leases := make([]providerCredentialMutationLease, 0, len(normalizedIDs))
	for _, providerID := range normalizedIDs {
		lock := c.reference(providerID)
		select {
		case <-ctx.Done():
			c.unreference(providerID, lock)
			c.release(leases)
			return nil, nil, ctx.Err()
		case <-lock.permit:
			// A canceled caller must not enter a credential write merely because a
			// permit and cancellation became ready in the same scheduler turn.
			if err := ctx.Err(); err != nil {
				lock.permit <- struct{}{}
				c.unreference(providerID, lock)
				c.release(leases)
				return nil, nil, err
			}
			leases = append(leases, providerCredentialMutationLease{
				providerID: providerID,
				lock:       lock,
			})
		}
	}

	ownership := &providerCredentialMutationOwnership{
		coordinator: c,
		providerIDs: normalizedIDs,
	}
	ownership.active.Store(true)
	ownedCtx := context.WithValue(ctx, providerCredentialMutationContextKey{}, ownership)
	var releaseOnce sync.Once
	return ownedCtx, func() {
		releaseOnce.Do(func() {
			ownership.active.Store(false)
			c.release(leases)
		})
	}, nil
}

func providerCredentialMutationOwnershipFromContext(ctx context.Context) *providerCredentialMutationOwnership {
	ownership, _ := ctx.Value(providerCredentialMutationContextKey{}).(*providerCredentialMutationOwnership)
	return ownership
}

func (o *providerCredentialMutationOwnership) covers(providerIDs []string) bool {
	for _, providerID := range providerIDs {
		if _, found := slices.BinarySearch(o.providerIDs, providerID); !found {
			return false
		}
	}
	return true
}

func (c *providerCredentialMutationCoordinator) contextOwns(ctx context.Context, providerID string) bool {
	ownership := providerCredentialMutationOwnershipFromContext(ctx)
	if ownership == nil || ownership.coordinator != c || !ownership.active.Load() {
		return false
	}
	return ownership.covers([]string{strings.TrimSpace(providerID)})
}

func (s *SQLiteStore) runWithProviderCredentialMutations(
	ctx context.Context,
	providerIDs []string,
	operation func(context.Context) error,
) error {
	requestedIDs := append([]string(nil), providerIDs...)
	for {
		ownedCtx, release, err := s.WithProviderCredentialMutations(ctx, requestedIDs)
		if err != nil {
			return err
		}
		err = func() error {
			defer release()
			return operation(ownedCtx)
		}()

		var expansion *providerCredentialMutationLeaseExpansionError
		if !errors.As(err, &expansion) {
			return err
		}
		expansionID := strings.TrimSpace(expansion.providerID)
		if expansionID == "" || slices.Contains(requestedIDs, expansionID) {
			return fmt.Errorf("expand provider credential mutation lease: %w", err)
		}
		// Binding replacement can discover the current owner only inside the same
		// transaction that validates uniqueness. Retrying with that owner leased
		// closes the cross-provider clear race without serializing unrelated accounts.
		requestedIDs = append(requestedIDs, expansionID)
	}
}

func normalizeProviderCredentialMutationIDs(providerIDs []string) ([]string, error) {
	unique := make(map[string]struct{}, len(providerIDs))
	for _, providerID := range providerIDs {
		providerID = strings.TrimSpace(providerID)
		if providerID == "" {
			return nil, fmt.Errorf("acquire provider credential mutations: provider ID must not be blank")
		}
		unique[providerID] = struct{}{}
	}

	normalized := make([]string, 0, len(unique))
	for providerID := range unique {
		normalized = append(normalized, providerID)
	}
	slices.Sort(normalized)
	return normalized, nil
}

func (c *providerCredentialMutationCoordinator) reference(providerID string) *providerCredentialMutationLock {
	c.mu.Lock()
	defer c.mu.Unlock()

	lock := c.providers[providerID]
	if lock == nil {
		lock = &providerCredentialMutationLock{
			permit: make(chan struct{}, 1),
		}
		lock.permit <- struct{}{}
		c.providers[providerID] = lock
	}
	lock.references++
	return lock
}

func (c *providerCredentialMutationCoordinator) unreference(
	providerID string,
	lock *providerCredentialMutationLock,
) {
	c.mu.Lock()
	defer c.mu.Unlock()

	current := c.providers[providerID]
	if current != lock || lock.references < 1 {
		panic("store: invalid provider credential mutation lock lifecycle")
	}
	lock.references--
	if lock.references == 0 {
		// Entries exist only while held or awaited, so churn in provider IDs cannot
		// turn the coordinator into an unbounded process-lifetime registry.
		delete(c.providers, providerID)
	}
}

func (c *providerCredentialMutationCoordinator) release(leases []providerCredentialMutationLease) {
	for index := len(leases) - 1; index >= 0; index-- {
		lease := leases[index]
		lease.lock.permit <- struct{}{}
		c.unreference(lease.providerID, lease.lock)
	}
}
