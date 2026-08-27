package providerimport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth"
	"github.com/doraemonkeys/switch-a/internal/store"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestCommitProviderImportAtomicallyCreatesAndUpdatesCredentialSessions(t *testing.T) {
	t.Parallel()
	updateProvider := providerImportTestProvider(t, "existing-provider", []string{"codex", "responses"}, "existing-session", "shared-account", 7)
	candidates := []providerauth.ChatGPTProviderImportCandidate{
		providerImportTestCandidate(t, "create-a", "shared-account", `{"access_token":"create-a"}`, providerauth.ChatGPTProviderImportCandidateDisposition{State: providerauth.ChatGPTProviderImportCandidateStateReady}),
		providerImportTestCandidate(t, "update", "shared-account", `{"access_token":"updated"}`, providerauth.ChatGPTProviderImportCandidateDisposition{State: providerauth.ChatGPTProviderImportCandidateStateExisting, ExpectedSessionID: "existing-session", ExpectedCredentialVersion: 7}),
		providerImportTestCandidate(t, "create-b", "shared-account", `{"access_token":"create-b"}`, providerauth.ChatGPTProviderImportCandidateDisposition{State: providerauth.ChatGPTProviderImportCandidateStateReady}),
		{CandidateID: "skipped", State: providerauth.ChatGPTProviderImportCandidateStateInvalid},
	}
	drafts := &providerImportTestDrafts{candidates: candidates, finalizeErr: errors.New("cleanup delayed"), releaseErr: errors.New("release delayed")}
	importStore := &providerImportTestStore{}
	lifecycles := &providerImportTestLifecycles{}
	core, logs := observer.New(zap.DebugLevel)
	handler := newProviderImportTestHandler(
		&providerImportTestCatalog{providers: []model.Provider{updateProvider}}, drafts, importStore, lifecycles, zap.New(core),
	)
	body := `{
		"group_id":" group-a ",
		"items":[
			{"candidate_id":"create-a","action":"create","provider_id":"new-a","name":" New A ","priority":2,"concurrency":3},
			{"candidate_id":"update","action":"update","provider_id":" existing-provider ","name":"ignored","priority":999},
			{"candidate_id":"create-b","action":"create","provider_id":"new-b","name":"New B"}
		]
	}`
	w := httptest.NewRecorder()
	handler.CommitProviderImport(w, providerImportTestCommitRequest(t, "import-commit", body))

	requireProviderImportStatus(t, w, http.StatusOK)
	originalPayload := w.Body.String()
	var response ProviderImportCommitResponse
	decodeProviderImportTestJSON(t, w, &response)
	if response.Summary != (ProviderImportCommitSummary{Created: 2, Updated: 1, Skipped: 1}) {
		t.Fatalf("summary = %#v", response.Summary)
	}
	if len(response.Items) != 3 || response.Items[0].CandidateID != "create-a" || response.Items[1].CandidateID != "update" || response.Items[2].CandidateID != "create-b" {
		t.Fatalf("result order = %#v", response.Items)
	}
	if len(importStore.applied) != 1 {
		t.Fatalf("apply calls = %d", len(importStore.applied))
	}
	bundle := importStore.applied[0]
	if len(bundle.Creates) != 2 || len(bundle.CredentialUpdates) != 1 || bundle.Receipt == nil {
		t.Fatalf("bundle = %#v", bundle)
	}
	firstSession := bundle.Creates[0].Sessions[0]
	secondSession := bundle.Creates[1].Sessions[0]
	if firstSession.ID == secondSession.ID || firstSession.Subject().Kind != credentialsession.SubjectAccount || string(firstSession.Subject().Value) != "shared-account" {
		t.Fatal("same account was collapsed or lost its account subject")
	}
	for i := range bundle.Creates {
		created := bundle.Creates[i]
		if created.Provider.GroupID == nil || *created.Provider.GroupID != "group-a" || created.Provider.Vendor != "openai" {
			t.Fatalf("created provider = %#v", created.Provider)
		}
		if len(created.Provider.APITypes) != 1 || created.Provider.APITypes[0].APIType != "codex" {
			t.Fatalf("route API mapping = %#v", created.Provider.APITypes)
		}
		snapshot, ok := created.Provider.CredentialSessionForAPIType("codex")
		if !ok || snapshot.SessionID != created.Sessions[0].ID {
			t.Fatalf("created route/session mapping = %#v", created.Provider.CredentialSessions)
		}
	}
	updated := bundle.CredentialUpdates[0]
	if updated.SessionID != "existing-session" || updated.ExpectedVersion != 7 || updated.SecretData != `{"access_token":"updated"}` {
		t.Fatalf("credential update = %#v", updated)
	}
	if got := importStore.mutationIDs[0]; len(got) != 3 || !slices.Contains(got, "existing-session") || !slices.Contains(got, firstSession.ID) || !slices.Contains(got, secondSession.ID) {
		t.Fatalf("credential mutation IDs = %v", got)
	}
	if importStore.releaseCalls != 1 || lifecycles.calls != 1 || len(drafts.invalidated) != 1 {
		t.Fatalf("boundaries = lease releases %d lifecycle %d invalidations %v", importStore.releaseCalls, lifecycles.calls, drafts.invalidated)
	}
	if drafts.finalizeCalls != 1 || drafts.releaseCalls != 1 {
		t.Fatalf("draft cleanup = finalize %d release %d", drafts.finalizeCalls, drafts.releaseCalls)
	}
	if len(drafts.verified) != 3 {
		t.Fatalf("verified candidates = %d", len(drafts.verified))
	}
	if logs.FilterMessage("provider import committed").Len() != 1 || logs.FilterMessage("provider import committed but draft cleanup failed").Len() != 1 {
		t.Fatalf("milestone/error logs = %#v", logs.All())
	}

	// Durable replay is authoritative and never reclaims or reapplies the secret-bearing draft.
	replay := httptest.NewRecorder()
	handler.CommitProviderImport(replay, providerImportTestCommitRequest(t, "import-commit", body))
	requireProviderImportStatus(t, replay, http.StatusOK)
	if replay.Body.String() != originalPayload || drafts.claimCalls != 1 || len(importStore.applied) != 1 {
		t.Fatalf("replay body/calls = %q claims=%d applies=%d", replay.Body.String(), drafts.claimCalls, len(importStore.applied))
	}

	mismatch := httptest.NewRecorder()
	different := strings.Replace(body, `"name":"New B"`, `"name":"Different"`, 1)
	handler.CommitProviderImport(mismatch, providerImportTestCommitRequest(t, "import-commit", different))
	requireProviderImportStatus(t, mismatch, http.StatusConflict)
	if !strings.Contains(mismatch.Body.String(), "provider_import_commit_mismatch") {
		t.Fatalf("mismatch body = %s", mismatch.Body.String())
	}
}

