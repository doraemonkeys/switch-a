package clientdisguiseapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise/officialversion"
)

type fakeVersions struct {
	state  officialversion.State
	err    error
	forced bool
}

func (f *fakeVersions) State(context.Context) (officialversion.State, error) { return f.state, f.err }
func (f *fakeVersions) Sync(_ context.Context, force bool) (officialversion.State, error) {
	f.forced = force
	return f.state, f.err
}

func TestOfficialVersionAdministration(t *testing.T) {
	handler, _ := setup()
	if response := invoke(handler.SyncOfficialVersion, ""); response.Code != http.StatusServiceUnavailable {
		t.Fatal(response)
	}
	versions := &fakeVersions{state: officialversion.State{Release: officialversion.Release{Version: "0.151.0"}}}
	handler.versions = versions
	response := invoke(handler.SyncOfficialVersion, "")
	if response.Code != 200 || !versions.forced {
		t.Fatal(response, versions)
	}
	var state officialversion.State
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil || state.Release.Version != "0.151.0" {
		t.Fatal(state, err)
	}
	response = invoke(handler.Get, "")
	var overview Overview
	if err := json.Unmarshal(response.Body.Bytes(), &overview); err != nil || overview.OfficialVersion == nil || overview.OfficialVersion.Release.Version != "0.151.0" {
		t.Fatal(overview, err)
	}
	versions.err = errors.New("GitHub unavailable")
	if response = invoke(handler.SyncOfficialVersion, ""); response.Code != http.StatusBadGateway {
		t.Fatal(response)
	}
	if response = invoke(handler.Get, ""); response.Code != http.StatusInternalServerError {
		t.Fatal(response)
	}
}
func TestOfficialVersionBindingChoice(t *testing.T) {
	handler, repo := setup()
	response := invoke(handler.SaveBinding, `{"version_source":"official_stable","revision_id":"revision","mode":"pinned"}`)
	if response.Code != 200 || repo.binding.VersionSource != officialversion.Source {
		t.Fatal(response, repo.binding)
	}
}
