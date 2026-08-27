package providercookie

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/identity"
)

const (
	JarIDEntropyBytes = 32
	cookieScopeCodec  = "provider-cookie-scope/v1"
	cookieAADCodec    = "provider-cookie-value-context/v1"
)

type JarID struct {
	value [JarIDEntropyBytes]byte
}

func JarIDFromBytes(value []byte) (JarID, error) {
	if len(value) != JarIDEntropyBytes {
		return JarID{}, &ConfigurationError{Field: "jar_id", Reason: "must be exactly 32 bytes"}
	}
	var id JarID
	copy(id.value[:], value)
	if id == (JarID{}) {
		return JarID{}, &ConfigurationError{Field: "jar_id", Reason: "must not be all zero"}
	}
	return id, nil
}

func (id JarID) Bytes() []byte { return append([]byte(nil), id.value[:]...) }

func (id JarID) String() string   { return "provider-cookie-jar(redacted)" }
func (id JarID) GoString() string { return id.String() }

func (id JarID) MarshalJSON() ([]byte, error) { return json.Marshal("redacted") }

type CookieScope struct {
	jarID     JarID
	authority codexidentity.CookieAuthority
}

func NewCookieScope(jarID JarID, authority codexidentity.CookieAuthority) (CookieScope, error) {
	if jarID == (JarID{}) {
		return CookieScope{}, &ConfigurationError{Field: "jar_id", Reason: "must be initialized"}
	}
	if _, err := authority.MarshalBinary(); err != nil {
		return CookieScope{}, &ConfigurationError{Field: "authority", Reason: "must be initialized"}
	}
	return CookieScope{jarID: jarID, authority: authority}, nil
}

func (s CookieScope) JarID() JarID                             { return s.jarID }
func (s CookieScope) Authority() codexidentity.CookieAuthority { return s.authority }

func (s CookieScope) MarshalBinary() ([]byte, error) {
	if s.jarID == (JarID{}) {
		return nil, &StateError{Reason: "cookie scope JarID is uninitialized"}
	}
	authority, err := s.authority.MarshalBinary()
	if err != nil {
		return nil, &StateError{Reason: "cookie scope authority is uninitialized", Cause: err}
	}
	return encodeCookieFields(cookieScopeCodec, s.jarID.value[:], authority)
}

func (s CookieScope) String() string {
	return fmt.Sprintf("provider-cookie-scope(jar=redacted,authority=%s)", s.authority)
}

func (s CookieScope) GoString() string { return s.String() }

func (s CookieScope) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Jar       string                        `json:"jar"`
		Authority codexidentity.CookieAuthority `json:"authority"`
	}{Jar: "redacted", Authority: s.authority})
}

type CookieKey struct {
	name   string
	domain string
	path   string
}

func NewCookieKey(name, domain, path string) (CookieKey, error) {
	if err := validateCookieName(name); err != nil {
		return CookieKey{}, err
	}
	if domain == "" {
		return CookieKey{}, &DomainError{Domain: domain, Reason: "must not be empty"}
	}
	if domain != strings.ToLower(domain) || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return CookieKey{}, &DomainError{Domain: domain, Reason: "must be canonical lowercase without surrounding dots"}
	}
	if net.ParseIP(domain) == nil && !validDNSName(domain) {
		return CookieKey{}, &DomainError{Domain: domain, Reason: "must be an ASCII DNS name or IP address"}
	}
	if path == "" || path[0] != '/' {
		return CookieKey{}, &ParseError{Index: -1, Field: "path", Reason: "must start with /"}
	}
	return CookieKey{name: name, domain: domain, path: path}, nil
}

func (k CookieKey) Name() string   { return k.name }
func (k CookieKey) Domain() string { return k.domain }
func (k CookieKey) Path() string   { return k.path }

type SameSite uint8

const (
	SameSiteDefault SameSite = iota
	SameSiteLax
	SameSiteStrict
	SameSiteNone
)

type CookieOptions struct {
	HostOnly  bool
	Secure    bool
	HTTPOnly  bool
	Quoted    bool
	Session   bool
	SameSite  SameSite
	ExpiresAt time.Time
	CreatedAt time.Time
}

type StoredCookie struct {
	key       CookieKey
	value     string
	hostOnly  bool
	secure    bool
	httpOnly  bool
	quoted    bool
	session   bool
	sameSite  SameSite
	expiresAt time.Time
	createdAt time.Time
}