func TestCommitProviderImportValidationBoundary(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		body        string
		importID    string
		unavailable bool
		want        string
	}{
		{name: "unavailable", body: `{}`, importID: "id", unavailable: true, want: "unavailable"},
		{name: "missing import id", body: `{}`, importID: " ", want: "import_id is required"},
		{name: "invalid json", body: `{`, importID: "id", want: "invalid request body"},
		{name: "unknown field", body: `{"items":[],"secret":"x"}`, importID: "id", want: "unknown field"},
		{name: "trailing json", body: `{"items":[]} {}`, importID: "id", want: "exactly one"},
		{name: "empty selection", body: `{"items":[]}`, importID: "id", want: "at least one"},
		{name: "missing candidate", body: `{"items":[{"action":"update","provider_id":"p"}]}`, importID: "id", want: "candidate_id is required"},
		{name: "duplicate candidate", body: `{"items":[{"candidate_id":"c","action":"update","provider_id":"p"},{"candidate_id":"c","action":"update","provider_id":"p"}]}`, importID: "id", want: "selected more than once"},
		{name: "unknown action", body: `{"items":[{"candidate_id":"c","action":"delete","provider_id":"p"}]}`, importID: "id", want: "action must be"},
		{name: "missing update provider", body: `{"items":[{"candidate_id":"c","action":"update"}]}`, importID: "id", want: "required for update"},
		{name: "missing create provider", body: `{"items":[{"candidate_id":"c","action":"create","name":"n"}]}`, importID: "id", want: "lowercase letters"},
		{name: "missing create name", body: `{"items":[{"candidate_id":"c","action":"create","provider_id":"p"}]}`, importID: "id", want: "name is required"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			h := newProviderImportTestHandler(nil, &providerImportTestDrafts{}, &providerImportTestStore{}, nil, nil)
			if testCase.unavailable {
				h.providerImports = nil
			}
			w := httptest.NewRecorder()
			h.CommitProviderImport(w, providerImportTestCommitRequest(t, testCase.importID, testCase.body))
			if testCase.unavailable {
				requireProviderImportStatus(t, w, http.StatusNotImplemented)
			} else {
				requireProviderImportStatus(t, w, http.StatusBadRequest)
			}
			if !strings.Contains(w.Body.String(), testCase.want) {
				t.Fatalf("body = %s, want %q", w.Body.String(), testCase.want)
			}
		})
	}
}

