package clientdisguise

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestTerminalEntryPointRecognition(t *testing.T) {
	for _, tc := range []struct {
		name, userAgent, originator, wantType, wantVersion string
	}{
		{"interactive", "codex-tui/0.150.0 (Linux 6.8; x86_64) xterm", "codex-tui", "tui", "0.150.0"},
		{"exec", "codex_exec/0.150.0-alpha.8 (Linux 6.8; x86_64) unknown", "codex_exec", "exec", "0.150.0-alpha.8"},
		{"exec UA only", "codex_exec/0.150.0 (Linux 6.8; x86_64)", "", "exec", "0.150.0"},
		{"exec originator with generic UA", "codex_cli_rs/0.150.0 (Linux 6.8; x86_64)", "codex_exec", "exec", "0.150.0"},
		{"exec alias", "codex-exec/0.150.0 (Linux 6.8; x86_64)", "", "exec", "0.150.0"},
		{"default client", "codex_cli_rs/0.150.0 (Linux 6.8; x86_64)", "codex_cli_rs", "cli", "0.150.0"},
		{"generic Codex UA", "codex/0.150.0 (Linux 6.8; x86_64)", "", "cli", "0.150.0"},
		{"unknown", "other/1.0 (Linux 6.8; x86_64)", "other", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts := ProjectPlatform(http.Header{"User-Agent": {tc.userAgent}, "Originator": {tc.originator}})
			want := Tuple{ClientType: tc.wantType, Platform: "linux", Arch: "amd64"}
			if facts.Tuple != want || facts.Tuple.Valid() != (tc.wantType != "") {
				t.Fatalf("tuple = %+v, want %+v", facts.Tuple, want)
			}
			if got := userAgentVersion(tc.userAgent); got != tc.wantVersion {
				t.Fatalf("version = %q, want %q", got, tc.wantVersion)
			}
		})
	}
}

func TestWindowsMatchingAllowsAllEntryPointsAndArchitectures(t *testing.T) {
	for _, client := range clientTypes {
		for _, arch := range []string{"amd64", "arm64", ""} {
			facts := PlatformFacts{Tuple: Tuple{ClientType: client.name, Platform: "windows", Arch: arch}}
			if decision := EvaluatePlatform(Policy{Enabled: true}, facts, windowsDesktop); !decision.Allowed {
				t.Fatalf("Windows client excluded: %+v", decision)
			}
		}
	}
}

func TestExecRequestCanBindLearnAndRestore(t *testing.T) {
	ctx := context.Background()
	repo := testRepository(t)
	headers := http.Header{
		"User-Agent": {"codex_exec/0.151.0 (Linux 6.8; x86_64) unknown (codex_exec; 0.151.0)"},
		"Originator": {"codex_exec"},
	}
	facts := ProjectPlatform(headers)
	candidate, err := repo.EvaluateCandidate(ctx, "exec-login", account("account"), Policy{Enabled: true}, facts)
	if err != nil || !candidate.Decision.Allowed {
		t.Fatalf("exec candidate excluded: %+v, %v", candidate.Decision, err)
	}
	target, err := repo.CommitTarget(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	wantTuple := Tuple{ClientType: "exec", Platform: "linux", Arch: "amd64"}
	if target.Binding.Tuple != wantTuple || target.Profile.Features.Originator != "codex_exec" || target.Profile.Features.UserAgent != "" {
		t.Fatalf("wrong exec default: %+v", target.Profile)
	}
	if err := repo.SaveReference(ctx, ReferenceSource{ID: "exec-reference", Name: "Exec", ClientIdentityID: "client"}); err != nil {
		t.Fatal(err)
	}
	binding := target.Binding
	binding.ReferenceSourceID = "exec-reference"
	if _, err := repo.SetBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	if err := repo.ObserveClient(ctx, "client", headers, time.Now()); err != nil {
		t.Fatal(err)
	}
	requests, err := repo.ListClientRequests(ctx)
	if err != nil || len(requests) != 1 || requests[0].Tuple != wantTuple || requests[0].ClientVersion != "0.151.0" {
		t.Fatalf("exec observation = %+v, %v", requests, err)
	}
	learned := candidateFor(t, repo, "exec-login", wantTuple)
	if learned.Profile.Features.UserAgent != headers.Get("User-Agent") || learned.Profile.ClientVersion != "0.151.0" {
		t.Fatalf("exec binding did not follow its reference: %+v", learned.Profile)
	}
	snapshot, err := repo.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	restored := testRepository(t)
	if err := restored.Import(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	restoredCandidate := candidateFor(t, restored, "exec-login", wantTuple)
	if !restoredCandidate.Decision.Allowed || restoredCandidate.Profile.ID != learned.Profile.ID {
		t.Fatalf("exec profile was not restored: %+v", restoredCandidate)
	}
}
