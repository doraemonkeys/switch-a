package codexhttp

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
)

type TrustedProxySchemeResolver struct {
	prefixes []netip.Prefix
}

func NewTrustedProxySchemeResolver(prefixes []netip.Prefix) TrustedProxySchemeResolver {
	return TrustedProxySchemeResolver{prefixes: append([]netip.Prefix(nil), prefixes...)}
}

func (r TrustedProxySchemeResolver) ResolveExternalScheme(
	request *http.Request,
) (providercookie.ResolvedExternalScheme, error) {
	if request == nil {
		return providercookie.ResolvedExternalScheme{}, errors.New("request is required")
	}
	if request.TLS != nil {
		return providercookie.NewResolvedExternalScheme("https")
	}
	if !r.trustsImmediatePeer(request.RemoteAddr) {
		return providercookie.NewResolvedExternalScheme("http")
	}
	forwarded, forwardedPresent, err := forwardedProto(headerValues(request.Header, "Forwarded"))
	if err != nil {
		return providercookie.ResolvedExternalScheme{}, err
	}
	xForwarded, xForwardedPresent, err := xForwardedProto(headerValues(request.Header, "X-Forwarded-Proto"))
	if err != nil {
		return providercookie.ResolvedExternalScheme{}, err
	}
	if !forwardedPresent && !xForwardedPresent {
		return providercookie.ResolvedExternalScheme{}, errors.New("trusted proxy did not provide an external scheme")
	}
	if forwardedPresent && xForwardedPresent && forwarded != xForwarded {
		return providercookie.ResolvedExternalScheme{}, errors.New("trusted proxy scheme headers conflict")
	}
	if forwardedPresent {
		return providercookie.NewResolvedExternalScheme(forwarded)
	}
	return providercookie.NewResolvedExternalScheme(xForwarded)
}

func (r TrustedProxySchemeResolver) trustsImmediatePeer(remoteAddress string) bool {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		host = remoteAddress
	}
	address, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return false
	}
	for _, prefix := range r.prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func forwardedProto(values []string) (string, bool, error) {
	if len(values) == 0 {
		return "", false, nil
	}
	if len(values) != 1 || strings.Contains(values[0], ",") {
		return "", false, errors.New("forwarded must contain exactly one element")
	}
	var proto string
	for _, parameter := range strings.Split(values[0], ";") {
		name, value, present := strings.Cut(strings.TrimSpace(parameter), "=")
		if !present || !strings.EqualFold(strings.TrimSpace(name), "proto") {
			continue
		}
		if proto != "" {
			return "", false, errors.New("forwarded contains multiple proto parameters")
		}
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "\"") {
			unquoted, err := strconv.Unquote(value)
			if err != nil {
				return "", false, errors.New("forwarded proto is malformed")
			}
			value = unquoted
		}
		proto = strings.ToLower(value)
		if proto != "http" && proto != "https" {
			return "", false, errors.New("forwarded proto is unsupported")
		}
	}
	if proto == "" {
		return "", false, errors.New("forwarded does not contain proto")
	}
	return proto, true, nil
}

func xForwardedProto(values []string) (string, bool, error) {
	if len(values) == 0 {
		return "", false, nil
	}
	if len(values) != 1 || strings.Contains(values[0], ",") {
		return "", false, errors.New("x-forwarded-proto must contain one value")
	}
	proto := strings.ToLower(strings.TrimSpace(values[0]))
	if proto != "http" && proto != "https" {
		return "", false, errors.New("x-forwarded-proto is unsupported")
	}
	return proto, true, nil
}
