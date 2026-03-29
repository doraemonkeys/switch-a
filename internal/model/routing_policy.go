package model

import (
	"strings"
	"time"
)

// RoutingPolicyModelMatchType controls how a request model name is matched.
type RoutingPolicyModelMatchType string

const (
	RoutingPolicyModelMatchTypeNone   RoutingPolicyModelMatchType = ""
	RoutingPolicyModelMatchTypeExact  RoutingPolicyModelMatchType = "exact"
	RoutingPolicyModelMatchTypePrefix RoutingPolicyModelMatchType = "prefix"
)

// IsValidRoutingPolicyModelMatchType reports whether the match operator is supported.
func IsValidRoutingPolicyModelMatchType(value RoutingPolicyModelMatchType) bool {
	switch value {
	case RoutingPolicyModelMatchTypeNone, RoutingPolicyModelMatchTypeExact, RoutingPolicyModelMatchTypePrefix:
		return true
	default:
		return false
	}
}

// RoutingPolicyNaturalKey identifies routing rules independently of storage-local
// IDs so config transfer and uniqueness checks can talk about the semantic rule.
type RoutingPolicyNaturalKey struct {
	APIType         string
	ModelMatchType  RoutingPolicyModelMatchType
	ModelMatchValue string
}

// NewRoutingPolicyNaturalKey trims transport noise so every caller compares the
// same canonical rule identity before touching persistence.
func NewRoutingPolicyNaturalKey(
	apiType string,
	modelMatchType RoutingPolicyModelMatchType,
	modelMatchValue string,
) RoutingPolicyNaturalKey {
	key := RoutingPolicyNaturalKey{
		APIType:         strings.TrimSpace(apiType),
		ModelMatchType:  RoutingPolicyModelMatchType(strings.TrimSpace(string(modelMatchType))),
		ModelMatchValue: strings.TrimSpace(modelMatchValue),
	}
	if key.ModelMatchType == RoutingPolicyModelMatchTypeNone {
		key.ModelMatchValue = ""
	}
	return key
}

// RoutingPolicy constrains the provider candidate set before failover rules run.
type RoutingPolicy struct {
	ID              uint                        `gorm:"primaryKey" json:"id"`
	APIType         string                      `gorm:"type:text;not null;index:idx_routing_policy_match,unique" json:"api_type"`
	ModelMatchType  RoutingPolicyModelMatchType `gorm:"type:text;not null;default:'';index:idx_routing_policy_match,unique" json:"model_match_type,omitempty"`
	ModelMatchValue string                      `gorm:"type:text;not null;default:'';index:idx_routing_policy_match,unique" json:"model_match_value,omitempty"`
	// Enabled is part of the durable resource contract so lifecycle changes keep
	// the natural rule key claimed instead of faking deletion through separate APIs.
	Enabled bool `gorm:"type:boolean;not null;default:true;index" json:"enabled"`
	// TargetProviderID captures the exact-provider terminal constraint. Group and
	// vendor filters remain separate so filter-mode edits never need to rewrite IDs.
	TargetProviderID *string               `gorm:"type:text;index" json:"target_provider_id,omitempty"`
	Groups           []RoutingPolicyGroup  `gorm:"foreignKey:RoutingPolicyID;constraint:OnDelete:CASCADE" json:"groups,omitempty"`
	Vendors          []RoutingPolicyVendor `gorm:"foreignKey:RoutingPolicyID;constraint:OnDelete:CASCADE" json:"vendors,omitempty"`
	CreatedAt        time.Time             `json:"created_at"`
	UpdatedAt        time.Time             `json:"updated_at"`
}

// NaturalKey returns the canonical identity used for uniqueness and config transfer.
func (p RoutingPolicy) NaturalKey() RoutingPolicyNaturalKey {
	return NewRoutingPolicyNaturalKey(p.APIType, p.ModelMatchType, p.ModelMatchValue)
}

// RoutingPolicyGroup stores hard group constraints separately so future writes
// can diff group and vendor scopes independently.
type RoutingPolicyGroup struct {
	RoutingPolicyID uint   `gorm:"primaryKey" json:"-"`
	GroupID         string `gorm:"primaryKey;type:text" json:"group_id"`
}

// RoutingPolicyVendor stores hard vendor constraints separately for the same
// reason as RoutingPolicyGroup: updates should not require rewriting the rule row.
type RoutingPolicyVendor struct {
	RoutingPolicyID uint   `gorm:"primaryKey" json:"-"`
	Vendor          string `gorm:"primaryKey;type:text" json:"vendor"`
}
