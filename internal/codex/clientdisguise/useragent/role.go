// Package useragent separates a request's caller role from the client environment
// selected for its upstream login.
package useragent

import "strings"

type Role string

const (
	Primary              Role = "primary"
	BrowserUse           Role = "browser-use"
	BrowserUseOriginator      = "codex-browser-use"
)

// The process product is authoritative. A thread originator or a caller suffix
// can name another client and must not reclassify an otherwise explicit UA.
func RequestRole(ua, originator string) Role {
	if strings.HasPrefix(strings.ToLower(ua), BrowserUseOriginator+"/") ||
		(ua == "" && strings.EqualFold(originator, BrowserUseOriginator)) {
		return BrowserUse
	}
	return Primary
}
