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

type authMode string

const (
	authModeAuto    = "auto"
	authModeBearer  = "bearer"
	authModeXAPIKey = "x-api-key"

	headerAuthorization = "Authorization"
	headerAPIKey        = "X-Api-Key"
	bearerPrefix        = "Bearer "
)

// ApplyProviderCredentials is the final authority boundary for one physical
// attempt. The candidate freezes the session identity and expected Authority;
// the service reloads current credential material and proves that authority
// before any provider secret is injected.
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
	snapshot, snapshotErr := s.authoritativeCredentialSnapshot(ctx, candidate)
	if snapshotErr != nil {
		return codexidentity.AppliedIdentity{}, snapshotErr
	}
	if err := snapshot.RequireResolvedSubject(); err != nil {
		return codexidentity.AppliedIdentity{}, err
	}

	var (
		applied codexidentity.AppliedIdentity
		err     error
	)
	vendorScope := candidate.Authority().Vendor()
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
		applied, err = codexidentity.AppliedIdentityFromRequest(vendorScope, finalURL, actualSubject)
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
		applied, err = codexidentity.AppliedIdentityFromRequest(vendorScope, finalURL, actualSubject)
		if err != nil {
			return codexidentity.AppliedIdentity{}, err
		}
		if err := candidate.ValidateApplied(applied); err != nil {
			s.logAppliedIdentityMismatch(candidate, err)
			return codexidentity.AppliedIdentity{}, err
		}
		headers.Set(headerAuthorization, bearerPrefix+strings.TrimSpace(credential.AccessToken))
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
		applied, err = codexidentity.AppliedIdentityFromRequest(vendorScope, finalURL, actualSubject)
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
		zap.Int64("selected_credential_version", candidate.CredentialVersion()),
		zap.Int64("applied_credential_version", snapshot.Version),
		zap.Bool("credential_revision_advanced", snapshot.Version > candidate.CredentialVersion()),
		zap.String("credential_kind", string(snapshot.Kind)),
	)
	return applied, nil
}

type credentialSessionReader interface {
	GetCredentialSession(context.Context, string) (*credentialsession.Session, error)
}

func (s *Service) authoritativeCredentialSnapshot(
	ctx context.Context,
	candidate codexidentity.CandidateSnapshot,
) (credentialsession.Snapshot, error) {
	selected := candidate.Credential()
	if strings.TrimSpace(selected.SessionID) == "" {
		return credentialsession.Snapshot{}, fmt.Errorf("credential candidate is required")
	}
	reader, available := s.credentialStore.(credentialSessionReader)
	if !available {
		// A service without persistence cannot observe a newer revision; the
		// immutable selection snapshot is authoritative in that reduced mode.
		return selected, nil
	}
	session, err := reader.GetCredentialSession(ctx, candidate.CredentialSessionID())
	if err != nil {
		return credentialsession.Snapshot{}, fmt.Errorf(
			"load credential session %q for dispatch: %w",
			candidate.CredentialSessionID(), err,
		)
	}
	if session == nil || strings.TrimSpace(session.ID) != candidate.CredentialSessionID() {
		return credentialsession.Snapshot{}, fmt.Errorf(
			"credential session %q is unavailable for dispatch",
			candidate.CredentialSessionID(),
		)
	}
	live, err := session.Snapshot()
	if err != nil {
		return credentialsession.Snapshot{}, fmt.Errorf(
			"snapshot credential session %q for dispatch: %w",
			candidate.CredentialSessionID(), err,
		)
	}
	if live.Version < candidate.CredentialVersion() {
		return credentialsession.Snapshot{}, fmt.Errorf(
			"credential session %q revision regressed from %d to %d",
			candidate.CredentialSessionID(), candidate.CredentialVersion(), live.Version,
		)
	}
	return live, nil
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
	switch resolveAuthMode(providerAuthMode, globalAuthMode, originalReq) {
	case authModeXAPIKey:
		headers.Set(headerAPIKey, apiKey)
	default:
		headers.Set(headerAuthorization, bearerPrefix+apiKey)
	}
}

func resolveAuthMode(providerMode, globalMode string, originalReq *http.Request) authMode {
	mode := authMode(providerMode)
	if mode == "" {
		mode = authMode(globalMode)
	}
	if mode == authModeAuto {
		return detectAuthMode(originalReq)
	}
	return mode
}

func detectAuthMode(r *http.Request) authMode {
	if r != nil && r.Header.Get(headerAuthorization) != "" {
		return authModeBearer
	}
	if r != nil && r.Header.Get(headerAPIKey) != "" {
		return authModeXAPIKey
	}
	return authModeBearer
}
