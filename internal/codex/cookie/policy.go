package providercookie

import "time"

const (
	DefaultHandleIdleTTL           = 30 * 24 * time.Hour
	DefaultHandleAbsoluteTTL       = 180 * 24 * time.Hour
	DefaultHandleRefreshWindow     = 7 * 24 * time.Hour
	DefaultSessionCookieTTL        = 24 * time.Hour
	DefaultMaxPersistentCookieTTL  = 90 * 24 * time.Hour
	DefaultOrphanAuthorityGrace    = 24 * time.Hour
	DefaultMaxSetCookieHeaders     = 64
	DefaultMaxSetCookieLineBytes   = 8 * 1024
	DefaultMaxSetCookieBytes       = 64 * 1024
	DefaultMaxCookieNameBytes      = 256
	DefaultMaxCookieValueBytes     = 4096
	DefaultMaxCookieDomainBytes    = 253
	DefaultMaxCookiePathBytes      = 1024
	DefaultMaxOutboundHeaderBytes  = 16 * 1024
	DefaultMaxCookiesPerAuthority  = 180
	DefaultMaxAuthoritiesPerJar    = 32
	DefaultMaxCookiesPerJar        = 720
	DefaultMaxHandleBindingsGlobal = 10_000
	DefaultMaxCookieEntriesGlobal  = 100_000
)

// Policy is captured once per logical request so a request cannot observe mixed resource rules.
type Policy struct {
	HandleIdleTTL           time.Duration
	HandleAbsoluteTTL       time.Duration
	HandleRefreshWindow     time.Duration
	SessionCookieTTL        time.Duration
	MaxPersistentCookieTTL  time.Duration
	OrphanAuthorityGrace    time.Duration
	MaxSetCookieHeaders     int
	MaxSetCookieLineBytes   int
	MaxSetCookieBytes       int
	MaxCookieNameBytes      int
	MaxCookieValueBytes     int
	MaxCookieDomainBytes    int
	MaxCookiePathBytes      int
	MaxOutboundHeaderBytes  int
	MaxCookiesPerAuthority  int
	MaxAuthoritiesPerJar    int
	MaxCookiesPerJar        int
	MaxHandleBindingsGlobal int
	MaxCookieEntriesGlobal  int
}

func DefaultPolicy() Policy {
	return Policy{
		HandleIdleTTL:           DefaultHandleIdleTTL,
		HandleAbsoluteTTL:       DefaultHandleAbsoluteTTL,
		HandleRefreshWindow:     DefaultHandleRefreshWindow,
		SessionCookieTTL:        DefaultSessionCookieTTL,
		MaxPersistentCookieTTL:  DefaultMaxPersistentCookieTTL,
		OrphanAuthorityGrace:    DefaultOrphanAuthorityGrace,
		MaxSetCookieHeaders:     DefaultMaxSetCookieHeaders,
		MaxSetCookieLineBytes:   DefaultMaxSetCookieLineBytes,
		MaxSetCookieBytes:       DefaultMaxSetCookieBytes,
		MaxCookieNameBytes:      DefaultMaxCookieNameBytes,
		MaxCookieValueBytes:     DefaultMaxCookieValueBytes,
		MaxCookieDomainBytes:    DefaultMaxCookieDomainBytes,
		MaxCookiePathBytes:      DefaultMaxCookiePathBytes,
		MaxOutboundHeaderBytes:  DefaultMaxOutboundHeaderBytes,
		MaxCookiesPerAuthority:  DefaultMaxCookiesPerAuthority,
		MaxAuthoritiesPerJar:    DefaultMaxAuthoritiesPerJar,
		MaxCookiesPerJar:        DefaultMaxCookiesPerJar,
		MaxHandleBindingsGlobal: DefaultMaxHandleBindingsGlobal,
		MaxCookieEntriesGlobal:  DefaultMaxCookieEntriesGlobal,
	}
}

