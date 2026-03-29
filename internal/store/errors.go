package store

import (
	"errors"
	"fmt"

	"switch-a/internal/model"
)

// ErrNotFound is returned when a requested resource does not exist.
var ErrNotFound = errors.New("not found")

// ErrCredentialBindingConflict reports that a login-backed credential is already
// bound to another provider. This is surfaced as a conflict instead of a generic
// validation error because the payload is internally valid but violates a global
// uniqueness invariant.
var ErrCredentialBindingConflict = errors.New("credential binding conflict")

// ErrCredentialVersionConflict reports that a credential write raced with another
// successful update and the caller must re-read before retrying.
var ErrCredentialVersionConflict = errors.New("credential version conflict")

// ErrRoutingPolicyConflict reports that a routing policy write violated the
// uniqueness of the (api_type, model_match_type, model_match_value) rule key.
var ErrRoutingPolicyConflict = errors.New("routing policy conflict")

// ErrRoutingPolicyReferenceConflict reports that another resource mutation would
// break a direct routing-policy reference or an exact-provider API-type contract.
var ErrRoutingPolicyReferenceConflict = errors.New("routing policy reference conflict")

// CredentialBindingConflictError describes which provider already owns the login.
type CredentialBindingConflictError struct {
	AccountID  string
	ProviderID string
}

func (e *CredentialBindingConflictError) Error() string {
	return fmt.Sprintf("GPT account %q is already bound to provider %q", e.AccountID, e.ProviderID)
}

func (e *CredentialBindingConflictError) Is(target error) bool {
	return target == ErrCredentialBindingConflict
}

// CredentialVersionConflictError describes the stale version that lost the CAS race.
type CredentialVersionConflictError struct {
	ProviderID      string
	ExpectedVersion int64
	CurrentVersion  int64
}

func (e *CredentialVersionConflictError) Error() string {
	return fmt.Sprintf(
		"provider %q credential version conflict: expected %d, current %d",
		e.ProviderID,
		e.ExpectedVersion,
		e.CurrentVersion,
	)
}

func (e *CredentialVersionConflictError) Is(target error) bool {
	return target == ErrCredentialVersionConflict
}

// RoutingPolicyConflictError describes which routing rule key is already claimed.
type RoutingPolicyConflictError struct {
	ExistingID      uint
	APIType         string
	ModelMatchType  model.RoutingPolicyModelMatchType
	ModelMatchValue string
}

func (e *RoutingPolicyConflictError) Error() string {
	if e.ModelMatchType == model.RoutingPolicyModelMatchTypeNone {
		return fmt.Sprintf("routing policy for api_type %q already exists", e.APIType)
	}
	return fmt.Sprintf(
		"routing policy for api_type %q and %s model match %q already exists",
		e.APIType,
		e.ModelMatchType,
		e.ModelMatchValue,
	)
}

func (e *RoutingPolicyConflictError) Is(target error) bool {
	return target == ErrRoutingPolicyConflict
}

// RoutingPolicyGroupReferenceConflictError reports that a group cannot be
// deleted while a routing policy still scopes to it.
type RoutingPolicyGroupReferenceConflictError struct {
	GroupID  string
	PolicyID uint
	Key      model.RoutingPolicyNaturalKey
}

func (e *RoutingPolicyGroupReferenceConflictError) Error() string {
	return fmt.Sprintf(
		"group %q is referenced by %s",
		e.GroupID,
		formatRoutingPolicyReference(e.PolicyID, e.Key),
	)
}

func (e *RoutingPolicyGroupReferenceConflictError) Is(target error) bool {
	return target == ErrRoutingPolicyReferenceConflict
}

// RoutingPolicyProviderReferenceConflictError reports that an exact-provider
// rule still targets the provider being deleted.
type RoutingPolicyProviderReferenceConflictError struct {
	ProviderID string
	PolicyID   uint
	Key        model.RoutingPolicyNaturalKey
}

func (e *RoutingPolicyProviderReferenceConflictError) Error() string {
	return fmt.Sprintf(
		"provider %q is targeted by %s",
		e.ProviderID,
		formatRoutingPolicyReference(e.PolicyID, e.Key),
	)
}

func (e *RoutingPolicyProviderReferenceConflictError) Is(target error) bool {
	return target == ErrRoutingPolicyReferenceConflict
}

// RoutingPolicyProviderAPITypeConflictError reports that an exact-provider rule
// would become invalid if the provider stopped advertising the referenced API type.
type RoutingPolicyProviderAPITypeConflictError struct {
	ProviderID string
	APIType    string
	PolicyID   uint
	Key        model.RoutingPolicyNaturalKey
}

func (e *RoutingPolicyProviderAPITypeConflictError) Error() string {
	return fmt.Sprintf(
		"provider %q cannot remove api_type %q because %s targets it",
		e.ProviderID,
		e.APIType,
		formatRoutingPolicyReference(e.PolicyID, e.Key),
	)
}

func (e *RoutingPolicyProviderAPITypeConflictError) Is(target error) bool {
	return target == ErrRoutingPolicyReferenceConflict
}

func formatRoutingPolicyReference(policyID uint, key model.RoutingPolicyNaturalKey) string {
	if key.ModelMatchType == model.RoutingPolicyModelMatchTypeNone {
		return fmt.Sprintf("routing policy %d for api_type %q", policyID, key.APIType)
	}
	return fmt.Sprintf(
		"routing policy %d for api_type %q and %s model match %q",
		policyID,
		key.APIType,
		key.ModelMatchType,
		key.ModelMatchValue,
	)
}
