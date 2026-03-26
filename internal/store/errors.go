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