func (p Policy) Validate() error {
	durations := []struct {
		name  string
		value time.Duration
	}{
		{"handle_idle_ttl", p.HandleIdleTTL},
		{"handle_absolute_ttl", p.HandleAbsoluteTTL},
		{"handle_refresh_window", p.HandleRefreshWindow},
		{"session_cookie_ttl", p.SessionCookieTTL},
		{"max_persistent_cookie_ttl", p.MaxPersistentCookieTTL},
		{"orphan_authority_grace", p.OrphanAuthorityGrace},
	}
	for _, item := range durations {
		if item.value <= 0 {
			return &ConfigurationError{Field: item.name, Reason: "must be positive"}
		}
	}
	values := []struct {
		name  LimitName
		value int
	}{
		{LimitSetCookieHeaders, p.MaxSetCookieHeaders},
		{LimitSetCookieLineBytes, p.MaxSetCookieLineBytes},
		{LimitSetCookieBytes, p.MaxSetCookieBytes},
		{LimitCookieNameBytes, p.MaxCookieNameBytes},
		{LimitCookieValueBytes, p.MaxCookieValueBytes},
		{LimitCookieDomainBytes, p.MaxCookieDomainBytes},
		{LimitCookiePathBytes, p.MaxCookiePathBytes},
		{LimitOutboundHeaderBytes, p.MaxOutboundHeaderBytes},
		{LimitAuthorityEntries, p.MaxCookiesPerAuthority},
		{LimitAuthoritiesPerJar, p.MaxAuthoritiesPerJar},
		{LimitJarEntries, p.MaxCookiesPerJar},
		{LimitHandleBindingsGlobal, p.MaxHandleBindingsGlobal},
		{LimitGlobalEntries, p.MaxCookieEntriesGlobal},
	}
	for _, item := range values {
		if item.value <= 0 {
			return &ConfigurationError{Field: string(item.name), Reason: "must be positive"}
		}
	}
	if p.HandleRefreshWindow > p.HandleIdleTTL {
		return &ConfigurationError{Field: "handle_refresh_window", Reason: "cannot exceed the idle TTL"}
	}
	if p.HandleIdleTTL > p.HandleAbsoluteTTL {
		return &ConfigurationError{Field: "handle_idle_ttl", Reason: "cannot exceed the absolute TTL"}
	}
	if p.SessionCookieTTL > p.MaxPersistentCookieTTL {
		return &ConfigurationError{Field: "session_cookie_ttl", Reason: "cannot exceed the persistent Cookie TTL cap"}
	}
	if p.MaxSetCookieLineBytes > p.MaxSetCookieBytes {
		return &ConfigurationError{Field: string(LimitSetCookieLineBytes), Reason: "cannot exceed the response byte limit"}
	}
	if p.MaxCookiesPerAuthority > p.MaxCookiesPerJar {
		return &ConfigurationError{Field: string(LimitAuthorityEntries), Reason: "cannot exceed the per-jar limit"}
	}
	if p.MaxCookiesPerJar > p.MaxCookieEntriesGlobal {
		return &ConfigurationError{Field: string(LimitJarEntries), Reason: "cannot exceed the global limit"}
	}
	return nil
}

type CapacityUsage struct {
	AuthorityEntries     int
	AuthoritiesPerJar    int
	JarEntries           int
	HandleBindingsGlobal int
	GlobalEntries        int
}

// CheckCapacity accepts projected post-mutation counts, avoiding ambiguous off-by-one behavior.
func (p Policy) CheckCapacity(projected CapacityUsage) error {
	if err := p.Validate(); err != nil {
		return err
	}
	checks := []struct {
		name   LimitName
		max    int
		actual int
	}{
		{LimitAuthorityEntries, p.MaxCookiesPerAuthority, projected.AuthorityEntries},
		{LimitAuthoritiesPerJar, p.MaxAuthoritiesPerJar, projected.AuthoritiesPerJar},
		{LimitJarEntries, p.MaxCookiesPerJar, projected.JarEntries},
		{LimitHandleBindingsGlobal, p.MaxHandleBindingsGlobal, projected.HandleBindingsGlobal},
		{LimitGlobalEntries, p.MaxCookieEntriesGlobal, projected.GlobalEntries},
	}
	for _, check := range checks {
		if check.actual < 0 {
			return &ConfigurationError{Field: string(check.name), Reason: "projected usage cannot be negative"}
		}
		if check.actual > check.max {
			return &LimitError{Limit: check.name, Max: check.max, Actual: check.actual}
		}
	}
	return nil
}
