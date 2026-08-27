package providercookie

import (
	"encoding/base64"
	"net/http"
	"strings"
)

const (
	GatewayHandleName          = "switch_a_codex_jar"
	GatewayHandlePath          = "/"
	GatewayHandleEntropyBytes  = 32
	GatewayHandleEncodedLength = 43
)

type externalSchemeKind uint8

const (
	externalSchemeHTTP externalSchemeKind = iota + 1
	externalSchemeHTTPS
)

// ResolvedExternalScheme is created only after direct TLS or trusted-proxy evidence is resolved.
// An ambiguous external scheme must remain an error and never reach handle construction.
type ResolvedExternalScheme struct {
	kind externalSchemeKind
}

func NewResolvedExternalScheme(scheme string) (ResolvedExternalScheme, error) {
	switch strings.ToLower(scheme) {
	case "https":
		return ResolvedExternalScheme{kind: externalSchemeHTTPS}, nil
	case "http":
		return ResolvedExternalScheme{kind: externalSchemeHTTP}, nil
	default:
		return ResolvedExternalScheme{}, &RequestTargetError{Reason: "external scheme is unknown"}
	}
}

func (s ResolvedExternalScheme) HTTPS() bool { return s.kind == externalSchemeHTTPS }

type GatewayHandleCookie struct {
	name   string
	value  string
	secure bool
}

func NewGatewayHandleCookie(value string, scheme ResolvedExternalScheme) (GatewayHandleCookie, error) {
	if scheme.kind != externalSchemeHTTP && scheme.kind != externalSchemeHTTPS {
		return GatewayHandleCookie{}, &RequestTargetError{Reason: "external scheme was not resolved"}
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != GatewayHandleEntropyBytes || len(value) != GatewayHandleEncodedLength || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return GatewayHandleCookie{}, &ParseError{Index: -1, Field: "gateway_handle", Reason: "must be canonical unpadded base64url for 32 bytes"}
	}
	cookie := &http.Cookie{
		Name:     GatewayHandleName,
		Value:    value,
		Path:     GatewayHandlePath,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   scheme.HTTPS(),
	}
	if err := cookie.Valid(); err != nil {
		return GatewayHandleCookie{}, &ParseError{Index: -1, Field: "gateway_handle", Reason: "name or value is malformed", Cause: err}
	}
	return GatewayHandleCookie{name: GatewayHandleName, value: value, secure: cookie.Secure}, nil
}

func (c GatewayHandleCookie) Name() string   { return c.name }
func (c GatewayHandleCookie) Secure() bool   { return c.secure }
func (c GatewayHandleCookie) Path() string   { return GatewayHandlePath }
func (c GatewayHandleCookie) HTTPOnly() bool { return true }
func (c GatewayHandleCookie) SameSite() SameSite {
	return SameSiteLax
}

func (c GatewayHandleCookie) HeaderValue() (string, error) {
	cookie := &http.Cookie{
		Name:     c.name,
		Value:    c.value,
		Path:     GatewayHandlePath,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   c.secure,
	}
	if err := cookie.Valid(); err != nil {
		return "", &StateError{Reason: "gateway handle cookie was not constructed by NewGatewayHandleCookie"}
	}
	return cookie.String(), nil
}
