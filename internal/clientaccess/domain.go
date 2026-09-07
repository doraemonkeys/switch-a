// Package clientaccess owns downstream API key admission independently of
// upstream credentials and persistent client identity.
package clientaccess

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type Mode string

const (
	ModePermissive Mode = "permissive"
	ModeRestricted Mode = "restricted"
	MaxKeyBytes         = 8192
)

var (
	ErrValidation = errors.New("invalid client API key configuration")
	ErrDuplicate  = errors.New("client API key already exists")
	ErrNotFound   = errors.New("client API key not found")
)

type Key struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Key       string    `json:"key"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Snapshot struct {
	Mode Mode  `json:"mode"`
	Keys []Key `json:"keys"`
}

func validateMode(mode Mode) error {
	if mode != ModePermissive && mode != ModeRestricted {
		return fmt.Errorf("%w: unsupported access mode", ErrValidation)
	}
	return nil
}

func validateName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: name is required", ErrValidation)
	}
	return nil
}

func validateKey(value string) error {
	if value == "" || len(value) > MaxKeyBytes || strings.TrimSpace(value) != value {
		return fmt.Errorf("%w: key must contain 1–%d bytes without surrounding whitespace", ErrValidation, MaxKeyBytes)
	}
	// Imported keys must survive HTTP header transport without changing their
	// identity; no prefix or vendor-specific token alphabet is imposed.
	for _, b := range []byte(value) {
		if b < ' ' || b == 127 {
			return fmt.Errorf("%w: key contains a control character", ErrValidation)
		}
	}
	return nil
}

func validateRecord(key Key) error {
	if strings.TrimSpace(key.ID) == "" {
		return fmt.Errorf("%w: key ID is required", ErrValidation)
	}
	// Browsers normalize dot segments before sending item URLs, so these IDs
	// cannot be addressed by the management API after a portable restore.
	if key.ID == "." || key.ID == ".." {
		return fmt.Errorf("%w: key ID cannot be a URL dot segment", ErrValidation)
	}
	if err := validateName(key.Name); err != nil {
		return err
	}
	return validateKey(key.Key)
}

func ValidateSnapshot(snapshot Snapshot) error {
	if err := validateMode(snapshot.Mode); err != nil {
		return err
	}
	ids := make(map[string]struct{}, len(snapshot.Keys))
	values := make(map[string]struct{}, len(snapshot.Keys))
	for _, key := range snapshot.Keys {
		if err := validateRecord(key); err != nil {
			return err
		}
		if _, exists := ids[key.ID]; exists {
			return fmt.Errorf("%w: duplicate key ID", ErrValidation)
		}
		if _, exists := values[key.Key]; exists {
			return fmt.Errorf("%w: duplicate key value", ErrValidation)
		}
		ids[key.ID] = struct{}{}
		values[key.Key] = struct{}{}
	}
	return nil
}
