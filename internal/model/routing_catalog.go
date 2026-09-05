package model

// RoutingCatalog pins the same routing rules across admission and dispatch.
// Availability and leases remain live; only the rules consuming request facts
// are frozen so a later config edit cannot invalidate early upload admission.
type RoutingCatalog struct{ policies []RoutingPolicy }

func NewRoutingCatalog(policies []RoutingPolicy) *RoutingCatalog {
	return &RoutingCatalog{policies: cloneRoutingPolicies(policies)}
}

func (c *RoutingCatalog) Policies() []RoutingPolicy {
	if c == nil {
		return nil
	}
	return cloneRoutingPolicies(c.policies)
}

func cloneRoutingPolicies(source []RoutingPolicy) []RoutingPolicy {
	copied := append([]RoutingPolicy(nil), source...)
	for i := range copied {
		copied[i].Groups = append([]RoutingPolicyGroup(nil), source[i].Groups...)
		copied[i].Vendors = append([]RoutingPolicyVendor(nil), source[i].Vendors...)
		if source[i].TargetProviderID != nil {
			value := *source[i].TargetProviderID
			copied[i].TargetProviderID = &value
		}
	}
	return copied
}
