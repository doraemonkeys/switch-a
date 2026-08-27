package store

import (
	"errors"
	"fmt"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/model"
)

// ErrNotFound is returned when a requested resource does not exist.
var ErrNotFound = errors.New("not found")

// ErrProviderImportConflict reports that an atomic provider import plan no longer
// matches durable provider state. Callers can safely re-preview and retry because
// the store returns this error before committing any mutation in the bundle.
var ErrProviderImportConflict = errors.New("provider import conflict")

// ErrProviderImportReceiptNotFound treats missing and expired receipts alike so
// callers can fall back to the staged-draft path without learning storage details.
var ErrProviderImportReceiptNotFound = errors.New("provider import receipt not found")

// ErrProviderImportReceiptReplay identifies an already-committed request whose
// exact response is available on ProviderImportReceiptReplayError.
var ErrProviderImportReceiptReplay = errors.New("provider import receipt replay")

// ErrProviderImportReceiptConflict rejects a different request attempting to
// reuse an import ID that already has a durable commit proof.
var ErrProviderImportReceiptConflict = errors.New("provider import receipt conflict")

// ErrProviderImportReceiptResponsePayloadTooLarge prevents durable replay data
// from growing beyond the store's explicit admission bound.
var ErrProviderImportReceiptResponsePayloadTooLarge = errors.New(
	"provider import receipt response payload too large",
)

// ErrRoutingPolicyConflict reports that a routing policy write violated the
// uniqueness of the (api_type, model_match_type, model_match_value) rule key.
var ErrRoutingPolicyConflict = errors.New("routing policy conflict")

// ErrRoutingPolicyReferenceConflict reports that another resource mutation would
// break a direct routing-policy reference or an exact-provider API-type contract.
var ErrRoutingPolicyReferenceConflict = errors.New("routing policy reference conflict")

// ProviderImportConflictKind identifies the invariant that rejected one selected
// import candidate. Stable values let the admin surface present targeted recovery
// actions without parsing human-readable database errors.
type ProviderImportConflictKind string

const (
	ProviderImportConflictProviderAlreadyExists     ProviderImportConflictKind = "provider_already_exists"
	ProviderImportConflictProviderNotFound          ProviderImportConflictKind = "provider_not_found"
	ProviderImportConflictSessionNotFound           ProviderImportConflictKind = "credential_session_not_found"
	ProviderImportConflictCredentialVersionMismatch ProviderImportConflictKind = "credential_version_mismatch"
)

// ProviderImportConflict carries only non-secret identity and concurrency facts.
// Candidate IDs correlate conflicts back to preview rows; token material must never
// cross this error boundary.
type ProviderImportConflict struct {
	CandidateID               string                     `json:"candidate_id"`
	Kind                      ProviderImportConflictKind `json:"kind"`
	ProviderID                string                     `json:"provider_id,omitempty"`
	ExpectedCredentialVersion int64                      `json:"expected_credential_version,omitempty"`
	CurrentCredentialVersion  int64                      `json:"current_credential_version,omitempty"`
}

// ProviderImportConflictError aggregates every state conflict found during the
// transaction preflight so users can resolve a stale batch in one pass.
type ProviderImportConflictError struct {
	Conflicts []ProviderImportConflict `json:"conflicts"`
}

func (e *ProviderImportConflictError) Error() string {
	if e == nil || len(e.Conflicts) == 0 {
		return ErrProviderImportConflict.Error()
	}
	kinds := make([]string, 0, len(e.Conflicts))
	for i := range e.Conflicts {
		kinds = append(kinds, string(e.Conflicts[i].Kind))
	}
	return fmt.Sprintf("%s: %s", ErrProviderImportConflict, strings.Join(kinds, ", "))
}

func (e *ProviderImportConflictError) Is(target error) bool {
	return target == ErrProviderImportConflict
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
