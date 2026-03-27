package store

import (
	"errors"
	"fmt"
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
