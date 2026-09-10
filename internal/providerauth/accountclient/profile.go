// Package accountclient applies credential-owned client features to requests
// initiated by the gateway, without importing downstream conversation headers.
package accountclient

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/buildinfo"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	TokenRefresh = "token_refresh"
	OAuthLogin   = "oauth_login"
	TokenImport  = "token_import"
	UsageQuery   = "usage_query"
)

// ProfileStore resolves existing session bindings without selecting a provider.
type ProfileStore interface {
	ResolveLoginProfile(context.Context, string) (clientdisguise.LoginProfile, error)
}

type Resolver struct {
	profiles ProfileStore
	policy   PolicyStore
	logger   *zap.Logger
}

type Config struct {
	Profiles ProfileStore
	Policy   PolicyStore
	Logger   *zap.Logger
}

func NewResolver(config Config) *Resolver {
	if config.Logger == nil {
		config.Logger = zap.NewNop()
	}
	return &Resolver{profiles: config.Profiles, policy: config.Policy, logger: config.Logger}
}

// Operation freezes identity for all transmissions of an account operation,
// including usage endpoint fallback and the callback of a pending OAuth login.
type Operation struct {
	userAgent  string
	originator string
	logger     *zap.Logger
}

func (r *Resolver) Resolve(ctx context.Context, sessionID, kind string) (Operation, error) {
	log := r.logger.With(
		zap.String("operation_id", uuid.NewString()),
		zap.String("operation", kind),
		zap.String("credential_session_id", sessionID),
	)
	profile := clientdisguise.LoginProfile{}
	if sessionID != "" && r.profiles != nil {
		var err error
		profile, err = r.profiles.ResolveLoginProfile(ctx, sessionID)
		if err != nil {
			log.Warn("account_client.profile_resolution_failed", zap.Error(err))
			return Operation{}, fmt.Errorf("resolve account client profile for session %q: %w", sessionID, err)
		}
	}

	ua, source := profile.UserAgent(), "profile"
	originator := refreshOriginator(ua)
	var policy Policy
	fallbackReason := ""
	if ua == "" {
		var err error
		policy, err = r.resolvePolicy(ctx)
		if err != nil {
			log.Warn("account_client.policy_resolution_failed", zap.Error(err))
			return Operation{}, err
		}
		ua = buildinfo.Current().UserAgent()
		switch {
		case sessionID == "":
			source = "unassigned_login"
		case r.profiles == nil:
			source = "profile_store_unavailable"
		case profile.Binding.RevisionID == "":
			source = "unbound_session"
		default:
			source = "profile_without_user_agent"
		}
		fallbackReason = source
	}
	release, uaRevisionID := profile.OfficialVersion, profile.Profile.ID
	versionSource := profile.Binding.VersionSource
	// A credential's disguise UA and version selection remain authoritative;
	// this global setting only replaces the gateway's otherwise-default UA.
	officialFallback := profile.UserAgent() == "" && policy.FallbackClient == FallbackOfficialStable
	if officialFallback {
		selected := clientdisguise.BuiltinAccountProfile()
		release = policy.OfficialVersion
		versionSource = string(policy.FallbackClient)
		ua, source, uaRevisionID = selected.UserAgent(release.Version), "official_stable_builtin", selected.ID
		originator = refreshOriginator(ua)
	}
	log = log.With(
		zap.String("account_client_fallback", string(policy.FallbackClient)),
		zap.String("user_agent_profile_revision_id", uaRevisionID),
		zap.Bool("official_version_pending", officialFallback && release.Version == ""),
		zap.String("profile_revision_id", profile.Binding.RevisionID),
		zap.String("login_generation_id", profile.Login.GenerationID),
		zap.String("version_source", versionSource),
		zap.String("fallback_reason", fallbackReason),
		zap.String("official_version", release.Version),
		zap.String("user_agent_source", source),
		zap.String("user_agent", ua),
		zap.String("refresh_originator", originator),
	)
	log.Debug("account_client.profile_resolved")
	return Operation{userAgent: ua, originator: originator, logger: log}, nil
}

// The default Codex UA starts with its process originator. Inference samples may
// carry a thread-specific Originator override, so that header cannot identify the
// account client. An unsampled gateway fallback has no Codex originator.
func refreshOriginator(userAgent string) string {
	name, version, ok := strings.Cut(userAgent, "/")
	if !ok || strings.TrimSpace(version) == "" {
		return ""
	}
	return strings.TrimSpace(name)
}

// ApplyTokenRefresh mirrors Codex's default auth client. Authorization-code
// exchange uses a raw auth client, so sharing an OAuth operation or token URL
// must not implicitly add this header to that request.
func (o Operation) ApplyTokenRefresh(request *http.Request) {
	if o.originator != "" {
		request.Header.Set("Originator", o.originator)
	}
	o.Apply(request)
}

// Apply projects the configured UA without importing inference headers.
// Token refresh uses ApplyTokenRefresh to include its paired process originator.
func (o Operation) Apply(request *http.Request) {
	request.Header.Set("User-Agent", o.userAgent)
	o.logger.Debug("account_client.request_prepared",
		zap.String("method", request.Method),
		zap.String("host", request.URL.Host),
		zap.String("path", request.URL.Path),
		zap.String("originator", request.Header.Get("Originator")),
	)
}
