// Package maintenance owns the process-level cleanup lifecycle for Codex
// continuity and provider-Cookie persistence.
package maintenance

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
)

// CatalogRoute is the persistence read model needed to derive one reachable
// Cookie Authority. Secret material and mutable authentication diagnostics are
// deliberately absent.
type CatalogRoute struct {
	RouteTargetID string
	Vendor        string
	FinalURL      string
	Subject       credentialsession.Subject
}

// CatalogSnapshot owns a deep copy of every route. A sweep either derives its
// complete Authority set from this immutable value or does not mutate Cookie
// reachability at all.
type CatalogSnapshot struct {
	routes []CatalogRoute
}

func NewCatalogSnapshot(routes []CatalogRoute) CatalogSnapshot {
	return CatalogSnapshot{routes: cloneCatalogRoutes(routes)}
}

func (s CatalogSnapshot) Routes() []CatalogRoute {
	return cloneCatalogRoutes(s.routes)
}

func (s CatalogSnapshot) ReachableCookieAuthorities() ([]codexidentity.CookieAuthority, error) {
	reachable := make([]codexidentity.CookieAuthority, 0, len(s.routes))
	seen := make(map[string]struct{}, len(s.routes))
	for index, route := range s.routes {
		authority, err := cookieAuthorityForRoute(route)
		if err != nil {
			return nil, fmt.Errorf("catalog route %d (%q): %w", index, route.RouteTargetID, err)
		}
		encoded, err := authority.MarshalBinary()
		if err != nil {
			return nil, fmt.Errorf("catalog route %d (%q): encode Cookie Authority: %w", index, route.RouteTargetID, err)
		}
		key := string(encoded)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		reachable = append(reachable, authority)
	}
	return reachable, nil
}

func cookieAuthorityForRoute(route CatalogRoute) (codexidentity.CookieAuthority, error) {
	if strings.TrimSpace(route.RouteTargetID) == "" {
		return codexidentity.CookieAuthority{}, fmt.Errorf("route target ID is empty")
	}
	if strings.TrimSpace(route.Vendor) == "" {
		return codexidentity.CookieAuthority{}, fmt.Errorf("vendor is empty")
	}
	target, err := url.Parse(route.FinalURL)
	if err != nil {
		return codexidentity.CookieAuthority{}, fmt.Errorf("parse final URL: %w", err)
	}
	origin, err := codexidentity.OriginFromRequestURL(target)
	if err != nil {
		return codexidentity.CookieAuthority{}, fmt.Errorf("normalize final Origin: %w", err)
	}
	subject, err := codexidentity.CredentialSubjectFromSession(route.Subject)
	if err != nil {
		return codexidentity.CookieAuthority{}, fmt.Errorf("resolve credential subject: %w", err)
	}
	upstream, err := codexidentity.NewUpstreamAuthority(route.Vendor, origin, subject)
	if err != nil {
		return codexidentity.CookieAuthority{}, fmt.Errorf("construct upstream Authority: %w", err)
	}
	return upstream.CookieAuthority(), nil
}

func cloneCatalogRoutes(routes []CatalogRoute) []CatalogRoute {
	cloned := make([]CatalogRoute, len(routes))
	for index, route := range routes {
		cloned[index] = route
		cloned[index].Subject = route.Subject.Clone()
	}
	return cloned
}