func TestValidateProviderImportCommitRequestBounds(t *testing.T) {
	t.Parallel()
	valid := providerImportTestCreateItem("candidate", "provider", "Provider")
	tests := []struct {
		name   string
		mutate func(*ProviderImportCommitRequest)
		want   string
	}{
		{"too many", func(r *ProviderImportCommitRequest) {
			r.Items = make([]ProviderImportCommitItem, maxProviderImportSelections+1)
		}, "at most"},
		{"group too long", func(r *ProviderImportCommitRequest) {
			value := strings.Repeat("界", maxProviderImportIdentifierCharacters+1)
			r.GroupID = &value
		}, "group_id"},
		{"candidate too long", func(r *ProviderImportCommitRequest) {
			r.Items[0].CandidateID = strings.Repeat("界", maxProviderImportCandidateIDCharacters+1)
		}, "candidate_id"},
		{"provider invalid", func(r *ProviderImportCommitRequest) { r.Items[0].ProviderID = "UPPER" }, "lowercase"},
		{"provider too long", func(r *ProviderImportCommitRequest) {
			r.Items[0].ProviderID = strings.Repeat("a", maxProviderImportIdentifierCharacters+1)
		}, "provider_id"},
		{"name too long", func(r *ProviderImportCommitRequest) {
			r.Items[0].Name = strings.Repeat("界", maxProviderImportNameCharacters+1)
		}, "name"},
		{"priority negative", func(r *ProviderImportCommitRequest) { r.Items[0].Priority = -1 }, "priority"},
		{"priority high", func(r *ProviderImportCommitRequest) { r.Items[0].Priority = maxProviderImportRoutingValue + 1 }, "priority"},
		{"weight zero", func(r *ProviderImportCommitRequest) { value := 0; r.Items[0].Weight = &value }, "weight"},
		{"weight high", func(r *ProviderImportCommitRequest) {
			value := maxProviderImportRoutingValue + 1
			r.Items[0].Weight = &value
		}, "weight"},
		{"concurrency negative", func(r *ProviderImportCommitRequest) { r.Items[0].Concurrency = -1 }, "concurrency"},
		{"concurrency high", func(r *ProviderImportCommitRequest) { r.Items[0].Concurrency = maxProviderImportRoutingValue + 1 }, "concurrency"},
		{"retries negative", func(r *ProviderImportCommitRequest) { value := -1; r.Items[0].MaxRetries = &value }, "max_retries"},
		{"retries high", func(r *ProviderImportCommitRequest) {
			value := maxProviderImportRetryCount + 1
			r.Items[0].MaxRetries = &value
		}, "max_retries"},
		{"max delay negative", func(r *ProviderImportCommitRequest) {
			value := model.BackoffPolicy{MaxDelay: model.Duration(-time.Second), Multiplier: 1}
			r.Items[0].Backoff = &value
		}, "max_delay"},
		{"multiplier high", func(r *ProviderImportCommitRequest) {
			value := model.BackoffPolicy{Multiplier: maxProviderImportBackoffMultiplier + 1}
			r.Items[0].Backoff = &value
		}, "multiplier"},
		{"invalid backoff", func(r *ProviderImportCommitRequest) {
			value := model.BackoffPolicy{InitialDelay: model.Duration(time.Second), MaxDelay: model.Duration(time.Millisecond), Multiplier: 1}
			r.Items[0].Backoff = &value
		}, "backoff"},
		{"update id too long", func(r *ProviderImportCommitRequest) {
			r.Items[0] = ProviderImportCommitItem{CandidateID: "candidate", Action: providerImportActionUpdate, ProviderID: strings.Repeat("界", maxProviderImportIdentifierCharacters+1)}
		}, "provider_id"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			req := ProviderImportCommitRequest{Items: []ProviderImportCommitItem{valid}}
			testCase.mutate(&req)
			if err := validateProviderImportCommitRequest(&req); err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
		})
	}

	group := "   "
	req := ProviderImportCommitRequest{GroupID: &group, Items: []ProviderImportCommitItem{valid}}
	if err := validateProviderImportCommitRequest(&req); err != nil {
		t.Fatal(err)
	}
	if req.GroupID == nil || *req.GroupID != "" || req.Items[0].Weight == nil || req.Items[0].MaxRetries == nil || req.Items[0].Backoff == nil {
		t.Fatalf("normalized request = %#v", req)
	}
	if normalizedProviderImportGroupID(req.GroupID) != nil || normalizedProviderImportGroupID(nil) != nil {
		t.Fatal("blank group should not be persisted")
	}
}