func NewStoredCookie(key CookieKey, value string, options CookieOptions) (StoredCookie, error) {
	if key.name == "" || key.domain == "" || key.path == "" {
		return StoredCookie{}, &StateError{Reason: "cookie key was not constructed by NewCookieKey"}
	}
	if err := validateCookieValue(value); err != nil {
		return StoredCookie{}, err
	}
	if options.SameSite > SameSiteNone {
		return StoredCookie{}, &ParseError{Index: -1, Field: "same_site", Reason: "unknown mode"}
	}
	return StoredCookie{
		key:       key,
		value:     value,
		hostOnly:  options.HostOnly,
		secure:    options.Secure,
		httpOnly:  options.HTTPOnly,
		quoted:    options.Quoted,
		session:   options.Session,
		sameSite:  options.SameSite,
		expiresAt: canonicalTime(options.ExpiresAt),
		createdAt: canonicalTime(options.CreatedAt),
	}, nil
}

func (c StoredCookie) Key() CookieKey       { return c.key }
func (c StoredCookie) Value() string        { return c.value }
func (c StoredCookie) HostOnly() bool       { return c.hostOnly }
func (c StoredCookie) Secure() bool         { return c.secure }
func (c StoredCookie) HTTPOnly() bool       { return c.httpOnly }
func (c StoredCookie) Quoted() bool         { return c.quoted }
func (c StoredCookie) Session() bool        { return c.session }
func (c StoredCookie) SameSite() SameSite   { return c.sameSite }
func (c StoredCookie) ExpiresAt() time.Time { return c.expiresAt }
func (c StoredCookie) CreatedAt() time.Time { return c.createdAt }
func (c StoredCookie) Expired(at time.Time) bool {
	return !c.expiresAt.IsZero() && !c.expiresAt.After(at)
}

type MutationKind uint8

const (
	MutationUpsert MutationKind = iota + 1
	MutationTombstone
)

type Mutation struct {
	kind   MutationKind
	key    CookieKey
	cookie StoredCookie
}

func Upsert(cookie StoredCookie) Mutation {
	return Mutation{kind: MutationUpsert, key: cookie.key, cookie: cookie}
}

func Tombstone(key CookieKey) Mutation {
	return Mutation{kind: MutationTombstone, key: key}
}

func (m Mutation) Kind() MutationKind { return m.kind }
func (m Mutation) Key() CookieKey     { return m.key }
func (m Mutation) Cookie() (StoredCookie, bool) {
	return m.cookie, m.kind == MutationUpsert
}

func validateCookieName(name string) error {
	if err := (&http.Cookie{Name: name}).Valid(); err != nil {
		return &ParseError{Index: -1, Field: "name", Reason: "contains an invalid byte", Cause: err}
	}
	return nil
}

func validateCookieValue(value string) error {
	if err := (&http.Cookie{Name: "value", Value: value}).Valid(); err != nil {
		return &ParseError{Index: -1, Field: "value", Reason: "contains an invalid byte", Cause: err}
	}
	return nil
}

func canonicalTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	return value.UTC()
}

func cookieAssociatedContext(scope CookieScope, key CookieKey) ([]byte, error) {
	scopeBytes, err := scope.MarshalBinary()
	if err != nil {
		return nil, err
	}
	if key.name == "" || key.domain == "" || key.path == "" {
		return nil, &StateError{Reason: "cookie key is uninitialized"}
	}
	return encodeCookieFields(cookieAADCodec, scopeBytes, []byte(key.name), []byte(key.domain), []byte(key.path))
}

// EncodeValueAssociatedData is the only persistence encoding accepted for a
// Cookie value. Keeping JarID, every Authority field, and the complete
// CookieKey in one versioned codec makes row transplantation fail AEAD
// authentication rather than silently changing scope.
func EncodeValueAssociatedData(scope CookieScope, key CookieKey) ([]byte, error) {
	return cookieAssociatedContext(scope, key)
}

func encodeCookieFields(codec string, fields ...[]byte) ([]byte, error) {
	all := make([][]byte, 0, len(fields)+1)
	all = append(all, []byte(codec))
	all = append(all, fields...)
	total := 0
	for _, field := range all {
		if uint64(len(field)) > math.MaxUint32 || total > math.MaxInt-4-len(field) {
			return nil, &StateError{Reason: "cookie persistence encoding is too large"}
		}
		total += 4 + len(field)
	}
	encoded := make([]byte, 0, total)
	var size [4]byte
	for _, field := range all {
		binary.BigEndian.PutUint32(size[:], uint32(len(field)))
		encoded = append(encoded, size[:]...)
		encoded = append(encoded, field...)
	}
	return encoded, nil
}
