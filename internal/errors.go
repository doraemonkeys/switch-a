// Package internal provides shared errors for the switch-a proxy.
package internal

import "errors"

// Shared errors used across packages.
var (
	// ErrNoProvider indicates no provider is available.
	ErrNoProvider = errors.New("no available provider")
)
