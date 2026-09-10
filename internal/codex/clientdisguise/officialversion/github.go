package officialversion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	releasesEndpoint = "https://api.github.com/repos/openai/codex/releases"
	releasePageSize  = 100
	maxReleasePages  = 10
	githubAPIVersion = "2026-03-10"
)

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}
type GitHub struct {
	client   HTTPClient
	endpoint string
}

func NewGitHub(client HTTPClient) *GitHub { return &GitHub{client: client, endpoint: releasesEndpoint} }

type githubRelease struct {
	Tag         string    `json:"tag_name"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
}

func (r githubRelease) stable() (Release, bool) {
	version, found := strings.CutPrefix(r.Tag, tagPrefix)
	if !found || r.Draft || r.Prerelease || !ValidVersion(version) {
		return Release{}, false
	}
	return Release{Version: version, Tag: r.Tag, URL: RepositoryURL + "/releases/tag/" + r.Tag, PublishedAt: r.PublishedAt}, true
}

func (g *GitHub) Latest(ctx context.Context) (Release, error) {
	var latest githubRelease
	err := g.get(ctx, g.endpoint+"/latest", &latest)
	if err == nil {
		if release, ok := latest.stable(); ok {
			return release, nil
		}
	}
	// The repository publishes other components too. Pagination avoids allowing a
	// dense run of alpha releases or an unrelated latest tag to hide stable Codex.
	for page := 1; page <= maxReleasePages; page++ {
		var releases []githubRelease
		if listErr := g.get(ctx, fmt.Sprintf("%s?per_page=%d&page=%d", g.endpoint, releasePageSize, page), &releases); listErr != nil {
			return Release{}, errors.Join(err, listErr)
		}
		var best Release
		for _, candidate := range releases {
			if release, ok := candidate.stable(); ok && Compare(release.Version, best.Version) > 0 {
				best = release
			}
		}
		if best.Version != "" {
			return best, nil
		}
		if len(releases) < releasePageSize {
			break
		}
	}
	return Release{}, fmt.Errorf("no stable Codex release found: %w", errors.Join(err, errors.New("expected a published rust-vMAJOR.MINOR.PATCH release")))
}

func (g *GitHub) get(ctx context.Context, endpoint string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	req.Header.Set("User-Agent", "switch-a-release-sync")
	response, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub releases: HTTP %d", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(result); err != nil {
		return fmt.Errorf("decode GitHub release: %w", err)
	}
	return nil
}