func TestCommitProviderImportWorkflowFailures(t *testing.T) {
	t.Parallel()
	validBody := `{"items":[{"candidate_id":"candidate","action":"create","provider_id":"provider","name":"Provider"}]}`
	ready := providerImportTestCandidate(t, "candidate", "account", `{"access_token":"token"}`, providerauth.ChatGPTProviderImportCandidateDisposition{State: providerauth.ChatGPTProviderImportCandidateStateReady})
	tests := []struct {
		name       string
		configure  func(*providerImportTestCatalog, *providerImportTestDrafts, *providerImportTestStore, *Handler)
		wantStatus int
		want       string
	}{
		{"receipt lookup", func(_ *providerImportTestCatalog, _ *providerImportTestDrafts, s *providerImportTestStore, _ *Handler) {
			s.receiptErr = errors.New("receipt db")
		}, http.StatusInternalServerError, "inspect"},
		{"claim expired", func(_ *providerImportTestCatalog, d *providerImportTestDrafts, _ *providerImportTestStore, _ *Handler) {
			d.claimErr = providerauth.ErrChatGPTProviderImportExpired
		}, http.StatusGone, "expired"},
		{"unknown candidate", func(_ *providerImportTestCatalog, d *providerImportTestDrafts, _ *providerImportTestStore, _ *Handler) {
			d.candidates = nil
		}, http.StatusBadRequest, "Unknown candidate_id"},
		{"catalog", func(c *providerImportTestCatalog, _ *providerImportTestDrafts, _ *providerImportTestStore, _ *Handler) {
			c.err = errors.New("catalog")
		}, http.StatusInternalServerError, "Failed to inspect"},
		{"not ready", func(_ *providerImportTestCatalog, d *providerImportTestDrafts, _ *providerImportTestStore, _ *Handler) {
			blocked := ready
			blocked.State = providerauth.ChatGPTProviderImportCandidateStateInvalid
			d.candidates = []providerauth.ChatGPTProviderImportCandidate{blocked}
		}, http.StatusBadRequest, "not importable"},
		{"no disposition", func(_ *providerImportTestCatalog, d *providerImportTestDrafts, _ *providerImportTestStore, _ *Handler) {
			candidate := ready
			candidate.Disposition = nil
			d.candidates = []providerauth.ChatGPTProviderImportCandidate{candidate}
		}, http.StatusBadRequest, "no sealed"},
		{"invalid credential", func(_ *providerImportTestCatalog, d *providerImportTestDrafts, _ *providerImportTestStore, _ *Handler) {
			candidate := ready
			candidate.Credential.SecretData = ""
			d.candidates = []providerauth.ChatGPTProviderImportCandidate{candidate}
		}, http.StatusBadRequest, "no importable credential"},
		{"jwks", func(_ *providerImportTestCatalog, d *providerImportTestDrafts, _ *providerImportTestStore, _ *Handler) {
			d.candidates = []providerauth.ChatGPTProviderImportCandidate{ready}
			d.verifyErr = providerauth.ErrChatGPTProviderImportJWKSUnavailable
		}, http.StatusServiceUnavailable, "signing_keys_unavailable"},
		{"verification", func(_ *providerImportTestCatalog, d *providerImportTestDrafts, _ *providerImportTestStore, _ *Handler) {
			d.candidates = []providerauth.ChatGPTProviderImportCandidate{ready}
			d.verifyErr = &providerauth.ChatGPTProviderImportVerificationError{CandidateID: "candidate"}
		}, http.StatusUnprocessableEntity, "candidate"},
		{"verification internal", func(_ *providerImportTestCatalog, d *providerImportTestDrafts, _ *providerImportTestStore, _ *Handler) {
			d.candidates = []providerauth.ChatGPTProviderImportCandidate{ready}
			d.verifyErr = errors.New("verify")
		}, http.StatusInternalServerError, "Failed to verify"},
		{"mutation lease", func(_ *providerImportTestCatalog, d *providerImportTestDrafts, s *providerImportTestStore, _ *Handler) {
			d.candidates = []providerauth.ChatGPTProviderImportCandidate{ready}
			s.mutationErr = errors.New("busy")
		}, http.StatusRequestTimeout, "Timed out"},
		{"apply receipt conflict", func(_ *providerImportTestCatalog, d *providerImportTestDrafts, s *providerImportTestStore, _ *Handler) {
			d.candidates = []providerauth.ChatGPTProviderImportCandidate{ready}
			s.applyErr = store.ErrProviderImportReceiptConflict
		}, http.StatusConflict, "commit_mismatch"},
		{"apply state conflict", func(_ *providerImportTestCatalog, d *providerImportTestDrafts, s *providerImportTestStore, _ *Handler) {
			d.candidates = []providerauth.ChatGPTProviderImportCandidate{ready}
			s.applyErr = &store.ProviderImportConflictError{Conflicts: []store.ProviderImportConflict{{CandidateID: "candidate", Kind: store.ProviderImportConflictProviderAlreadyExists}}}
		}, http.StatusConflict, "provider_already_exists"},
		{"apply internal", func(_ *providerImportTestCatalog, d *providerImportTestDrafts, s *providerImportTestStore, _ *Handler) {
			d.candidates = []providerauth.ChatGPTProviderImportCandidate{ready}
			s.applyErr = errors.New("apply")
		}, http.StatusInternalServerError, "Failed to commit"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			catalog := &providerImportTestCatalog{}
			drafts := &providerImportTestDrafts{candidates: []providerauth.ChatGPTProviderImportCandidate{ready}}
			importStore := &providerImportTestStore{}
			h := newProviderImportTestHandler(catalog, drafts, importStore, nil, zap.NewNop())
			testCase.configure(catalog, drafts, importStore, h)
			w := httptest.NewRecorder()
			h.CommitProviderImport(w, providerImportTestCommitRequest(t, "failure", validBody))
			requireProviderImportStatus(t, w, testCase.wantStatus)
			if !strings.Contains(w.Body.String(), testCase.want) {
				t.Fatalf("body = %s, want %q", w.Body.String(), testCase.want)
			}
		})
	}
}

