package codexidentity

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	upstreamAuthorityCodec = "codex-upstream-authority/v1"
	protocolScopeCodec     = "codex-protocol-scope/v1"
	cookieAuthorityCodec   = "codex-cookie-authority/v1"
)

type UpstreamAuthority struct {
	vendor  string
	origin  NormalizedOrigin
	subject CredentialSubject
}

func NewUpstreamAuthority(vendor string, origin NormalizedOrigin, subject CredentialSubject) (UpstreamAuthority, error) {
	vendor = strings.TrimSpace(vendor)
	if _, err := origin.MarshalBinary(); err != nil {
		return UpstreamAuthority{}, err
	}
	if err := subject.validate(); err != nil {
		return UpstreamAuthority{}, err
	}
	return UpstreamAuthority{vendor: vendor, origin: origin, subject: subject}, nil
}

func (a UpstreamAuthority) Vendor() string                     { return a.vendor }
func (a UpstreamAuthority) Origin() NormalizedOrigin           { return a.origin }
func (a UpstreamAuthority) Subject() CredentialSubject         { return a.subject }
func (a UpstreamAuthority) Equal(other UpstreamAuthority) bool { return a == other }

func (a UpstreamAuthority) MarshalBinary() ([]byte, error) {
	origin, err := a.origin.MarshalBinary()
	if err != nil {
		return nil, err
	}
	subject, err := a.subject.MarshalBinary()
	if err != nil {
		return nil, err
	}
	return encodeFields(upstreamAuthorityCodec, []byte(a.vendor), origin, subject)
}

func (a UpstreamAuthority) String() string {
	return fmt.Sprintf(
		"upstream-authority(vendor=%s,origin=%s,subject=redacted)",
		safeOptionalLabel(a.vendor),
		a.origin.String(),
	)
}

func (a UpstreamAuthority) GoString() string { return a.String() }

func (a UpstreamAuthority) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Vendor  string `json:"vendor"`
		Origin  string `json:"origin"`
		Subject string `json:"subject"`
	}{Vendor: safeOptionalLabel(a.vendor), Origin: a.origin.String(), Subject: "redacted"})
}

type ProtocolScope struct {
	authority UpstreamAuthority
	apiType   string
}

func NewProtocolScope(authority UpstreamAuthority, apiType string) (ProtocolScope, error) {
	apiType = strings.TrimSpace(apiType)
	if _, err := authority.MarshalBinary(); err != nil {
		return ProtocolScope{}, err
	}
	if apiType == "" {
		return ProtocolScope{}, errorOf(ErrorInvalidInput, "api_type", "API type is empty", nil)
	}
	return ProtocolScope{authority: authority, apiType: apiType}, nil
}

func (s ProtocolScope) Authority() UpstreamAuthority   { return s.authority }
func (s ProtocolScope) APIType() string                { return s.apiType }
func (s ProtocolScope) Equal(other ProtocolScope) bool { return s == other }

func (s ProtocolScope) MarshalBinary() ([]byte, error) {
	authority, err := s.authority.MarshalBinary()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(s.apiType) == "" {
		return nil, errorOf(ErrorInvalidInput, "protocol_scope", "scope is uninitialized", nil)
	}
	return encodeFields(protocolScopeCodec, authority, []byte(s.apiType))
}

func (s ProtocolScope) String() string {
	return fmt.Sprintf("protocol-scope(authority=%s,api_type=%s)", s.authority, safeLabel(s.apiType))
}

func (s ProtocolScope) GoString() string { return s.String() }

func (s ProtocolScope) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Authority UpstreamAuthority `json:"authority"`
		APIType   string            `json:"api_type"`
	}{Authority: s.authority, APIType: safeLabel(s.apiType)})
}

// CookieAuthority is deliberately distinct from ProtocolScope: cookies ignore
// APIType but retain every UpstreamAuthority dimension.
type CookieAuthority struct {
	authority UpstreamAuthority
}

func (a UpstreamAuthority) CookieAuthority() CookieAuthority {
	return CookieAuthority{authority: a}
}

func (a CookieAuthority) Authority() UpstreamAuthority     { return a.authority }
func (a CookieAuthority) Equal(other CookieAuthority) bool { return a == other }

func (a CookieAuthority) MarshalBinary() ([]byte, error) {
	authority, err := a.authority.MarshalBinary()
	if err != nil {
		return nil, err
	}
	return encodeFields(cookieAuthorityCodec, authority)
}

func (a CookieAuthority) String() string {
	return fmt.Sprintf("cookie-authority(%s)", a.authority)
}

func (a CookieAuthority) GoString() string { return a.String() }

func (a CookieAuthority) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Authority UpstreamAuthority `json:"authority"`
	}{Authority: a.authority})
}

func safeLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "invalid"
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return "invalid"
		}
	}
	return value
}

func safeOptionalLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return safeLabel(value)
}
