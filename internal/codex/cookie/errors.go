package providercookie

import (
	"errors"
	"fmt"
)

var (
	ErrMalformedCookie  = errors.New("malformed provider cookie")
	ErrInvalidDomain    = errors.New("invalid provider cookie domain")
	ErrPublicSuffix     = errors.New("provider cookie domain is a public suffix")
	ErrLimitExceeded    = errors.New("provider cookie limit exceeded")
	ErrScopeMismatch    = errors.New("provider cookie scope mismatch")
	ErrOverlayDiscarded = errors.New("provider cookie overlay discarded")
	ErrInvalidRequest   = errors.New("invalid provider cookie request target")
	ErrInvalidConfig    = errors.New("invalid provider cookie configuration")
	ErrBindingNotFound  = errors.New("provider cookie handle binding not found")
	ErrIdentifierClash  = errors.New("provider cookie generated identifier collided")
	ErrStorage          = errors.New("provider cookie storage unavailable")
	ErrStorageCorrupt   = errors.New("provider cookie storage is corrupt")
	ErrCrypto           = errors.New("provider cookie cryptography unavailable")
	ErrDecrypt          = errors.New("provider cookie value authentication failed")
)

// ParseError deliberately excludes the raw Set-Cookie value because it may contain a secret.
type ParseError struct {
	Index  int
	Field  string
	Reason string
	Cause  error
}

func (e *ParseError) Error() string {
	location := "set-cookie"
	if e.Index >= 0 {
		location = fmt.Sprintf("set-cookie[%d]", e.Index)
	}
	if e.Field != "" {
		location += "." + e.Field
	}
	return fmt.Sprintf("%s: %s", location, e.Reason)
}

func (e *ParseError) Unwrap() []error {
	if e.Cause == nil {
		return []error{ErrMalformedCookie}
	}
	return []error{ErrMalformedCookie, e.Cause}
}

type DomainError struct {
	Host   string
	Domain string
	Reason string
	Public bool
}

func (e *DomainError) Error() string {
	return fmt.Sprintf("provider cookie domain %q is invalid for host %q: %s", e.Domain, e.Host, e.Reason)
}

func (e *DomainError) Unwrap() []error {
	errs := []error{ErrInvalidDomain}
	if e.Public {
		errs = append(errs, ErrPublicSuffix)
	}
	return errs
}

type LimitName string

const (
	LimitSetCookieHeaders     LimitName = "set_cookie_headers"
	LimitSetCookieLineBytes   LimitName = "set_cookie_line_bytes"
	LimitSetCookieBytes       LimitName = "set_cookie_bytes"
	LimitCookieNameBytes      LimitName = "cookie_name_bytes"
	LimitCookieValueBytes     LimitName = "cookie_value_bytes"
	LimitCookieDomainBytes    LimitName = "cookie_domain_bytes"
	LimitCookiePathBytes      LimitName = "cookie_path_bytes"
	LimitOutboundHeaderBytes  LimitName = "outbound_cookie_header_bytes"
	LimitAuthorityEntries     LimitName = "authority_entries"
	LimitAuthoritiesPerJar    LimitName = "authorities_per_jar"
	LimitJarEntries           LimitName = "jar_entries"
	LimitHandleBindingsGlobal LimitName = "handle_bindings_global"
	LimitGlobalEntries        LimitName = "global_entries"
)

type LimitError struct {
	Limit  LimitName
	Max    int
	Actual int
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("provider cookie %s limit exceeded: maximum %d, got %d", e.Limit, e.Max, e.Actual)
}

func (e *LimitError) Unwrap() error { return ErrLimitExceeded }

// ScopeError intentionally omits opaque scope values from Error so callers can log it safely.
type ScopeError struct {
	Expected CookieScope
	Actual   CookieScope
}

func (e *ScopeError) Error() string { return "provider cookie scope does not match overlay scope" }
func (e *ScopeError) Unwrap() error { return ErrScopeMismatch }

type StateError struct {
	Reason string
	Cause  error
}

func (e *StateError) Error() string { return "provider cookie state is invalid: " + e.Reason }
func (e *StateError) Unwrap() error {
	if e.Cause == nil {
		return ErrMalformedCookie
	}
	return e.Cause
}

type RequestTargetError struct {
	Reason string
}

func (e *RequestTargetError) Error() string {
	return "provider cookie request target is invalid: " + e.Reason
}

func (e *RequestTargetError) Unwrap() error { return ErrInvalidRequest }

type ConfigurationError struct {
	Field  string
	Reason string
}

func (e *ConfigurationError) Error() string {
	return fmt.Sprintf("provider cookie configuration %s is invalid: %s", e.Field, e.Reason)
}

func (e *ConfigurationError) Unwrap() error { return ErrInvalidConfig }

type PersistenceErrorKind string

const (
	PersistenceUnavailable PersistenceErrorKind = "unavailable"
	PersistenceCorrupt     PersistenceErrorKind = "corrupt"
	PersistenceCrypto      PersistenceErrorKind = "crypto_unavailable"
	PersistenceDecrypt     PersistenceErrorKind = "decrypt_failed"
)

// PersistenceError carries only stable decision context. Database statements,
// handles, authority bytes, Cookie keys, values, nonces, and ciphertext stay out
// of the formatted error so callers can safely attach it to structured traces.
type PersistenceError struct {
	Kind      PersistenceErrorKind
	Operation string
	Cause     error
}

func (e *PersistenceError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("provider cookie persistence %s failed: %s", e.Operation, e.Kind)
}

func (e *PersistenceError) Unwrap() []error {
	category := ErrStorage
	switch e.Kind {
	case PersistenceCorrupt:
		category = ErrStorageCorrupt
	case PersistenceCrypto:
		category = ErrCrypto
	case PersistenceDecrypt:
		category = ErrDecrypt
	}
	if e.Cause == nil {
		return []error{category}
	}
	return []error{category, e.Cause}
}
