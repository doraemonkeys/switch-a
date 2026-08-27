package providercookie

import "sort"

type Snapshot struct {
	scope   CookieScope
	cookies []StoredCookie
}

func NewSnapshot(scope CookieScope, cookies []StoredCookie) (Snapshot, error) {
	if _, err := scope.MarshalBinary(); err != nil {
		return Snapshot{}, &StateError{Reason: "snapshot scope is empty"}
	}
	seen := make(map[CookieKey]struct{}, len(cookies))
	for _, cookie := range cookies {
		if cookie.key.name == "" {
			return Snapshot{}, &StateError{Reason: "snapshot contains an invalid cookie"}
		}
		if _, exists := seen[cookie.key]; exists {
			return Snapshot{}, &StateError{Reason: "snapshot contains duplicate CookieKey values"}
		}
		seen[cookie.key] = struct{}{}
	}
	return Snapshot{scope: scope, cookies: append([]StoredCookie(nil), cookies...)}, nil
}

func (s Snapshot) Scope() CookieScope { return s.scope }
func (s Snapshot) Cookies() []StoredCookie {
	return append([]StoredCookie(nil), s.cookies...)
}

type Overlay struct {
	scope     CookieScope
	entries   map[CookieKey]Mutation
	max       int
	discarded bool
}

func NewOverlay(scope CookieScope, policy Policy) (*Overlay, error) {
	if _, err := scope.MarshalBinary(); err != nil {
		return nil, &StateError{Reason: "overlay scope is empty"}
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &Overlay{scope: scope, entries: make(map[CookieKey]Mutation), max: policy.MaxCookiesPerAuthority}, nil
}

func (o *Overlay) ApplyBatch(scope CookieScope, mutations []Mutation) error {
	if err := o.requireScope(scope); err != nil {
		return err
	}
	if o.discarded {
		return &StateError{Reason: "cannot apply to a discarded overlay", Cause: ErrOverlayDiscarded}
	}
	projected := make(map[CookieKey]Mutation, len(o.entries)+len(mutations))
	for key, mutation := range o.entries {
		projected[key] = mutation
	}
	for _, mutation := range mutations {
		if mutation.kind != MutationUpsert && mutation.kind != MutationTombstone {
			return &StateError{Reason: "unknown mutation kind"}
		}
		if mutation.key.name == "" {
			return &StateError{Reason: "mutation key is empty"}
		}
		projected[mutation.key] = mutation
	}
	if len(projected) > o.max {
		return &LimitError{Limit: LimitAuthorityEntries, Max: o.max, Actual: len(projected)}
	}
	o.entries = projected
	return nil
}

func (o *Overlay) Apply(scope CookieScope, mutation Mutation) error {
	if err := o.requireScope(scope); err != nil {
		return err
	}
	if o.discarded {
		return &StateError{Reason: "cannot apply to a discarded overlay", Cause: ErrOverlayDiscarded}
	}
	if mutation.kind != MutationUpsert && mutation.kind != MutationTombstone {
		return &StateError{Reason: "unknown mutation kind"}
	}
	if mutation.key.name == "" {
		return &StateError{Reason: "mutation key is empty"}
	}
	if _, exists := o.entries[mutation.key]; !exists && len(o.entries) >= o.max {
		return &LimitError{Limit: LimitAuthorityEntries, Max: o.max, Actual: len(o.entries) + 1}
	}
	o.entries[mutation.key] = mutation
	return nil
}

func (o *Overlay) Changes(scope CookieScope) ([]Mutation, error) {
	if err := o.requireScope(scope); err != nil {
		return nil, err
	}
	if o.discarded {
		return nil, &StateError{Reason: "cannot read a discarded overlay", Cause: ErrOverlayDiscarded}
	}
	changes := make([]Mutation, 0, len(o.entries))
	for _, mutation := range o.entries {
		changes = append(changes, mutation)
	}
	sort.Slice(changes, func(i, j int) bool { return keyLess(changes[i].key, changes[j].key) })
	return changes, nil
}

func (o *Overlay) Discard(scope CookieScope) error {
	if err := o.requireScope(scope); err != nil {
		return err
	}
	o.discarded = true
	o.entries = nil
	return nil
}

func (o *Overlay) requireScope(scope CookieScope) error {
	if o == nil {
		return &StateError{Reason: "overlay is nil"}
	}
	if o.scope != scope {
		return &ScopeError{Expected: o.scope, Actual: scope}
	}
	return nil
}

func keyLess(left, right CookieKey) bool {
	if left.name != right.name {
		return left.name < right.name
	}
	if left.domain != right.domain {
		return left.domain < right.domain
	}
	return left.path < right.path
}