func TestBuildProviderImportUpdateRequiresExactSessionAndSubject(t *testing.T) {
	t.Parallel()
	selection := ProviderImportCommitItem{CandidateID: "candidate", Action: providerImportActionUpdate, ProviderID: "provider"}
	candidate := providerImportTestCandidate(t, "candidate", "account", `{"access_token":"new"}`, providerauth.ChatGPTProviderImportCandidateDisposition{
		State: providerauth.ChatGPTProviderImportCandidateStateExisting, ExpectedSessionID: "session", ExpectedCredentialVersion: 3,
	})
	provider := providerImportTestProvider(t, "provider", []string{"codex", "responses"}, "session", "account", 3)
	staticProvider := providerImportTestProvider(t, "provider", []string{"codex"}, "static-session", "account", 3)
	staticProvider.CredentialSessions[0].Credential.Kind = credentialsession.KindAPIKey
	if snapshot, ok := provider.CredentialSessionForAPIType("codex"); !ok || snapshot.SessionID != "session" || snapshot.Kind != credentialsession.KindChatGPT {
		t.Fatalf("provider fixture session = %#v, found=%v", snapshot, ok)
	}
	update, result, err := buildProviderImportUpdate(selection, candidate, *candidate.Disposition, map[string]model.Provider{"provider": provider})
	if err != nil {
		t.Fatal(err)
	}
	if update.SessionID != "session" || update.ExpectedVersion != 3 || result.Outcome != providerImportOutcomeUpdated {
		t.Fatalf("update/result = %#v %#v", update, result)
	}

	tests := []struct {
		name      string
		providers map[string]model.Provider
		mutate    func(*providerauth.ChatGPTProviderImportCandidateDisposition, *providerauth.ChatGPTProviderImportCandidate)
		want      string
	}{
		{"disposition not ready", map[string]model.Provider{"provider": provider}, func(d *providerauth.ChatGPTProviderImportCandidateDisposition, _ *providerauth.ChatGPTProviderImportCandidate) {
			d.State = providerauth.ChatGPTProviderImportCandidateStateInvalid
		}, "credential_session_not_found"},
		{"provider missing", nil, func(_ *providerauth.ChatGPTProviderImportCandidateDisposition, _ *providerauth.ChatGPTProviderImportCandidate) {
		}, "provider_not_found"},
		{"api missing", map[string]model.Provider{"provider": providerImportTestProvider(t, "provider", []string{"claude"}, "session", "account", 3)}, func(_ *providerauth.ChatGPTProviderImportCandidateDisposition, _ *providerauth.ChatGPTProviderImportCandidate) {
		}, "credential_session_not_found"},
		{"static target", map[string]model.Provider{"provider": staticProvider}, func(d *providerauth.ChatGPTProviderImportCandidateDisposition, _ *providerauth.ChatGPTProviderImportCandidate) {
			d.ExpectedSessionID = "static-session"
		}, "credential_session_not_found"},
		{"wrong session", map[string]model.Provider{"provider": provider}, func(d *providerauth.ChatGPTProviderImportCandidateDisposition, _ *providerauth.ChatGPTProviderImportCandidate) {
			d.ExpectedSessionID = "other"
		}, "credential_session_not_found"},
		{"zero version", map[string]model.Provider{"provider": provider}, func(d *providerauth.ChatGPTProviderImportCandidateDisposition, _ *providerauth.ChatGPTProviderImportCandidate) {
			d.ExpectedCredentialVersion = 0
		}, "credential_session_not_found"},
		{"subject mismatch", map[string]model.Provider{"provider": provider}, func(_ *providerauth.ChatGPTProviderImportCandidateDisposition, c *providerauth.ChatGPTProviderImportCandidate) {
			other, _ := credentialsession.AccountSubject("other")
			c.Credential.Subject = other
			c.Credential.AuthState.AccountID = "other"
		}, "subject does not match"},
		{"invalid imported session", map[string]model.Provider{"provider": provider}, func(_ *providerauth.ChatGPTProviderImportCandidateDisposition, c *providerauth.ChatGPTProviderImportCandidate) {
			c.Credential.SecretData = ""
		}, "no importable credential"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			disposition := *candidate.Disposition
			input := candidate
			testCase.mutate(&disposition, &input)
			_, _, err := buildProviderImportUpdate(selection, input, disposition, testCase.providers)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func TestApplyProviderImportBundleErrorAndReplaySemantics(t *testing.T) {
	t.Parallel()
	replayPayload := []byte(`{"durable":true}` + "\n")
	tests := []struct {
		name       string
		err        error
		wantOK     bool
		wantStatus int
		wantBody   string
	}{
		{"success", nil, true, http.StatusOK, "response"},
		{"durable replay", &store.ProviderImportReceiptReplayError{Receipt: store.ProviderImportReceipt{ResponsePayload: replayPayload}}, true, http.StatusOK, "durable"},
		{"receipt conflict", store.ErrProviderImportReceiptConflict, false, http.StatusConflict, "commit_mismatch"},
		{"state conflict", &store.ProviderImportConflictError{Conflicts: []store.ProviderImportConflict{{Kind: store.ProviderImportConflictCredentialVersionMismatch}}}, false, http.StatusConflict, "credential_version_mismatch"},
		{"internal", errors.New("db"), false, http.StatusInternalServerError, "Failed to commit"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			h := newProviderImportTestHandler(nil, &providerImportTestDrafts{}, &providerImportTestStore{applyErr: testCase.err}, nil, zap.NewNop())
			w := httptest.NewRecorder()
			payload, ok := h.applyProviderImportBundle(w, context.Background(), "import", &store.ProviderImportBundle{}, []byte("response\n"))
			if ok != testCase.wantOK || !strings.Contains(string(payload)+w.Body.String(), testCase.wantBody) {
				t.Fatalf("payload=%q ok=%v status=%d body=%s", payload, ok, w.Code, w.Body.String())
			}
			if !ok {
				requireProviderImportStatus(t, w, testCase.wantStatus)
			}
		})
	}
}

func TestCommitLifecycleCoordinatorFailureDoesNotApplyOrInvalidate(t *testing.T) {
	t.Parallel()
	drafts := &providerImportTestDrafts{}
	importStore := &providerImportTestStore{}
	lifecycles := &providerImportTestLifecycles{err: errors.New("retirement failed")}
	h := newProviderImportTestHandler(nil, drafts, importStore, lifecycles, zap.NewNop())
	w := httptest.NewRecorder()
	_, ok := h.commitProviderImportAtLifecycleBoundary(w, context.Background(), "import", &store.ProviderImportBundle{}, []byte("response"))
	if ok || len(importStore.applied) != 0 || len(drafts.invalidated) != 0 {
		t.Fatalf("commit=%v applies=%d invalidations=%d", ok, len(importStore.applied), len(drafts.invalidated))
	}
	requireProviderImportStatus(t, w, http.StatusRequestTimeout)
}

func TestProviderImportCommitFingerprintIsCanonicalAndActionPrecise(t *testing.T) {
	t.Parallel()
	weight, retries := 1, 2
	backoff := model.BackoffPolicy{Multiplier: 1}
	group := " group "
	first := ProviderImportCommitRequest{GroupID: &group, Items: []ProviderImportCommitItem{
		{CandidateID: " b ", Action: providerImportActionUpdate, ProviderID: " p-b ", Name: "ignored", Priority: 99},
		{CandidateID: " a ", Action: providerImportActionCreate, ProviderID: " p-a ", Name: " Name ", Weight: &weight, MaxRetries: &retries, Backoff: &backoff},
	}}
	second := ProviderImportCommitRequest{GroupID: ptrProviderImportString("group"), Items: []ProviderImportCommitItem{
		{CandidateID: "a", Action: providerImportActionCreate, ProviderID: "p-a", Name: "Name", Weight: &weight, MaxRetries: &retries, Backoff: &backoff},
		{CandidateID: "b", Action: providerImportActionUpdate, ProviderID: "p-b"},
	}}
	if got, want := providerImportCommitRequestFingerprint(first), providerImportCommitRequestFingerprint(second); got != want || len(got) != 64 {
		t.Fatalf("fingerprints = %q %q", got, want)
	}
	second.Items[0].Name = "Different"
	if providerImportCommitRequestFingerprint(first) == providerImportCommitRequestFingerprint(second) {
		t.Fatal("create semantics were omitted from fingerprint")
	}
}

func ptrProviderImportString(value string) *string { return &value }

func TestIndexProviderImportCandidates(t *testing.T) {
	t.Parallel()
	candidates := []providerauth.ChatGPTProviderImportCandidate{{CandidateID: "a"}, {CandidateID: "b"}}
	indexed, err := indexProviderImportCandidates([]ProviderImportCommitItem{{CandidateID: "b"}}, candidates)
	if err != nil || indexed["b"].CandidateID != "b" {
		t.Fatalf("indexed = %#v err=%v", indexed, err)
	}
	if _, err := indexProviderImportCandidates([]ProviderImportCommitItem{{CandidateID: "missing"}}, candidates); err == nil {
		t.Fatal("unknown candidate accepted")
	}
}

func TestCommitResponseCountsSourceOrder(t *testing.T) {
	t.Parallel()
	response := buildProviderImportCommitResponse("import", []providerauth.ChatGPTProviderImportCandidate{{CandidateID: "skip"}, {CandidateID: "update"}, {CandidateID: "create"}}, map[string]ProviderImportCommitResultItem{
		"create": {CandidateID: "create", Outcome: providerImportOutcomeCreated},
		"update": {CandidateID: "update", Outcome: providerImportOutcomeUpdated},
	})
	if response.Summary != (ProviderImportCommitSummary{Created: 1, Updated: 1, Skipped: 1}) || response.Items[0].CandidateID != "update" {
		t.Fatalf("response = %#v", response)
	}
}

func TestDecodeProviderImportCommitRequestEnforcesBodyLimit(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("x", MaxRequestBodySize+1)))
	if err := decodeProviderImportCommitRequest(w, r, &ProviderImportCommitRequest{}); err == nil {
		t.Fatal("oversized request accepted")
	}
}

func TestProviderImportVerificationErrorResponseDoesNotLeakCause(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	writeProviderImportVerificationError(w, errors.New("secret-token"))
	requireProviderImportStatus(t, w, http.StatusInternalServerError)
	if strings.Contains(w.Body.String(), "secret-token") {
		t.Fatal("verification cause leaked")
	}
}

func TestJSONCommitResponseContract(t *testing.T) {
	t.Parallel()
	payload, err := json.Marshal(ProviderImportCommitResponse{ImportID: "id", Items: []ProviderImportCommitResultItem{}})
	if err != nil || strings.Contains(string(payload), "credential") {
		t.Fatalf("payload=%s err=%v", payload, err)
	}
}
