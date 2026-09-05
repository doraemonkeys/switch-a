package clientdisguise

import (
	"context"
	"net/http"
	"regexp"
	"sort"
	"time"
)

var desktopBuildPattern = regexp.MustCompile(`\(Codex Desktop; ([^)]+)\)`)
var osVersionPattern = regexp.MustCompile("(?i)(?:Windows|Linux|Darwin|macOS|Mac OS X) ([0-9][0-9._-]*)")

// ObserveClient receives original ingress headers, so learning can never ingest
// the gateway's own mapped identifiers or confuse them with reference features.
func (r *Repository) ObserveClient(ctx context.Context, clientID string, headers http.Header, capturedAt time.Time) error {
	facts := ProjectPlatform(headers)
	if clientID == "" || facts.Conflict || !facts.Tuple.Valid() {
		return nil
	}
	ua := headers.Get("User-Agent")
	match := versionPattern.FindStringSubmatch(ua)
	if len(match) != 2 {
		return nil
	}
	features := Features{UserAgent: ua, Originator: headers.Get("Originator"), ClientVersion: match[1]}
	if build := desktopBuildPattern.FindStringSubmatch(ua); len(build) == 2 {
		features.DesktopBuild = build[1]
	}
	if os := osVersionPattern.FindStringSubmatch(ua); len(os) == 2 {
		features.OSVersion = os[1]
	}
	_, err := r.ObserveReference(ctx, clientID, Sample{Tuple: facts.Tuple, ClientVersion: match[1], Features: features, CapturedAt: capturedAt})
	return err
}
func (r *Repository) RequiredHMACVersions(ctx context.Context) ([]string, error) {
	snapshot, err := r.Export(ctx)
	if err != nil {
		return nil, err
	}
	versions := make(map[string]bool)
	add := func(basis AccountBasis) {
		if basis.Kind == "keyed_digest" && basis.KeyVersion != "" {
			versions[basis.KeyVersion] = true
		}
	}
	for _, login := range snapshot.Logins {
		add(login.AccountBasis)
	}
	for _, history := range snapshot.LoginHistory {
		add(history.Identity.AccountBasis)
	}
	result := make([]string, 0, len(versions))
	for version := range versions {
		result = append(result, version)
	}
	sort.Strings(result)
	return result, nil
}
