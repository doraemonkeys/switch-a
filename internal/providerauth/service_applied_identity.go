package providerauth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"

	"go.uber.org/zap"
)

// ApplyProviderCredentials is the final authority boundary for one physical
// attempt. The candidate owns both the exact credential snapshot and expected
// Authority, preventing a mutable route target from tearing selection and auth.
func (s *Service) ApplyProviderCredentials(
	ctx context.Context,
	headers http.Header,
	candidate codexidentity.CandidateSnapshot,
	providerAuthMode string,
	globalAuthMode string,
	originalReq *http.Request,
	finalURL *url.URL,
) (codexidentity.AppliedIdentity, error) {
	if headers == nil {
		return codexidentity.AppliedIdentity{}, fmt.Errorf("upstream headers are required")
	}
	snapshot := candidate.Credential()
	if strings.TrimSpace(snapshot.SessionID) == "" {
		return codexidentity.AppliedIdentity{}, fmt.Errorf("credential candidate is required")
	}
	if err := snapshot.RequireResolvedSubject(); err != nil {
		return codexidentity.AppliedIdentity{}, err
	}

	var (
		applied codexidentity.AppliedIdentity
		err     error
	)
	switch snapshot.Kind {
	case credentialsession.KindChatGPT:
		if candidate.APIType() != codexAPIType {
			return codexidentity.AppliedIdentity{}, fmt.Errorf("chatgpt credential session %q only supports api_type %q", snapshot.SessionID, codexAPIType)
		}

		credential, credentialErr := s.validatedChatGPTSessionCredential(candidate.RouteTargetID(), &snapshot)
		if credentialErr != nil {
			return codexidentity.AppliedIdentity{}, credentialErr
		}
		actualSubject, subjectErr := codexidentity.NewAccountCredentialSubject(credential.AccountID)
		if subjectErr != nil {
			return codexidentity.AppliedIdentity{}, subjectErr
		}
		applied, err = codexidentity.AppliedIdentityFromRequest(snapshot.Vendor, finalURL, actualSubject)
		if err != nil {
			return codexidentity.AppliedIdentity{}, err
		}
		if err := candidate.ValidateApplied(applied); err != nil {
			s.logAppliedIdentityMismatch(candidate, err)
			return codexidentity.AppliedIdentity{}, err
		}
		// Validate the local credential and final origin before a refresh can send
		// secret state to the OAuth endpoint. Refresh preserves the session subject,
		// but this preflight is what makes an origin mismatch fail without I/O.
		credential, refreshErr := s.ensureFreshValidatedChatGPTSessionCredential(
			ctx, candidate.RouteTargetID(), &snapshot, credential, false,
		)
		if refreshErr != nil {
			return codexidentity.AppliedIdentity{}, refreshErr
		}
		actualSubject, subjectErr = codexidentity.NewAccountCredentialSubject(credential.AccountID)
		if subjectErr != nil {
			return codexidentity.AppliedIdentity{}, subjectErr
		}
		applied, err = codexidentity.AppliedIdentityFromRequest(snapshot.Vendor, finalURL, actualSubject)
		if err != nil {
			return codexidentity.AppliedIdentity{}, err
		}
		if err := candidate.ValidateApplied(applied); err != nil {
			s.logAppliedIdentityMismatch(candidate, err)
			return codexidentity.AppliedIdentity{}, err
		}
		headers.Set("Authorization", "Bearer "+strings.TrimSpace(credential.AccessToken))
		headers.Set("ChatGPT-Account-Id", strings.TrimSpace(credential.AccountID))
		// Preserve a caller-supplied Originator so Codex variants can retain their
		// own identity; only default when the proxy is the first component to add it.
		if headers.Get("Originator") == "" {
			headers.Set("Originator", chatGPTCodexOriginator)
		}
	case credentialsession.KindAPIKey:
		actualSubject, subjectErr := codexidentity.CredentialSubjectFromSession(snapshot.Subject)
		if subjectErr != nil {
			return codexidentity.AppliedIdentity{}, subjectErr
		}
		applied, err = codexidentity.AppliedIdentityFromRequest(snapshot.Vendor, finalURL, actualSubject)
		if err != nil {
			return codexidentity.AppliedIdentity{}, err
		}
		if err := candidate.ValidateApplied(applied); err != nil {
			s.logAppliedIdentityMismatch(candidate, err)
			return codexidentity.AppliedIdentity{}, err
		}
		applyStaticAuthHeader(headers, snapshot.SecretData, providerAuthMode, globalAuthMode, originalReq)
	default:
		return codexidentity.AppliedIdentity{}, fmt.Errorf("credential session %q has unsupported kind %q", snapshot.SessionID, snapshot.Kind)
	}
	s.logger.Debug("provider credential applied",
		zap.String("route_target_id", candidate.RouteTargetID()),
		zap.String("credential_session_id", snapshot.SessionID),
		zap.Int64("credential_version", snapshot.Version),
		zap.String("credential_kind", string(snapshot.Kind)),
	)
	return applied, nil
}

func (s *Service) logAppliedIdentityMismatch(candidate codexidentity.CandidateSnapshot, err error) {
	s.logger.Warn("provider credential identity did not match selected authority",
		zap.String("route_target_id", candidate.RouteTargetID()),
		zap.String("credential_session_id", candidate.CredentialSessionID()),
		zap.Int64("credential_version", candidate.CredentialVersion()),
		zap.Error(err),
	)
}

func applyStaticAuthHeader(headers http.Header, apiKey, providerAuthMode, globalAuthMode string, originalReq *http.Request) {
	mode := providerAuthMode
	if mode == "" {
		mode = globalAuthMode
	}
	if mode == authModeAuto {
		mode = detectAuthMode(originalReq)
	}

	switch mode {
	case authModeXAPIKey:
		headers.Set("x-api-key", apiKey)
	default:
		headers.Set("Authorization", "Bearer "+apiKey)
	}
}

func detectAuthMode(r *http.Request) string {
	if r != nil && r.Header.Get("Authorization") != "" {
		return authModeBearer
	}
	if r != nil && r.Header.Get("X-Api-Key") != "" {
		return authModeXAPIKey
	}
	return authModeBearer
}
