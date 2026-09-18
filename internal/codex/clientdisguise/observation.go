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
	if clientID == "" {
		return nil
	}
	if capturedAt.IsZero() {
		capturedAt = r.now()
	}
	facts := ProjectPlatform(headers)
	ua := headers.Get("User-Agent")
	version := userAgentVersion(ua)
	observedTuple := facts.Tuple
	if facts.Conflict {
		observedTuple.Platform = ""
	}
	if err := r.recordClientRequest(ctx, ClientRequestObservation{
		ClientID: clientID, ObservedAt: capturedAt, Tuple: observedTuple,
		ClientVersion: version, UserAgent: ua, Originator: headers.Get("Originator"),
	}); err != nil {
		return err
	}
	if facts.Conflict || !facts.Tuple.Valid() || version == "" {
		return nil
	}
	features := Features{UserAgent: ua, Originator: headers.Get("Originator"), ClientVersion: version}
	if build := desktopBuildPattern.FindStringSubmatch(ua); len(build) == 2 {
		features.DesktopBuild = build[1]
	}
	if os := osVersionPattern.FindStringSubmatch(ua); len(os) == 2 {
		features.OSVersion = os[1]
	}
	_, err := r.ObserveReference(ctx, clientID, Sample{Tuple: facts.Tuple, ClientVersion: version, Features: features, CapturedAt: capturedAt})
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
