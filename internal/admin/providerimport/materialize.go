package providerimport

import (
	"fmt"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/model/providerroute"
	"github.com/doraemonkeys/switch-a/internal/providerauth"
	"github.com/doraemonkeys/switch-a/internal/store"
	"github.com/google/uuid"
)

func buildProviderImportCreate(
	selection ProviderImportCommitItem,
	candidate providerauth.ChatGPTProviderImportCandidate,
	groupID *string,
) (store.ProviderImportCreate, ProviderImportCommitResultItem, error) {
	provider := model.Provider{
		ID:       selection.ProviderID,
		Name:     strings.TrimSpace(selection.Name),
		AuthMode: "bearer",
		APITypes: []model.ProviderAPIType{{
			ProviderID: selection.ProviderID,
			APIType:    "codex",
			Transport:  providerroute.HTTP,
			BaseURL:    providerauth.ChatGPTCodexBaseURL(),
		}},
		GroupID:        groupID,
		Weight:         *selection.Weight,
		Priority:       selection.Priority,
		Concurrency:    *selection.Concurrency,
		MaxRetries:     *selection.MaxRetries,
		Backoff:        *selection.Backoff,
		FailoverScope:  model.ScopeAny,
		AcceptFailover: model.ScopeAny,
		Enabled:        true,
	}
	session, err := importedChatGPTSession(candidate, uuid.NewString())
	if err != nil {
		return store.ProviderImportCreate{}, ProviderImportCommitResultItem{}, err
	}
	provider.Vendor = chatGPTProviderVendor
	snapshot, err := session.Snapshot()
	if err != nil {
		return store.ProviderImportCreate{}, ProviderImportCommitResultItem{}, err
	}
	provider.APITypes = append(provider.APITypes, model.ProviderAPIType{ProviderID: provider.ID, APIType: "codex", Transport: providerroute.WebSocket, BaseURL: providerauth.ChatGPTCodexBaseURL()})
	provider.CredentialSessions = []credentialsession.RouteSnapshot{{
		RouteTargetID: provider.ID,
		APIType:       "codex",
		Transport:     providerroute.HTTP,
		VendorScope:   provider.Vendor,
		Credential:    snapshot,
	}, {RouteTargetID: provider.ID, APIType: "codex", Transport: providerroute.WebSocket, VendorScope: provider.Vendor, Credential: snapshot}}
	return store.ProviderImportCreate{
		CandidateID: selection.CandidateID,
		Provider:    provider,
		Sessions:    []credentialsession.Session{session},
	}, ProviderImportCommitResultItem{
		CandidateID: selection.CandidateID,
		Outcome:     providerImportOutcomeCreated,
		ProviderID:  provider.ID,
		Name:        provider.Name,
	}, nil
}

func buildProviderImportUpdate(
	selection ProviderImportCommitItem,
	candidate providerauth.ChatGPTProviderImportCandidate,
	disposition providerauth.ChatGPTProviderImportCandidateDisposition,
	providersByID map[string]model.Provider,
) (store.ProviderImportCredentialUpdate, ProviderImportCommitResultItem, error) {
	if disposition.State != providerauth.ChatGPTProviderImportCandidateStateExisting {
		return store.ProviderImportCredentialUpdate{}, ProviderImportCommitResultItem{}, providerImportConflict(selection, store.ProviderImportConflictSessionNotFound)
	}
	target, exists := providersByID[selection.ProviderID]
	if !exists {
		return store.ProviderImportCredentialUpdate{}, ProviderImportCommitResultItem{}, providerImportConflict(selection, store.ProviderImportConflictProviderNotFound)
	}
	var targetSession *credentialsession.Snapshot
	for _, route := range target.CredentialSessions {
		if route.APIType == "codex" && route.Credential.SessionID == disposition.ExpectedSessionID {
			snapshot := route.Credential
			targetSession = &snapshot
			break
		}
	}
	if targetSession == nil || targetSession.Kind != credentialsession.KindChatGPT ||
		targetSession.SessionID != disposition.ExpectedSessionID || disposition.ExpectedCredentialVersion < 1 {
		return store.ProviderImportCredentialUpdate{}, ProviderImportCommitResultItem{}, providerImportConflict(selection, store.ProviderImportConflictSessionNotFound)
	}
	updated, err := importedChatGPTSession(candidate, targetSession.SessionID)
	if err != nil {
		return store.ProviderImportCredentialUpdate{}, ProviderImportCommitResultItem{}, err
	}
	updatedSubject := updated.Subject()
	if targetSession.Subject.Kind != credentialsession.SubjectAccount || string(targetSession.Subject.Value) != string(updatedSubject.Value) {
		return store.ProviderImportCredentialUpdate{}, ProviderImportCommitResultItem{}, fmt.Errorf("candidate %q subject does not match credential session %q", selection.CandidateID, targetSession.SessionID)
	}
	return store.ProviderImportCredentialUpdate{
		CandidateID:     selection.CandidateID,
		SessionID:       targetSession.SessionID,
		ExpectedVersion: disposition.ExpectedCredentialVersion,
		SecretData:      updated.SecretData,
		Subject:         updated.Subject(),
		AuthState:       updated.AuthState,
	}, ProviderImportCommitResultItem{
		CandidateID: selection.CandidateID,
		Outcome:     providerImportOutcomeUpdated,
		ProviderID:  selection.ProviderID,
	}, nil
}

func importedChatGPTSession(
	candidate providerauth.ChatGPTProviderImportCandidate,
	sessionID string,
) (credentialsession.Session, error) {
	session, err := providerauth.BuildCredentialSessionFromChatGPTProviderImportCandidate(candidate, sessionID)
	if err != nil {
		return credentialsession.Session{}, err
	}
	return *session, nil
}
