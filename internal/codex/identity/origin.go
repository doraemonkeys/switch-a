package codexidentity

import (
	"encoding/json"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

const (
	originCodec      = "codex-normalized-origin/v1"
	MaxDNSHostBytes  = 253
	MaxDNSLabelBytes = 63
	minimumPort      = 1
	maximumPort      = 65535
	defaultHTTPPort  = 80
	defaultHTTPSPort = 443
)

type NormalizedOrigin struct {
	scheme string
	host   string
	port   uint16
	ipv6   bool
}

// OriginFromRequestURL extracts only the origin from the final physical
// request or dial URL. Path and query are valid request-target data, but never
// participate in authority identity.
func OriginFromRequestURL(target *url.URL) (NormalizedOrigin, error) {
	return normalizeURL(target, false)
}

// ParseOrigin accepts the origin-only form used by persistence and config.
// Rejecting request-target bytes here prevents accidental codec expansion.
func ParseOrigin(value string) (NormalizedOrigin, error) {
	target, err := url.Parse(value)
	if err != nil {
		return NormalizedOrigin{}, errorOf(ErrorInvalidOrigin, "url", "origin cannot be parsed", err)
	}
	return normalizeURL(target, true)
}

func normalizeURL(target *url.URL, originOnly bool) (NormalizedOrigin, error) {
	if target == nil {
		return NormalizedOrigin{}, errorOf(ErrorInvalidOrigin, "url", "URL is nil", nil)
	}
	if target.Opaque != "" {
		return NormalizedOrigin{}, errorOf(ErrorInvalidOrigin, "url", "opaque URLs are unsupported", nil)
	}
	if target.User != nil {
		return NormalizedOrigin{}, errorOf(ErrorInvalidOrigin, "userinfo", "userinfo is forbidden", nil)
	}
	if target.Fragment != "" {
		return NormalizedOrigin{}, errorOf(ErrorInvalidOrigin, "fragment", "fragment is forbidden", nil)
	}
	if originOnly && ((target.Path != "" && target.EscapedPath() != "/") || target.RawQuery != "" || target.ForceQuery) {
		return NormalizedOrigin{}, errorOf(ErrorInvalidOrigin, "request_target", "path and query are forbidden in an origin", nil)
	}
	scheme, defaultPort, err := canonicalScheme(target.Scheme)
	if err != nil {
		return NormalizedOrigin{}, err
	}
	host, port, hasPort, ipv6, err := splitAndCanonicalizeAuthority(target.Host)
	if err != nil {
		return NormalizedOrigin{}, err
	}
	if hasPort && port == defaultPort {
		hasPort = false
	}
	if !hasPort {
		port = 0
	}
	return NormalizedOrigin{scheme: scheme, host: host, port: uint16(port), ipv6: ipv6}, nil
}

func canonicalScheme(raw string) (string, int, error) {
	switch strings.ToLower(raw) {
	case "http", "ws":
		return "http", defaultHTTPPort, nil
	case "https", "wss":
		return "https", defaultHTTPSPort, nil
	default:
		return "", 0, errorOf(ErrorInvalidOrigin, "scheme", "scheme must be http, https, ws, or wss", nil)
	}
}

func splitAndCanonicalizeAuthority(authority string) (host string, port int, hasPort, ipv6 bool, err error) {
	if authority == "" {
		return "", 0, false, false, errorOf(ErrorInvalidOrigin, "host", "host is empty", nil)
	}
	if strings.Contains(authority, "@") {
		return "", 0, false, false, errorOf(ErrorInvalidOrigin, "userinfo", "userinfo is forbidden", nil)
	}
	var rawPort string
	if strings.HasPrefix(authority, "[") {
		host, rawPort, hasPort, err = splitBracketedAuthority(authority)
		if err != nil {
			return "", 0, false, false, err
		}
		ipv6 = true
	} else {
		host, rawPort, hasPort, err = splitPlainAuthority(authority)
		if err != nil {
			return "", 0, false, false, err
		}
	}
	if hasPort {
		port, err = parsePort(rawPort)
		if err != nil {
			return "", 0, false, false, err
		}
	}
	return host, port, hasPort, ipv6, nil
}

func splitBracketedAuthority(authority string) (host, rawPort string, hasPort bool, err error) {
	closing := strings.IndexByte(authority, ']')
	if closing < 0 {
		return "", "", false, errorOf(ErrorInvalidOrigin, "host", "IPv6 host is missing a closing bracket", nil)
	}
	remainder := authority[closing+1:]
	if remainder != "" {
		if !strings.HasPrefix(remainder, ":") || len(remainder) == 1 {
			return "", "", false, errorOf(ErrorInvalidOrigin, "port", "authority suffix is invalid", nil)
		}
		rawPort = remainder[1:]
		hasPort = true
	}
	address, parseErr := canonicalIP(authority[1:closing])
	if parseErr != nil || !address.Is6() {
		return "", "", false, errorOf(ErrorInvalidOrigin, "host", "brackets require an IPv6 literal", parseErr)
	}
	return address.String(), rawPort, hasPort, nil
}

func splitPlainAuthority(authority string) (host, rawPort string, hasPort bool, err error) {
	rawHost := authority
	switch strings.Count(authority, ":") {
	case 0:
	case 1:
		rawHost, rawPort, _ = strings.Cut(authority, ":")
		if rawPort == "" {
			return "", "", false, errorOf(ErrorInvalidOrigin, "port", "port is empty", nil)
		}
		hasPort = true
	default:
		return "", "", false, errorOf(ErrorInvalidOrigin, "host", "IPv6 literals must be bracketed", nil)
	}
	host, err = CanonicalizeCookieHost(rawHost)
	if err != nil {
		return "", "", false, errorOf(ErrorInvalidOrigin, "host", "host cannot be canonicalized", err)
	}
	if address, parseErr := netip.ParseAddr(host); parseErr == nil && address.Is6() {
		return "", "", false, errorOf(ErrorInvalidOrigin, "host", "IPv6 literals must be bracketed", nil)
	}
	return host, rawPort, hasPort, nil
}

func parsePort(raw string) (int, error) {
	for _, char := range raw {
		if char < '0' || char > '9' {
			return 0, errorOf(ErrorInvalidOrigin, "port", "port must be decimal", nil)
		}
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port < minimumPort || port > maximumPort {
		return 0, errorOf(ErrorInvalidOrigin, "port", "port is outside 1..65535", err)
	}
	return port, nil
}

// CanonicalizeCookieHost is shared with the provider Cookie parser so request
// hosts, Domain attributes, and authority origins cannot drift in IDNA rules.
func CanonicalizeCookieHost(raw string) (string, error) {
	if raw == "" || raw != strings.TrimSpace(raw) || strings.Contains(raw, "%") {
		return "", errorOf(ErrorInvalidOrigin, "host", "host is empty, non-canonical, or zoned", nil)
	}
	if address, err := canonicalIP(raw); err == nil {
		return address.String(), nil
	}
	raw = strings.TrimSuffix(raw, ".")
	if raw == "" || strings.HasSuffix(raw, ".") {
		return "", errorOf(ErrorInvalidOrigin, "host", "DNS host contains an empty label", nil)
	}
	ascii, err := idna.Lookup.ToASCII(raw)
	if err != nil {
		return "", errorOf(ErrorInvalidOrigin, "host", "IDNA lookup conversion failed", err)
	}
	ascii = strings.ToLower(ascii)
	if !validDNSHost(ascii) {
		return "", errorOf(ErrorInvalidOrigin, "host", "DNS host is invalid", nil)
	}
	return ascii, nil
}

func canonicalIP(raw string) (netip.Addr, error) {
	if strings.Contains(raw, "%") {
		return netip.Addr{}, errorOf(ErrorInvalidOrigin, "host", "IPv6 zones are forbidden", nil)
	}
	address, err := netip.ParseAddr(raw)
	if err != nil {
		return netip.Addr{}, err
	}
	if address.Zone() != "" {
		return netip.Addr{}, errorOf(ErrorInvalidOrigin, "host", "IPv6 zones are forbidden", nil)
	}
	return address, nil
}

func validDNSHost(host string) bool {
	if host == "" || len(host) > MaxDNSHostBytes {
		return false
	}
	for label := range strings.SplitSeq(host, ".") {
		if len(label) == 0 || len(label) > MaxDNSLabelBytes || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for index := 0; index < len(label); index++ {
			char := label[index]
			if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' {
				continue
			}
			return false
		}
	}
	return true
}

// PublicSuffixList pins Cookie public/private-suffix decisions to the same
// x/net release as IDNA normalization. Public suffixes never collapse origins.
type PublicSuffixList struct{}

func (PublicSuffixList) PublicSuffix(domain string) string {
	suffix, _ := publicsuffix.PublicSuffix(domain)
	return suffix
}

func (o NormalizedOrigin) Scheme() string { return o.scheme }
func (o NormalizedOrigin) Host() string   { return o.host }
func (o NormalizedOrigin) Port() (uint16, bool) {
	return o.port, o.port != 0
}
func (o NormalizedOrigin) Equal(other NormalizedOrigin) bool { return o == other }

func (o NormalizedOrigin) String() string {
	host := o.host
	if o.ipv6 {
		host = "[" + host + "]"
	}
	if o.port != 0 {
		host += ":" + strconv.Itoa(int(o.port))
	}
	return o.scheme + "://" + host
}

func (o NormalizedOrigin) MarshalText() ([]byte, error) {
	if o.scheme == "" || o.host == "" {
		return nil, errorOf(ErrorInvalidOrigin, "origin", "origin is uninitialized", nil)
	}
	return []byte(o.String()), nil
}

func (o NormalizedOrigin) MarshalJSON() ([]byte, error) {
	text, err := o.MarshalText()
	if err != nil {
		return nil, err
	}
	return json.Marshal(string(text))
}

func (o NormalizedOrigin) MarshalBinary() ([]byte, error) {
	if o.scheme == "" || o.host == "" {
		return nil, errorOf(ErrorInvalidOrigin, "origin", "origin is uninitialized", nil)
	}
	port := ""
	if o.port != 0 {
		port = strconv.Itoa(int(o.port))
	}
	ipv6 := "0"
	if o.ipv6 {
		ipv6 = "1"
	}
	return encodeFields(originCodec, []byte(o.scheme), []byte(o.host), []byte(port), []byte(ipv6))
}
